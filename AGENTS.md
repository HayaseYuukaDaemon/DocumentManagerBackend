# AGENTS.md

本文件为 Codex (Codex.ai/code) 在此仓库中工作提供指导。

## 常用命令

- 启动服务：`go run ./cmd/server`
- 构建服务：`go build -o bin/document-archive ./cmd/server`
- 运行全部测试：`go test ./...`
- 运行单个包的测试：`go test ./internal/documents`
- 运行单个测试或一组测试：`go test ./internal/documents -run TestSQLiteStore`
- 提交前格式化 Go 代码：`gofmt -w <files>`
- 用默认值补全部分配置：`go run ./tools/config_merge.go -in config.partial.yml -out config.yml`

配置只从默认值和当前工作目录下的 `config.yml` 读取，不再读取环境变量。需要修改监听地址、鉴权 token、文档存储、默认对象存储、S3 参数或 CORS 时，直接编辑 `config.yml`。
主程序仍要求配置包含全部必要字段；`tools/config_merge.go` 仅用于在启动前将部分配置与程序默认值合并为完整配置。

配置示例见 [internal/config/config.demo.yml](internal/config/config.demo.yml)。

## 生产阶段原则

- 项目已经投入生产使用。默认应保护现有数据库、HTTP API、配置格式和运行行为，不能再以早期开发阶段为由自行实施破坏性变更。
- 破坏性变更并非禁止；如果 schema、接口、配置或行为调整能够明显简化实现、改善架构或提升健壮性，应主动提出方案。
- 实施任何破坏性变更前，必须先与用户讨论具体收益、影响范围、数据迁移、兼容策略和回滚方式，并获得明确确认；未经确认只能分析和提出建议，不能直接修改实现。
- 用户确认破坏性方案后，按讨论结果实施，不要自行增加未约定的兼容层，也不要省略已约定的迁移或过渡措施。
- 以 SQLite 实现作为语义基准；内存实现和测试应尽量向 SQLite 行为对齐，即使内部实现较朴素也可以。
- 该后端服务面向授权请求。已通过授权的请求应被视为合法控制面请求；如果调用方显式指定状态、查询范围等参数，除非参数本身完全违背软件工程约束或会破坏内部不变量，否则应尊重请求语义。例如授权请求显式查询 `deleted` / `purged` 状态时，应返回对应文档，而不是因为它们不属于常规读取结果就额外隐藏。

## 架构概览

这是 ComicManager 的 Go HTTP 归档/采集服务。它负责按来源执行下载与归档流程，并维护文档元数据；页面和 manifest 等二进制对象通过对象存储接口抽象出去。

- [cmd/server/main.go](cmd/server/main.go) 负责服务装配：读取 `config.yml`、创建文档存储、注册 Hitomi/JMComic 来源工厂、按 `storages` 配置创建并注册命名对象存储实例、启动归档 worker，然后启动 HTTP 服务。
- [internal/config/](internal/config/) 读取配置。优先级为默认值 < `config.yml`；不读取环境变量。默认值包括 `addr=:8080`、`document_store=sqlite`、`sqlite_path=document-archive.db`、`default_storage=memory`、`deleted_sweep_interval=24h`、`allow_cors=[]`。设置 `auth_token` 后会启用 Bearer Token 鉴权。
- [internal/httpapi/](internal/httpapi/) 是 HTTP 层。它使用 Go 的 `http.ServeMux` 路由模式，并把业务操作委托给 `archive.App`。所有 `/v1/...` 业务路由在配置 token 后都会经过鉴权；`/healthz` 是公开接口。CORS 作为 mux 外层 middleware 统一处理，`allow_cors` 为空时不启用 CORS，非空时按显式 Origin 白名单或 `"*"` 匹配，预检请求会在鉴权前返回。
- [internal/archive/](internal/archive/) 是应用层。`App` 持有注册后的 `documents.Store`、来源工厂和按 `StorageName` 注册的对象存储。`RunWorker` 每秒轮询 queued 文档；处理单个文档时根据来源取得工厂、根据文档的 `storage_backend` 取得对象存储实例，再创建绑定对象存储和页面 hook 的任务级 handler。worker 还会在启动时及 `deleted_sweep_interval` 周期上扫描 `deleted` 文档，只有在文档对象前缀清理成功后才推进为 `purged`。
- [internal/documents/](internal/documents/) 定义公开文档模型和 store 接口。当前有两个实现：[internal/documents/store.go](internal/documents/store.go) 中的内存存储，以及 [internal/documents/sqlite_store.go](internal/documents/sqlite_store.go) 中的 SQLite 存储。SQLite 存储把文档和页面放在两张表中，使用 `document_status` 表达完整文档状态，并按全局唯一模型对 `(source, source_document_id)` 组合保持唯一约束。
- [internal/storage/](internal/storage/) 定义 `ObjectStore` 抽象。`StorageName` 表示配置实例名，`StorageType` 表示实现类型；`ObjectStore.Name()` 和 `Type()` 分别暴露两者。当前实现包括 `MemoryStore` 和 S3-compatible `S3Store`，同一类型可注册多个命名实例。
- [internal/sources/](internal/sources/) 包含来源抽象和公共 `ConcurrencyScheduler`；来源可通过 `ErrRateLimited` 及可选的 `RateLimitError` 退避时间向调度器反馈限流。每个来源工厂是 App 生命周期内的共享实例，持有 HTTP client、resolver、调度器等长期状态，并为每次归档创建短期 handler。[internal/sources/hitomi/](internal/sources/hitomi/) 的工厂共享 resolver 和公共限流调度器，任务级 handler 负责解析页面、下载或复用对象并发出页面更新。

