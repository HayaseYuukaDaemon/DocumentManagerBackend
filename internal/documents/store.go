package documents

import (
	"context"
	"errors"
	"sync"
	"time"

	"document-archive/internal/sources"
)

var ErrNotFound = errors.New("document not found")
var ErrAlreadyExists = errors.New("document already exists")

// 这里UpdateMeta里必须实现pages检查逻辑，并拒绝pages修改
type Store interface {
	Create(ctx context.Context, document Document) (Document, error)
	Get(ctx context.Context, id int) (Document, error)
	GetBySourceDocumentID(ctx context.Context, source sources.SourceType, sourceDocumentID string) (Document, error)
	Remove(ctx context.Context, id int) (Document, error)
	ListByStatus(ctx context.Context, status ArchiveStatus, limit int) ([]Document, error)
	UpdateMeta(ctx context.Context, id int, fn func(*DocumentMeta) error) (Document, error)
	AddPage(ctx context.Context, id int, page Page) error
	RemovePage(ctx context.Context, id int, pageIndex int) error
}

type MemoryStore struct {
	mu    sync.RWMutex
	idMap []Document
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		idMap: make([]Document, 0, 10),
	}
}

func (s *MemoryStore) Get(ctx context.Context, id int) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if id < 0 || id >= len(s.idMap) {
		return Document{}, ErrNotFound
	}
	d := s.idMap[id]
	if d.Removed {
		return Document{}, ErrNotFound
	}
	return d, nil
}

func (s *MemoryStore) GetBySourceDocumentID(ctx context.Context, source sources.SourceType, sourceDocumentID string) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.idMap {
		if d.Source == source && d.SourceDocumentID == sourceDocumentID && !d.Removed {
			return d, nil
		}
	}
	return Document{}, ErrNotFound
}

func (s *MemoryStore) Create(ctx context.Context, document Document) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.idMap {
		if d.Source == document.Source && d.SourceDocumentID == document.SourceDocumentID && !d.Removed {
			return d, ErrAlreadyExists
		}
	}

	now := time.Now().UTC()

	document.ID = len(s.idMap)
	document.CreatedAt = now
	document.UpdatedAt = now
	document.Removed = false
	s.idMap = append(s.idMap, document)
	return document, nil
}

func (s *MemoryStore) Remove(ctx context.Context, id int) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 0 || id >= len(s.idMap) {
		return Document{}, ErrNotFound
	}
	document := s.idMap[id]
	document.Removed = true
	document.UpdatedAt = time.Now().UTC()
	s.idMap[id] = document
	return document, nil
}

func (s *MemoryStore) ListByStatus(ctx context.Context, status ArchiveStatus, limit int) ([]Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Document, 0, limit)
	for _, document := range s.idMap {
		if document.Removed {
			continue
		}
		if document.ArchiveStatus != status {
			continue
		}
		result = append(result, document)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (s *MemoryStore) pageEqual(page1 Page, page2 Page) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if page1.ContentType != page2.ContentType {
		return false, errors.New("page content type mismatch")
	}
	if page1.Hash != page2.Hash {
		return false, errors.New("page hash mismatch")
	}
	if page1.Size != page2.Size {
		return false, errors.New("page size mismatch")
	}
	if page1.Index != page2.Index {
		return false, errors.New("page index mismatch")
	}
	if page1.Key != page2.Key {
		return false, errors.New("page key mismatch")
	}
	return true, nil
}

func (s *MemoryStore) UpdateMeta(ctx context.Context, id int, fn func(*DocumentMeta) error) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 0 || id >= len(s.idMap) {
		return Document{}, ErrNotFound
	}
	if fn == nil {
		return Document{}, errors.New("document update callback is required")
	}

	document := &s.idMap[id]
	documentMeta := &DocumentMeta{
		SourceMeta:     document.SourceMeta,
		Title:          document.Title,
		StorageBackend: document.StorageBackend,
		ArchiveStatus:  document.ArchiveStatus,
		Progress:       document.Progress,
		Error:          document.Error,
		Removed:        document.Removed,
	}

	if err := fn(documentMeta); err != nil {
		return Document{}, err
	}

	document.SourceMeta = documentMeta.SourceMeta
	document.Title = documentMeta.Title
	document.StorageBackend = documentMeta.StorageBackend
	document.ArchiveStatus = documentMeta.ArchiveStatus
	document.Progress = documentMeta.Progress
	document.Error = documentMeta.Error
	document.Removed = documentMeta.Removed

	document.UpdatedAt = time.Now().UTC()
	return *document, nil
}

func (s *MemoryStore) AddPage(ctx context.Context, id int, page Page) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 0 || id >= len(s.idMap) {
		return ErrNotFound
	}
	// 这里添加的时候可以考虑一下是否需要检查页码是否已经存在，或者页码是否连续等逻辑，这里暂时不做处理，对于sqlite实现，需要做一下。
	document := &s.idMap[id]
	document.Pages = append(document.Pages, page)
	document.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *MemoryStore) RemovePage(ctx context.Context, id int, pageIndex int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 0 || id >= len(s.idMap) {
		return ErrNotFound
	}
	document := &s.idMap[id]
	if pageIndex < 0 || pageIndex >= len(document.Pages) {
		return errors.New("invalid page index")
	}
	// 这里在sqlite实现直接移除就行了
	document.Pages = append(document.Pages[:pageIndex], document.Pages[pageIndex+1:]...)
	document.UpdatedAt = time.Now().UTC()
	return nil
}
