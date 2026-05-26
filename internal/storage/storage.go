package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrNotImplemented = errors.New("object store not implemented")

type ObjectInfo struct {
	Key         string
	Size        int64
	ContentType string
	ETag        string
}

type ObjectStore interface {
	PutObject(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	HeadObject(ctx context.Context, key string) (ObjectInfo, error)
	PresignGetObject(ctx context.Context, key string, ttl time.Duration) (string, error)
}

type NoopStore struct{}

func NewNoopStore() NoopStore {
	return NoopStore{}
}

func (NoopStore) PutObject(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	return ErrNotImplemented
}

func (NoopStore) HeadObject(ctx context.Context, key string) (ObjectInfo, error) {
	return ObjectInfo{}, ErrNotImplemented
}

func (NoopStore) PresignGetObject(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return "", ErrNotImplemented
}
