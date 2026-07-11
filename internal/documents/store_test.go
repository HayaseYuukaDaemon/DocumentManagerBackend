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
	})
	if err != nil {
		t.Fatalf("first Create returned error: %v", err)
	}

	second, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "abc",
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

	bySource := queryDocuments(t, store, QueryBuilder{}.BySourceDocumentID(testSource, "abc"))
	if len(bySource) != 1 {
		t.Fatalf("expected one document by source ID, got %#v", bySource)
	}
	if bySource[0].ID != first.ID {
		t.Fatalf("unexpected document by source ID: %d", bySource[0].ID)
	}
}

func TestMemoryStoreDeletedDocumentsAreHiddenUnlessExplicitlyQueried(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "deleted",
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
	bySource := queryDocuments(t, store, QueryBuilder{}.BySourceDocumentID(testSource, "deleted"))
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

func TestMemoryStoreCreateAfterRemoveReturnsExistingDocument(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	first, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "recreate",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.Delete(ctx, first.ID); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}

	second, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "recreate",
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

func TestMemoryStoreBoundsChecks(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	if _, err := store.Get(ctx, -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for negative id, got %v", err)
	}
	if _, err := store.Get(ctx, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for empty store, got %v", err)
	}
	if _, err := store.Delete(ctx, 0); !errors.Is(err, ErrNotFound) {
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

	bySource := queryDocuments(t, store, QueryBuilder{}.BySourceDocumentID(testSource, "old"))
	if len(bySource) != 1 {
		t.Fatalf("expected one document by source id, got %#v", bySource)
	}
	if bySource[0].ID != doc.ID {
		t.Fatalf("unexpected document ID for source id: %d", bySource[0].ID)
	}
	if bySource[0].Title != "updated" {
		t.Fatalf("unexpected updated title: %q", bySource[0].Title)
	}
}

func TestStoreQueryFiltersBySourceDocumentID(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()

		match, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "query-source-match",
			Title:            "match",
		})
		if err != nil {
			t.Fatalf("Create(match) returned error: %v", err)
		}
		if _, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "query-source-other",
			Title:            "other",
		}); err != nil {
			t.Fatalf("Create(other) returned error: %v", err)
		}

		got := queryDocuments(t, store, QueryBuilder{}.BySourceDocumentID(testSource, "query-source-match"))
		if len(got) != 1 || got[0].ID != match.ID {
			t.Fatalf("expected only source match %d, got %#v", match.ID, got)
		}

		missing := queryDocuments(t, store, QueryBuilder{}.BySourceDocumentID(testSource, "missing"))
		if len(missing) != 0 {
			t.Fatalf("expected missing source query to return empty list, got %#v", missing)
		}
	})
}

func TestStoreQueryHidesRemovedDocumentsUnlessStatusIsExplicit(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()

		active, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "query-active",
		})
		if err != nil {
			t.Fatalf("Create(active) returned error: %v", err)
		}
		removed, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "query-removed",
		})
		if err != nil {
			t.Fatalf("Create(removed) returned error: %v", err)
		}
		if _, err := store.Delete(ctx, removed.ID); err != nil {
			t.Fatalf("Remove returned error: %v", err)
		}

		implicit := queryDocuments(t, store, QueryBuilder{})
		if len(implicit) != 1 || implicit[0].ID != active.ID {
			t.Fatalf("expected implicit query to return only active document %d, got %#v", active.ID, implicit)
		}

		removedBySource := queryDocuments(t, store, QueryBuilder{}.BySourceDocumentID(testSource, "query-removed"))
		if len(removedBySource) != 0 {
			t.Fatalf("expected implicit source query to hide removed document, got %#v", removedBySource)
		}

		deleted := queryDocuments(t, store, QueryBuilder{}.ByStatus(StatusDeleted))
		if len(deleted) != 1 || deleted[0].ID != removed.ID {
			t.Fatalf("expected explicit deleted query to return removed document %d, got %#v", removed.ID, deleted)
		}
	})
}

