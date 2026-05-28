package documents

import (
	"context"
	"errors"
	"sync"
	"time"

	"document-archive/internal/sources"
)

var ErrNotFound = errors.New("document not found")

type Store interface {
	Create(ctx context.Context, document Document) (Document, error)
	Get(ctx context.Context, id int) (Document, error)
	GetBySourceDocumentID(ctx context.Context, source sources.SourceType, sourceDocumentID string) (Document, error)
	Remove(ctx context.Context, id int) (Document, error)
	ListByStatus(ctx context.Context, status ArchiveStatus, limit int) ([]Document, error)
	Update(ctx context.Context, id int, fn func(*Document) error) (Document, error)
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
		if d.Source == source && d.SourceDocumentID == sourceDocumentID {
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
	document.ID = len(s.idMap)
	for _, d := range s.idMap {
		if d.Source == document.Source && d.SourceDocumentID == document.SourceDocumentID {
			if d.Removed == false {
				return Document{}, errors.New("document already exists")
			}
			document.ID = d.ID
		}
	}

	now := time.Now().UTC()

	document.CreatedAt = now
	document.UpdatedAt = now
	if document.ID == len(s.idMap) {
		s.idMap = append(s.idMap, document)
	} else {
		s.idMap[document.ID] = document
	}
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

func (s *MemoryStore) Update(ctx context.Context, id int, fn func(*Document) error) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 0 || id >= len(s.idMap) {
		return Document{}, ErrNotFound
	}

	document := &s.idMap[id]

	if err := fn(document); err != nil {
		return Document{}, err
	}

	document.UpdatedAt = time.Now().UTC()
	return *document, nil
}
