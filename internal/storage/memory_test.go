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
	store := NewMemoryStore()
	ctx := context.Background()

	_, err := store.PutObject(ctx, "documents/test/pages/000001.webp", strings.NewReader("page"), 4, "image/webp")
	if err != nil {
		t.Fatalf("PutObject returned error: %v", err)
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
	if info.ETag == "" {
		t.Fatal("expected non-empty etag")
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

func TestMemoryStoreHeadMissingObject(t *testing.T) {
	store := NewMemoryStore()

	_, err := store.HeadObject(context.Background(), "missing")
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("expected ErrObjectNotFound, got %v", err)
	}
}
