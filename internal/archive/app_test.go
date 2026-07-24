package archive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

const testStorageName storage.StorageName = "test-storage"
const testSourceType sources.SourceType = "test-source"

type fakeObjectStore struct {
	name            storage.StorageName
	typ             storage.StorageType
	deletedPrefixes []string
	failPrefixes    map[string]error
}

type fakeSourceFactory struct {
	resolved  documents.Document
	downloads int
	handlers  int
}

type fakeSourceHandler struct {
	factory          *fakeSourceFactory
	objects          storage.ObjectStore
	pageDownloadHook PageDownloadHook
}

func (f *fakeSourceFactory) Source() sources.SourceType {
	return testSourceType
}

func (f *fakeSourceFactory) NewHandler(objects storage.ObjectStore, hook PageDownloadHook) SourceHandler {
	f.handlers++
	return &fakeSourceHandler{
		factory:          f,
		objects:          objects,
		pageDownloadHook: hook,
	}
}

func (h *fakeSourceHandler) ResolveDocument(ctx context.Context, document documents.Document) (documents.Document, error) {
	return h.factory.resolved, nil
}

func (h *fakeSourceHandler) ArchiveContent(ctx context.Context, document documents.Document) ([]documents.Page, error) {
	pages := make([]documents.Page, 0, len(h.factory.resolved.Pages))
	for index, resolvedPage := range h.factory.resolved.Pages {
		key, err := PageObjectKey(strconv.Itoa(document.ID), resolvedPage.Hash, resolvedPage.ContentType)
		if err != nil {
			return pages, err
		}
		info, err := h.objects.HeadObject(ctx, key)
		if errors.Is(err, storage.ErrObjectNotFound) {
			h.factory.downloads++
			body := "page-" + resolvedPage.Hash
			info, err = h.objects.PutObject(ctx, storage.ObjectInfo{
				Key:         key,
				Size:        int64(len(body)),
				ContentType: resolvedPage.ContentType,
				ETag:        resolvedPage.Hash,
			}, strings.NewReader(body))
		}
		if err != nil {
			return pages, err
		}
		page := documents.Page{
			Index:       index,
			Key:         key,
			ContentType: resolvedPage.ContentType,
			Size:        info.Size,
			Hash:        resolvedPage.Hash,
		}
		if h.pageDownloadHook != nil {
			if err := h.pageDownloadHook(ctx, document.ID, page); err != nil {
				return pages, err
			}
		}
		pages = append(pages, page)
	}
	return pages, nil
}

func (h *fakeSourceHandler) ArchiveManifest(ctx context.Context, document documents.Document) error {
	body, err := json.Marshal(document)
	if err != nil {
		return err
	}
	_, err = h.objects.PutObject(ctx, storage.ObjectInfo{
		Key:         ManifestObjectKey(strconv.Itoa(document.ID)),
		Size:        int64(len(body)),
		ContentType: "application/json",
	}, strings.NewReader(string(body)))
	return err
}

func (s *fakeObjectStore) Name() storage.StorageName {
	return s.name
}

func (s *fakeObjectStore) Type() storage.StorageType {
	if s.typ == "" {
		return storage.MemoryStorageType
	}
	return s.typ
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
	return storage.ErrNotImplemented
}

func (s *fakeObjectStore) DeletePrefix(ctx context.Context, prefix string) error {
	s.deletedPrefixes = append(s.deletedPrefixes, prefix)
	if err, ok := s.failPrefixes[prefix]; ok {
		return err
	}
	return nil
}

func (s *fakeObjectStore) PresignGetObject(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return "", storage.ErrNotImplemented
}

func TestRegisterSourceFactoryRejectsDuplicateSource(t *testing.T) {
	app := NewApp(documents.NewMemoryStore(), discardLogger(), testStorageName, 24*time.Hour)
	if err := app.RegisterSourceFactory(&fakeSourceFactory{}); err != nil {
		t.Fatalf("first RegisterSourceFactory returned error: %v", err)
	}
	if err := app.RegisterSourceFactory(&fakeSourceFactory{}); err == nil {
		t.Fatal("duplicate RegisterSourceFactory returned nil error")
	}
}

