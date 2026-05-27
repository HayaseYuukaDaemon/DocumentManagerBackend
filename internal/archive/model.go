package archive

import (
	"context"
	"encoding/json"

	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

type SourceName string

type Page struct {
	Key         string `json:"key"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

type Manifest struct {
	SchemaVersion    int                `json:"schema_version"`
	Source           sources.SourceType `json:"source"`
	SourceMeta       json.RawMessage    `json:"source_meta,omitempty"`
	SourceDocumentID string             `json:"source_document_id"`
	Title            string             `json:"title,omitempty"`
	PageCount        int                `json:"page_count"`
	Pages            []Page             `json:"pages"`
}

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
	ArchiveContent(ctx context.Context, document documents.Document, objects storage.ObjectStore) error
	ArchiveManifest(ctx context.Context, document documents.Document, objects storage.ObjectStore) (Manifest, error)
	RegisterPageDownloadHook(hook func(ctx context.Context, documentID int, page Page) error) error
}
