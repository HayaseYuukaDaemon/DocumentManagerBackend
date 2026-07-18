package jmcomic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"document-archive/internal/archive"
	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

const SourceTypeJmcomic sources.SourceType = "jmcomic"

var ErrMultiChapterAlbum = errors.New("jmcomic multi-chapter albums are not supported")

type Handler struct {
	client           *ApiClient
	pageDownloadHook func(ctx context.Context, documentID int, page documents.Page) error
}

type resolvedPage struct {
	Name        string
	Hash        string
	ContentType string
}

func NewHandler() (*Handler, error) {
	client, err := NewAPIClient(10 * time.Second)
	if err != nil {
		return nil, err
	}
	return &Handler{client: client}, nil
}

func (h *Handler) Source() sources.SourceType {
	return SourceTypeJmcomic
}

func (h *Handler) ResolveDocument(ctx context.Context, document documents.Document) (documents.Document, error) {
	if err := ctx.Err(); err != nil {
		return documents.Document{}, err
	}
	album, err := h.client.GetAlbum(document.SourceDocumentID)
	if err != nil {
		return documents.Document{}, fmt.Errorf("get jmcomic album %s: %w", document.SourceDocumentID, err)
	}
	if err := validateSingleChapter(*album); err != nil {
		return documents.Document{}, err
	}

	photo, err := h.client.GetPhoto(string(album.ID))
	if err != nil {
		return documents.Document{}, fmt.Errorf("get jmcomic photo %s: %w", album.ID, err)
	}
	if len(photo.Raw) == 0 {
		return documents.Document{}, errors.New("jmcomic photo has no raw metadata")
	}

	resolvedPages := resolvePages(*photo)
	pages := make([]documents.Page, len(resolvedPages))
	for index, page := range resolvedPages {
		pages[index] = documents.Page{
			Index:       index,
			ContentType: page.ContentType,
			Hash:        page.Hash,
		}
	}
	return documents.Document{
		Source:           SourceTypeJmcomic,
		SourceMeta:       append(json.RawMessage(nil), album.Raw...),
		SourceDocumentID: string(album.ID),
		Title:            album.Name,
		Pages:            pages,
	}, nil
}

func (h *Handler) ArchiveContent(ctx context.Context, document documents.Document, objects storage.ObjectStore) ([]documents.Page, error) {
	photo := Photo{}
	if err := json.Unmarshal(document.SourceMeta, &photo); err != nil {
		return nil, fmt.Errorf("decode jmcomic document metadata: %w", err)
	}
	if photo.ID == "" {
		return nil, errors.New("jmcomic document metadata has no photo id")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scrambleID, err := h.client.getScrambleID(string(photo.ID))
	if err != nil {
		return nil, fmt.Errorf("get jmcomic photo %s scramble id: %w", photo.ID, err)
	}
	photo.ScrambleID = scrambleID

	resolvedPages := resolvePages(photo)
	archivedPages := make([]documents.Page, 0, len(resolvedPages))
	for index, page := range resolvedPages {
		if err := ctx.Err(); err != nil {
			return archivedPages, err
		}
		key, err := archive.PageObjectKey(strconv.Itoa(document.ID), page.Hash, page.ContentType)
		if err != nil {
			return archivedPages, fmt.Errorf("build page %d object key: %w", index, err)
		}

		objectInfo, err := objects.HeadObject(ctx, key)
		if err == nil {
			if objectInfo.ETag != "" && objectInfo.ETag != page.Hash {
				return archivedPages, fmt.Errorf("page %d object hash mismatch: expected %s, got %s", index, page.Hash, objectInfo.ETag)
			}
			documentPage := documents.Page{
				Index:       index,
				Key:         key,
				ContentType: page.ContentType,
				Size:        objectInfo.Size,
				Hash:        page.Hash,
			}
			if err := h.recordPage(ctx, document.ID, documentPage); err != nil {
				return archivedPages, err
			}
			archivedPages = append(archivedPages, documentPage)
			continue
		}
		if !errors.Is(err, storage.ErrObjectNotFound) {
			return archivedPages, fmt.Errorf("head page %d object: %w", index, err)
		}

		image, err := h.client.DownloadDecodedImage(&photo, page.Name)
		if err != nil {
			return archivedPages, fmt.Errorf("download jmcomic page %d: %w", index, err)
		}
		body := bytes.Buffer{}
		if err := encodeImage(&body, page.Name, image); err != nil {
			return archivedPages, fmt.Errorf("encode jmcomic page %d: %w", index, err)
		}

		objectInfo, err = objects.PutObject(ctx, storage.ObjectInfo{
			Key:         key,
			Size:        int64(body.Len()),
			ContentType: page.ContentType,
			ETag:        page.Hash,
		}, bytes.NewReader(body.Bytes()))
		if err != nil {
			return archivedPages, fmt.Errorf("put jmcomic page %d object: %w", index, err)
		}
		documentPage := documents.Page{
			Index:       index,
			Key:         key,
			ContentType: page.ContentType,
			Size:        objectInfo.Size,
			Hash:        page.Hash,
		}
		if err := h.recordPage(ctx, document.ID, documentPage); err != nil {
			return archivedPages, err
		}
		archivedPages = append(archivedPages, documentPage)
	}
	return archivedPages, nil
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

func (h *Handler) RegisterPageDownloadHook(hook func(ctx context.Context, documentID int, page documents.Page) error) error {
	h.pageDownloadHook = hook
	return nil
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

func resolvePages(photo Photo) []resolvedPage {
	pages := make([]resolvedPage, len(photo.Images))
	for index, name := range photo.Images {
		pages[index] = resolvedPage{
			Name:        name,
			Hash:        md5Hex(string(photo.ID) + "\x00" + string(photo.AddTime) + "\x00" + name),
			ContentType: imageContentType(name),
		}
	}
	return pages
}

func validateSingleChapter(album Album) error {
	if len(album.Episodes) != 0 {
		return fmt.Errorf("%w: album %s has %d episodes", ErrMultiChapterAlbum, album.ID, len(album.Episodes))
	}
	return nil
}
