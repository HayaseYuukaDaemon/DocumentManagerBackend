package hitomi

import (
	"context"
	"errors"

	"document-archive/internal/archive"
	"document-archive/internal/documents"
	"document-archive/internal/storage"
)

var ErrNotImplemented = errors.New("hitomi archiver not implemented")

type Handler struct{}

func NewHandler() Handler {
	return Handler{}
}

func (Handler) Source() string {
	return "hitomi"
}

func (Handler) Archive(ctx context.Context, document documents.Document, objects storage.ObjectStore) (archive.Manifest, error) {
	return archive.Manifest{}, ErrNotImplemented
}
