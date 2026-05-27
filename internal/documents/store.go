package documents

import (
	"context"
	"document-archive/internal/sources"
	"errors"
	"sync"
	"time"
)

var ErrNotFound = errors.New("document not found")

type Store interface {
	Create(ctx context.Context, document *Document) (*Document, error)
	Get(ctx context.Context, id int) (*Document, error)
	GetBySourceDocumentID(ctx context.Context, source sources.SourceType, sourceDocumentID string) (*Document, error)
	Remove(ctx context.Context, id int) (*Document, error)
	ListByStatus(ctx context.Context, status ArchiveStatus, limit int) ([]*Document, error)
	Update(ctx context.Context, document *Document) (*Document, error)
}

type MemoryStore struct {
	mu        sync.RWMutex
	idMap     []*Document
	sourceMap map[sources.SourceType]map[string]int
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		idMap:     make([]*Document, 0, 10),
		sourceMap: make(map[sources.SourceType]map[string]int),
	}
}

func (s *MemoryStore) Get(ctx context.Context, id int) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if id < 0 || id >= len(s.idMap) {
		return nil, ErrNotFound
	}
	document := s.idMap[id]
	if document == nil {
		return nil, ErrNotFound
	}
	return cloneDocument(document), nil
}

func (s *MemoryStore) GetBySourceDocumentID(ctx context.Context, source sources.SourceType, sourceDocumentID string) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	sourceDocuments := s.sourceMap[source]
	if sourceDocuments == nil {
		return nil, ErrNotFound
	}
	id, exists := sourceDocuments[sourceDocumentID]
	if !exists || id < 0 || id >= len(s.idMap) || s.idMap[id] == nil {
		return nil, ErrNotFound
	}
	return cloneDocument(s.idMap[id]), nil
}

func (s *MemoryStore) Create(ctx context.Context, document *Document) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sourceMap[document.Source] == nil {
		s.sourceMap[document.Source] = make(map[string]int)
	}

	if id, exists := s.sourceMap[document.Source][document.SourceDocumentID]; exists {
		if id >= 0 && id < len(s.idMap) && s.idMap[id] != nil {
			return cloneDocument(s.idMap[id]), nil
		}
		delete(s.sourceMap[document.Source], document.SourceDocumentID)
	}

	now := time.Now().UTC()
	document.ID = len(s.idMap)
	document.CreatedAt = now
	document.UpdatedAt = now
	stored := cloneDocument(document)
	s.idMap = append(s.idMap, stored)
	s.sourceMap[stored.Source][stored.SourceDocumentID] = stored.ID
	return cloneDocument(stored), nil
}

func (s *MemoryStore) Remove(ctx context.Context, id int) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 0 || id >= len(s.idMap) {
		return nil, ErrNotFound
	}
	document := s.idMap[id]
	if document == nil {
		return nil, ErrNotFound
	}
	document.Removed = true
	document.UpdatedAt = time.Now().UTC()
	return cloneDocument(document), nil
}

func (s *MemoryStore) ListByStatus(ctx context.Context, status ArchiveStatus, limit int) ([]*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Document, 0, limit)
	for _, document := range s.idMap {
		if document == nil {
			continue
		}
		if document.ArchiveStatus != status {
			continue
		}
		result = append(result, cloneDocument(document))
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (s *MemoryStore) Update(ctx context.Context, document *Document) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if document.ID < 0 || document.ID >= len(s.idMap) || s.idMap[document.ID] == nil {
		return nil, ErrNotFound
	}

	current := s.idMap[document.ID]
	if current.Source != document.Source || current.SourceDocumentID != document.SourceDocumentID {
		if sourceDocuments := s.sourceMap[document.Source]; sourceDocuments != nil {
			if existingID, exists := sourceDocuments[document.SourceDocumentID]; exists && existingID != document.ID {
				return nil, errors.New("document source mapping already exists")
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
	s.idMap[document.ID] = cloneDocument(document)
	return cloneDocument(s.idMap[document.ID]), nil
}

func cloneDocument(document *Document) *Document {
	if document == nil {
		return nil
	}
	cloned := *document
	if document.SourceMeta != nil {
		cloned.SourceMeta = append([]byte(nil), document.SourceMeta...)
	}
	return &cloned
}
