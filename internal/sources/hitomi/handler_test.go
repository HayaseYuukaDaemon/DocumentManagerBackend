package hitomi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

func TestFactoryCreatesHandlersWithSharedRuntime(t *testing.T) {
	factory := NewFactory()
	objects1 := storage.NewMemoryStore()
	objects2 := storage.NewMemoryStore()

	handler1 := factory.NewHandler(objects1, nil).(*Handler)
	handler2 := factory.NewHandler(objects2, nil).(*Handler)
	if handler1 == handler2 {
		t.Fatal("factory returned the same handler instance")
	}
	if handler1.objects != objects1 || handler2.objects != objects2 {
		t.Fatal("handlers were not bound to their object stores")
	}
	if handler1.resolver != handler2.resolver || handler1.scheduler != handler2.scheduler {
		t.Fatal("handlers do not share factory runtime")
	}
}

func TestResolverDownloadPageReturnsRateLimitImmediately(t *testing.T) {
	var requests atomic.Int32
	resolver := NewResolver(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return response(http.StatusServiceUnavailable, "", http.Header{"Retry-After": []string{"2"}}), nil
	})})

	err := resolver.DownloadPage(context.Background(), DownloadPage{URL: "https://example.test/page.webp"}, io.Discard)
	if !errors.Is(err, sources.ErrRateLimited) {
		t.Fatalf("DownloadPage error = %v, want sources.ErrRateLimited", err)
	}
	var rateLimitErr *HitomiRateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("DownloadPage error type = %T, want *HitomiRateLimitError", err)
	}
	if rateLimitErr.RetryAfter != 2*time.Second {
		t.Fatalf("RetryAfter = %s, want 2s", rateLimitErr.RetryAfter)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestArchiveContentDownloadsPagesConcurrently(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/gg.js" {
			return response(http.StatusOK, `var o = 0; var settings = {b: "images"};`, nil), nil
		}

		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
		return response(http.StatusOK, "page", nil), nil
	})}

	files := []PageInfo{
		{Hash: strings.Repeat("1", 40), Name: "1.jpg"},
		{Hash: strings.Repeat("2", 40), Name: "2.jpg"},
		{Hash: strings.Repeat("3", 40), Name: "3.jpg"},
		{Hash: strings.Repeat("4", 40), Name: "4.jpg"},
		{Hash: strings.Repeat("5", 40), Name: "5.jpg"},
		{Hash: strings.Repeat("6", 40), Name: "6.jpg"},
	}
	metadata, err := json.Marshal(rawComic{
		ID:         "123",
		Title:      "test",
		GalleryURL: "/galleries/123.html",
		Files:      files,
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	factory := &Factory{
		resolver:  NewResolver(client),
		scheduler: sources.NewConcurrencyScheduler(2, 3),
	}
	var recordedMu sync.Mutex
	recorded := make([]documents.Page, 0, len(files))
	handler := factory.NewHandler(storage.NewMemoryStore(), func(_ context.Context, _ int, page documents.Page) error {
		recordedMu.Lock()
		defer recordedMu.Unlock()
		recorded = append(recorded, page)
		return nil
	})

	pages, err := handler.ArchiveContent(context.Background(), documents.Document{
		ID:         7,
		SourceMeta: metadata,
	})
	if err != nil {
		t.Fatalf("ArchiveContent: %v", err)
	}
	if len(pages) != len(files) {
		t.Fatalf("archived pages = %d, want %d", len(pages), len(files))
	}
	if maximum.Load() < 2 || maximum.Load() > 3 {
		t.Fatalf("maximum concurrent downloads = %d, want between 2 and 3", maximum.Load())
	}
	recordedMu.Lock()
	defer recordedMu.Unlock()
	if len(recorded) != len(files) {
		t.Fatalf("recorded pages = %d, want %d", len(recorded), len(files))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func response(statusCode int, body string, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
