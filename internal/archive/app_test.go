package archive

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

const testStorageBackend storage.StorageName = "test-storage"
const testSourceType sources.SourceType = "test-source"

type fakeObjectStore struct {
	name    storage.StorageName
	deleted []string
	fail    map[string]error
}

func (s *fakeObjectStore) StorageName() storage.StorageName {
	return s.name
}

func (s *fakeObjectStore) PutObject(ctx context.Context, info storage.ObjectInfo, body io.ReadSeeker) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, storage.ErrNotImplemented
}

func (s *fakeObjectStore) GetObject(ctx context.Context, key string) (storage.Object, error) {
	return storage.Object{}, storage.ErrNotImplemented
}

func (s *fakeObjectStore) HeadObject(ctx context.Context, key string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, storage.ErrNotImplemented
}

func (s *fakeObjectStore) DeleteObject(ctx context.Context, key string) error {
	s.deleted = append(s.deleted, key)
	if err, ok := s.fail[key]; ok {
		return err
	}
	return nil
}

func (s *fakeObjectStore) PresignGetObject(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return "", storage.ErrNotImplemented
}

func TestProcessDeletedPurgesDocumentAndDeletesManifest(t *testing.T) {
	ctx := context.Background()
	store := documents.NewMemoryStore()
	app := NewApp(store, discardLogger(), storage.MemoryStorageName, 24*time.Hour)
	objects := &fakeObjectStore{name: testStorageBackend}
	app.RegisterStorage(objects)

	doc := createDeletedDocumentForPurge(t, ctx, store)

	app.processDeleted(ctx)

	purged := queryDocumentsByStatus(t, ctx, store, documents.StatusPurged)
	if len(purged) != 1 || purged[0].ID != doc.ID {
		t.Fatalf("expected document %d to be purged, got %#v", doc.ID, purged)
	}
	wantKeys := []string{
		PageObjectKey(strconv.Itoa(doc.ID), 0, "image/webp"),
		PageObjectKey(strconv.Itoa(doc.ID), 2, "image/webp"),
		ManifestObjectKey(strconv.Itoa(doc.ID)),
	}
	assertDeletedKeys(t, objects.deleted, wantKeys)
}

func TestProcessDeletedLeavesDocumentDeletedWhenObjectDeletionFails(t *testing.T) {
	ctx := context.Background()
	store := documents.NewMemoryStore()
	app := NewApp(store, discardLogger(), storage.MemoryStorageName, 24*time.Hour)

	doc := createDeletedDocumentForPurge(t, ctx, store)
	pageKey := PageObjectKey(strconv.Itoa(doc.ID), 0, "image/webp")
	objects := &fakeObjectStore{
		name: testStorageBackend,
		fail: map[string]error{pageKey: errors.New("delete failed")},
	}
	app.RegisterStorage(objects)

	app.processDeleted(ctx)

	deleted := queryDocumentsByStatus(t, ctx, store, documents.StatusDeleted)
	if len(deleted) != 1 || deleted[0].ID != doc.ID {
		t.Fatalf("expected document %d to remain deleted, got %#v", doc.ID, deleted)
	}
	purged := queryDocumentsByStatus(t, ctx, store, documents.StatusPurged)
	if len(purged) != 0 {
		t.Fatalf("expected purge to be skipped after object deletion failure, got %#v", purged)
	}
}

func TestProcessDeletedTreatsMissingObjectsAsPurgeable(t *testing.T) {
	ctx := context.Background()
	store := documents.NewMemoryStore()
	app := NewApp(store, discardLogger(), storage.MemoryStorageName, 24*time.Hour)

	doc := createDeletedDocumentForPurge(t, ctx, store)
	pageKey := PageObjectKey(strconv.Itoa(doc.ID), 0, "image/webp")
	manifestKey := ManifestObjectKey(strconv.Itoa(doc.ID))
	objects := &fakeObjectStore{
		name: testStorageBackend,
		fail: map[string]error{
			pageKey:     storage.ErrObjectNotFound,
			manifestKey: storage.ErrObjectNotFound,
		},
	}
	app.RegisterStorage(objects)

	app.processDeleted(ctx)

	purged := queryDocumentsByStatus(t, ctx, store, documents.StatusPurged)
	if len(purged) != 1 || purged[0].ID != doc.ID {
		t.Fatalf("expected missing objects to still allow purge for document %d, got %#v", doc.ID, purged)
	}
}

func createDeletedDocumentForPurge(t *testing.T, ctx context.Context, store *documents.MemoryStore) documents.Document {
	t.Helper()

	doc, err := store.Create(ctx, documents.Document{
		Source:           testSourceType,
		SourceDocumentID: "purge-me",
		StorageBackend:   testStorageBackend,
		Progress:         documents.Progress{Total: 3},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if err := store.AddPage(ctx, doc.ID, documents.Page{
		Index:       0,
		Key:         PageObjectKey(strconv.Itoa(doc.ID), 0, "image/webp"),
		ContentType: "image/webp",
		Size:        123,
	}); err != nil {
		t.Fatalf("AddPage(0) returned error: %v", err)
	}
	if err := store.AddPage(ctx, doc.ID, documents.Page{
		Index:       2,
		Key:         PageObjectKey(strconv.Itoa(doc.ID), 2, "image/webp"),
		ContentType: "image/webp",
		Size:        789,
	}); err != nil {
		t.Fatalf("AddPage(2) returned error: %v", err)
	}
	if _, err := store.Delete(ctx, doc.ID); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	return doc
}

func queryDocumentsByStatus(t *testing.T, ctx context.Context, store documents.Store, status documents.DocumentStatus) []documents.Document {
	t.Helper()

	query, err := documents.QueryBuilder{}.ByStatus(status).Limit(10).Build()
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	result, err := store.Query(ctx, query)
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}
	return result
}

func assertDeletedKeys(t *testing.T, got []string, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("unexpected deleted keys length: got=%v want=%v", got, want)
	}
	for index, key := range want {
		if got[index] != key {
			t.Fatalf("unexpected deleted key order/content at %d: got=%v want=%v", index, got, want)
		}
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
