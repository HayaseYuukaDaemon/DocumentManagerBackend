package hitomi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"document-archive/internal/archive"
	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

const SourceTypeHitomi sources.SourceType = "hitomi"

type Handler struct {
	resolver         *Resolver
	pageDownloadHook func(ctx context.Context, documentID int, page documents.Page) error
}

func NewHandler() *Handler {
	return &Handler{resolver: NewResolver(nil)}
}

func (h *Handler) Source() sources.SourceType {
	return SourceTypeHitomi
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

func (h *Handler) ArchiveContent(ctx context.Context, document documents.Document, objects storage.ObjectStore) ([]documents.Page, error) {
	comic, err := DeserializeGalleryInfo(document.SourceMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize gallery info: %w", err)
	}
	resolvedComic, err := h.resolver.ResolveComic(ctx, comic)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve comic: %w", err)
	}
	archivedPages := make([]documents.Page, 0, len(resolvedComic.Pages))
	for index, page := range resolvedComic.Pages {
		key, err := archive.PageObjectKey(strconv.Itoa(document.ID), page.Hash)
		if err != nil {
			return archivedPages, fmt.Errorf("build page %d object key: %w", index, err)
		}

		objectInfo, err := objects.HeadObject(ctx, key)
		if err == nil {
			if objectInfo.ETag != "" && objectInfo.ETag != page.Hash {
				return archivedPages, fmt.Errorf("page %d object hash mismatch: expected %s, got %s", index, page.Hash, objectInfo.ETag)
			}
			docPage := documents.Page{
				Index:       index,
				Key:         key,
				ContentType: page.ContentType,
				Size:        objectInfo.Size,
				Hash:        page.Hash,
			}
			if err := h.recordPage(ctx, document.ID, docPage); err != nil {
				return archivedPages, err
			}
			archivedPages = append(archivedPages, docPage)
			continue
		}
		if !errors.Is(err, storage.ErrObjectNotFound) {
			return archivedPages, fmt.Errorf("head page %d object: %w", index, err)
		}

		buf := bytes.Buffer{}
		err = h.resolver.DownloadPage(ctx, page, &buf)
		if err != nil {
			return archivedPages, fmt.Errorf("failed to download page %d: %w", index, err)
		}

		size := int64(buf.Len())
		objectInfo, err = objects.PutObject(ctx, storage.ObjectInfo{
			Key:         key,
			Size:        size,
			ContentType: page.ContentType,
			ETag:        page.Hash,
		}, bytes.NewReader(buf.Bytes()))
		if err != nil {
			return archivedPages, fmt.Errorf("failed to put object: %w", err)
		}
		docPage := documents.Page{
			Index:       index,
			Key:         key,
			ContentType: page.ContentType,
			Size:        size,
			Hash:        page.Hash,
		}
		if err := h.recordPage(ctx, document.ID, docPage); err != nil {
			return archivedPages, err
		}
		archivedPages = append(archivedPages, docPage)
	}
	return archivedPages, nil
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
