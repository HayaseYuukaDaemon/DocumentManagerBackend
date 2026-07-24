# document-archive

`document-archive` 是 ComicManager 的 Go 归档/采集服务。它负责解析来源元数据、下载和归档页面、维护任务状态并生成文档快照；页面等二进制对象由命名的 memory 或 S3-compatible 对象存储实例保存。

当前支持的来源：

- Hitomi
- JMComic（仅支持单章节作品）

## 项目状态

本项目已经投入生产使用。涉及数据库 schema、HTTP API、配置格式或既有运行行为的破坏性变更，需要在实施前明确讨论收益、影响、迁移和回滚方案并获得确认。能够简化实现、改善架构或提升健壮性的破坏性方案仍然欢迎提出。

## 功能概览

- 基于 `http.ServeMux` 的 HTTP API
- 基于 token、角色和路由权限的可选鉴权
- SQLite 文档存储，以及行为对齐的内存实现
- queued 归档 worker 和 deleted 清理 worker
- 支持多个命名实例的 memory 与 S3-compatible 对象存储
- 页面 hash 寻址、对象复用和全量刷新
- deleted、purged、restore 文档生命周期
- 显式 Origin 白名单或通配符 CORS

## 运行

要求 Go 1.25.5 或兼容版本。

```bash
go run ./cmd/server
```

构建和测试：

```bash
go build -o bin/document-archive ./cmd/server
go test ./...
```

服务默认监听 `:8080`，文档元数据默认写入当前目录下的 `document-archive.db`。

## 配置

配置只来自内置默认值和当前工作目录下的 `config.yml`，不读取环境变量。首次启动时如果 `config.yml` 不存在，服务会以 `0600` 权限创建默认配置并立即应用。文件中的字段覆盖内置默认值，未配置字段继续使用默认值。

完整示例见 [`internal/config/config.demo.yml`](internal/config/config.demo.yml)。

如果手头只有部分配置，可以先用合并工具补齐未声明字段：

```bash
go run ./tools/config_merge.go -in config.partial.yml -out config.yml
```

工具以程序内置默认值为底合并配置并输出完整 YAML；服务本身仍要求 `config.yml` 包含全部必要字段。

```yaml
addr: ":8080"
log_level: "info"
default_storage: "memory"
document_store: "sqlite"
sqlite_path: "document-archive.db"
deleted_sweep_interval: "24h"
allow_cors: []

storages:
  memory:
    type: "memory"
  minio:
    type: "s3"
    s3:
      internal_endpoint: ""
      endpoint: "http://127.0.0.1:9000"
      bucket: "archive"
      region: "us-east-1"
      access_key_id: "minioadmin"
      secret_access_key: "minioadmin"
      session_token: ""
      use_path_style: true

role:
  admin-token:
    name: "admin"
    admin: true
  contributor-token:
    name: "contributor"
    permissions:
      - "document:create"
      - "document:read"
  viewer-token:
    name: "viewer"
    permissions:
      - "document:read"
```

主要配置项：

- `document_store`：`sqlite` 或 `memory`。
- `default_storage`：未在归档请求中指定存储时使用的对象存储实例名，必须存在于 `storages` 中。
- `storages`：以实例名为 key 的对象存储映射；每个实例通过 `type` 声明实现类型，目前支持 `memory` 和 `s3`。
- `deleted_sweep_interval`：deleted 文档的周期清理间隔；设为 `0` 可关闭周期清理，但启动时仍会补扫一次。
- `allow_cors`：允许的 Origin 列表；空列表表示不启用 CORS，支持精确 Origin 和 `"*"`。
- `role`：以 Bearer Token 为 key 的角色映射；为空时不启用鉴权。

服务会注册 `storages` 中声明的全部实例。同一实现类型可以有多个实例，例如 `minio` 与 `r2` 都可声明为 `type: s3`，文档的 `storage_backend` 保存所选实例名。Cloudflare R2、AWS S3 通常使用 virtual-hosted-style；MinIO 等服务可设置 `use_path_style: true`。

从旧配置升级时，需要把顶层 `s3` 配置移动到 `storages.<实例名>.s3`，并让 `default_storage` 引用该实例名。数据库 schema 和 HTTP 字段不变，`storage_backend` 的值从固定实现类型明确为 `storages` 中的实例名。

## 权限

业务 API 使用 `Authorization: Bearer <token>`。`admin: true` 的角色拥有全部权限，普通角色可配置：

