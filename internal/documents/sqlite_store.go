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
	"document-archive/internal/utils"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore 打开 SQLite 文件并确保 Schema 存在。
// 返回的 store 拥有 db 实例的所有权；调用方应在服务关闭期间调用 Close。
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
	// SQLite 允许多个并发读取者，但同一时间只允许一个写入者。保持单一的
	// 数据库连接可以使该服务的事务行为具有可预测性。
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
		// WAL 模式允许读取者在另一个事务写入时继续操作，这比默认的回滚日志
		// 更适合工作线程与 HTTP 访问并发的模式。
		`PRAGMA journal_mode = WAL`,
		// 当数据库被锁定时让 SQLite 等待一小会儿，而不是
		// 在请求/工作线程出现竞争时立即失败。
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS documents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			source_document_id TEXT NOT NULL,
			source_meta TEXT,
			title TEXT NOT NULL DEFAULT '',
			storage_backend TEXT NOT NULL DEFAULT '',
			document_status TEXT NOT NULL CHECK (document_status IN ('queued', 'resolving', 'downloading', 'archived', 'failed', 'deleted', 'purged')),
			progress_done INTEGER NOT NULL DEFAULT 0,
			progress_total INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		// 页面单独存储是因为页面钩子会逐个更新它们。使用
		// 单独的表可以避免在下载时重写大型的 JSON blob。
		`CREATE TABLE IF NOT EXISTS document_pages (
			document_id INTEGER NOT NULL,
			page_index INTEGER NOT NULL,
			object_key TEXT NOT NULL,
			content_type TEXT NOT NULL,
			size INTEGER NOT NULL,
			hash TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(document_id, page_index),
			FOREIGN KEY(document_id) REFERENCES documents(id) ON DELETE CASCADE
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_documents_status ON documents(document_status, id)`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_documents_active_source_identity
		ON documents(source, source_document_id)
		WHERE document_status IN ('queued', 'resolving', 'downloading', 'archived', 'failed')`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_document_pages_document ON document_pages(document_id, page_index)`); err != nil {
		return err
	}
	// SQLite 默认禁用 foreign_keys；启用它以便页面行保持
	// 关联到有效的文档行。
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return err
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
	case err == nil:
		return existing, ErrAlreadyExists
	case !errors.Is(err, ErrNotFound):
		return Document{}, err
	}

	now := time.Now().UTC()
	document.CreatedAt = now
	document.UpdatedAt = now
	document.status = StatusQueued

	pages := document.Pages
	document.Pages = nil
	document.Progress.Done = 0

	result, err := tx.ExecContext(ctx, `INSERT INTO documents (
		source, source_document_id, source_meta, title, storage_backend, document_status,
		progress_done, progress_total, error, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(document.Source),
		document.SourceDocumentID,
		rawJSONToNullString(document.SourceMeta),
		document.Title,
		string(document.StorageBackend),
		string(document.status),
		document.Progress.Done,
		document.Progress.Total,
		document.Error,
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

	for index, page := range pages {
		if index != page.Index {
			return Document{}, fmt.Errorf("page index mismatch: expected %d, got %d", index, page.Index)
		}
		err = s.addPage(ctx, document.ID, page, tx)
		if err != nil {
			return Document{}, utils.NewIndexedError(err, index)
		}
	}

	document, err = getTx(ctx, tx, document.ID, false)
	if err != nil {
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

	document, err := getTx(ctx, tx, id, true)
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

	document, err := getBySourceDocumentIDTx(ctx, tx, source, sourceDocumentID, true)
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

	document, err := getTx(ctx, tx, id, true)
	if err != nil {
		return Document{}, err
	}
	if !canTransitionDocumentStatus(document.status, StatusDeleted) {
		return Document{}, fmt.Errorf("invalid document status transition: %s -> %s", document.status, StatusDeleted)
	}
	document.status = StatusDeleted
	document.UpdatedAt = time.Now().UTC()
	if err := updateDocumentTx(ctx, tx, document); err != nil {
		return Document{}, err
	}
	if err := tx.Commit(); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *SQLiteStore) ListByStatus(ctx context.Context, status DocumentStatus, limit int) ([]Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	query := `SELECT
		id, source, source_document_id, source_meta, title, storage_backend, document_status,
		progress_done, progress_total, error, created_at, updated_at
		FROM documents
		WHERE document_status = ?
		ORDER BY id`
	var rows *sql.Rows
	if limit <= 0 {
		rows, err = tx.QueryContext(ctx, query, string(status))
	} else {
		rows, err = tx.QueryContext(ctx, query+" LIMIT ?", string(status), limit)
	}
	if err != nil {
		return nil, err
	}

	capacity := 0
	if limit > 0 {
		capacity = limit
	}
	result := make([]Document, 0, capacity)
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
	// 在关闭文档行游标后加载页面。在只有一个 SQLite 连接的情况下，
	// 在发出另一个查询时保持游标打开可能会造成不必要的序列化
	// 或阻塞后续的语句。
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

func extractDocumentMeta(document Document) DocumentMeta {
	return DocumentMeta{
		SourceMeta:     document.SourceMeta,
		Title:          document.Title,
		StorageBackend: document.StorageBackend,
		status:         document.status,
		Progress:       document.Progress,
		Error:          document.Error,
	}
}

func fillDocumentMeta(document *Document, meta DocumentMeta) {
	document.SourceMeta = meta.SourceMeta
	document.Title = meta.Title
	document.StorageBackend = meta.StorageBackend
	document.status = meta.status
	document.Progress = meta.Progress
	document.Error = meta.Error
}

func (s *SQLiteStore) updateMeta(ctx context.Context, id int, fn func(*DocumentMeta) error, tx *sql.Tx) (Document, error) {
	document, err := getTx(ctx, tx, id, true)
	if err != nil {
		return Document{}, err
	}
	// 该回调在读写相同的事务中运行。这可以
	// 防止钩子更新和状态转换使用陈旧的
	// 文档快照相互覆盖。
	meta := extractDocumentMeta(document)
	if err := fn(&meta); err != nil {
		return Document{}, err
	}
	fillDocumentMeta(&document, meta)
	document.ID = id
	document.UpdatedAt = time.Now().UTC()

	if err := updateDocumentTx(ctx, tx, document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *SQLiteStore) UpdateMeta(ctx context.Context, id int, fn func(*DocumentMeta) error) (Document, error) {
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

	document, err := s.updateMeta(ctx, id, fn, tx)
	if err != nil {
		return Document{}, err
	}

	if err := tx.Commit(); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *SQLiteStore) addPage(ctx context.Context, id int, page Page, tx *sql.Tx) error {
	_, err := s.updateMeta(ctx, id, func(meta *DocumentMeta) error {
		meta.Progress.Done++
		if meta.Progress.Total < meta.Progress.Done {
			meta.Progress.Total = meta.Progress.Done
		}
		return nil
	}, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO document_pages (
		document_id, page_index, object_key, content_type, size, hash
	) VALUES (?, ?, ?, ?, ?, ?)`,
		id,
		page.Index,
		page.Key,
		page.ContentType,
		page.Size,
		page.Hash,
	)
	if err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) AddPage(ctx context.Context, id int, page Page) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	err = s.addPage(ctx, id, page, tx)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) removePage(ctx context.Context, id int, pageIndex int, tx *sql.Tx) error {
	result, err := tx.ExecContext(ctx, `DELETE FROM document_pages WHERE document_id = ? AND page_index = ?`, id, pageIndex)
	if err != nil {
		return err
	}
	cnt, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if cnt == 0 {
		return ErrPageNotFound
	}
	_, err = s.updateMeta(ctx, id, func(meta *DocumentMeta) error {
		if meta.Progress.Done > 0 {
			meta.Progress.Done--
		}
		return nil
	}, tx)
	if err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) RemovePage(ctx context.Context, id int, pageIndex int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	err = s.removePage(ctx, id, pageIndex, tx)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func getTx(ctx context.Context, tx *sql.Tx, id int, visibleOnly bool) (Document, error) {
	query := `SELECT
		id, source, source_document_id, source_meta, title, storage_backend, document_status,
		progress_done, progress_total, error, created_at, updated_at
		FROM documents WHERE id = ?`
	if visibleOnly {
		query += " AND document_status IN ('queued', 'resolving', 'downloading', 'archived', 'failed')"
	}
	return getByQueryRow(ctx, tx, tx.QueryRowContext(ctx, query, id))
}

func getBySourceDocumentIDTx(ctx context.Context, tx *sql.Tx, source sources.SourceType, sourceDocumentID string, visibleOnly bool) (Document, error) {
	query := `SELECT
		id, source, source_document_id, source_meta, title, storage_backend, document_status,
		progress_done, progress_total, error, created_at, updated_at
		FROM documents WHERE source = ? AND source_document_id = ?`
	if visibleOnly {
		query += " AND document_status IN ('queued', 'resolving', 'downloading', 'archived', 'failed')"
	}
	return getByQueryRow(ctx, tx, tx.QueryRowContext(ctx, query, string(source), sourceDocumentID))
}

func getByQueryRow(ctx context.Context, tx *sql.Tx, row documentScanner) (Document, error) {
	document, err := scanDocument(row)
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

func (s *SQLiteStore) TransitionTo(ctx context.Context, id int, newStatus DocumentStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	document, err := getTx(ctx, tx, id, false)
	if err != nil {
		return err
	}
	if !canTransitionDocumentStatus(document.status, newStatus) {
		return fmt.Errorf("invalid document status transition: %s -> %s", document.status, newStatus)
	}

	document.status = newStatus
	document.UpdatedAt = time.Now().UTC()
	if err := updateDocumentTx(ctx, tx, document); err != nil {
		return err
	}

	return tx.Commit()
}

// scanDocument 将扁平的 documents 表映射到公共的 Document 模型。
// 页面行由调用者单独加载，因为它们存放在 document_pages 表中。
func scanDocument(scanner documentScanner) (Document, error) {
	var document Document
	var source string
	var sourceMeta sql.NullString
	var storageBackend string
	var documentStatus string
	var createdAt string
	var updatedAt string

	err := scanner.Scan(
		&document.ID,
		&source,
		&document.SourceDocumentID,
		&sourceMeta,
		&document.Title,
		&storageBackend,
		&documentStatus,
		&document.Progress.Done,
		&document.Progress.Total,
		&document.Error,
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
	document.status = DocumentStatus(documentStatus)

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
		document_status = ?,
		progress_done = ?,
		progress_total = ?,
		error = ?,
		created_at = ?,
		updated_at = ?
		WHERE id = ?`,
		string(document.Source),
		document.SourceDocumentID,
		rawJSONToNullString(document.SourceMeta),
		document.Title,
		string(document.StorageBackend),
		string(document.status),
		document.Progress.Done,
		document.Progress.Total,
		document.Error,
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
	rows, err := tx.QueryContext(ctx, `SELECT page_index, object_key, content_type, size, hash
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
		if err := rows.Scan(&page.Index, &page.Key, &page.ContentType, &page.Size, &page.Hash); err != nil {
			return nil, err
		}
		// 保留直接通过页面索引寻址：即使之前的下载留下了空隙，
		// document.Pages[pageIndex] 也应该能够解析到该页面。
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

func rollback(tx *sql.Tx) {
	// 在 Commit 之后进行 Rollback 会返回 sql.ErrTxDone；调用者已经处理了
	// 真正的错误路径，所以这个清理辅助函数故意忽略了该错误。
	_ = tx.Rollback()
}

func rawJSONToNullString(raw []byte) sql.NullString {
	if len(raw) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: string(raw), Valid: true}
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
