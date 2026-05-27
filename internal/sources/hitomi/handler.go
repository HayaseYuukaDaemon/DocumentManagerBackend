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

type Handler struct{}

func NewHandler() Handler {
	return Handler{}
}

func (Handler) Source() sources.SourceType {
	return SourceTypeHitomi
}

func (Handler) Archive(ctx context.Context, document documents.Document, objects storage.ObjectStore) (archive.Manifest, error) {
	return archive.Manifest{}, ErrNotImplemented
}
