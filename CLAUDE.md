# CLAUDE.md

本文件为 Claude Code (claude.ai/code) 在此仓库中工作提供指导。

## 常用命令

- 启动服务：`go run ./cmd/server`
- 构建服务：`go build -o bin/document-archive ./cmd/server`
- 运行全部测试：`go test ./...`
- 运行单个包的测试：`go test ./internal/documents`
- 运行单个测试或一组测试：`go test ./internal/documents -run TestSQLiteStore`
- 提交前格式化 Go 代码：`gofmt -w <files>`

配置只从默认值和当前工作目录下的 `config.yml` 读取，不再读取环境变量。需要修改监听地址、鉴权 token、文档存储、默认对象存储、S3 参数或 CORS 时，直接编辑 `config.yml`。

配置示例见 [internal/config/config.demo.yml](internal/config/config.demo.yml)。

## 开发阶段原则

- 项目仍处于极早期开发阶段，尚未应用于生产环境；可以接受破坏性变更。
- 优先保持架构、数据模型和实现简单易懂；除非明确要求，不要为旧数据库、旧 API 或旧行为添加兼容迁移/兼容层。
- 如果 schema 或接口变更能明显提升简洁性和易用性，直接修改目标结构即可，不需要保留历史包袱。
- 以 SQLite 实现作为语义基准；内存实现和测试应尽量向 SQLite 行为对齐，即使内部实现较朴素也可以。
- 该后端服务面向授权请求。已通过授权的请求应被视为合法控制面请求；如果调用方显式指定状态、查询范围等参数，除非参数本身完全违背软件工程约束或会破坏内部不变量，否则应尊重请求语义。例如授权请求显式查询 `deleted` / `purged` 状态时，应返回对应文档，而不是因为它们不属于常规读取结果就额外隐藏。

## 架构概览

这是 ComicManager 的 Go HTTP 归档/采集服务。它负责按来源执行下载与归档流程，并维护文档元数据；页面和 manifest 等二进制对象通过对象存储接口抽象出去。

- [cmd/server/main.go](cmd/server/main.go) 负责服务装配：读取 `config.yml`、创建文档存储、注册 Hitomi 来源处理器和内存对象存储、按配置注册 S3-compatible 对象存储、启动归档 worker，然后启动 HTTP 服务。
- [internal/config/](internal/config/) 读取配置。优先级为默认值 < `config.yml`；不读取环境变量。默认值包括 `addr=:8080`、`document_store=sqlite`、`sqlite_path=document-archive.db`、`default_storage=memory`、`allow_cors=[]`。设置 `auth_token` 后会启用 Bearer Token 鉴权。
- [internal/httpapi/](internal/httpapi/) 是 HTTP 层。它使用 Go 的 `http.ServeMux` 路由模式，并把业务操作委托给 `archive.App`。所有 `/v1/...` 业务路由在配置 token 后都会经过鉴权；`/healthz` 是公开接口。CORS 作为 mux 外层 middleware 统一处理，`allow_cors` 为空时不启用 CORS，非空时按显式 Origin 白名单或 `"*"` 匹配，预检请求会在鉴权前返回。
- [internal/archive/](internal/archive/) 是应用层。`App` 持有注册后的 `documents.Store`、来源处理器和对象存储。`RunWorker` 每秒轮询 queued 文档，解析元数据、下载内容、通过页面下载 hook 写入页面更新，并推进文档状态。
- [internal/documents/](internal/documents/) 定义公开文档模型和 store 接口。当前有两个实现：[internal/documents/store.go](internal/documents/store.go) 中的内存存储，以及 [internal/documents/sqlite_store.go](internal/documents/sqlite_store.go) 中的 SQLite 存储。SQLite 存储把文档和页面放在两张表中，使用 `document_status` 表达完整文档状态，并对 active 状态下的 `(source, source_document_id)` 组合保持唯一约束。
- [internal/storage/](internal/storage/) 定义 `ObjectStore` 抽象。当前实现包括 `MemoryStore` 和 S3-compatible `S3Store`。memory 对象保存在进程内存中，S3 对象通过 AWS SDK v2 访问，可配置 endpoint、bucket、region、credentials 和 path-style 行为。
- [internal/sources/](internal/sources/) 包含来源抽象。[internal/sources/hitomi/](internal/sources/hitomi/) 是当前的 Hitomi handler/resolver：获取图库元数据、解析页面下载 URL、下载页面、写入对象，并向 archive app 发出页面更新。

## 请求流程

