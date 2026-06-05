package documents

import (
	"context"
	"errors"
	"testing"

	"document-archive/internal/sources"
)

const testSource sources.SourceType = "test"

func TestMemoryStoreCreateRejectsDuplicateSourceDocument(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	first, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "abc",
		ArchiveStatus:    StatusQueued,
	})
	if err != nil {
		t.Fatalf("first Create returned error: %v", err)
	}

	second, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "abc",
		ArchiveStatus:    StatusQueued,
	})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second Create should have returned ErrAlreadyExists, got %v", err)
	}

	if first.ID != 0 {
		t.Fatalf("unexpected first ID: %d", first.ID)
	}
	if second.ID != first.ID {
		t.Fatalf("expected duplicate create to return existing document ID %d, got %d", first.ID, second.ID)
	}

	bySource, err := store.GetBySourceDocumentID(ctx, testSource, "abc")
	if err != nil {
		t.Fatalf("GetBySourceDocumentID returned error: %v", err)
	}
	if bySource.ID != first.ID {
		t.Fatalf("unexpected document by source ID: %d", bySource.ID)
	}
}

func TestMemoryStoreRemovedDocumentsAreHidden(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "removed",
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
	if _, err := store.GetBySourceDocumentID(ctx, testSource, "removed"); !errors.Is(err, ErrNotFound) {
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

func TestMemoryStoreCreateAfterRemoveAllocatesNewID(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	first, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "recreate",
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
		SourceDocumentID: "recreate",
		ArchiveStatus:    StatusQueued,
	})
	if err != nil {
		t.Fatalf("second Create returned error: %v", err)
	}
	if second.ID <= first.ID {
		t.Fatalf("expected recreated document to get a new larger ID, first=%d second=%d", first.ID, second.ID)
	}
}

func TestMemoryStoreBoundsChecks(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	if _, err := store.Get(ctx, -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for negative id, got %v", err)
	}
	if _, err := store.Get(ctx, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for empty store, got %v", err)
	}
	if _, err := store.Remove(ctx, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for remove missing, got %v", err)
	}
	if _, err := store.UpdateMeta(ctx, 0, func(*DocumentMeta) error { return nil }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for update missing, got %v", err)
	}
}

func TestMemoryStoreUpdateMaintainsSourceIndex(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "old",
		ArchiveStatus:    StatusQueued,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if _, err := store.UpdateMeta(ctx, doc.ID, func(d *DocumentMeta) error {
		d.Title = "updated"
		return nil
	}); err != nil {
		t.Fatalf("UpdateMeta returned error: %v", err)
	}

	if bySource, err := store.GetBySourceDocumentID(ctx, testSource, "old"); err != nil {
		t.Fatalf("GetBySourceDocumentID(old) returned error: %v", err)
	} else if bySource.ID != doc.ID {
		t.Fatalf("unexpected document ID for source id: %d", bySource.ID)
	} else if bySource.Title != "updated" {
		t.Fatalf("unexpected updated title: %q", bySource.Title)
	}
}

func TestMemoryStoreCreatePersistsPagesLikeSQLite(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "memory-pages",
		ArchiveStatus:    StatusQueued,
		Progress: Progress{
			Done:  99,
			Total: 2,
		},
		Pages: []Page{
			{Index: 0, Key: "documents/1/pages/000001.webp", ContentType: "image/webp", Size: 123, Hash: "page-hash-1"},
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if doc.Progress.Done != 1 {
		t.Fatalf("unexpected progress done: %d", doc.Progress.Done)
	}
	if doc.Progress.Total != 2 {
		t.Fatalf("unexpected progress total: %d", doc.Progress.Total)
	}
	if len(doc.Pages) != 1 || doc.Pages[0].Hash != "page-hash-1" {
		t.Fatalf("unexpected pages: %#v", doc.Pages)
	}
}

func TestMemoryStoreAddAndRemovePageLikeSQLite(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "memory-add-remove-pages",
		ArchiveStatus:    StatusDownloading,
		Progress: Progress{
			Total: 2,
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if err := store.AddPage(ctx, doc.ID, Page{Index: 0, Key: "documents/1/pages/000001.webp", ContentType: "image/webp", Size: 123}); err != nil {
		t.Fatalf("AddPage(0) returned error: %v", err)
	}
	if err := store.AddPage(ctx, doc.ID, Page{Index: 0, Key: "documents/1/pages/000001-new.webp", ContentType: "image/webp", Size: 456}); !errors.Is(err, ErrPageAlreadyExists) {
		t.Fatalf("expected duplicate AddPage to fail with ErrPageAlreadyExists, got %v", err)
	}
	if err := store.AddPage(ctx, doc.ID, Page{Index: 2, Key: "documents/1/pages/000003.webp", ContentType: "image/webp", Size: 789}); err != nil {
		t.Fatalf("AddPage(2) returned error: %v", err)
	}

	got, err := store.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Progress.Done != 2 || got.Progress.Total != 2 {
		t.Fatalf("unexpected progress after add: %#v", got.Progress)
	}
	if len(got.Pages) != 3 || got.Pages[2].Key == "" {
		t.Fatalf("unexpected sparse pages after add: %#v", got.Pages)
	}

	if err := store.RemovePage(ctx, doc.ID, 0); err != nil {
		t.Fatalf("RemovePage(0) returned error: %v", err)
	}
	got, err = store.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Get after remove returned error: %v", err)
	}
	if got.Progress.Done != 1 {
		t.Fatalf("unexpected progress done after remove: %d", got.Progress.Done)
	}
	if len(got.Pages) != 3 || got.Pages[0].Key != "" || got.Pages[2].Key == "" {
		t.Fatalf("expected remove to preserve sparse page indexes, got %#v", got.Pages)
	}
	if err := store.RemovePage(ctx, doc.ID, 0); !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("expected removing missing page to fail with ErrPageNotFound, got %v", err)
	}
}
