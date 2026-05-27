package documents

import (
	"document-archive/internal/sources"
	"document-archive/internal/storage"
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
	ID               int                 `json:"document_id"`
	Source           sources.SourceType  `json:"source"`
	SourceDocumentID string              `json:"source_document_id"`
	SourceMeta       json.RawMessage     `json:"source_meta,omitempty"`
	Title            string              `json:"title,omitempty"`
	StorageBackend   storage.StorageName `json:"storage_backend,omitempty"`
	ArchiveStatus    ArchiveStatus       `json:"archive_status"`
	Progress         Progress            `json:"progress"`
	Error            string              `json:"error,omitempty"`
	PageCount        int                 `json:"page_count,omitempty"`
	ManifestKey      string              `json:"manifest_key,omitempty"`
	Removed          bool                `json:"removed"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

type RequestDocumentInput struct {
	Source           sources.SourceType  `json:"source"`
	SourceDocumentID string              `json:"source_document_id"`
	SourceMeta       json.RawMessage     `json:"source_meta,omitempty"`
	StorageBackend   storage.StorageName `json:"storage_backend,omitempty"`
}

type QueryMode string

const (
	QueryBySourceDocumentID QueryMode = "by_source_document_id"
	QueryByRequestTime      QueryMode = "by_request_time"
)

type QueryInput struct {
	Mode   QueryMode       `json:"mode"`
	Params json.RawMessage `json:"params,omitempty"`
}

type QueryBySourceDocumentIDParams struct {
	Source           sources.SourceType `json:"source"`
	SourceDocumentID string             `json:"source_document_id"`
}
