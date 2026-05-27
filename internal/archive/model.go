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
	Index       int    `json:"index"`
	Key         string `json:"key"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

type Manifest struct {
	SchemaVersion  int             `json:"schema_version"`
	DocumentID     string          `json:"document_id"`
	Source         string          `json:"source"`
	SourceIdentity json.RawMessage `json:"source_identity"`
	Title          string          `json:"title,omitempty"`
	PageCount      int             `json:"page_count"`
	Pages          []Page          `json:"pages"`
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
	Archive(ctx context.Context, document documents.Document, objects storage.ObjectStore) (Manifest, error)
}
