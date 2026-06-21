package documents

import (
	"encoding/json"
	"time"

	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

type DocumentStatus string

const (
	StatusQueued      DocumentStatus = "queued"
	StatusResolving   DocumentStatus = "resolving"
	StatusDownloading DocumentStatus = "downloading"
	StatusArchived    DocumentStatus = "archived"
	StatusFailed      DocumentStatus = "failed"
	StatusDeleted     DocumentStatus = "deleted"
	StatusPurged      DocumentStatus = "purged"
)

func isKnownDocumentStatus(status DocumentStatus) bool {
	switch status {
	case StatusQueued, StatusResolving, StatusDownloading, StatusArchived, StatusFailed, StatusDeleted, StatusPurged:
		return true
	default:
		return false
	}
}

func isVisibleDocumentStatus(status DocumentStatus) bool {
	switch status {
	case StatusQueued, StatusResolving, StatusDownloading, StatusArchived, StatusFailed:
		return true
	default:
		return false
	}
}

func canTransitionDocumentStatus(from, to DocumentStatus) bool {
	if !isKnownDocumentStatus(from) || !isKnownDocumentStatus(to) {
		return false
	}
	if from == to {
		return true
	}

	switch from {
	case StatusQueued:
		return to == StatusResolving || to == StatusFailed || to == StatusDeleted
	case StatusResolving:
		return to == StatusDownloading || to == StatusArchived || to == StatusFailed || to == StatusDeleted
	case StatusDownloading:
		return to == StatusArchived || to == StatusFailed || to == StatusDeleted
	case StatusArchived:
		return to == StatusQueued || to == StatusDeleted
	case StatusFailed:
		return to == StatusQueued || to == StatusDeleted
	case StatusDeleted:
		return to == StatusPurged
	case StatusPurged:
		return false
	default:
		return false
	}
}

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
	Progress         Progress            `json:"progress"`
	Error            string              `json:"error,omitempty"`
	Pages            []Page              `json:"pages"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`

	status DocumentStatus
}

func (d Document) MarshalJSON() ([]byte, error) {
	type documentJSON Document

	return json.Marshal(struct {
		documentJSON
		DocumentStatus DocumentStatus `json:"status"`
	}{
		documentJSON:   documentJSON(d),
		DocumentStatus: d.status,
	})
}

func (d Document) Status() DocumentStatus {
	return d.status
}

type DocumentMeta struct {
	SourceMeta     json.RawMessage
	Title          string
	StorageBackend storage.StorageName
	status         DocumentStatus
	Progress       Progress
	Error          string
}

func (d *DocumentMeta) Status() DocumentStatus {
	return d.status
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
