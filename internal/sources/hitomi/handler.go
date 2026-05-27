package hitomi

import (
	"context"
	"errors"

	"document-archive/internal/archive"
	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

var ErrNotImplemented = errors.New("hitomi archiver not implemented")

const SourceTypeHitomi sources.SourceType = "hitomi"

type Handler struct {
	pageDownloadHook func(ctx context.Context, documentID int, page archive.Page) error
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) Source() sources.SourceType {
	return SourceTypeHitomi
}

func (h *Handler) ArchiveContent(ctx context.Context, document documents.Document, objects storage.ObjectStore) error {
	return ErrNotImplemented
}

func (h *Handler) ArchiveManifest(ctx context.Context, document documents.Document, objects storage.ObjectStore) (archive.Manifest, error) {
	return archive.Manifest{}, ErrNotImplemented
}

func (h *Handler) RegisterPageDownloadHook(hook func(ctx context.Context, documentID int, page archive.Page) error) error {
	h.pageDownloadHook = hook
	return nil
}
