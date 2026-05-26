package documents

import (
	"encoding/json"
	"time"
)

type ArchiveStatus string

const (
	StatusQueued      ArchiveStatus = "queued"
	StatusResolving   ArchiveStatus = "resolving"
	StatusDownloading ArchiveStatus = "downloading"
	StatusUploading   ArchiveStatus = "uploading"
	StatusArchived    ArchiveStatus = "archived"
	StatusFailed      ArchiveStatus = "failed"
)

type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

type Document struct {
	ID                string          `json:"document_id"`
	Source            string          `json:"source"`
	SourceIdentity    json.RawMessage `json:"source_identity"`
	SourceMeta        json.RawMessage `json:"source_meta,omitempty"`
	Title             string          `json:"title,omitempty"`
	StorageBackend    string          `json:"storage_backend,omitempty"`
	ArchiveStatus     ArchiveStatus   `json:"archive_status"`
	Progress          Progress        `json:"progress"`
	Error             string          `json:"error,omitempty"`
	PageCount         int             `json:"page_count,omitempty"`
	ManifestKey       string          `json:"manifest_key,omitempty"`
	Removed           bool            `json:"removed"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	MetadataUpdatedAt *time.Time      `json:"metadata_updated_at,omitempty"`
}

type RequestDocumentInput struct {
	Source         string          `json:"source"`
	SourceIdentity json.RawMessage `json:"source_identity"`
	SourceMeta     json.RawMessage `json:"source_meta,omitempty"`
}

type QueryMode string

const (
	QueryBySourceIdentity QueryMode = "by_source_identity"
	QueryByRequestTime    QueryMode = "by_request_time"
)

type QueryInput struct {
	Mode   QueryMode       `json:"mode"`
	Params json.RawMessage `json:"params,omitempty"`
}

type QueryBySourceIdentityParams struct {
	Source         string          `json:"source"`
	SourceIdentity json.RawMessage `json:"source_identity"`
}
