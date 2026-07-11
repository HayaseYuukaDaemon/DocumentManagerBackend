package documents

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"document-archive/internal/sources"
	"document-archive/internal/utils"
)

var ErrNotFound = errors.New("document not found")
var ErrAlreadyExists = errors.New("document already exists")
var ErrPageNotFound = errors.New("page not found")

type ErrPageAlreadyExists struct {
	DocumentID int
	PageIndex  int
}

func (e ErrPageAlreadyExists) Error() string {
	return fmt.Sprintf("page already exists at index %d for document %d", e.PageIndex, e.DocumentID)
}

func (e ErrPageAlreadyExists) Is(target error) bool {
	typed, ok := target.(ErrPageAlreadyExists)
	if !ok {
		return false
	}
	if typed.DocumentID != 0 && e.DocumentID != typed.DocumentID {
		return false
	}
	if typed.PageIndex != 0 && e.PageIndex != typed.PageIndex {
		return false
	}
	return true
}

type ErrInvalidStatusTransition struct {
	From, To DocumentStatus
}

func (e ErrInvalidStatusTransition) Error() string {
	return fmt.Sprintf("invalid status transition from %s to %s", e.From, e.To)
}

func (e ErrInvalidStatusTransition) Is(target error) bool {
	typed, ok := target.(ErrInvalidStatusTransition)
	if !ok {
		return false
	}
	if typed.From != "" && e.From != typed.From {
		return false
	}
	if typed.To != "" && e.To != typed.To {
		return false
	}
	return true
}

type QueryBuilder struct {
	source           *sources.SourceType
	sourceDocumentID *string

	status *DocumentStatus

	orderBy string // 比如ORDER BY ID, 这里填ID
	order   string // 比如ASC/DESC, 这里填ASC/DESC
	limit   int
}

func (qb QueryBuilder) BySourceDocumentID(source sources.SourceType, sourceDocumentID string) QueryBuilder {
	qb.source = &source
	qb.sourceDocumentID = &sourceDocumentID
	return qb
}

func (qb QueryBuilder) ByStatus(status DocumentStatus) QueryBuilder {
	qb.status = &status
	return qb
}

func (qb QueryBuilder) Visible(source sources.SourceType) QueryBuilder {
	qb.source = &source
	return qb
}

func (qb QueryBuilder) OrderBy(orderBy string) QueryBuilder {
	qb.orderBy = orderBy
	return qb
}

func (qb QueryBuilder) Order(order string) QueryBuilder {
	qb.order = order
	return qb
}

func (qb QueryBuilder) Limit(limit int) QueryBuilder {
	qb.limit = limit
	return qb
}

func (qb QueryBuilder) Build() (DocumentQuery, error) {
	if qb.source != nil && qb.sourceDocumentID == nil {
		return DocumentQuery{}, ErrQueryParamMismatch{
			Expected: "sourceDocumentID",
			Actual:   "nil",
		}
	}
	if qb.source == nil && qb.sourceDocumentID != nil {
		return DocumentQuery{}, ErrQueryParamMismatch{
			Expected: "source",
			Actual:   "nil",
		}
	}
	if qb.order != "" {
		switch qb.order {
		case "ASC", "DESC":
		default:
			return DocumentQuery{}, ErrQueryParamMismatch{
				Expected: "ASC or DESC",
				Actual:   qb.order,
			}
		}
	} else {
		qb.order = "ASC"
	}
	if qb.orderBy != "" {
		switch qb.orderBy {
		case "id", "created_at", "updated_at":
		default:
			return DocumentQuery{}, ErrQueryParamMismatch{
				Expected: "id or created_at or updated_at",
				Actual:   qb.orderBy,
			}
		}
	} else {
		qb.orderBy = "id"
	}
	if qb.limit == 0 {
		qb.limit = 10
	}
	return DocumentQuery(qb), nil
}

