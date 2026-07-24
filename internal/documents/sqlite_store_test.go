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
		StorageBackend:   storage.StorageName("memory"),
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

func TestSQLiteStoreDeletedDocumentsAreHiddenUnlessExplicitlyQueried(t *testing.T) {
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

	if _, err := store.Delete(ctx, doc.ID); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if _, err := store.Get(ctx, doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted document to be hidden by Get, got %v", err)
	}
	bySource := queryDocuments(t, store, QueryBuilder{}.BySourceDocumentID(testSource, "sqlite-deleted"))
	if len(bySource) != 0 {
		t.Fatalf("expected deleted document to be hidden by implicit source query, got %#v", bySource)
	}
	queued := queryDocuments(t, store, QueryBuilder{}.ByStatus(StatusQueued).Limit(10))
	if len(queued) != 0 {
		t.Fatalf("expected deleted document to be absent from queued query, got %#v", queued)
	}
	deleted := queryDocuments(t, store, QueryBuilder{}.ByStatus(StatusDeleted).Limit(10))
	if len(deleted) != 1 || deleted[0].ID != doc.ID {
		t.Fatalf("expected explicit deleted query to return document, got %#v", deleted)
	}
}

func TestSQLiteStoreCreateAfterRemoveReturnsExistingDocument(t *testing.T) {
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
	if _, err := store.Delete(ctx, first.ID); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}

	second, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "sqlite-recreate",
		status:           StatusQueued,
	})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected second Create to return ErrAlreadyExists, got %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected duplicate create after remove to return existing ID %d, got %d", first.ID, second.ID)
	}
	if second.Status() != StatusDeleted {
		t.Fatalf("expected duplicate create after remove to return deleted document, got %s", second.Status())
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
	archived := queryDocuments(t, store, QueryBuilder{}.ByStatus(StatusArchived).Limit(10))
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
	queued := queryDocuments(t, store, QueryBuilder{}.ByStatus(StatusQueued).Limit(10))
	if len(queued) != 1 || queued[0].ID != doc.ID {
		t.Fatalf("expected document to remain queued after invalid transition, got %#v", queued)
	}
}

func TestSQLiteStorePurgeMovesDeletedDocumentToPurged(t *testing.T) {
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
	if _, err := store.Delete(ctx, doc.ID); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if _, err := store.Purge(ctx, doc.ID); err != nil {
		t.Fatalf("Purge returned error: %v", err)
	}
	purged := queryDocuments(t, store, QueryBuilder{}.ByStatus(StatusPurged).Limit(10))
	if len(purged) != 1 || purged[0].ID != doc.ID {
		t.Fatalf("expected purged document after transition, got %#v", purged)
	}
	if err := store.TransitionTo(ctx, doc.ID, StatusQueued); err == nil {
		t.Fatalf("purged -> queued should be rejected")
	}
}
