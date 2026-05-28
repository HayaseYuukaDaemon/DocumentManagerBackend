package documents

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"document-archive/internal/storage"
)

func TestSQLiteStorePersistsDocuments(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "documents.db")

	store, err := NewSQLiteStore(ctx, path)
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "sqlite-persist",
		SourceMeta:       []byte(`{"ok":true}`),
		Title:            "SQLite Persist",
		StorageBackend:   storage.MemoryStorageName,
		ArchiveStatus:    StatusQueued,
		Progress: Progress{
			Done:  1,
			Total: 2,
		},
		Pages: []Page{
			{Index: 0, Key: "documents/1/pages/000001.webp", ContentType: "image/webp", Size: 123},
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	reopened, err := NewSQLiteStore(ctx, path)
	if err != nil {
		t.Fatalf("reopen NewSQLiteStore returned error: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Title != doc.Title {
		t.Fatalf("unexpected title after reopen: %q", got.Title)
	}
	if string(got.SourceMeta) != string(doc.SourceMeta) {
		t.Fatalf("unexpected source meta after reopen: %s", got.SourceMeta)
	}
	if len(got.Pages) != 1 || got.Pages[0].Size != 123 {
		t.Fatalf("unexpected pages after reopen: %#v", got.Pages)
	}
}

func TestSQLiteStoreUpdatePersistsPages(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	defer store.Close()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "sqlite-pages",
		ArchiveStatus:    StatusDownloading,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	_, err = store.Update(ctx, doc.ID, func(d *Document) error {
		d.Pages = []Page{
			{Index: 0, Key: "documents/1/pages/000001.webp", ContentType: "image/webp", Size: 123},
			{Index: 1, Key: "documents/1/pages/000002.webp", ContentType: "image/webp", Size: 456},
		}
		d.Progress.Done = 2
		return nil
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	got, err := store.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Progress.Done != 2 {
		t.Fatalf("unexpected progress done: %d", got.Progress.Done)
	}
	if len(got.Pages) != 2 || got.Pages[1].Size != 456 {
		t.Fatalf("unexpected pages: %#v", got.Pages)
	}
}

func TestSQLiteStoreRemovedDocumentsAreHidden(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	defer store.Close()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "sqlite-removed",
		ArchiveStatus:    StatusQueued,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if _, err := store.Remove(ctx, doc.ID); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if _, err := store.Get(ctx, doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected removed document to be hidden by Get, got %v", err)
	}
	if _, err := store.GetBySourceDocumentID(ctx, testSource, "sqlite-removed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected removed document to be hidden by source lookup, got %v", err)
	}
	queued, err := store.ListByStatus(ctx, StatusQueued, 10)
	if err != nil {
		t.Fatalf("ListByStatus returned error: %v", err)
	}
	if len(queued) != 0 {
		t.Fatalf("expected removed document to be hidden by ListByStatus, got %#v", queued)
	}
}

func TestSQLiteStoreCreateAfterRemoveAllocatesNewID(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	defer store.Close()

	first, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "sqlite-recreate",
		ArchiveStatus:    StatusQueued,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.Remove(ctx, first.ID); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}

	second, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "sqlite-recreate",
		ArchiveStatus:    StatusQueued,
	})
	if err != nil {
		t.Fatalf("second Create returned error: %v", err)
	}
	if second.ID <= first.ID {
		t.Fatalf("expected recreated document to get a new larger ID, first=%d second=%d", first.ID, second.ID)
	}
}

func TestSQLiteStoreMigratesLegacyUniqueConstraint(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "documents.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE documents (
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
	);
	CREATE TABLE document_pages (
		document_id INTEGER NOT NULL,
		page_index INTEGER NOT NULL,
		object_key TEXT NOT NULL,
		content_type TEXT NOT NULL,
		size INTEGER NOT NULL,
		PRIMARY KEY(document_id, page_index),
		FOREIGN KEY(document_id) REFERENCES documents(id) ON DELETE CASCADE
	);
	INSERT INTO documents (
		source, source_document_id, title, storage_backend, archive_status,
		progress_done, progress_total, error, removed, created_at, updated_at
	) VALUES (
		'test', 'legacy-recreate', '', '', 'queued',
		0, 0, '', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'
	)`)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("db.Close returned error: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("legacy schema setup returned error: %v", err)
	}

	store, err := NewSQLiteStore(ctx, path)
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "legacy-recreate",
		ArchiveStatus:    StatusQueued,
	})
	if err != nil {
		t.Fatalf("Create after migration returned error: %v", err)
	}
	if doc.ID <= 1 {
		t.Fatalf("expected migrated store to allocate a new ID, got %d", doc.ID)
	}
}

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()

	store, err := NewSQLiteStore(context.Background(), filepath.Join(t.TempDir(), "documents.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	return store
}
