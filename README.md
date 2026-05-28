# document-archive

`document-archive` is the Go archive/ingest service for ComicManager.

It owns source-specific download and archive workflows. Actual binary storage is delegated to an S3-compatible object store such as Cloudflare R2, MinIO, or AWS S3.

## Current scope

- HTTP API skeleton
- Bearer-token auth middleware
- SQLite document store with in-memory option
- Document status worker
- ObjectStore interface
- Hitomi resolver and source handler

## Run

```bash
ARCHIVE_ADDR=:8080 go run ./cmd/server
```

Document metadata is stored in `document-archive.db` by default.

Optional config:

```bash
ARCHIVE_TOKEN=dev-secret go run ./cmd/server
ARCHIVE_DEFAULT_STORAGE=memory go run ./cmd/server
ARCHIVE_DOCUMENT_STORE=memory go run ./cmd/server
ARCHIVE_SQLITE_PATH=/var/lib/document-archive/documents.db go run ./cmd/server
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