1. `POST /v1/documents/request` 通过 `archive.App.RequestDocument` 创建 `queued` 文档。
2. 后台 worker 调用 `ListByStatus(queued, 5)` 获取待处理文档并逐个处理。
3. 处理时先通过 `TransitionTo` 把状态设为 `resolving`，调用来源 handler 生成 manifest，并根据 manifest 页数提前写入 `Progress.Total`。
4. 首次下载内容时状态进入 `downloading`；每个已下载页面都会通过来源 handler 的 page-download hook 增量写入文档存储，并由 store 维护 `Progress.Done`。
5. 成功后状态变为 `archived`；失败后状态变为 `failed`，并记录错误信息。
6. 删除文档时状态变为 `deleted`；维护任务后续可将其推进为 `purged`。

## HTTP API 注意事项

- 已实现的路由包括：请求归档文档、按来源文档 ID 查询、按状态查询、获取文档、软删除、刷新、获取页面。
- `QueryByStatus` / `ListByStatus` 是授权控制面语义：调用方显式传入什么状态，就按该状态查询，包括 `deleted` / `purged`。这与 `Get` / `GetBySourceDocumentID` 的常规读取语义不同。
- `GET /v1/documents/{document_id}/manifest` 当前返回 `501 Not Implemented`。
- `GET /v1/documents/{document_id}/pages/{page_index}` 对 memory storage backend 直接返回对象内容；对 S3 等非 memory backend 返回预签名 GET URL 的 302 redirect。
- CORS 由 `allow_cors` 控制。空列表表示不返回 CORS header；列表中可配置精确 Origin 或 `"*"`。CORS middleware 在鉴权前处理 OPTIONS preflight，因此浏览器预检不需要 Bearer Token。

## 文档状态与删除语义

- `DocumentStatus` 是文档的统一状态字段，当前状态包括：`queued`、`resolving`、`downloading`、`archived`、`failed`、`deleted`、`purged`。
- `Document.status` 是私有字段，外部通过 `Status()` 读取；JSON 输出通过自定义 `MarshalJSON` 导出为 `status`。
- 状态变更必须走 `Store.TransitionTo`，不要在 meta 更新或业务逻辑中直接覆盖状态字段。
- 当前合法状态流转为：
  - `queued -> resolving | failed | deleted`
  - `resolving -> downloading | archived | failed | deleted`
  - `downloading -> archived | failed | deleted`
  - `archived -> queued | deleted`
  - `failed -> queued | deleted`
  - `deleted -> purged`
  - `purged` 为终态
- `Get`、`GetBySourceDocumentID`、`UpdateMeta`、页面增删等常规操作只处理 active 状态：`queued`、`resolving`、`downloading`、`archived`、`failed`。
- `ListByStatus` 是显式状态查询，允许返回 `deleted` / `purged`，用于授权查询和维护任务。

## 数据与存储注意事项

- 不同文档存储的 ID 语义不同：内存存储使用从 0 开始的 slice 下标，SQLite 使用自增 ID。
- 两种文档存储中的 `Remove` 都是软删除：将状态流转为 `deleted`。已删除文档不会出现在常规 `Get` / `GetBySourceDocumentID` 中，但会在显式 `ListByStatus(deleted, ...)` 中返回。
- SQLite 的 `documents.document_status` 有 CHECK 约束；active 状态下的 `(source, source_document_id)` 有唯一索引，删除或清理后可以重新创建同来源同 ID 文档。
- 当前服务总会注册 memory object store；当配置 `s3.bucket` 或 `default_storage=s3` 时也会注册 S3-compatible object store。
- `storage.ObjectInfo.ETag` 是对象抽象层暴露给 HTTP/browser 的 ETag。`PutObject` 输入携带 ETag 时 storage 原样保存并返回；未携带时优先使用具体后端提供的 ETag（例如 S3 原生 ETag），后端未提供时再由 storage 基于对象内容计算。S3 后端把自定义输入 ETag 写入 user metadata `archive-etag`，以便后续 `HEAD/GET` 能读回同一个值。
- SQLite 初始化会设置 WAL 模式、busy timeout、单连接和 foreign keys。文档/页面更新应保持事务化，避免页面 hook 与状态流转之间发生陈旧写入。
- `Progress.Done` 由 store 的 `AddPage` / `RemovePage` 维护；`Progress.Total` 当前由 archive app 根据 manifest 页数写入。后续如需进一步收窄权限，可提供专门的 progress API。

## 提交说明约定

- 当用户要求按 diff 撰写 commit message 时，按影响范围从大到小组织，用中文描述。
- 只有用户明确要求时才添加 `manual:` 前缀；如果要求“不加 manual”，不要添加。
- 如一次提交中有 AI 直接撰写的实现和用户手写实现，应按用户要求在 commit message 中区分来源。
