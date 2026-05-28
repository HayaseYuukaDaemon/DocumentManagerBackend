# TODO

## Near Term

- Change `replacePagesTx` to `upsertPagesTx`.
  - Use `(document_id, page_index)` as the page identity.
  - Insert pages that do not exist, update pages that already exist, and keep existing pages that are not included in the current update.
  - Persist `Page.Hash` in SQLite so download validation can reuse existing page metadata.
- Review `internal/documents/sqlite_store.go`.
  - Check transaction boundaries, page update semantics, timestamp updates, and duplicated helper logic.
  - Confirm document updates cannot accidentally erase existing page rows.
- Implement `S3Storage`.
  - Compare the current `ObjectStore` interface against S3 semantics before coding.
  - Add configuration for endpoint, bucket, region, credentials, and path-style/virtual-host style behavior.
