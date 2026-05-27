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
	Update(ctx context.Context, document Document) (Document, error)
}

type MemoryStore struct {
	mu        sync.RWMutex
	idMap     []Document
	sourceMap map[sources.SourceType]map[string]int
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		idMap:     make([]Document, 0, 10),
		sourceMap: make(map[sources.SourceType]map[string]int),
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
	return s.idMap[id], nil
}

func (s *MemoryStore) GetBySourceDocumentID(ctx context.Context, source sources.SourceType, sourceDocumentID string) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	sourceDocuments := s.sourceMap[source]
	if sourceDocuments == nil {
		return Document{}, ErrNotFound
	}
	id, exists := sourceDocuments[sourceDocumentID]
	if !exists || id < 0 || id >= len(s.idMap) {
		return Document{}, ErrNotFound
	}
	return s.idMap[id], nil
}

func (s *MemoryStore) Create(ctx context.Context, document Document) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sourceMap[document.Source] == nil {
		s.sourceMap[document.Source] = make(map[string]int)
	}

	if id, exists := s.sourceMap[document.Source][document.SourceDocumentID]; exists {
		if id >= 0 && id < len(s.idMap) {
			return s.idMap[id], nil
		}
		delete(s.sourceMap[document.Source], document.SourceDocumentID)
	}

	now := time.Now().UTC()
	document.ID = len(s.idMap)
	document.CreatedAt = now
	document.UpdatedAt = now
	s.idMap = append(s.idMap, document)
	s.sourceMap[document.Source][document.SourceDocumentID] = document.ID
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

func (s *MemoryStore) Update(ctx context.Context, document Document) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if document.ID < 0 || document.ID >= len(s.idMap) {
		return Document{}, ErrNotFound
	}

	current := s.idMap[document.ID]
	if current.Source != document.Source || current.SourceDocumentID != document.SourceDocumentID {
		if sourceDocuments := s.sourceMap[document.Source]; sourceDocuments != nil {
			if existingID, exists := sourceDocuments[document.SourceDocumentID]; exists && existingID != document.ID {
				return Document{}, errors.New("document source mapping already exists")
			}
		}
		if sourceDocuments := s.sourceMap[current.Source]; sourceDocuments != nil {
			delete(sourceDocuments, current.SourceDocumentID)
		}
		if s.sourceMap[document.Source] == nil {
			s.sourceMap[document.Source] = make(map[string]int)
		}
		s.sourceMap[document.Source][document.SourceDocumentID] = document.ID
	}

	document.UpdatedAt = time.Now().UTC()
	s.idMap[document.ID] = document
	return document, nil
}