func TestRegisterStorageSeparatesInstanceNameFromType(t *testing.T) {
	app := NewApp(documents.NewMemoryStore(), discardLogger(), "cache", 24*time.Hour)
	cache := storage.NewMemoryStore("cache")
	staging := storage.NewMemoryStore("staging")

	if err := app.RegisterStorage(cache); err != nil {
		t.Fatalf("RegisterStorage(cache) returned error: %v", err)
	}
	if err := app.RegisterStorage(staging); err != nil {
		t.Fatalf("RegisterStorage(staging) returned error: %v", err)
	}
	if err := app.RegisterStorage(storage.NewMemoryStore("cache")); err == nil {
		t.Fatal("RegisterStorage should reject a duplicate instance name")
	}

	gotCache, err := app.getStorage("cache")
	if err != nil {
		t.Fatalf("getStorage(cache) returned error: %v", err)
	}
	gotStaging, err := app.getStorage("staging")
	if err != nil {
		t.Fatalf("getStorage(staging) returned error: %v", err)
	}
	if gotCache != cache || gotStaging != staging {
		t.Fatal("storage instances were not registered by name")
	}
	if gotCache.Type() != storage.MemoryStorageType || gotStaging.Type() != storage.MemoryStorageType {
		t.Fatal("storage instances should retain their shared implementation type")
	}
}

func TestGetPageDispatchesByStorageType(t *testing.T) {
	ctx := context.Background()
	app := NewApp(documents.NewMemoryStore(), discardLogger(), "cache", 24*time.Hour)
	objects := storage.NewMemoryStore("cache")
	if err := app.RegisterStorage(objects); err != nil {
		t.Fatalf("RegisterStorage returned error: %v", err)
	}

	const key = "documents/1/pages/hash.webp"
	if _, err := objects.PutObject(ctx, storage.ObjectInfo{
		Key:         key,
		Size:        4,
		ContentType: "image/webp",
	}, strings.NewReader("page")); err != nil {
		t.Fatalf("PutObject returned error: %v", err)
	}

	result, err := app.GetPage(ctx, documents.Document{
		StorageBackend: "cache",
		Pages:          []documents.Page{{Index: 0, Key: key}},
	}, 0)
	if err != nil {
		t.Fatalf("GetPage returned error: %v", err)
	}
	if result.Kind != PageResultObject {
		t.Fatalf("unexpected page result kind: %s", result.Kind)
	}
	if err := result.Object.Body.Close(); err != nil {
		t.Fatalf("close page object: %v", err)
	}
}

func TestProcessDeletedPurgesDocumentAndDeletesObjectPrefix(t *testing.T) {
	ctx := context.Background()
	store := documents.NewMemoryStore()
	app := NewApp(store, discardLogger(), testStorageName, 24*time.Hour)
	objects := &fakeObjectStore{name: testStorageName}
	if err := app.RegisterStorage(objects); err != nil {
		t.Fatalf("RegisterStorage returned error: %v", err)
	}

	doc := createDeletedDocumentForPurge(t, ctx, store)

	app.processDeleted(ctx)

	purged := queryDocumentsByStatus(t, ctx, store, documents.StatusPurged)
	if len(purged) != 1 || purged[0].ID != doc.ID {
		t.Fatalf("expected document %d to be purged, got %#v", doc.ID, purged)
	}
	wantPrefix := DocumentObjectPrefix(strconv.Itoa(doc.ID))
	if len(objects.deletedPrefixes) != 1 || objects.deletedPrefixes[0] != wantPrefix {
		t.Fatalf("expected deleted prefix %q, got %#v", wantPrefix, objects.deletedPrefixes)
	}
}

