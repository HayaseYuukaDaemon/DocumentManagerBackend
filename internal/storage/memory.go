package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

var ErrObjectNotFound = errors.New("object not found")

type MemoryStore struct {
	mu          sync.RWMutex
	storageName StorageName
	objects     map[string]memoryObject
}

type memoryObject struct {
	content     []byte
	contentType string
	etag        string
	updatedAt   time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		storageName: MemoryStorageName,
		objects:     make(map[string]memoryObject),
	}
}

func (s *MemoryStore) StorageName() StorageName {
	return s.storageName
}

func (s *MemoryStore) DeleteObject(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[key]; !ok {
		return ErrObjectNotFound
	}
	delete(s.objects, key)
	return nil
}

func (s *MemoryStore) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if prefix == "" {
		return errors.New("object prefix is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			delete(s.objects, key)
		}
	}
	return nil
}

func (s *MemoryStore) PutObject(ctx context.Context, info ObjectInfo, body io.ReadSeeker) (ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	if info.Key == "" {
		return ObjectInfo{}, errors.New("object key is required")
	}
	if body == nil {
		return ObjectInfo{}, errors.New("object body is required")
	}

	content, err := io.ReadAll(body)
	if err != nil {
		return ObjectInfo{}, err
	}
	if info.Size >= 0 && int64(len(content)) != info.Size {
		return ObjectInfo{}, fmt.Errorf("object size mismatch: expected %d, got %d", info.Size, len(content))
	}

	etag := info.ETag
	if etag == "" {
		sum := md5.Sum(content)
		etag = hex.EncodeToString(sum[:])
	}
	object := memoryObject{
		content:     append([]byte(nil), content...),
		contentType: info.ContentType,
		etag:        etag,
		updatedAt:   time.Now().UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[info.Key] = object
	return ObjectInfo{
		Key:         info.Key,
		Size:        int64(len(object.content)),
		ContentType: object.contentType,
		ETag:        object.etag,
	}, nil
}

func (s *MemoryStore) GetObject(ctx context.Context, key string) (Object, error) {
	if err := ctx.Err(); err != nil {
		return Object{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[key]
	if !ok {
		return Object{}, ErrObjectNotFound
	}

	content := append([]byte(nil), object.content...)
	return Object{
		ObjectInfo: ObjectInfo{
			Key:         key,
			Size:        int64(len(content)),
			ContentType: object.contentType,
			ETag:        object.etag,
		},
		Body: io.NopCloser(bytes.NewReader(content)),
	}, nil
}

func (s *MemoryStore) HeadObject(ctx context.Context, key string) (ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[key]
	if !ok {
		return ObjectInfo{}, ErrObjectNotFound
	}
	return ObjectInfo{
		Key:         key,
		Size:        int64(len(object.content)),
		ContentType: object.contentType,
		ETag:        object.etag,
	}, nil
}

func (s *MemoryStore) PresignGetObject(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if _, err := s.HeadObject(ctx, key); err != nil {
		return "", err
	}
	return "memory://" + key, nil
}