type Store interface {
	Create(ctx context.Context, document Document) (Document, error)
	Get(ctx context.Context, id int) (Document, error)
	Delete(ctx context.Context, id int) (Document, error)
	Purge(ctx context.Context, id int) (int, error)
	Restore(ctx context.Context, id int) (Document, error)
	ResetPages(ctx context.Context, id int) (Document, error)
	UpdateMeta(ctx context.Context, id int, fn func(*DocumentMeta) error) (Document, error)
	TransitionTo(ctx context.Context, id int, newStatus DocumentStatus) error
	AddPage(ctx context.Context, id int, page Page) error
	RemovePage(ctx context.Context, id int, pageIndex int) error
	Query(ctx context.Context, query DocumentQuery) ([]Document, error)
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

func (s *MemoryStore) Create(ctx context.Context, document Document) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.idMap {
		if d.Source == document.Source && d.SourceDocumentID == document.SourceDocumentID {
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

func (s *MemoryStore) Delete(ctx context.Context, id int) (Document, error) {
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

func (s *MemoryStore) Query(ctx context.Context, query DocumentQuery) ([]Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Document, 0)
	for _, document := range s.idMap {
		if query.status != nil {
			if document.status != *query.status {
				continue
			}
		} else if !isVisibleDocumentStatus(document.status) {
			continue
		}

		if query.source != nil {
			if document.Source != *query.source || document.SourceDocumentID != *query.sourceDocumentID {
				continue
			}
		}

		result = append(result, document)
	}

	sort.SliceStable(result, func(i, j int) bool {
		cmp := compareDocumentsByQueryOrder(result[i], result[j], query.orderBy)
		if query.order == "DESC" {
			return cmp > 0
		}
		return cmp < 0
	})

	if query.limit > 0 && len(result) > query.limit {
		result = result[:query.limit]
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
	if newStatus == StatusPurged {
		if err := validatePurgedDocumentState(*document); err != nil {
			return err
		}
	}
	document.status = newStatus
	document.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *MemoryStore) Purge(ctx context.Context, id int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 0 || id >= len(s.idMap) {
		return 0, ErrNotFound
	}

	document := &s.idMap[id]
	if !canTransitionDocumentStatus(document.status, StatusPurged) {
		return 0, fmt.Errorf("invalid document status transition: %s -> %s", document.status, StatusPurged)
	}

	pageCount := countExistingPages(document.Pages)
	document.Pages = nil
	document.Progress.Done = 0
	document.Progress.Total = 0
	document.status = StatusPurged
	document.UpdatedAt = time.Now().UTC()
	return pageCount, nil
}

func (s *MemoryStore) Restore(ctx context.Context, id int) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 0 || id >= len(s.idMap) {
		return Document{}, ErrNotFound
	}

	document := &s.idMap[id]
	switch document.status {
	case StatusDeleted, StatusPurged:
		document.status = StatusQueued
		document.UpdatedAt = time.Now().UTC()
		return *document, nil
	default:
		return Document{}, ErrInvalidStatusTransition{From: document.status, To: StatusQueued}
	}
}

func (s *MemoryStore) ResetPages(ctx context.Context, id int) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
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

	document.Pages = nil
	document.Progress.Done = 0
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
		return ErrPageAlreadyExists{DocumentID: id, PageIndex: page.Index}
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

func countExistingPages(pages []Page) int {
	count := 0
	for index, page := range pages {
		if pageSlotExists(page, index) {
			count++
		}
	}
	return count
}

func validatePurgedDocumentState(document Document) error {
	if document.Progress.Done != 0 {
		return fmt.Errorf("cannot transition to %s with progress.done=%d", StatusPurged, document.Progress.Done)
	}
	if document.Progress.Total != 0 {
		return fmt.Errorf("cannot transition to %s with progress.total=%d", StatusPurged, document.Progress.Total)
	}
	if pageCount := countExistingPages(document.Pages); pageCount != 0 {
		return fmt.Errorf("cannot transition to %s with %d persisted pages", StatusPurged, pageCount)
	}
	return nil
}

func compareDocumentsByQueryOrder(left Document, right Document, orderBy string) int {
	switch orderBy {
	case "created_at":
		return compareTime(left.CreatedAt, right.CreatedAt)
	case "updated_at":
		return compareTime(left.UpdatedAt, right.UpdatedAt)
	default:
		return compareInt(left.ID, right.ID)
	}
}

func compareInt(left int, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareTime(left time.Time, right time.Time) int {
	switch {
	case left.Before(right):
		return -1
	case left.After(right):
		return 1
	default:
		return 0
	}
}
