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

type StorageType string

const (
	MemoryStorageType StorageType = "memory"
	S3StorageType     StorageType = "s3"
)

type Object struct {
	ObjectInfo
	Body io.ReadCloser
}

type ObjectStore interface {
	Name() StorageName
	Type() StorageType
	PutObject(ctx context.Context, info ObjectInfo, body io.ReadSeeker) (ObjectInfo, error)
	GetObject(ctx context.Context, key string) (Object, error)
	HeadObject(ctx context.Context, key string) (ObjectInfo, error)
	DeleteObject(ctx context.Context, key string) error
	DeletePrefix(ctx context.Context, prefix string) error
	PresignGetObject(ctx context.Context, key string, ttl time.Duration) (string, error)
}
