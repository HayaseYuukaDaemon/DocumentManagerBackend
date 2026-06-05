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
	StatusArchived    ArchiveStatus = "archived"
	StatusFailed      ArchiveStatus = "failed"
)

type Progress struct {
	Done  int `json:"done"` // 这个应该是由store维护的，外部不应该修改（AddPage时自动加1，RemovePage时自动减1）
	Total int `json:"total"`
}

type Page struct {
	Index       int    `json:"index"`
	Key         string `json:"key"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Hash        string `json:"hash,omitempty"`
}

type Document struct {
	ID               int                 `json:"document_id"`
	Source           sources.SourceType  `json:"source"`
	SourceDocumentID string              `json:"source_document_id"`
	SourceMeta       json.RawMessage     `json:"source_meta,omitempty"`
	Title            string              `json:"title"`
	StorageBackend   storage.StorageName `json:"storage_backend"`
	ArchiveStatus    ArchiveStatus       `json:"archive_status"`
	Progress         Progress            `json:"progress"`
	Error            string              `json:"error,omitempty"`
	Pages            []Page              `json:"pages"`
	Removed          bool                `json:"removed"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

type DocumentMeta struct {
	SourceMeta     json.RawMessage
	Title          string
	StorageBackend storage.StorageName
	ArchiveStatus  ArchiveStatus
	Progress       Progress
	Error          string
	Removed        bool
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

type RefreshMode string

const (
	OnlyMetaData RefreshMode = "only_meta_data"
	All          RefreshMode = "all"
)

type QueryInput struct {
	Mode   QueryMode       `json:"mode"`
	Params json.RawMessage `json:"params,omitempty"`
}

type QueryBySourceDocumentIDParams struct {
	Source           sources.SourceType `json:"source"`
	SourceDocumentID string             `json:"source_document_id"`
}
