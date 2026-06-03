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

type StorageName string

const MemoryStorageName StorageName = "memory"

type Object struct {
	ObjectInfo
	Body io.ReadCloser
}

type ObjectStore interface {
	StorageName() StorageName
	PutObject(ctx context.Context, key string, body io.Reader, size int64, contentType string) (ObjectInfo, error)
	GetObject(ctx context.Context, key string) (Object, error)
	HeadObject(ctx context.Context, key string) (ObjectInfo, error)
	PresignGetObject(ctx context.Context, key string, ttl time.Duration) (string, error)
}
