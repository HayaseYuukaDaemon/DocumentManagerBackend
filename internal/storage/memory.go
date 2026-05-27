package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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

func (s *MemoryStore) PutObject(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return errors.New("object key is required")
	}

	content, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if size >= 0 && int64(len(content)) != size {
		return fmt.Errorf("object size mismatch: expected %d, got %d", size, len(content))
	}

	sum := md5.Sum(content)
	object := memoryObject{
		content:     append([]byte(nil), content...),
		contentType: contentType,
		etag:        hex.EncodeToString(sum[:]),
		updatedAt:   time.Now().UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = object
	return nil
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
