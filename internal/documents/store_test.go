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
	if _, err := store.Remove(ctx, doc.ID); err != nil {
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

func TestMemoryStoreCreateAfterRemoveAllocatesNewID(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	first, err := store.Create(ctx, Document{
		Source:           testSource,
		SourceDocumentID: "recreate",
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
		if _, err := store.Remove(ctx, removed.ID); err != nil {
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
