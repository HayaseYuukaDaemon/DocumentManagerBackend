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
	"document-archive/internal/storage"
)

func TestConcurrencySchedulerAppliesRateLimitOncePerGeneration(t *testing.T) {
	scheduler := NewConcurrencyScheduler(4, 8)
	scheduler.initialBackoff = 10 * time.Millisecond
	scheduler.maxBackoff = 20 * time.Millisecond

	permits := make([]schedulerPermit, 4)
	for index := range permits {
		permit, err := scheduler.acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire permit %d: %v", index, err)
		}
		permits[index] = permit
	}

	rateLimitErr := &HitomiRateLimitError{StatusCode: http.StatusServiceUnavailable}
	for _, permit := range permits {
		scheduler.finish(permit, rateLimitErr)
	}

	if scheduler.generation != 1 {
		t.Fatalf("generation = %d, want 1", scheduler.generation)
	}
	if scheduler.limit != 2 {
		t.Fatalf("limit = %d, want 2", scheduler.limit)
	}
	if scheduler.backoff != 10*time.Millisecond {
		t.Fatalf("backoff = %s, want 10ms", scheduler.backoff)
	}
}

func TestResolverDownloadPageReturnsRateLimitImmediately(t *testing.T) {
	var requests atomic.Int32
	resolver := NewResolver(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return response(http.StatusServiceUnavailable, "", http.Header{"Retry-After": []string{"2"}}), nil
	})})

	err := resolver.DownloadPage(context.Background(), DownloadPage{URL: "https://example.test/page.webp"}, io.Discard)
	if !errors.Is(err, ErrHitomiRateLimited) {
		t.Fatalf("DownloadPage error = %v, want ErrHitomiRateLimited", err)
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

	handler := &Handler{
		resolver:  NewResolver(client),
		scheduler: NewConcurrencyScheduler(2, 3),
	}
	var recordedMu sync.Mutex
	recorded := make([]documents.Page, 0, len(files))
	if err := handler.RegisterPageDownloadHook(func(_ context.Context, _ int, page documents.Page) error {
		recordedMu.Lock()
		defer recordedMu.Unlock()
		recorded = append(recorded, page)
		return nil
	}); err != nil {
		t.Fatalf("register page hook: %v", err)
	}

	pages, err := handler.ArchiveContent(context.Background(), documents.Document{
		ID:         7,
		SourceMeta: metadata,
	}, storage.NewMemoryStore())
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
