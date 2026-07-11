package archive

import (
	"context"

	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

type SourceName string

type PageResultKind string

const (
	PageResultRedirect PageResultKind = "redirect"
	PageResultObject   PageResultKind = "object"
)

type PageResult struct {
	Kind        PageResultKind
	RedirectURL string
	Object      storage.Object
}

type SourceHandler interface {
	Source() sources.SourceType
	ResolveDocument(ctx context.Context, document documents.Document) (documents.Document, error)
	ArchiveContent(ctx context.Context, document documents.Document, objects storage.ObjectStore) ([]documents.Page, error)
	ArchiveManifest(ctx context.Context, document documents.Document, objects storage.ObjectStore) error
	RegisterPageDownloadHook(hook func(ctx context.Context, documentID int, page documents.Page) error) error
}
