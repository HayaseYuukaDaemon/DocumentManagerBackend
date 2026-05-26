package archive

import (
	"context"
	"encoding/json"

	"document-archive/internal/documents"
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

type SourceHandler interface {
	Source() string
	Archive(ctx context.Context, document documents.Document, objects storage.ObjectStore) (Manifest, error)
}
