package documents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("document not found")

type Store interface {
	Create(ctx context.Context, document Document) (Document, error)
	Get(ctx context.Context, id string) (Document, error)
	GetBySourceIdentity(ctx context.Context, source string, sourceIdentity json.RawMessage) (Document, error)
	Remove(ctx context.Context, id string) (Document, error)
	ListByStatus(ctx context.Context, status ArchiveStatus, limit int) ([]Document, error)
	Update(ctx context.Context, document Document) (Document, error)
}

type MemoryStore struct {
	mu       sync.RWMutex
	byID     map[string]Document
	sourceID map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byID:     make(map[string]Document),
		sourceID: make(map[string]string),
	}
}

func (s *MemoryStore) Create(ctx context.Context, document Document) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	now := time.Now().UTC()
	document.CreatedAt = now
	document.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()

	key, err := sourceKey(document.Source, document.SourceIdentity)
	if err != nil {
		return Document{}, err
	}
	document.SourceIdentity = canonicalJSONRaw(document.SourceIdentity)
	if existingID, ok := s.sourceID[key]; ok {
		return s.byID[existingID], nil
	}

	s.byID[document.ID] = document
	s.sourceID[key] = document.ID
	return document, nil
}

func (s *MemoryStore) Get(ctx context.Context, id string) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	document, ok := s.byID[id]
	if !ok {
		return Document{}, ErrNotFound
	}
	return document, nil
}

func (s *MemoryStore) GetBySourceIdentity(ctx context.Context, source string, sourceIdentity json.RawMessage) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	key, err := sourceKey(source, sourceIdentity)
	if err != nil {
		return Document{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.sourceID[key]
	if !ok {
		return Document{}, ErrNotFound
	}
	return s.byID[id], nil
}

func (s *MemoryStore) Remove(ctx context.Context, id string) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	document, ok := s.byID[id]
	if !ok {
		return Document{}, ErrNotFound
	}
	document.Removed = true
	document.UpdatedAt = time.Now().UTC()
	s.byID[id] = document
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
	for _, document := range s.byID {
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
	current, ok := s.byID[document.ID]
	if !ok {
		return Document{}, ErrNotFound
	}

	oldKey, err := sourceKey(current.Source, current.SourceIdentity)
	if err != nil {
		return Document{}, err
	}
	newKey, err := sourceKey(document.Source, document.SourceIdentity)
	if err != nil {
		return Document{}, err
	}
	document.UpdatedAt = time.Now().UTC()
	document.SourceIdentity = canonicalJSONRaw(document.SourceIdentity)
	s.byID[document.ID] = document
	if oldKey != newKey {
		delete(s.sourceID, oldKey)
	}
	s.sourceID[newKey] = document.ID
	return document, nil
}

func sourceKey(source string, sourceIdentity json.RawMessage) (string, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return "", errors.New("source is required")
	}

	canonical, err := canonicalJSON(sourceIdentity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return source + ":" + hex.EncodeToString(sum[:]), nil
}

func canonicalJSONRaw(raw json.RawMessage) json.RawMessage {
	canonical, err := canonicalJSON(raw)
	if err != nil {
		return raw
	}
	return canonical
}

func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("source_identity is required")
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode source_identity: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("source_identity contains multiple json values")
		}
		return nil, fmt.Errorf("decode source_identity trailing data: %w", err)
	}

	if value == nil {
		return nil, errors.New("source_identity cannot be null")
	}

	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize source_identity: %w", err)
	}
	return canonical, nil
}
