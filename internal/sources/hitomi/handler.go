package hitomi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"document-archive/internal/archive"
	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

const SourceTypeHitomi sources.SourceType = "hitomi"

const (
	defaultInitialConcurrency = 4
	defaultMaxConcurrency     = 16
	defaultInitialBackoff     = time.Second
	defaultMaxBackoff         = 30 * time.Second
	defaultDownloadAttempts   = 5
)

type Handler struct {
	resolver         *Resolver
	scheduler        *ConcurrencyScheduler
	pageDownloadHook func(ctx context.Context, documentID int, page documents.Page) error
}

func NewHandler() *Handler {
	return &Handler{
		resolver:  NewResolver(nil),
		scheduler: NewConcurrencyScheduler(defaultInitialConcurrency, defaultMaxConcurrency),
	}
}

func (h *Handler) Source() sources.SourceType {
	return SourceTypeHitomi
}

type ConcurrencyScheduler struct {
	sem chan struct{}

	mu                sync.Mutex
	changed           chan struct{}
	limit             int
	successes         int
	generation        uint64
	blockedUntil      time.Time
	backoff           time.Duration
	initialBackoff    time.Duration
	maxBackoff        time.Duration
	maxAttemptsPerJob int
}

type schedulerPermit struct {
	generation uint64
}

func NewConcurrencyScheduler(initialConcurrency, maxConcurrency int) *ConcurrencyScheduler {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	if initialConcurrency < 1 {
		initialConcurrency = 1
	}
	if initialConcurrency > maxConcurrency {
		initialConcurrency = maxConcurrency
	}
	return &ConcurrencyScheduler{
		sem:               make(chan struct{}, maxConcurrency),
		changed:           make(chan struct{}),
		limit:             initialConcurrency,
		initialBackoff:    defaultInitialBackoff,
		maxBackoff:        defaultMaxBackoff,
		maxAttemptsPerJob: defaultDownloadAttempts,
	}
}

func (s *ConcurrencyScheduler) Do(ctx context.Context, task func(context.Context) error) error {
	for attempt := 1; ; attempt++ {
		permit, err := s.acquire(ctx)
		if err != nil {
			return err
		}

		err = task(ctx)
		s.finish(permit, err)
		if !errors.Is(err, ErrHitomiRateLimited) {
			return err
		}
		if attempt >= s.maxAttemptsPerJob {
			return fmt.Errorf("hitomi request remained rate limited after %d attempts: %w", attempt, err)
		}
	}
}

func (s *ConcurrencyScheduler) acquire(ctx context.Context) (schedulerPermit, error) {
	for {
		s.mu.Lock()
		wait := time.Until(s.blockedUntil)
		if wait <= 0 && len(s.sem) < s.limit {
			s.sem <- struct{}{}
			permit := schedulerPermit{generation: s.generation}
			s.mu.Unlock()
			return permit, nil
		}
		changed := s.changed
		s.mu.Unlock()

		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return schedulerPermit{}, ctx.Err()
			case <-changed:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}

		select {
		case <-ctx.Done():
			return schedulerPermit{}, ctx.Err()
		case <-changed:
		}
	}
}

func (s *ConcurrencyScheduler) finish(permit schedulerPermit, taskErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	<-s.sem
	if errors.Is(taskErr, ErrHitomiRateLimited) {
		s.applyRateLimitLocked(permit, taskErr)
	} else if taskErr == nil && permit.generation == s.generation {
		s.applySuccessLocked()
	}
	s.signalLocked()
}

func (s *ConcurrencyScheduler) applyRateLimitLocked(permit schedulerPermit, taskErr error) {
	// Requests from the same in-flight generation commonly fail together. Only the
	// first response changes the shared schedule; the rest observe the new generation.
	if permit.generation != s.generation {
		return
	}

	s.generation++
	s.limit = max(1, s.limit/2)
	s.successes = 0
	if s.backoff < s.initialBackoff {
		s.backoff = s.initialBackoff
	} else {
		s.backoff = min(s.backoff*2, s.maxBackoff)
	}

	var rateLimitErr *HitomiRateLimitError
	if errors.As(taskErr, &rateLimitErr) && rateLimitErr.RetryAfter > s.backoff {
		s.backoff = min(rateLimitErr.RetryAfter, s.maxBackoff)
	}
	s.blockedUntil = time.Now().Add(s.backoff)
}

func (s *ConcurrencyScheduler) applySuccessLocked() {
	s.successes++
	if s.successes < s.limit {
		return
	}

	s.successes = 0
	if s.limit < cap(s.sem) {
		s.limit++
	}
	if s.backoff > s.initialBackoff {
		s.backoff /= 2
	} else {
		s.backoff = 0
	}
}

func (s *ConcurrencyScheduler) signalLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (h *Handler) ArchiveManifest(ctx context.Context, document documents.Document, objects storage.ObjectStore) error {
	body, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("marshal document snapshot: %w", err)
	}
	_, err = objects.PutObject(ctx, storage.ObjectInfo{
		Key:         archive.ManifestObjectKey(strconv.Itoa(document.ID)),
		Size:        int64(len(body)),
		ContentType: "application/json",
	}, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("put document snapshot: %w", err)
	}
	return nil
}

