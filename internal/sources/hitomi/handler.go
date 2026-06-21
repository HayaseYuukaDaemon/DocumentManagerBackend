package hitomi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"document-archive/internal/archive"
	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

const SourceTypeHitomi sources.SourceType = "hitomi"

type Handler struct {
	client           *http.Client
	resolver         *Resolver
	pageDownloadHook func(ctx context.Context, documentID int, page documents.Page) error
}

func NewHandler() *Handler {
	client := http.Client{}
	h := &Handler{
		client:   &client,
		resolver: NewResolver(&client)}
	return h
}

func (h *Handler) Source() sources.SourceType {
	return SourceTypeHitomi
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
	pageLength := len(document.Pages)
	var archivedPages []documents.Page
	for index, page := range resolvedComic.Pages {
		key := archive.PageObjectKey(strconv.Itoa(document.ID), index, page.ContentType)
		if pageLength > 0 && index < pageLength {
			oldPage := document.Pages[index]
			if err == nil {
				if page.Hash == oldPage.Hash {
					continue
				}
			}
		}

		buf := bytes.Buffer{}
		err = h.resolver.DownloadPage(ctx, page, &buf)
		if err != nil {
			return archivedPages, fmt.Errorf("failed to download page %d: %w", index, err)
		}

		size := int64(buf.Len())
		objectInfo, err := objects.PutObject(ctx, storage.ObjectInfo{
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
			Hash:        objectInfo.ETag,
		}
		if h.pageDownloadHook != nil {
			if err := h.pageDownloadHook(ctx, document.ID, docPage); err != nil {
				return archivedPages, fmt.Errorf("failed to execute page download hook: %w", err)
			}
		}
		archivedPages = append(archivedPages, docPage)
	}
	return archivedPages, nil
}

func (h *Handler) ArchiveManifest(ctx context.Context, document documents.Document, objects storage.ObjectStore) (archive.Manifest, error) {
	comic, err := h.resolver.ResolveID(ctx, document.SourceDocumentID)
	if err != nil {
		return archive.Manifest{}, err
	}
	key := archive.ManifestObjectKey(strconv.Itoa(document.ID))
	docJSON, err := json.Marshal(document)
	if err != nil {
		return archive.Manifest{}, fmt.Errorf("failed to marshal document: %w", err)
	}
	_, err = objects.PutObject(ctx, storage.ObjectInfo{
		Key:         key,
		Size:        int64(len(docJSON)),
		ContentType: "json",
	}, bytes.NewReader(docJSON))
	if err != nil {
		return archive.Manifest{}, fmt.Errorf("failed to put object: %w", err)
	}
	return archive.Manifest{
		SchemaVersion:    0,
		SourceMeta:       comic.RawJSON,
		SourceDocumentID: string(comic.Comic.ID),
		Title:            comic.Comic.Title,
		Pages:            make([]documents.Page, len(comic.Pages)),
	}, nil
}

func (h *Handler) RegisterPageDownloadHook(hook func(ctx context.Context, documentID int, page documents.Page) error) error {
	h.pageDownloadHook = hook
	return nil
}
