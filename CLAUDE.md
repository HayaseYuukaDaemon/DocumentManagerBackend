# CLAUDE.md

本文件为 Claude Code (claude.ai/code) 在此仓库中工作提供指导。

## 常用命令

- 启动服务：`ARCHIVE_ADDR=:8080 go run ./cmd/server`
- 启动服务并启用可选 Bearer Token：`ARCHIVE_TOKEN=dev-secret go run ./cmd/server`
- 开发时使用内存后端：`ARCHIVE_DOCUMENT_STORE=memory ARCHIVE_DEFAULT_STORAGE=memory go run ./cmd/server`
- 使用指定 SQLite 数据库路径：`ARCHIVE_DOCUMENT_STORE=sqlite ARCHIVE_SQLITE_PATH=/path/to/documents.db go run ./cmd/server`
- 构建服务：`go build -o bin/document-archive ./cmd/server`
- 运行全部测试：`go test ./...`
- 运行单个包的测试：`go test ./internal/documents`
- 运行单个测试或一组测试：`go test ./internal/documents -run TestSQLiteStore`
- 提交前格式化 Go 代码：`gofmt -w <files>`

## 开发阶段原则

- 项目仍处于极早期开发阶段，尚未应用于生产环境；可以接受破坏性变更。
- 优先保持架构、数据模型和实现简单易懂；除非明确要求，不要为旧数据库、旧 API 或旧行为添加兼容迁移/兼容层。
- 如果 schema 或接口变更能明显提升简洁性和易用性，直接修改目标结构即可，不需要保留历史包袱。

## 架构概览

这是 ComicManager 的 Go HTTP 归档/采集服务。它负责按来源执行下载与归档流程，并维护文档元数据；页面和 manifest 等二进制对象通过对象存储接口抽象出去。

- [cmd/server/main.go](cmd/server/main.go) 负责服务装配：读取环境配置、创建文档存储、注册 Hitomi 来源处理器和内存对象存储、启动归档 worker，然后启动 HTTP 服务。
- [internal/config/](internal/config/) 读取环境配置。默认值包括 `ARCHIVE_ADDR=:8080`、`ARCHIVE_DOCUMENT_STORE=sqlite`、`ARCHIVE_SQLITE_PATH=document-archive.db`、`ARCHIVE_DEFAULT_STORAGE=memory`。设置 `ARCHIVE_TOKEN` 后会启用 Bearer Token 鉴权。
- [internal/httpapi/](internal/httpapi/) 是 HTTP 层。它使用 Go 的 `http.ServeMux` 路由模式，并把业务操作委托给 `archive.App`。所有 `/v1/...` 路由在配置 token 后都会经过鉴权；`/healthz` 是公开接口。
- [internal/archive/](internal/archive/) 是应用层。`App` 持有注册后的 `documents.Store`、来源处理器和对象存储。`RunWorker` 每秒轮询 queued 文档，解析元数据、下载内容、通过页面下载 hook 写入页面更新，并推进文档状态。
- [internal/documents/](internal/documents/) 定义公开文档模型和 store 接口。当前有两个实现：[internal/documents/store.go](internal/documents/store.go) 中的内存存储，以及 [internal/documents/sqlite_store.go](internal/documents/sqlite_store.go) 中的 SQLite 存储。SQLite 存储把文档和页面放在两张表中，使用软删除，并对未删除的 `(source, source_document_id)` 组合保持唯一约束。
- [internal/storage/](internal/storage/) 定义 `ObjectStore` 抽象。当前实现是 `MemoryStore`，对象保存在进程内存中，并支持页面接口直接读取对象内容。
- [internal/sources/](internal/sources/) 包含来源抽象。[internal/sources/hitomi/](internal/sources/hitomi/) 是当前的 Hitomi handler/resolver：获取图库元数据、解析页面下载 URL、下载页面、写入对象，并向 archive app 发出页面更新。

## 请求流程

1. `POST /v1/documents/request` 通过 `archive.App.RequestDocument` 创建 queued 文档。
2. 后台 worker 调用 `ListByStatus(queued, 5)` 获取待处理文档并逐个处理。
3. 处理时先把状态设为 `resolving`，调用来源 handler 生成 manifest，然后把状态设为 `downloading` 并按需下载页面。
4. 每个已下载页面都会通过来源 handler 的 page-download hook 增量写入文档存储。
5. 成功后状态变为 `archived`；失败后状态变为 `failed`，并记录错误信息。

## HTTP API 注意事项

- 已实现的路由包括：请求归档文档、按来源文档 ID 查询、获取文档、软删除、刷新、获取页面。
- `GET /v1/documents/{document_id}/manifest` 当前返回 `501 Not Implemented`。
- `GET /v1/documents/{document_id}/pages/{page_index}` 当前只支持 memory storage backend 的直接对象返回；其他存储后端尚未实现。

## 数据与存储注意事项

- 不同文档存储的 ID 语义不同：内存存储使用从 0 开始的 slice 下标，SQLite 使用自增 ID。
- 两种文档存储中的 `Remove` 都是软删除；已删除文档不会出现在常规读取和查询结果中。
- 当前服务只注册了 memory object store，即使文档元数据持久化在 SQLite 中，页面对象也仍然只在内存对象存储中。
- SQLite 初始化会设置 WAL 模式、busy timeout、单连接和 foreign keys。文档/页面更新应保持事务化，避免页面 hook 与状态流转之间发生陈旧写入。
