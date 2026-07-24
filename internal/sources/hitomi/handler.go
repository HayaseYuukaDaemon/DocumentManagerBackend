package hitomi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"document-archive/internal/archive"
	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

const SourceTypeHitomi sources.SourceType = "hitomi"

const (
	defaultInitialConcurrency = 4
	defaultMaxConcurrency     = 16
)

type Factory struct {
	resolver  *Resolver
	scheduler *sources.ConcurrencyScheduler
}

type Handler struct {
	objects          storage.ObjectStore
	resolver         *Resolver
	scheduler        *sources.ConcurrencyScheduler
	pageDownloadHook archive.PageDownloadHook
}

var _ archive.SourceHandlerFactory = (*Factory)(nil)
var _ archive.SourceHandler = (*Handler)(nil)

func NewFactory() *Factory {
	return &Factory{
		resolver:  NewResolver(nil),
		scheduler: sources.NewConcurrencyScheduler(defaultInitialConcurrency, defaultMaxConcurrency),
	}
}

func (f *Factory) Source() sources.SourceType {
	return SourceTypeHitomi
}

func (f *Factory) NewHandler(objects storage.ObjectStore, hook archive.PageDownloadHook) archive.SourceHandler {
	return &Handler{
		objects:          objects,
		resolver:         f.resolver,
		scheduler:        f.scheduler,
		pageDownloadHook: hook,
	}
}

func (h *Handler) ArchiveManifest(ctx context.Context, document documents.Document) error {
	if h.objects == nil {
		return errors.New("object store is required")
	}
	body, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("marshal document snapshot: %w", err)
	}
	_, err = h.objects.PutObject(ctx, storage.ObjectInfo{
		Key:         archive.ManifestObjectKey(strconv.Itoa(document.ID)),
		Size:        int64(len(body)),
		ContentType: "application/json",
	}, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("put document snapshot: %w", err)
	}
	return nil
}

func (h *Handler) downloadPage(ctx context.Context, page DownloadPage, document documents.Document) (documents.Page, error) {
	key, err := archive.PageObjectKey(strconv.Itoa(document.ID), page.Hash, page.ContentType)
	if err != nil {
		return documents.Page{}, fmt.Errorf("build page %d object key: %w", page.Index, err)
	}

	objectInfo, err := h.objects.HeadObject(ctx, key)
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
	objectInfo, err = h.objects.PutObject(ctx, storage.ObjectInfo{
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

func (h *Handler) ArchiveContent(ctx context.Context, document documents.Document) ([]documents.Page, error) {
	if h.objects == nil {
		return nil, errors.New("object store is required")
	}
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
				archivedPage, err = h.downloadPage(ctx, page, document)
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
