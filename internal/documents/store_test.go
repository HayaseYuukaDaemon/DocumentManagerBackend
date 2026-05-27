package documents

import (
	"context"
	"errors"
	"testing"

	"document-archive/internal/sources"
)

const testSource sources.SourceType = "test"

func TestMemoryStoreCreateIsIdempotent(t *testing.T) {
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
	if err != nil {
		t.Fatalf("second Create returned error: %v", err)
	}

	if first.ID != 0 {
		t.Fatalf("unexpected first ID: %d", first.ID)
	}
	if second.ID != first.ID {
		t.Fatalf("expected idempotent create to return existing document ID %d, got %d", first.ID, second.ID)
	}

	bySource, err := store.GetBySourceDocumentID(ctx, testSource, "abc")
	if err != nil {
		t.Fatalf("GetBySourceDocumentID returned error: %v", err)
	}
	if bySource.ID != first.ID {
		t.Fatalf("unexpected document by source ID: %d", bySource.ID)
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
	if _, err := store.Update(ctx, Document{ID: 0}); !errors.Is(err, ErrNotFound) {
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

	doc.SourceDocumentID = "new"
	updated, err := store.Update(ctx, doc)
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if updated.SourceDocumentID != "new" {
		t.Fatalf("unexpected updated source document id: %s", updated.SourceDocumentID)
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