func TestStoreAddPageRejectsDuplicateIndex(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()

		doc, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "duplicate-page-index",
		})
		if err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
		if err := store.AddPage(ctx, doc.ID, Page{Index: 0, Key: "documents/1/pages/000001.webp", ContentType: "image/webp", Size: 123}); err != nil {
			t.Fatalf("first AddPage returned error: %v", err)
		}

		err = store.AddPage(ctx, doc.ID, Page{Index: 0, Key: "documents/1/pages/000001-new.webp", ContentType: "image/webp", Size: 456})
		if !errors.Is(err, ErrPageAlreadyExists{}) {
			t.Fatalf("expected duplicate AddPage to fail with ErrPageAlreadyExists, got %v", err)
		}

		var duplicate ErrPageAlreadyExists
		if !errors.As(err, &duplicate) {
			t.Fatalf("expected duplicate AddPage error to unwrap as ErrPageAlreadyExists, got %T", err)
		}
		if duplicate.DocumentID != doc.ID || duplicate.PageIndex != 0 {
			t.Fatalf("unexpected duplicate AddPage error payload: %#v", duplicate)
		}
	})
}

func TestStoreCreateAfterPurgeReturnsExistingDocument(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()

		first, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "query-purged",
		})
		if err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
		if _, err := store.Delete(ctx, first.ID); err != nil {
			t.Fatalf("Remove returned error: %v", err)
		}
		if _, err := store.Purge(ctx, first.ID); err != nil {
			t.Fatalf("Purge returned error: %v", err)
		}

		second, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "query-purged",
		})
		if !errors.Is(err, ErrAlreadyExists) {
			t.Fatalf("expected duplicate Create after purge to return ErrAlreadyExists, got %v", err)
		}
		if second.ID != first.ID {
			t.Fatalf("expected duplicate Create after purge to return existing ID %d, got %d", first.ID, second.ID)
		}
		if second.Status() != StatusPurged {
			t.Fatalf("expected duplicate Create after purge to return purged document, got %s", second.Status())
		}
	})
}

func TestStoreTransitionToPurgedRejectsDocumentsWithPagesOrProgress(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()

		doc, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "purge-guard",
			Progress: Progress{
				Total: 1,
			},
		})
		if err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
		if err := store.AddPage(ctx, doc.ID, Page{Index: 0, Key: "documents/1/pages/000001.webp", ContentType: "image/webp", Size: 123}); err != nil {
			t.Fatalf("AddPage returned error: %v", err)
		}
		if _, err := store.Delete(ctx, doc.ID); err != nil {
			t.Fatalf("Delete returned error: %v", err)
		}
		if err := store.TransitionTo(ctx, doc.ID, StatusPurged); err == nil {
			t.Fatalf("expected TransitionTo(StatusPurged) to reject documents with pages/progress")
		}
	})
}

func TestStorePurgeClearsPagesAndProgress(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()

		doc, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "purge-clears-pages",
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
		if err := store.AddPage(ctx, doc.ID, Page{Index: 2, Key: "documents/1/pages/000003.webp", ContentType: "image/webp", Size: 789}); err != nil {
			t.Fatalf("AddPage(2) returned error: %v", err)
		}
		if _, err := store.Delete(ctx, doc.ID); err != nil {
			t.Fatalf("Delete returned error: %v", err)
		}

		removedPages, err := store.Purge(ctx, doc.ID)
		if err != nil {
			t.Fatalf("Purge returned error: %v", err)
		}
		if removedPages != 2 {
			t.Fatalf("expected Purge to remove 2 pages, got %d", removedPages)
		}

		purged := queryDocuments(t, store, QueryBuilder{}.ByStatus(StatusPurged))
		if len(purged) != 1 || purged[0].ID != doc.ID {
			t.Fatalf("expected purged query to return document %d, got %#v", doc.ID, purged)
		}
		if purged[0].Progress.Done != 0 || purged[0].Progress.Total != 0 {
			t.Fatalf("expected Purge to clear progress, got %#v", purged[0].Progress)
		}
		if countExistingPages(purged[0].Pages) != 0 {
			t.Fatalf("expected Purge to clear persisted pages, got %#v", purged[0].Pages)
		}
	})
}

func TestStoreRestoreMakesDeletedDocumentVisibleAgain(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()

		doc, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "restore-deleted",
			Progress: Progress{
				Total: 1,
			},
		})
		if err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
		if err := store.AddPage(ctx, doc.ID, Page{Index: 0, Key: "documents/1/pages/000001.webp", ContentType: "image/webp", Size: 123}); err != nil {
			t.Fatalf("AddPage returned error: %v", err)
		}
		if _, err := store.Delete(ctx, doc.ID); err != nil {
			t.Fatalf("Delete returned error: %v", err)
		}

		restored, err := store.Restore(ctx, doc.ID)
		if err != nil {
			t.Fatalf("Restore returned error: %v", err)
		}
		if restored.Status() != StatusQueued {
			t.Fatalf("expected Restore to move deleted document to queued, got %s", restored.Status())
		}
		if restored.Progress.Done != 1 || restored.Progress.Total != 1 {
			t.Fatalf("expected Restore to preserve deleted document progress, got %#v", restored.Progress)
		}
		if countExistingPages(restored.Pages) != 1 {
			t.Fatalf("expected Restore to preserve deleted document pages, got %#v", restored.Pages)
		}

		got, err := store.Get(ctx, doc.ID)
		if err != nil {
			t.Fatalf("Get after Restore returned error: %v", err)
		}
		if got.Status() != StatusQueued {
			t.Fatalf("expected restored document to be visible and queued, got %s", got.Status())
		}
	})
}

