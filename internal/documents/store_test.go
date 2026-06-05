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
	if _, err := store.UpdateMeta(ctx, 0, nil); !errors.Is(err, ErrNotFound) {
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

	if _, err := store.GetBySourceDocumentID(ctx, testSource, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected old source id to be removed, got %v", err)
	}
	if bySource, err := store.GetBySourceDocumentID(ctx, testSource, "new"); err != nil {
		t.Fatalf("GetBySourceDocumentID(new) returned error: %v", err)
	} else if bySource.ID != doc.ID {
		t.Fatalf("unexpected document ID for new source id: %d", bySource.ID)
	}
}
