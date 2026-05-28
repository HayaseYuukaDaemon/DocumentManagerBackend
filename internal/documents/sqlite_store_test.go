package documents

import (
	"context"
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

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()

	store, err := NewSQLiteStore(context.Background(), filepath.Join(t.TempDir(), "documents.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	return store
}