func TestStoreRestoreMakesPurgedDocumentVisibleAgain(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()

		doc, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "restore-purged",
			Progress: Progress{
				Total: 2,
			},
		})
		if err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
		if err := store.AddPage(ctx, doc.ID, Page{Index: 0, Key: "documents/1/pages/000001.webp", ContentType: "image/webp", Size: 123}); err != nil {
			t.Fatalf("AddPage returned error: %v", err)
		}
		if _, err := store.Delete(ctx, doc.ID); err != nil {
			t.Fatalf("Delete returned error: %v", err)
		}
		if _, err := store.Purge(ctx, doc.ID); err != nil {
			t.Fatalf("Purge returned error: %v", err)
		}

		restored, err := store.Restore(ctx, doc.ID)
		if err != nil {
			t.Fatalf("Restore returned error: %v", err)
		}
		if restored.Status() != StatusQueued {
			t.Fatalf("expected Restore to move purged document to queued, got %s", restored.Status())
		}
		if restored.Progress.Done != 0 || restored.Progress.Total != 0 {
			t.Fatalf("expected Restore to preserve purged zeroed progress, got %#v", restored.Progress)
		}
		if countExistingPages(restored.Pages) != 0 {
			t.Fatalf("expected Restore to keep purged document pages empty, got %#v", restored.Pages)
		}
	})
}

func TestStoreRestoreRejectsVisibleDocument(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()

		doc, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "restore-visible",
		})
		if err != nil {
			t.Fatalf("Create returned error: %v", err)
		}

		err = func() error {
			_, err := store.Restore(ctx, doc.ID)
			return err
		}()
		if !errors.Is(err, ErrInvalidStatusTransition{To: StatusQueued}) {
			t.Fatalf("expected Restore on visible document to fail with ErrInvalidStatusTransition, got %v", err)
		}
	})
}

func TestStoreResetPagesClearsPagesAndPreservesStatus(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()

		doc, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "reset-pages",
			Progress:         Progress{Total: 2},
		})
		if err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
		for index, hash := range []string{"hash-a", "hash-b"} {
			if err := store.AddPage(ctx, doc.ID, Page{
				Index:       index,
				Key:         "documents/refresh/pages/" + hash,
				ContentType: "image/webp",
				Size:        123,
				Hash:        hash,
			}); err != nil {
				t.Fatalf("AddPage(%d) returned error: %v", index, err)
			}
		}
		if err := store.TransitionTo(ctx, doc.ID, StatusResolving); err != nil {
			t.Fatalf("TransitionTo(resolving) returned error: %v", err)
		}
		if err := store.TransitionTo(ctx, doc.ID, StatusArchived); err != nil {
			t.Fatalf("TransitionTo(archived) returned error: %v", err)
		}

		reset, err := store.ResetPages(ctx, doc.ID)
		if err != nil {
			t.Fatalf("ResetPages returned error: %v", err)
		}
		if reset.Status() != StatusArchived {
			t.Fatalf("expected status to remain archived, got %s", reset.Status())
		}
		if len(reset.Pages) != 0 {
			t.Fatalf("expected pages to be cleared, got %#v", reset.Pages)
		}
		if reset.Progress.Done != 0 || reset.Progress.Total != 2 {
			t.Fatalf("expected done reset and total preserved, got %#v", reset.Progress)
		}

		got, err := store.Get(ctx, doc.ID)
		if err != nil {
			t.Fatalf("Get after ResetPages returned error: %v", err)
		}
		if got.Status() != StatusArchived || len(got.Pages) != 0 || got.Progress.Done != 0 || got.Progress.Total != 2 {
			t.Fatalf("unexpected persisted document after ResetPages: %#v", got)
		}
	})
}