func TestProcessDeletedLeavesDocumentDeletedWhenObjectDeletionFails(t *testing.T) {
	ctx := context.Background()
	store := documents.NewMemoryStore()
	app := NewApp(store, discardLogger(), testStorageName, 24*time.Hour)

	doc := createDeletedDocumentForPurge(t, ctx, store)
	prefix := DocumentObjectPrefix(strconv.Itoa(doc.ID))
	objects := &fakeObjectStore{
		name:         testStorageName,
		failPrefixes: map[string]error{prefix: errors.New("delete failed")},
	}
	if err := app.RegisterStorage(objects); err != nil {
		t.Fatalf("RegisterStorage returned error: %v", err)
	}

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
	app := NewApp(store, discardLogger(), testStorageName, 24*time.Hour)

	doc := createDeletedDocumentForPurge(t, ctx, store)
	prefix := DocumentObjectPrefix(strconv.Itoa(doc.ID))
	objects := &fakeObjectStore{
		name:         testStorageName,
		failPrefixes: map[string]error{prefix: storage.ErrObjectNotFound},
	}
	if err := app.RegisterStorage(objects); err != nil {
		t.Fatalf("RegisterStorage returned error: %v", err)
	}

	app.processDeleted(ctx)

	purged := queryDocumentsByStatus(t, ctx, store, documents.StatusPurged)
	if len(purged) != 1 || purged[0].ID != doc.ID {
		t.Fatalf("expected missing objects to still allow purge for document %d, got %#v", doc.ID, purged)
	}
}