## 请求流程

1. `POST /v1/documents/request` 通过 `archive.App.RequestDocument` 创建 `queued` 文档。
2. 后台 worker 通过显式 `queued` 状态查询获取待处理文档并逐个处理。
3. 处理时 archive app 通过来源工厂创建绑定当前对象存储和页面 hook 的任务级 handler，再通过 `TransitionTo` 把状态设为 `resolving`，调用 `ResolveDocument` 返回补充了来源元数据、标题和页面描述的 `Document`，并根据页数写入 `Progress.Total`。
4. 文档在 `Progress.Done < Progress.Total` 时会继续进入 `downloading`。进入内容处理前，archive app 调用 `Store.ResetPages` 清空当前页面元数据和 `Done`，然后以 OSS 为内容真相重建页面。页面对象 key 由 `document_id + hash` 确定；来源 handler 先通过 `HeadObject` 查询 OSS，命中时直接 `AddPage`，未命中时按 `PutObject -> AddPage` 的顺序下载并记录页面。
5. 内容处理完成后，archive app 从 Store 重新读取最新 `Document`，交给来源 handler 的 `ArchiveManifest` 写入 `manifest.json` 对象，再将状态变为 `archived`。失败时状态变为 `failed`，并记录错误信息。
6. 删除文档时状态变为 `deleted`；维护任务会在启动时补扫一次并在 `deleted_sweep_interval` 周期上继续扫描，按 `documents/{document_id}/` 前缀清理当前和历史对象成功后再推进为 `purged`。

## HTTP API 注意事项

- 已实现的路由包括：请求归档文档、按来源文档 ID 查询、按状态查询、获取文档、软删除、刷新、获取页面。
- `QueryByStatus` / 显式状态查询是授权控制面语义：调用方显式传入什么状态，就按该状态查询，包括 `deleted` / `purged`。这与 `Get` / 默认查询 / 按来源 ID 的隐式查询语义不同。
- `GET /v1/documents/{document_id}/manifest` 当前返回 `501 Not Implemented`。
- `GET /v1/documents/{document_id}/pages/{page_index}` 根据对象存储实例的 `StorageType` 分发：memory 直接返回对象内容，S3 返回预签名 GET URL 的 302 redirect。
- `POST /v1/documents/{document_id}/refresh` 当前支持三种 mode：`only_metadata` 重新入队但保留页面和进度；`all` 先通过 `TransitionTo` 重新入队，再调用 `Store.ResetPages` 清空 Pages 并把 `Done` 重置为 0，但不删除 OSS 对象；`restore` 仅用于通过 `Store.Restore` 恢复 `deleted` / `purged` 文档。前两种 mode 仍受常规状态流转图约束。
- CORS 由 `allow_cors` 控制。空列表表示不返回 CORS header；列表中可配置精确 Origin 或 `"*"`。CORS middleware 在鉴权前处理 OPTIONS preflight，因此浏览器预检不需要 Bearer Token。

## 文档状态与删除语义