- `document:create`
- `document:update`
- `document:delete`
- `document:read`
- `document:refresh`

当前路由权限：

- 请求归档：`document:create`
- 查询文档、读取文档和页面：`document:read`
- 删除文档：`document:delete`
- 刷新或恢复文档：`document:refresh`
- `/healthz`：公开

已配置角色但 token 缺失或无效时返回 `401`；角色缺少路由权限时返回 `403`。CORS 预检在鉴权之前处理。

## API

以下示例假定服务运行在 `http://localhost:8080`，并使用示例管理员 token：

```bash
API=http://localhost:8080
TOKEN=admin-token
```

### 健康检查

```bash
curl "$API/healthz"
```

### 请求归档

```bash
curl -X POST "$API/v1/documents/request" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"source":"hitomi","source_document_id":"3886065"}'
```

JMComic 请求将 `source` 改为 `jmcomic`。请求体还可通过 `storage_backend` 为单个文档指定 `storages` 中注册的实例名。

### 获取文档

```bash
curl "$API/v1/documents/<document_id>" \
  -H "Authorization: Bearer $TOKEN"
```

### 按来源 ID 查询

```bash
curl -X POST "$API/v1/documents/query" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "mode":"by_source_document_id",
    "params":{"source":"hitomi","source_document_id":"3886065"}
  }'
```

### 按状态查询

```bash
curl -X POST "$API/v1/documents/query" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "mode":"by_status",
    "params":{"status":"archived"},
    "orderby":"updated_at",
    "order":"DESC",
    "limit":20
  }'
```

显式状态查询支持 `deleted` 和 `purged`。可用排序字段为 `id`、`created_at`、`updated_at`，排序方向为 `ASC` 或 `DESC`。

### 获取页面

```bash
curl -L "$API/v1/documents/<document_id>/pages/<page_index>" \
  -H "Authorization: Bearer $TOKEN" \
  -o page.bin
```

`type: memory` 的实例由服务直接返回对象内容；`type: s3` 的实例返回有效期 24 小时的预签名 GET URL，并通过 `302` 跳转。

### 刷新和恢复

```bash
curl -X POST "$API/v1/documents/<document_id>/refresh?mode=all" \
  -H "Authorization: Bearer $TOKEN"
```

支持的 mode：

- `only_metadata`：重新入队并解析元数据，保留现有页面和进度。
- `all`：重新入队，清空当前页面元数据和 `Progress.Done`；不删除对象存储中的 hash 对象，worker 可直接复用。
- `restore`：将 `deleted` 或 `purged` 文档恢复为 queued，继续使用原文档 ID。

### 删除文档

```bash
curl -X DELETE "$API/v1/documents/<document_id>" \
  -H "Authorization: Bearer $TOKEN"
```

删除首先把文档标记为 `deleted`。后台维护任务清理 `documents/{document_id}/` 对象前缀成功后，才会清空页面和进度并推进为 `purged`；对象删除失败时保持 `deleted`，等待下一次重试。

## 文档生命周期

文档状态包括：

```text
queued -> resolving -> downloading -> archived
                  \-> failed

active -> deleted -> purged
deleted/purged --restore--> queued
```

同一 `(source, source_document_id)` 在整个文档集合中全局唯一，deleted 和 purged 记录也继续占用该身份。重新加入已删除文档必须使用 `restore`，不能再次创建新记录。

## 页面与对象存储

数据库中的 `Document.Pages` 表示当前页面顺序；对象存储作为 hash 寻址的内容缓存。页面 key 格式为：

```text
documents/{document_id}/pages/{hash}.{ext}
```

全量刷新会清空数据库页面记录，再通过 `HEAD` 检查并复用已存在的 hash 对象。页面对象写入成功后才记录页面进度。文档归档完成时还会写入 `documents/{document_id}/manifest.json` 对象；当前没有公开的 Manifest HTTP 路由。

## 项目结构

- `cmd/server/`：服务装配、worker 和 HTTP server 启动。
- `internal/archive/`：归档流程、刷新、清理、来源工厂和存储注册；worker 按文档创建任务级来源 handler。
- `internal/documents/`：文档模型、状态机以及 memory/SQLite store。
- `internal/httpapi/`：路由、鉴权、CORS 和响应。
- `internal/sources/`：来源工厂、handler 及公共自适应并发调度器；工厂持有共享 client/resolver/scheduler，handler 绑定单次归档的对象存储。
- `internal/storage/`：命名对象存储实例、存储类型以及 memory/S3-compatible 实现。
