package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestMemoryStorePutHeadAndPresign(t *testing.T) {
	store := NewMemoryStore("test-memory")
	ctx := context.Background()
	if store.Name() != "test-memory" {
		t.Fatalf("unexpected storage name: %q", store.Name())
	}
	if store.Type() != MemoryStorageType {
		t.Fatalf("unexpected storage type: %q", store.Type())
	}

	putInfo, err := store.PutObject(ctx, ObjectInfo{
		Key:         "documents/test/pages/000001.webp",
		Size:        4,
		ContentType: "image/webp",
		ETag:        "source-etag",
	}, strings.NewReader("page"))
	if err != nil {
		t.Fatalf("PutObject returned error: %v", err)
	}
	if putInfo.ETag != "source-etag" {
		t.Fatalf("unexpected put etag: %s", putInfo.ETag)
	}

	info, err := store.HeadObject(ctx, "documents/test/pages/000001.webp")
	if err != nil {
		t.Fatalf("HeadObject returned error: %v", err)
	}
	if info.Key != "documents/test/pages/000001.webp" {
		t.Fatalf("unexpected key: %s", info.Key)
	}
	if info.Size != 4 {
		t.Fatalf("unexpected size: %d", info.Size)
	}
	if info.ContentType != "image/webp" {
		t.Fatalf("unexpected content type: %s", info.ContentType)
	}
	if info.ETag != "source-etag" {
		t.Fatalf("unexpected etag: %s", info.ETag)
	}

	object, err := store.GetObject(ctx, "documents/test/pages/000001.webp")
	if err != nil {
		t.Fatalf("GetObject returned error: %v", err)
	}
	defer object.Body.Close()
	content, err := io.ReadAll(object.Body)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(content) != "page" {
		t.Fatalf("unexpected object content: %q", content)
	}

	url, err := store.PresignGetObject(ctx, "documents/test/pages/000001.webp", time.Minute)
	if err != nil {
		t.Fatalf("PresignGetObject returned error: %v", err)
	}
	if url != "memory://documents/test/pages/000001.webp" {
		t.Fatalf("unexpected presigned url: %s", url)
	}
}

func TestMemoryStorePutCalculatesETag(t *testing.T) {
	store := NewMemoryStore("test-memory")

	info, err := store.PutObject(context.Background(), ObjectInfo{
		Key:         "documents/test/pages/000002.webp",
		Size:        4,
		ContentType: "image/webp",
	}, strings.NewReader("page"))
	if err != nil {
		t.Fatalf("PutObject returned error: %v", err)
	}
	if info.ETag == "" {
		t.Fatalf("expected storage-calculated etag")
	}
}

func TestMemoryStoreHeadMissingObject(t *testing.T) {
	store := NewMemoryStore("test-memory")

	_, err := store.HeadObject(context.Background(), "missing")
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("expected ErrObjectNotFound, got %v", err)
	}
}

func TestMemoryStoreDeletePrefix(t *testing.T) {
	store := NewMemoryStore("test-memory")
	ctx := context.Background()

	for _, key := range []string{
		"documents/1/pages/hash-a",
		"documents/1/pages/hash-b",
		"documents/1/manifest.json",
		"documents/10/pages/hash-c",
	} {
		if _, err := store.PutObject(ctx, ObjectInfo{Key: key, Size: 1}, strings.NewReader("x")); err != nil {
			t.Fatalf("PutObject(%q) returned error: %v", key, err)
		}
	}

	if err := store.DeletePrefix(ctx, "documents/1/"); err != nil {
		t.Fatalf("DeletePrefix returned error: %v", err)
	}
	for _, key := range []string{
		"documents/1/pages/hash-a",
		"documents/1/pages/hash-b",
		"documents/1/manifest.json",
	} {
		if _, err := store.HeadObject(ctx, key); !errors.Is(err, ErrObjectNotFound) {
			t.Fatalf("expected %q to be deleted, got %v", key, err)
		}
	}
	if _, err := store.HeadObject(ctx, "documents/10/pages/hash-c"); err != nil {
		t.Fatalf("DeletePrefix removed sibling prefix object: %v", err)
	}
	if err := store.DeletePrefix(ctx, "documents/1/"); err != nil {
		t.Fatalf("DeletePrefix should be idempotent, got %v", err)
	}
}
