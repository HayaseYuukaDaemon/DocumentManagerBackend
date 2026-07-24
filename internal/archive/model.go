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

type PageDownloadHook func(ctx context.Context, documentID int, page documents.Page) error

type SourceHandlerFactory interface {
	Source() sources.SourceType
	NewHandler(objects storage.ObjectStore, hook PageDownloadHook) SourceHandler
}

type SourceHandler interface {
	ResolveDocument(ctx context.Context, document documents.Document) (documents.Document, error)
	ArchiveContent(ctx context.Context, document documents.Document) ([]documents.Page, error)
	ArchiveManifest(ctx context.Context, document documents.Document) error
}
