# document-archive

`document-archive` is the Go archive/ingest service for ComicManager.

It owns source-specific download and archive workflows. Actual binary storage is delegated to an S3-compatible object store such as Cloudflare R2, MinIO, or AWS S3.

## Current scope

- HTTP API skeleton
- Bearer-token auth middleware
- SQLite document store with in-memory option
- Document status worker
- Memory and S3-compatible ObjectStore backends
- Hitomi resolver and source handler

## Run

```bash
ARCHIVE_ADDR=:8080 go run ./cmd/server
```

Document metadata is stored in `document-archive.db` by default.

Configuration is loaded from defaults, then `config.yml`, then environment variables. Environment variables always win over `config.yml`.

Example `config.yml`:

```yaml
addr: ":8080"
auth_token: "dev-secret"
log_level: "info"
default_storage: "s3"
document_store: "sqlite"
sqlite_path: "document-archive.db"
s3:
  endpoint: "https://<account-id>.r2.cloudflarestorage.com"
  bucket: "document-archive"
  region: "auto"
  access_key_id: "..."
  secret_access_key: "..."
  session_token: ""
  use_path_style: false
```

Optional environment overrides:

```bash
ARCHIVE_TOKEN=dev-secret go run ./cmd/server
ARCHIVE_DEFAULT_STORAGE=memory go run ./cmd/server
ARCHIVE_DOCUMENT_STORE=memory go run ./cmd/server
ARCHIVE_SQLITE_PATH=/var/lib/document-archive/documents.db go run ./cmd/server
```

S3-compatible object storage:

```bash
ARCHIVE_DEFAULT_STORAGE=s3 \
ARCHIVE_S3_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com \
ARCHIVE_S3_BUCKET=document-archive \
ARCHIVE_S3_REGION=auto \
ARCHIVE_S3_ACCESS_KEY_ID=... \
ARCHIVE_S3_SECRET_ACCESS_KEY=... \
go run ./cmd/server
```

For MinIO or other path-style services, also set:

```bash
ARCHIVE_S3_USE_PATH_STYLE=true
```

## API sketch

Request a document archive:

```bash
curl -X POST http://localhost:8080/v1/documents/request \
  -H 'Content-Type: application/json' \
  -d '{"source":"hitomi","source_document_id":"3886065"}'
```

Get a document:

```bash
curl http://localhost:8080/v1/documents/<document_id>
```

Query documents by source metadata:

```bash
curl -X POST http://localhost:8080/v1/documents/query \
  -H 'Content-Type: application/json' \
  -d '{"mode":"by_source_document_id","params":{"source":"hitomi","source_document_id":"3886065"}}'
```

Soft-remove a document:

```bash
curl -X DELETE http://localhost:8080/v1/documents/<document_id>
```
