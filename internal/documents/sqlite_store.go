package documents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"document-archive/internal/sources"
	"document-archive/internal/storage"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens the SQLite file and makes sure the schema exists.
// The returned store owns db; callers should call Close during service shutdown.
func NewSQLiteStore(ctx context.Context, path string) (*SQLiteStore, error) {
	if path == "" {
		path = "document-archive.db"
	}
	if path != ":memory:" && !isSQLiteURI(path) {
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, err
			}
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite allows concurrent readers, but only one writer at a time. Keeping one
	// database connection makes transaction behavior predictable for this service.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &SQLiteStore{db: db}
	if err := store.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) init(ctx context.Context) error {
	statements := []string{
		// foreign_keys is disabled by default in SQLite; enable it so page rows stay
		// attached to valid document rows.
		`PRAGMA foreign_keys = ON`,
		// WAL lets readers continue while another transaction is writing, which fits
		// the worker-plus-HTTP-access pattern better than the default rollback journal.
		`PRAGMA journal_mode = WAL`,
		// Let SQLite wait briefly when the database is locked instead of failing
		// immediately under a request/worker race.
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS documents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			source_document_id TEXT NOT NULL,
			source_meta TEXT,
			title TEXT NOT NULL DEFAULT '',
			storage_backend TEXT NOT NULL DEFAULT '',
			archive_status TEXT NOT NULL,
			progress_done INTEGER NOT NULL DEFAULT 0,
			progress_total INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			removed INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(source, source_document_id)
		)`,
		// Pages are stored separately because page hooks update them one by one. A
		// separate table avoids rewriting large JSON blobs during downloads.
		`CREATE TABLE IF NOT EXISTS document_pages (
			document_id INTEGER NOT NULL,
			page_index INTEGER NOT NULL,
			object_key TEXT NOT NULL,
			content_type TEXT NOT NULL,
			size INTEGER NOT NULL,
			PRIMARY KEY(document_id, page_index),
			FOREIGN KEY(document_id) REFERENCES documents(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_status ON documents(archive_status, removed, id)`,
		`CREATE INDEX IF NOT EXISTS idx_document_pages_document ON document_pages(document_id, page_index)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) Create(ctx context.Context, document Document) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, err
	}
	defer rollback(tx)

	existing, err := getBySourceDocumentIDTx(ctx, tx, document.Source, document.SourceDocumentID, true)
	switch {
	case err == nil && !existing.Removed:
		return existing, ErrAlreadyExists
	case err == nil && existing.Removed:
		// Source identity is unique. If the previous row was soft-deleted, reuse its
		// primary key instead of inserting another row with the same source identity.
		now := time.Now().UTC()
		document.ID = existing.ID
		document.CreatedAt = now
		document.UpdatedAt = now
		document.Removed = false
		if err := updateDocumentTx(ctx, tx, document); err != nil {
			return Document{}, err
		}
		if err := replacePagesTx(ctx, tx, document.ID, document.Pages); err != nil {
			return Document{}, err
		}
		if err := tx.Commit(); err != nil {
			return Document{}, err
		}
		return document, nil
	case err != nil && !errors.Is(err, ErrNotFound):
		return Document{}, err
	}

	now := time.Now().UTC()
	document.CreatedAt = now
	document.UpdatedAt = now
	document.Removed = false

	result, err := tx.ExecContext(ctx, `INSERT INTO documents (
		source, source_document_id, source_meta, title, storage_backend, archive_status,
		progress_done, progress_total, error, removed, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(document.Source),
		document.SourceDocumentID,
		rawJSONToNullString(document.SourceMeta),
		document.Title,
		string(document.StorageBackend),
		string(document.ArchiveStatus),
		document.Progress.Done,
		document.Progress.Total,
		document.Error,
		boolToInt(document.Removed),
		formatTime(document.CreatedAt),
		formatTime(document.UpdatedAt),
	)
	if err != nil {
		return Document{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Document{}, err
	}
	document.ID = int(id)

	if err := replacePagesTx(ctx, tx, document.ID, document.Pages); err != nil {
		return Document{}, err
	}
	if err := tx.Commit(); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *SQLiteStore) Get(ctx context.Context, id int) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Document{}, err
	}
	defer rollback(tx)

	document, err := getTx(ctx, tx, id, false)
	if err != nil {
		return Document{}, err
	}
	if err := tx.Commit(); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *SQLiteStore) GetBySourceDocumentID(ctx context.Context, source sources.SourceType, sourceDocumentID string) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Document{}, err
	}
	defer rollback(tx)

	document, err := getBySourceDocumentIDTx(ctx, tx, source, sourceDocumentID, false)
	if err != nil {
		return Document{}, err
	}
	if err := tx.Commit(); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *SQLiteStore) Remove(ctx context.Context, id int) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, err
	}
	defer rollback(tx)

	document, err := getTx(ctx, tx, id, false)
	if err != nil {
		return Document{}, err
	}
	document.Removed = true
	document.UpdatedAt = time.Now().UTC()
	if err := updateDocumentTx(ctx, tx, document); err != nil {
		return Document{}, err
	}
	if err := tx.Commit(); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *SQLiteStore) ListByStatus(ctx context.Context, status ArchiveStatus, limit int) ([]Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	rows, err := tx.QueryContext(ctx, `SELECT
		id, source, source_document_id, source_meta, title, storage_backend, archive_status,
		progress_done, progress_total, error, removed, created_at, updated_at
		FROM documents
		WHERE archive_status = ? AND removed = 0
		ORDER BY id
		LIMIT ?`, string(status), limit)
	if err != nil {
		return nil, err
	}

	result := make([]Document, 0, limit)
	for rows.Next() {
		document, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, document)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// Load pages after closing the document rows cursor. With one SQLite connection,
	// keeping a cursor open while issuing another query can unnecessarily serialize
	// or block follow-up statements.
	for i := range result {
		pages, err := listPagesTx(ctx, tx, result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].Pages = pages
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *SQLiteStore) Update(ctx context.Context, id int, fn func(*Document) error) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	if fn == nil {
		return Document{}, errors.New("document update callback is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, err
	}
	defer rollback(tx)

	document, err := getTx(ctx, tx, id, true)
	if err != nil {
		return Document{}, err
	}
	// The callback runs inside the same transaction as the read and write. This
	// keeps hook updates and status transitions from overwriting each other with
	// stale document snapshots.
	if err := fn(&document); err != nil {
		return Document{}, err
	}
	document.ID = id
	document.UpdatedAt = time.Now().UTC()

	if err := updateDocumentTx(ctx, tx, document); err != nil {
		return Document{}, err
	}
	if err := replacePagesTx(ctx, tx, document.ID, document.Pages); err != nil {
		return Document{}, err
	}
	if err := tx.Commit(); err != nil {
		return Document{}, err
	}
	return document, nil
}

func getTx(ctx context.Context, tx *sql.Tx, id int, includeRemoved bool) (Document, error) {
	query := `SELECT
		id, source, source_document_id, source_meta, title, storage_backend, archive_status,
		progress_done, progress_total, error, removed, created_at, updated_at
		FROM documents WHERE id = ?`
	if !includeRemoved {
		query += ` AND removed = 0`
	}

	document, err := scanDocument(tx.QueryRowContext(ctx, query, id))
	if err != nil {
		return Document{}, err
	}
	document.Pages, err = listPagesTx(ctx, tx, document.ID)
	if err != nil {
		return Document{}, err
	}
	return document, nil
}

func getBySourceDocumentIDTx(ctx context.Context, tx *sql.Tx, source sources.SourceType, sourceDocumentID string, includeRemoved bool) (Document, error) {
	query := `SELECT
		id, source, source_document_id, source_meta, title, storage_backend, archive_status,
		progress_done, progress_total, error, removed, created_at, updated_at
		FROM documents WHERE source = ? AND source_document_id = ?`
	if !includeRemoved {
		query += ` AND removed = 0`
	}

	document, err := scanDocument(tx.QueryRowContext(ctx, query, string(source), sourceDocumentID))
	if err != nil {
		return Document{}, err
	}
	document.Pages, err = listPagesTx(ctx, tx, document.ID)
	if err != nil {
		return Document{}, err
	}
	return document, nil
}

type documentScanner interface {
	Scan(dest ...any) error
}

// scanDocument maps the flat documents table into the public Document model.
// Page rows are loaded separately by callers because they live in document_pages.
func scanDocument(scanner documentScanner) (Document, error) {
	var document Document
	var source string
	var sourceMeta sql.NullString
	var storageBackend string
	var archiveStatus string
	var removed int
	var createdAt string
	var updatedAt string

	err := scanner.Scan(
		&document.ID,
		&source,
		&document.SourceDocumentID,
		&sourceMeta,
		&document.Title,
		&storageBackend,
		&archiveStatus,
		&document.Progress.Done,
		&document.Progress.Total,
		&document.Error,
		&removed,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, err
	}

	document.Source = sources.SourceType(source)
	if sourceMeta.Valid && sourceMeta.String != "" {
		document.SourceMeta = []byte(sourceMeta.String)
	}
	document.StorageBackend = storage.StorageName(storageBackend)
	document.ArchiveStatus = ArchiveStatus(archiveStatus)
	document.Removed = removed != 0

	document.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Document{}, fmt.Errorf("parse created_at: %w", err)
	}
	document.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Document{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return document, nil
}

func updateDocumentTx(ctx context.Context, tx *sql.Tx, document Document) error {
	result, err := tx.ExecContext(ctx, `UPDATE documents SET
		source = ?,
		source_document_id = ?,
		source_meta = ?,
		title = ?,
		storage_backend = ?,
		archive_status = ?,
		progress_done = ?,
		progress_total = ?,
		error = ?,
		removed = ?,
		created_at = ?,
		updated_at = ?
		WHERE id = ?`,
		string(document.Source),
		document.SourceDocumentID,
		rawJSONToNullString(document.SourceMeta),
		document.Title,
		string(document.StorageBackend),
		string(document.ArchiveStatus),
		document.Progress.Done,
		document.Progress.Total,
		document.Error,
		boolToInt(document.Removed),
		formatTime(document.CreatedAt),
		formatTime(document.UpdatedAt),
		document.ID,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func listPagesTx(ctx context.Context, tx *sql.Tx, documentID int) ([]Page, error) {
	rows, err := tx.QueryContext(ctx, `SELECT page_index, object_key, content_type, size
		FROM document_pages
		WHERE document_id = ?
		ORDER BY page_index`, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pages := make([]Page, 0)
	for rows.Next() {
		var page Page
		if err := rows.Scan(&page.Index, &page.Key, &page.ContentType, &page.Size); err != nil {
			return nil, err
		}
		// Preserve direct page-index addressing: document.Pages[pageIndex] should
		// resolve to that page even if a previous download left a gap.
		if page.Index >= len(pages) {
			pages = append(pages, make([]Page, page.Index-len(pages)+1)...)
		}
		pages[page.Index] = page
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pages, nil
}

func replacePagesTx(ctx context.Context, tx *sql.Tx, documentID int, pages []Page) error {
	// Updates usually mutate a full Document value. Replacing page rows keeps the
	// database in sync with that value and makes retries idempotent.
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_pages WHERE document_id = ?`, documentID); err != nil {
		return err
	}
	for _, page := range pages {
		if page.Key == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO document_pages (
			document_id, page_index, object_key, content_type, size
		) VALUES (?, ?, ?, ?, ?)`,
			documentID,
			page.Index,
			page.Key,
			page.ContentType,
			page.Size,
		); err != nil {
			return err
		}
	}
	return nil
}

func rollback(tx *sql.Tx) {
	// Rollback after Commit returns sql.ErrTxDone; the caller already handled the
	// real error path, so this cleanup helper intentionally ignores it.
	_ = tx.Rollback()
}

func rawJSONToNullString(raw []byte) sql.NullString {
	if len(raw) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: string(raw), Valid: true}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, raw)
}

func isSQLiteURI(path string) bool {
	return len(path) >= 5 && path[:5] == "file:"
}
