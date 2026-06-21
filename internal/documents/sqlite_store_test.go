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
		status:           StatusQueued,
		Progress: Progress{
			Done:  1,
			Total: 2,
		},
		Pages: []Page{
			{Index: 0, Key: "documents/1/pages/000001.webp", ContentType: "image/webp", Size: 123, Hash: "page-hash-1"},
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
	if got.Pages[0].Hash != "page-hash-1" {
		t.Fatalf("unexpected page hash after reopen: %q", got.Pages[0].Hash)
	}
	if got.Progress.Done != 1 {
		t.Fatalf("unexpected progress done after reopen: %d", got.Progress.Done)
	}
	if got.Progress.Total != 2 {
		t.Fatalf("unexpected progress total after reopen: %d", got.Progress.Total)
	}
}

func TestSQLiteStoreDeletedDocumentsAreHidden(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	defer store.Close()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "sqlite-deleted",
		status:           StatusQueued,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if _, err := store.Remove(ctx, doc.ID); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if _, err := store.Get(ctx, doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted document to be hidden by Get, got %v", err)
	}
	if _, err := store.GetBySourceDocumentID(ctx, testSource, "sqlite-deleted"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted document to be hidden by source lookup, got %v", err)
	}
	queued, err := store.ListByStatus(ctx, StatusQueued, 10)
	if err != nil {
		t.Fatalf("ListByStatus returned error: %v", err)
	}
	if len(queued) != 0 {
		t.Fatalf("expected deleted document to be hidden by ListByStatus, got %#v", queued)
	}
}

func TestSQLiteStoreCreateAfterRemoveAllocatesNewID(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	defer store.Close()

	first, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "sqlite-recreate",
		status:           StatusQueued,
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
		status:           StatusQueued,
	})
	if err != nil {
		t.Fatalf("second Create returned error: %v", err)
	}
	if second.ID <= first.ID {
		t.Fatalf("expected recreated document to get a new larger ID, first=%d second=%d", first.ID, second.ID)
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

func TestSQLiteStoreTransitionToValidatesStateGraph(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	defer store.Close()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "sqlite-transition",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if err := store.TransitionTo(ctx, doc.ID, StatusResolving); err != nil {
		t.Fatalf("queued -> resolving should be valid: %v", err)
	}
	if err := store.TransitionTo(ctx, doc.ID, StatusArchived); err != nil {
		t.Fatalf("resolving -> archived should be valid for metadata refresh: %v", err)
	}
	archived, err := store.ListByStatus(ctx, StatusArchived, 10)
	if err != nil {
		t.Fatalf("ListByStatus returned error: %v", err)
	}
	if len(archived) != 1 || archived[0].ID != doc.ID {
		t.Fatalf("expected archived document after transition, got %#v", archived)
	}
}

func TestSQLiteStoreTransitionToRejectsInvalidStateGraph(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	defer store.Close()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "sqlite-invalid-transition",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if err := store.TransitionTo(ctx, doc.ID, StatusArchived); err == nil {
		t.Fatalf("queued -> archived should be rejected")
	}
	queued, err := store.ListByStatus(ctx, StatusQueued, 10)
	if err != nil {
		t.Fatalf("ListByStatus returned error: %v", err)
	}
	if len(queued) != 1 || queued[0].ID != doc.ID {
		t.Fatalf("expected document to remain queued after invalid transition, got %#v", queued)
	}
}

func TestSQLiteStoreTransitionDeletedToPurged(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	defer store.Close()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "sqlite-purge-transition",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.Remove(ctx, doc.ID); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if err := store.TransitionTo(ctx, doc.ID, StatusPurged); err != nil {
		t.Fatalf("deleted -> purged should be valid: %v", err)
	}
	purged, err := store.ListByStatus(ctx, StatusPurged, 10)
	if err != nil {
		t.Fatalf("ListByStatus returned error: %v", err)
	}
	if len(purged) != 1 || purged[0].ID != doc.ID {
		t.Fatalf("expected purged document after transition, got %#v", purged)
	}
	if err := store.TransitionTo(ctx, doc.ID, StatusQueued); err == nil {
		t.Fatalf("purged -> queued should be rejected")
	}
}
