package documents

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"document-archive/internal/sources"
	"document-archive/internal/utils"
)

var ErrNotFound = errors.New("document not found")
var ErrAlreadyExists = errors.New("document already exists")
var ErrPageNotFound = errors.New("page not found")
var ErrPageAlreadyExists = errors.New("page already exists")

type Store interface {
	Create(ctx context.Context, document Document) (Document, error)
	Get(ctx context.Context, id int) (Document, error)
	GetBySourceDocumentID(ctx context.Context, source sources.SourceType, sourceDocumentID string) (Document, error)
	Remove(ctx context.Context, id int) (Document, error)
	ListByStatus(ctx context.Context, status DocumentStatus, limit int) ([]Document, error)
	UpdateMeta(ctx context.Context, id int, fn func(*DocumentMeta) error) (Document, error)
	TransitionTo(ctx context.Context, id int, newStatus DocumentStatus) error
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
	if !isVisibleDocumentStatus(d.status) {
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
		if d.Source == source && d.SourceDocumentID == sourceDocumentID && isVisibleDocumentStatus(d.status) {
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
		if d.Source == document.Source && d.SourceDocumentID == document.SourceDocumentID && isVisibleDocumentStatus(d.status) {
			return d, ErrAlreadyExists
		}
	}

	now := time.Now().UTC()

	pages := document.Pages
	document.Pages = nil
	document.Progress.Done = 0
	for index, page := range pages {
		if index != page.Index {
			return Document{}, fmt.Errorf("page index mismatch: expected %d, got %d", index, page.Index)
		}
		if page.Index < 0 {
			return Document{}, fmt.Errorf("invalid page index: %d", page.Index)
		}
	}

	document.ID = len(s.idMap)
	document.CreatedAt = now
	document.UpdatedAt = now
	document.status = StatusQueued
	s.idMap = append(s.idMap, document)

	for index, page := range pages {
		if err := s.addPageLocked(document.ID, page, now); err != nil {
			return Document{}, utils.NewIndexedError(err, index)
		}
	}
	return s.idMap[document.ID], nil
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
	if !isVisibleDocumentStatus(document.status) {
		return Document{}, ErrNotFound
	}
	if !canTransitionDocumentStatus(document.status, StatusDeleted) {
		return Document{}, fmt.Errorf("invalid document status transition: %s -> %s", document.status, StatusDeleted)
	}
	document.status = StatusDeleted
	document.UpdatedAt = time.Now().UTC()
	s.idMap[id] = document
	return document, nil
}

func (s *MemoryStore) ListByStatus(ctx context.Context, status DocumentStatus, limit int) ([]Document, error) {
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
		if document.status != status {
			continue
		}
		result = append(result, document)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (s *MemoryStore) UpdateMeta(ctx context.Context, id int, fn func(*DocumentMeta) error) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	if fn == nil {
		return Document{}, errors.New("document update callback is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 0 || id >= len(s.idMap) {
		return Document{}, ErrNotFound
	}

	document := &s.idMap[id]
	if !isVisibleDocumentStatus(document.status) {
		return Document{}, ErrNotFound
	}
	documentMeta := extractDocumentMeta(*document)

	if err := fn(&documentMeta); err != nil {
		return Document{}, err
	}

	fillDocumentMeta(document, documentMeta)
	document.UpdatedAt = time.Now().UTC()
	return *document, nil
}

func (s *MemoryStore) TransitionTo(ctx context.Context, id int, newStatus DocumentStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 0 || id >= len(s.idMap) {
		return ErrNotFound
	}
	document := &s.idMap[id]
	if !canTransitionDocumentStatus(document.status, newStatus) {
		return fmt.Errorf("invalid document status transition: %s -> %s", document.status, newStatus)
	}
	document.status = newStatus
	document.UpdatedAt = time.Now().UTC()
	return nil
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
	if !isVisibleDocumentStatus(s.idMap[id].status) {
		return ErrNotFound
	}
	return s.addPageLocked(id, page, time.Now().UTC())
}

func (s *MemoryStore) RemovePage(ctx context.Context, id int, pageIndex int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 0 || id >= len(s.idMap) {
		return ErrPageNotFound
	}
	if !isVisibleDocumentStatus(s.idMap[id].status) {
		return ErrNotFound
	}
	return s.removePageLocked(id, pageIndex, time.Now().UTC())
}

func (s *MemoryStore) addPageLocked(id int, page Page, now time.Time) error {
	if page.Index < 0 {
		return fmt.Errorf("invalid page index: %d", page.Index)
	}

	document := &s.idMap[id]
	if page.Index < len(document.Pages) && pageSlotExists(document.Pages[page.Index], page.Index) {
		return ErrPageAlreadyExists
	}
	for len(document.Pages) <= page.Index {
		document.Pages = append(document.Pages, Page{})
	}
	document.Pages[page.Index] = page
	document.Progress.Done++
	if document.Progress.Total < document.Progress.Done {
		document.Progress.Total = document.Progress.Done
	}
	document.UpdatedAt = now
	return nil
}

func (s *MemoryStore) removePageLocked(id int, pageIndex int, now time.Time) error {
	document := &s.idMap[id]
	if pageIndex < 0 || pageIndex >= len(document.Pages) || !pageSlotExists(document.Pages[pageIndex], pageIndex) {
		return ErrPageNotFound
	}

	document.Pages[pageIndex] = Page{}
	for len(document.Pages) > 0 {
		lastIndex := len(document.Pages) - 1
		if pageSlotExists(document.Pages[lastIndex], lastIndex) {
			break
		}
		document.Pages = document.Pages[:lastIndex]
	}
	if document.Progress.Done > 0 {
		document.Progress.Done--
	}
	document.UpdatedAt = now
	return nil
}

func pageSlotExists(page Page, index int) bool {
	return page.Index == index && page.Key != ""
}
