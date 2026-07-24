package storage

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const s3ObjectETagMetadataKey = "archive-etag"

type S3Config struct {
	InternalEndpoint string `yaml:"internal_endpoint"`
	Endpoint         string `yaml:"endpoint"`
	Bucket           string `yaml:"bucket"`
	Region           string `yaml:"region"`
	AccessKeyID      string `yaml:"access_key_id"`
	SecretAccessKey  string `yaml:"secret_access_key"`
	SessionToken     string `yaml:"session_token"`
	UsePathStyle     bool   `yaml:"use_path_style"`
}

type S3Store struct {
	storageName StorageName
	bucket      string
	client      *s3.Client
	presign     *s3.PresignClient
}

func NewS3Store(name StorageName, cfg S3Config) (*S3Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("s3 bucket is required")
	}
	if cfg.Region == "" {
		return nil, errors.New("s3 region is required")
	}
	if cfg.AccessKeyID == "" {
		return nil, errors.New("s3 access key id is required")
	}
	if cfg.SecretAccessKey == "" {
		return nil, errors.New("s3 secret access key is required")
	}

	options := s3.Options{
		Region:       cfg.Region,
		Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken)),
		UsePathStyle: cfg.UsePathStyle,
	}
	if cfg.Endpoint != "" {
		options.BaseEndpoint = aws.String(cfg.Endpoint)
	}
	var client *s3.Client
	if cfg.InternalEndpoint != "" {
		options.BaseEndpoint = aws.String(cfg.InternalEndpoint)
		client = s3.New(options)
		presignOptions := options
		presignOptions.BaseEndpoint = aws.String(cfg.Endpoint)
		return &S3Store{
			storageName: name,
			bucket:      cfg.Bucket,
			client:      client,
			presign:     s3.NewPresignClient(s3.New(presignOptions)),
		}, nil
	} else {
		client = s3.New(options)
		return &S3Store{
			storageName: name,
			bucket:      cfg.Bucket,
			client:      client,
			presign:     s3.NewPresignClient(client),
		}, nil
	}

}

func (s *S3Store) Name() StorageName {
	return s.storageName
}

func (s *S3Store) Type() StorageType {
	return S3StorageType
}

func (s *S3Store) DeleteObject(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return errors.New("object key is required")
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return normalizeS3Error(err)
	}
	return nil
}

func (s *S3Store) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return errors.New("object prefix is required")
	}

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	keys := make([]string, 0)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return normalizeS3Error(err)
		}
		for _, object := range page.Contents {
			if key := aws.ToString(object.Key); key != "" {
				keys = append(keys, key)
			}
		}
	}
	for _, key := range keys {
		if err := s.DeleteObject(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func (s *S3Store) PutObject(ctx context.Context, info ObjectInfo, body io.ReadSeeker) (ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	if info.Key == "" {
		return ObjectInfo{}, errors.New("object key is required")
	}
	if body == nil {
		return ObjectInfo{}, errors.New("object body is required")
	}
	bodyStart, err := body.Seek(0, io.SeekCurrent)
	if err != nil {
		return ObjectInfo{}, err
	}
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(info.Key),
		Body:   body,
	}
	if info.ETag != "" {
		input.Metadata = map[string]string{s3ObjectETagMetadataKey: info.ETag}
	}
	if info.Size >= 0 {
		input.ContentLength = aws.Int64(info.Size)
	}
	if info.ContentType != "" {
		input.ContentType = aws.String(info.ContentType)
	}

	output, err := s.client.PutObject(ctx, input)
	if err != nil {
		return ObjectInfo{}, normalizeS3Error(err)
	}
	etag := info.ETag
	if etag == "" {
		etag = cleanS3ETag(output.ETag)
	}
	if etag == "" {
		etag, err = objectETag(body, bodyStart)
		if err != nil {
			return ObjectInfo{}, err
		}
	}
	return ObjectInfo{
		Key:         info.Key,
		Size:        info.Size,
		ContentType: info.ContentType,
		ETag:        etag,
	}, nil
}

func (s *S3Store) GetObject(ctx context.Context, key string) (Object, error) {
	if err := ctx.Err(); err != nil {
		return Object{}, err
	}
	if key == "" {
		return Object{}, errors.New("object key is required")
	}
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return Object{}, normalizeS3Error(err)
	}
	return Object{
		ObjectInfo: s.objectInfoFromHeaders(key, output.ContentLength, output.ContentType, output.ETag, output.Metadata),
		Body:       output.Body,
	}, nil
}

func (s *S3Store) HeadObject(ctx context.Context, key string) (ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	if key == "" {
		return ObjectInfo{}, errors.New("object key is required")
	}
	output, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return ObjectInfo{}, normalizeS3Error(err)
	}
	return s.objectInfoFromHeaders(key, output.ContentLength, output.ContentType, output.ETag, output.Metadata), nil
}

func (s *S3Store) PresignGetObject(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if _, err := s.HeadObject(ctx, key); err != nil {
		return "", err
	}
	if ttl <= 0 {
		return "", errors.New("presign ttl must be positive")
	}
	output, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, func(options *s3.PresignOptions) {
		options.Expires = ttl
	})
	if err != nil {
		return "", normalizeS3Error(err)
	}
	return output.URL, nil
}

func (s *S3Store) objectInfoFromHeaders(key string, size *int64, contentType *string, etag *string, metadata map[string]string) ObjectInfo {
	info := ObjectInfo{
		Key:         key,
		ContentType: aws.ToString(contentType),
		ETag:        objectETagFromHeaders(etag, metadata),
	}
	if size != nil {
		info.Size = *size
	}
	return info
}

func objectETag(body io.ReadSeeker, start int64) (string, error) {
	current, err := body.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", err
	}

	digest := md5.New()
	if _, err := body.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	_, copyErr := io.Copy(digest, body)
	_, seekErr := body.Seek(current, io.SeekStart)
	if copyErr != nil {
		return "", copyErr
	}
	if seekErr != nil {
		return "", seekErr
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func objectETagFromHeaders(etag *string, metadata map[string]string) string {
	if metadataETag := metadataValue(metadata, s3ObjectETagMetadataKey); metadataETag != "" {
		return metadataETag
	}
	return cleanS3ETag(etag)
}

func metadataValue(metadata map[string]string, key string) string {
	for metadataKey, value := range metadata {
		if strings.EqualFold(metadataKey, key) {
			return value
		}
	}
	return ""
}

func normalizeS3Error(err error) error {
	var noSuchKey *s3types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return fmt.Errorf("%w: %w", ErrObjectNotFound, err)
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return fmt.Errorf("%w: %w", ErrObjectNotFound, err)
		}
	}
	return err
}

func cleanS3ETag(etag *string) string {
	return strings.Trim(aws.ToString(etag), `"`)
}