- `DocumentStatus` 是文档的统一状态字段，当前状态包括：`queued`、`resolving`、`downloading`、`archived`、`failed`、`deleted`、`purged`。
- `Document.status` 是私有字段，外部通过 `Status()` 读取；JSON 输出通过自定义 `MarshalJSON` 导出为 `status`。
- 常规状态变更必须走 `Store.TransitionTo`，不要在 meta 更新或业务逻辑中直接覆盖状态字段。删除、清理和恢复分别使用 Store 的 `Delete`、`Purge` 和 `Restore` 专用操作。
- `source + source_document_id` 按全局唯一模型建模：整个 `documents` 集合中最多只能有一条对应记录，`deleted` / `purged` 记录也继续占用该身份。
- 因此默认 `Create` 只用于首次引入该来源文档；“重新加回”已删除/已清理文档必须调用 `Store.Restore`（HTTP 层对应 `POST /v1/documents/{document_id}/refresh?mode=restore`）复用原记录，而不是再次插入新记录。
- `TransitionTo` 允许任一已知状态到自身的幂等调用；除此之外的合法跨状态流转为：
  - `queued -> resolving | failed | deleted`
  - `resolving -> downloading | archived | failed | deleted`
  - `downloading -> archived | failed | deleted`
  - `archived -> queued | deleted`
  - `failed -> queued | deleted`
  - `deleted -> purged`
  - `purged` 对常规 `TransitionTo` 是终态
- `Store.Restore` 是独立的显式恢复操作，不走上述常规状态流转；它允许 `deleted -> queued` 和 `purged -> queued`。`deleted` 恢复时保留尚未清理的页面元数据，`purged` 恢复时页面和进度已被清空。
- `Get`、默认 `Query`、`QueryBuilder{}.BySourceDocumentID(...)`、`UpdateMeta`、页面增删等常规操作只处理 active 状态：`queued`、`resolving`、`downloading`、`archived`、`failed`。
- 显式状态查询允许返回 `deleted` / `purged`，用于授权查询和维护任务；在全局唯一模型下，这些查询看到的仍然是同一条实体记录，而不是历史版本列表。
- `Delete` 只将文档变为待清理的 `deleted` tombstone，页面元数据、进度和对象仍保留。维护任务删除完整文档对象前缀后，`Store.Purge` 会清空 `Pages` / `Progress.Done` / `Progress.Total` 并转为 `purged`。对象删除失败时文档必须保持 `deleted`，等待下次周期重试。

## 数据与存储注意事项

- 不同文档存储的 ID 语义不同：内存存储使用从 0 开始的 slice 下标，SQLite 使用自增 ID。
- 两种文档存储中的 `Delete` 都是软删除：将状态流转为 `deleted`。已删除文档不会出现在常规 `Get` / 默认 `Query` / 按来源 ID 的隐式查询中，但会在显式 `deleted` 状态查询中返回。
- SQLite 的 `documents.document_status` 有 CHECK 约束；`(source, source_document_id)` 在整张 `documents` 表上全局唯一。删除或清理后不会释放该身份，后续如需重新加入应调用 `Store.Restore`。
- `storages` 以 `StorageName` 为 key 声明对象存储实例，每项通过 `type` 指定 `memory` 或 `s3`；`default_storage` 和文档的 `storage_backend` 都引用实例名。服务注册配置中的全部实例，同一类型可配置多个实例。
- `deleted_sweep_interval` 控制后台清理 `deleted` 文档的周期；设为 `0` 可关闭周期清理，但程序启动时仍会补扫一次。
- `storage.ObjectInfo.ETag` 是对象抽象层暴露给 HTTP/browser 的 ETag。`PutObject` 输入携带 ETag 时 storage 原样保存并返回；未携带时优先使用具体后端提供的 ETag（例如 S3 原生 ETag），后端未提供时再由 storage 基于对象内容计算。S3 后端把自定义输入 ETag 写入 user metadata `archive-etag`，以便后续 `HEAD/GET` 能读回同一个值。
- 页面对象使用 `documents/{document_id}/pages/{hash}.{ext}` key，不再把 page index 写入 key。扩展名由页面 Content-Type 决定（WebP/JPEG/PNG/AVIF，其他类型回退为 `.bin`）。数据库 Pages 只表示当前页面顺序，OSS 作为 hash 寻址的内容缓存；全量刷新可以清空 Pages 后按新顺序重建，同 hash 且同类型的页面直接复用已有对象。
- SQLite 初始化会设置 WAL 模式、busy timeout、单连接和 foreign keys。文档/页面更新应保持事务化，避免页面 hook 与状态流转之间发生陈旧写入。
- `Progress.Done` 由 store 的 `AddPage` / `RemovePage`、`ResetPages` 和 `Purge` 维护；`Progress.Total` 由 archive app 根据来源 handler 返回的 `Document.Pages` 数量写入。`ResetPages` 只处理页面和进度，不改变文档状态。

## 提交说明约定

- 当用户要求按 diff 撰写 commit message 时，按影响范围从大到小组织，用中文描述。
- 只有用户明确要求时才添加 `manual:` 前缀；如果要求“不加 manual”，不要添加。
- 如一次提交中有 AI 直接撰写的实现和用户手写实现，应按用户要求在 commit message 中区分来源。