func TestProcessDocumentRefreshRebuildsPagesFromHashAddressedObjects(t *testing.T) {
	ctx := context.Background()
	documentStore := documents.NewMemoryStore()
	objects := storage.NewMemoryStore(testStorageName)
	factory := &fakeSourceFactory{resolved: documents.Document{
		SourceMeta: json.RawMessage(`{"source":"resolved"}`),
		Title:      "resolved title",
		Pages: []documents.Page{{
			Index:       0,
			ContentType: "image/webp",
			Hash:        "hash-a",
		}},
	}}
	app := NewApp(documentStore, discardLogger(), testStorageName, 24*time.Hour)
	if err := app.RegisterSourceFactory(factory); err != nil {
		t.Fatalf("RegisterSourceFactory returned error: %v", err)
	}
	if err := app.RegisterStorage(objects); err != nil {
		t.Fatalf("RegisterStorage returned error: %v", err)
	}

	doc, err := app.RequestDocument(ctx, documents.RequestDocumentInput{
		Source:           testSourceType,
		SourceDocumentID: "source-1",
	})
	if err != nil {
		t.Fatalf("RequestDocument returned error: %v", err)
	}
	if factory.handlers != 0 {
		t.Fatalf("request stage created %d handlers, want 0", factory.handlers)
	}
	archived, err := app.processDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("processDocument returned error: %v", err)
	}
	if archived.Status() != documents.StatusArchived || archived.Progress.Done != 1 || archived.Progress.Total != 1 {
		t.Fatalf("unexpected archived document: %#v", archived)
	}
	if len(archived.Pages) != 1 || archived.Pages[0].Hash != "hash-a" {
		t.Fatalf("unexpected archived pages: %#v", archived.Pages)
	}
	if factory.downloads != 1 {
		t.Fatalf("expected initial archive to download once, got %d", factory.downloads)
	}

	snapshot, err := objects.GetObject(ctx, ManifestObjectKey(strconv.Itoa(doc.ID)))
	if err != nil {
		t.Fatalf("GetObject(document snapshot) returned error: %v", err)
	}
	defer snapshot.Body.Close()
	if snapshot.ContentType != "application/json" {
		t.Fatalf("unexpected snapshot content type: %s", snapshot.ContentType)
	}
	var snapshotJSON struct {
		Title    string             `json:"title"`
		Progress documents.Progress `json:"progress"`
		Pages    []documents.Page   `json:"pages"`
	}
	if err := json.NewDecoder(snapshot.Body).Decode(&snapshotJSON); err != nil {
		t.Fatalf("decode document snapshot: %v", err)
	}
	if snapshotJSON.Title != "resolved title" || snapshotJSON.Progress.Done != 1 || len(snapshotJSON.Pages) != 1 {
		t.Fatalf("unexpected document snapshot: %#v", snapshotJSON)
	}

	refreshed, err := app.RefreshDocument(ctx, doc.ID, documents.All)
	if err != nil {
		t.Fatalf("RefreshDocument(all) returned error: %v", err)
	}
	if refreshed.Status() != documents.StatusQueued || refreshed.Progress.Done != 0 || len(refreshed.Pages) != 0 {
		t.Fatalf("full refresh should queue and clear pages: %#v", refreshed)
	}
	pageKey, err := PageObjectKey(strconv.Itoa(doc.ID), "hash-a", "image/webp")
	if err != nil {
		t.Fatalf("PageObjectKey returned error: %v", err)
	}
	if _, err := objects.HeadObject(ctx, pageKey); err != nil {
		t.Fatalf("full refresh should retain hash-addressed object: %v", err)
	}

	rearchived, err := app.processDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("processDocument after refresh returned error: %v", err)
	}
	if factory.downloads != 1 {
		t.Fatalf("refresh should reuse existing object, got %d total downloads", factory.downloads)
	}
	if factory.handlers != 2 {
		t.Fatalf("archive runs created %d handlers, want 2", factory.handlers)
	}
	if rearchived.Progress.Done != 1 || len(rearchived.Pages) != 1 || rearchived.Pages[0].Key != pageKey {
		t.Fatalf("refresh did not rebuild page metadata: %#v", rearchived)
	}

	orphanedHistoryKey, err := PageObjectKey(strconv.Itoa(doc.ID), "old-hash", "image/webp")
	if err != nil {
		t.Fatalf("PageObjectKey(old-hash) returned error: %v", err)
	}
	if _, err := objects.PutObject(ctx, storage.ObjectInfo{
		Key:  orphanedHistoryKey,
		Size: 3,
		ETag: "old-hash",
	}, strings.NewReader("old")); err != nil {
		t.Fatalf("PutObject(old history) returned error: %v", err)
	}
	if _, err := app.RemoveDocument(ctx, doc.ID); err != nil {
		t.Fatalf("RemoveDocument returned error: %v", err)
	}
	app.processDeleted(ctx)
	for _, key := range []string{pageKey, orphanedHistoryKey, ManifestObjectKey(strconv.Itoa(doc.ID))} {
		if _, err := objects.HeadObject(ctx, key); !errors.Is(err, storage.ErrObjectNotFound) {
			t.Fatalf("purge should delete object %q, got %v", key, err)
		}
	}
	purged := queryDocumentsByStatus(t, ctx, documentStore, documents.StatusPurged)
	if len(purged) != 1 || purged[0].ID != doc.ID {
		t.Fatalf("expected document to be purged after prefix deletion, got %#v", purged)
	}
}