func (h *Handler) downloadPage(ctx context.Context, page DownloadPage, objects storage.ObjectStore, document documents.Document) (documents.Page, error) {
	key, err := archive.PageObjectKey(strconv.Itoa(document.ID), page.Hash, page.ContentType)
	if err != nil {
		return documents.Page{}, fmt.Errorf("build page %d object key: %w", page.Index, err)
	}

	objectInfo, err := objects.HeadObject(ctx, key)
	if err == nil {
		if objectInfo.ETag != "" && objectInfo.ETag != page.Hash {
			return documents.Page{}, fmt.Errorf("page %d object hash mismatch: expected %s, got %s", page.Index, page.Hash, objectInfo.ETag)
		}
		docPage := documents.Page{
			Index:       page.Index,
			Key:         key,
			ContentType: page.ContentType,
			Size:        objectInfo.Size,
			Hash:        page.Hash,
		}
		if err := h.recordPage(ctx, document.ID, docPage); err != nil {
			return documents.Page{}, err
		}
		return docPage, nil
	}
	if !errors.Is(err, storage.ErrObjectNotFound) {
		return documents.Page{}, fmt.Errorf("head page %d object: %w", page.Index, err)
	}

	buf := bytes.Buffer{}
	err = h.resolver.DownloadPage(ctx, page, &buf)
	if err != nil {
		return documents.Page{}, fmt.Errorf("failed to download page %d: %w", page.Index, err)
	}

	size := int64(buf.Len())
	objectInfo, err = objects.PutObject(ctx, storage.ObjectInfo{
		Key:         key,
		Size:        size,
		ContentType: page.ContentType,
		ETag:        page.Hash,
	}, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return documents.Page{}, fmt.Errorf("failed to put object: %w", err)
	}
	docPage := documents.Page{
		Index:       page.Index,
		Key:         key,
		ContentType: page.ContentType,
		Size:        size,
		Hash:        page.Hash,
	}
	if err := h.recordPage(ctx, document.ID, docPage); err != nil {
		return documents.Page{}, fmt.Errorf("failed to record page: %w", err)
	}
	return docPage, nil
}

func (h *Handler) ArchiveContent(ctx context.Context, document documents.Document, objects storage.ObjectStore) ([]documents.Page, error) {
	comic, err := DeserializeGalleryInfo(document.SourceMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize gallery info: %w", err)
	}
	resolvedComic, err := h.resolver.ResolveComic(ctx, comic)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve comic: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type pageResult struct {
		position int
		page     documents.Page
		err      error
	}
	results := make(chan pageResult, len(resolvedComic.Pages))
	var wg sync.WaitGroup
	for position, page := range resolvedComic.Pages {
		wg.Add(1)
		go func() {
			defer wg.Done()

			var archivedPage documents.Page
			err := h.scheduler.Do(ctx, func(ctx context.Context) error {
				var err error
				archivedPage, err = h.downloadPage(ctx, page, objects, document)
				return err
			})
			results <- pageResult{position: position, page: archivedPage, err: err}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	archivedPages := make([]documents.Page, len(resolvedComic.Pages))
	completed := make([]bool, len(resolvedComic.Pages))
	var firstErr error
	for result := range results {
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
				cancel()
			}
			continue
		}
		archivedPages[result.position] = result.page
		completed[result.position] = true
	}

	if firstErr == nil {
		return archivedPages, nil
	}
	partial := make([]documents.Page, 0, len(archivedPages))
	for position, page := range archivedPages {
		if completed[position] {
			partial = append(partial, page)
		}
	}
	return partial, firstErr
}

func (h *Handler) ResolveDocument(ctx context.Context, document documents.Document) (documents.Document, error) {
	comic, err := h.resolver.ResolveID(ctx, document.SourceDocumentID)
	if err != nil {
		return documents.Document{}, err
	}
	pages := make([]documents.Page, len(comic.Pages))
	for index, page := range comic.Pages {
		pages[index] = documents.Page{
			Index:       index,
			ContentType: page.ContentType,
			Hash:        page.Hash,
		}
	}
	return documents.Document{
		Source:           SourceTypeHitomi,
		SourceMeta:       comic.RawJSON,
		SourceDocumentID: string(comic.Comic.ID),
		Title:            comic.Comic.Title,
		Pages:            pages,
	}, nil
}

func (h *Handler) recordPage(ctx context.Context, documentID int, page documents.Page) error {
	if h.pageDownloadHook == nil {
		return nil
	}
	if err := h.pageDownloadHook(ctx, documentID, page); err != nil {
		return fmt.Errorf("failed to execute page download hook: %w", err)
	}
	return nil
}

func (h *Handler) RegisterPageDownloadHook(hook func(ctx context.Context, documentID int, page documents.Page) error) error {
	h.pageDownloadHook = hook
	return nil
}