func TestStoreResetPagesWorksDuringProcessing(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()

		doc, err := store.Create(ctx, Document{
			Source:           testSource,
			SourceDocumentID: "reset-processing",
			Progress:         Progress{Total: 1},
		})
		if err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
		if err := store.AddPage(ctx, doc.ID, Page{Index: 0, Key: "documents/processing/pages/hash-a", Hash: "hash-a"}); err != nil {
			t.Fatalf("AddPage returned error: %v", err)
		}
		if err := store.TransitionTo(ctx, doc.ID, StatusResolving); err != nil {
			t.Fatalf("TransitionTo(resolving) returned error: %v", err)
		}

		reset, err := store.ResetPages(ctx, doc.ID)
		if err != nil {
			t.Fatalf("ResetPages returned error: %v", err)
		}
		if reset.Status() != StatusResolving || len(reset.Pages) != 0 || reset.Progress.Done != 0 || reset.Progress.Total != 1 {
			t.Fatalf("unexpected document after ResetPages: %#v", reset)
		}
	})
}

func TestStoreQueryOrdersAndLimitsResults(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx := context.Background()
		first, err := store.Create(ctx, Document{Source: testSource, SourceDocumentID: "order-1"})
		if err != nil {
			t.Fatalf("Create(first) returned error: %v", err)
		}
		second, err := store.Create(ctx, Document{Source: testSource, SourceDocumentID: "order-2"})
		if err != nil {
			t.Fatalf("Create(second) returned error: %v", err)
		}
		third, err := store.Create(ctx, Document{Source: testSource, SourceDocumentID: "order-3"})
		if err != nil {
			t.Fatalf("Create(third) returned error: %v", err)
		}

		defaultOrder := queryDocuments(t, store, QueryBuilder{}.Limit(3))
		assertDocumentIDs(t, defaultOrder, []int{first.ID, second.ID, third.ID})

		descLimited := queryDocuments(t, store, QueryBuilder{}.Order("DESC").Limit(2))
		assertDocumentIDs(t, descLimited, []int{third.ID, second.ID})
	})
}

func TestStoreQueryReturnsContextError(t *testing.T) {
	forEachDocumentStore(t, func(t *testing.T, store Store) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		query := mustBuildQuery(t, QueryBuilder{})
		if _, err := store.Query(ctx, query); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}

func TestQueryBuilderRejectsInvalidParams(t *testing.T) {
	tests := []struct {
		name string
		qb   QueryBuilder
	}{
		{name: "invalid order", qb: QueryBuilder{}.Order("DOWN")},
		{name: "invalid order by", qb: QueryBuilder{}.OrderBy("title")},
		{name: "source without source document id", qb: QueryBuilder{source: ptr(testSource)}},
		{name: "source document id without source", qb: QueryBuilder{sourceDocumentID: ptr("orphan")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.qb.Build()
			var mismatch ErrQueryParamMismatch
			if !errors.As(err, &mismatch) {
				t.Fatalf("expected ErrQueryParamMismatch, got %T %v", err, err)
			}
		})
	}
}

func TestMemoryStoreCreatePersistsPagesLikeSQLite(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	doc, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "memory-pages",
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
	if err := store.AddPage(ctx, doc.ID, Page{Index: 0, Key: "documents/1/pages/000001-new.webp", ContentType: "image/webp", Size: 456}); !errors.Is(err, ErrPageAlreadyExists{}) {
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

func forEachDocumentStore(t *testing.T, fn func(t *testing.T, store Store)) {
	t.Helper()

	t.Run("memory", func(t *testing.T) {
		fn(t, NewMemoryStore())
	})
	t.Run("sqlite", func(t *testing.T) {
		store := newTestSQLiteStore(t)
		t.Cleanup(func() {
			if err := store.Close(); err != nil {
				t.Fatalf("Close returned error: %v", err)
			}
		})
		fn(t, store)
	})
}

func queryDocuments(t *testing.T, store Store, qb QueryBuilder) []Document {
	t.Helper()

	query := mustBuildQuery(t, qb)
	documents, err := store.Query(context.Background(), query)
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}
	return documents
}

func mustBuildQuery(t *testing.T, qb QueryBuilder) DocumentQuery {
	t.Helper()

	query, err := qb.Build()
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	return query
}

func assertDocumentIDs(t *testing.T, documents []Document, want []int) {
	t.Helper()

	if len(documents) != len(want) {
		t.Fatalf("expected IDs %v, got %#v", want, documents)
	}
	for i := range want {
		if documents[i].ID != want[i] {
			t.Fatalf("expected IDs %v, got %#v", want, documents)
		}
	}
}

func ptr[T any](value T) *T {
	return &value
}