func TestProcessDocumentRebuildsPartialPagesFromObjectStorage(t *testing.T) {
	ctx := context.Background()
	documentStore := documents.NewMemoryStore()
	objects := storage.NewMemoryStore(testStorageName)
	factory := &fakeSourceFactory{resolved: documents.Document{
		SourceMeta: json.RawMessage(`{"source":"resolved"}`),
		Title:      "resolved title",
		Pages: []documents.Page{
			{Index: 0, ContentType: "image/webp", Hash: "hash-a"},
			{Index: 1, ContentType: "image/webp", Hash: "hash-b"},
		},
	}}
	app := NewApp(documentStore, discardLogger(), testStorageName, 24*time.Hour)
	if err := app.RegisterSourceFactory(factory); err != nil {
		t.Fatalf("RegisterSourceFactory returned error: %v", err)
	}
	if err := app.RegisterStorage(objects); err != nil {
		t.Fatalf("RegisterStorage returned error: %v", err)
	}

	doc, err := app.RequestDocument(ctx, documents.RequestDocumentInput{
		Source:           testSourceType,
		SourceDocumentID: "partial-source",
	})
	if err != nil {
		t.Fatalf("RequestDocument returned error: %v", err)
	}
	if err := documentStore.TransitionTo(ctx, doc.ID, documents.StatusResolving); err != nil {
		t.Fatalf("TransitionTo(resolving) returned error: %v", err)
	}
	if err := documentStore.TransitionTo(ctx, doc.ID, documents.StatusDownloading); err != nil {
		t.Fatalf("TransitionTo(downloading) returned error: %v", err)
	}
	pageKey, err := PageObjectKey(strconv.Itoa(doc.ID), "hash-a", "image/webp")
	if err != nil {
		t.Fatalf("PageObjectKey returned error: %v", err)
	}
	if _, err := objects.PutObject(ctx, storage.ObjectInfo{
		Key:         pageKey,
		Size:        6,
		ContentType: "image/webp",
		ETag:        "hash-a",
	}, strings.NewReader("cached")); err != nil {
		t.Fatalf("PutObject(cached page) returned error: %v", err)
	}
	if err := documentStore.AddPage(ctx, doc.ID, documents.Page{
		Index:       0,
		Key:         pageKey,
		ContentType: "image/webp",
		Size:        6,
		Hash:        "hash-a",
	}); err != nil {
		t.Fatalf("AddPage(partial page) returned error: %v", err)
	}
	if err := documentStore.TransitionTo(ctx, doc.ID, documents.StatusFailed); err != nil {
		t.Fatalf("TransitionTo(failed) returned error: %v", err)
	}
	if _, err := app.RefreshDocument(ctx, doc.ID, documents.OnlyMetadata); err != nil {
		t.Fatalf("RefreshDocument(only_metadata) returned error: %v", err)
	}

	archived, err := app.processDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("processDocument returned error: %v", err)
	}
	if archived.Status() != documents.StatusArchived || archived.Progress.Done != 2 || archived.Progress.Total != 2 {
		t.Fatalf("unexpected rebuilt document: %#v", archived)
	}
	if len(archived.Pages) != 2 || archived.Pages[0].Hash != "hash-a" || archived.Pages[1].Hash != "hash-b" {
		t.Fatalf("unexpected rebuilt pages: %#v", archived.Pages)
	}
	if factory.downloads != 1 {
		t.Fatalf("expected cached page reuse and one new download, got %d downloads", factory.downloads)
	}
}

func createDeletedDocumentForPurge(t *testing.T, ctx context.Context, store *documents.MemoryStore) documents.Document {
	t.Helper()

	doc, err := store.Create(ctx, documents.Document{
		Source:           testSourceType,
		SourceDocumentID: "purge-me",
		StorageBackend:   testStorageName,
		Progress:         documents.Progress{Total: 3},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if err := store.AddPage(ctx, doc.ID, documents.Page{
		Index:       0,
		Key:         mustPageObjectKey(t, strconv.Itoa(doc.ID), "hash-0", "image/webp"),
		ContentType: "image/webp",
		Size:        123,
		Hash:        "hash-0",
	}); err != nil {
		t.Fatalf("AddPage(0) returned error: %v", err)
	}
	if err := store.AddPage(ctx, doc.ID, documents.Page{
		Index:       2,
		Key:         mustPageObjectKey(t, strconv.Itoa(doc.ID), "hash-2", "image/webp"),
		ContentType: "image/webp",
		Size:        789,
		Hash:        "hash-2",
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

func mustPageObjectKey(t *testing.T, documentID string, hash string, contentType string) string {
	t.Helper()
	key, err := PageObjectKey(documentID, hash, contentType)
	if err != nil {
		t.Fatalf("PageObjectKey returned error: %v", err)
	}
	return key
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
