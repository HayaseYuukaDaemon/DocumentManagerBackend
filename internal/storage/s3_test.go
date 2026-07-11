package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

const (
	s3TestEndpoint        = "http://127.0.0.1:9000"
	s3TestBucket          = "document-archive-test"
	s3TestRegion          = "us-east-1"
	s3TestAccessKeyID     = "minioadmin"
	s3TestSecretAccessKey = "minioadmin123"
)

func TestS3StorePutHeadGetAndPresign(t *testing.T) {
	store := newLocalS3TestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	key := fmt.Sprintf("storage-test/%d/pages/000001.webp", time.Now().UnixNano())
	defer deleteS3TestObject(t, store, key)

	body := []byte("page")
	putInfo, err := store.PutObject(ctx, ObjectInfo{
		Key:         key,
		Size:        int64(len(body)),
		ContentType: "image/webp",
		ETag:        "source-etag",
	}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("PutObject returned error: %v", err)
	}
	if putInfo.Key != key || putInfo.Size != 4 || putInfo.ContentType != "image/webp" || putInfo.ETag != "source-etag" {
		t.Fatalf("unexpected put info: %#v", putInfo)
	}

	headInfo, err := store.HeadObject(ctx, key)
	if err != nil {
		t.Fatalf("HeadObject returned error: %v", err)
	}
	if headInfo.Key != key || headInfo.Size != 4 || headInfo.ContentType != "image/webp" || headInfo.ETag != "source-etag" {
		t.Fatalf("unexpected head info: %#v", headInfo)
	}

	object, err := store.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject returned error: %v", err)
	}
	defer object.Body.Close()
	content, err := io.ReadAll(object.Body)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(content) != "page" {
		t.Fatalf("unexpected object content: %q", content)
	}
	if object.Key != key || object.Size != 4 || object.ContentType != "image/webp" || object.ETag != "source-etag" {
		t.Fatalf("unexpected object info: %#v", object.ObjectInfo)
	}

	presignedURL, err := store.PresignGetObject(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("PresignGetObject returned error: %v", err)
	}
	assertS3TestPresignedURL(t, presignedURL, key)
	assertS3TestPresignedURLContent(t, presignedURL, "page")
}

func TestS3StorePutUsesNativeETagWhenInputETagIsEmpty(t *testing.T) {
	store := newLocalS3TestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	key := fmt.Sprintf("storage-test/%d/pages/000002.webp", time.Now().UnixNano())
	defer deleteS3TestObject(t, store, key)

	body := []byte("page")
	putInfo, err := store.PutObject(ctx, ObjectInfo{
		Key:         key,
		Size:        int64(len(body)),
		ContentType: "image/webp",
	}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("PutObject returned error: %v", err)
	}
	if putInfo.ETag == "" {
		t.Fatalf("expected native etag in put info")
	}

	headInfo, err := store.HeadObject(ctx, key)
	if err != nil {
		t.Fatalf("HeadObject returned error: %v", err)
	}
	if headInfo.ETag != putInfo.ETag {
		t.Fatalf("head etag should match put etag: head=%s put=%s", headInfo.ETag, putInfo.ETag)
	}
}

func TestS3StoreHeadMissingObject(t *testing.T) {
	store := newLocalS3TestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	key := fmt.Sprintf("storage-test/%d/missing.webp", time.Now().UnixNano())
	_, err := store.HeadObject(ctx, key)
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("expected ErrObjectNotFound, got %v", err)
	}
}

func TestS3StoreDeletePrefix(t *testing.T) {
	store := newLocalS3TestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	base := fmt.Sprintf("storage-test/%d", time.Now().UnixNano())
	prefix := base + "/document/"
	sibling := base + "/document-sibling/page"
	defer deleteS3TestObject(t, store, sibling)
	for _, key := range []string{prefix + "pages/hash-a", prefix + "manifest.json", sibling} {
		if _, err := store.PutObject(ctx, ObjectInfo{Key: key, Size: 1}, bytes.NewReader([]byte("x"))); err != nil {
			t.Fatalf("PutObject(%q) returned error: %v", key, err)
		}
	}

	if err := store.DeletePrefix(ctx, prefix); err != nil {
		t.Fatalf("DeletePrefix returned error: %v", err)
	}
	for _, key := range []string{prefix + "pages/hash-a", prefix + "manifest.json"} {
		if _, err := store.HeadObject(ctx, key); !errors.Is(err, ErrObjectNotFound) {
			t.Fatalf("expected %q to be deleted, got %v", key, err)
		}
	}
	if _, err := store.HeadObject(ctx, sibling); err != nil {
		t.Fatalf("DeletePrefix removed sibling object: %v", err)
	}
}

func TestObjectETagComputesMD5AndRewinds(t *testing.T) {
	body := []byte("page")
	reader := bytes.NewReader(body)

	etag, err := objectETag(reader, 0)
	if err != nil {
		t.Fatalf("objectETag returned error: %v", err)
	}
	sum := md5.Sum(body)
	if etag != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected etag: %s", etag)
	}

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(content) != "page" {
		t.Fatalf("unexpected prepared content: %q", content)
	}
}

func newLocalS3TestStore(t *testing.T) *S3Store {
	t.Helper()

	if !localS3PortOpen() {
		t.Skip("local S3-compatible service is not listening on 127.0.0.1:9000")
	}

	store, err := NewS3Store(S3Config{
		Endpoint:        s3TestEndpoint,
		Bucket:          s3TestBucket,
		Region:          s3TestRegion,
		AccessKeyID:     s3TestAccessKeyID,
		SecretAccessKey: s3TestSecretAccessKey,
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("NewS3Store returned error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := probeLocalS3Service(ctx, store); err != nil {
		if isS3CredentialOrPermissionError(err) {
			t.Fatalf("local S3-compatible service rejected test credentials: %v", err)
		}
		t.Skipf("local 9000 endpoint is not a usable S3-compatible service: %v", err)
	}
	if err := ensureS3TestBucket(ctx, store); err != nil {
		t.Fatalf("ensure test bucket returned error: %v", err)
	}
	return store
}

func localS3PortOpen() bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:9000", time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func probeLocalS3Service(ctx context.Context, store *S3Store) error {
	_, err := store.client.ListBuckets(ctx, &s3.ListBucketsInput{})
	return err
}

func ensureS3TestBucket(ctx context.Context, store *S3Store) error {
	_, err := store.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s3TestBucket),
	})
	if err == nil {
		return nil
	}
	if !isS3BucketNotFound(err) {
		return err
	}

	_, err = store.client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(s3TestBucket),
	})
	return err
}

func isS3BucketNotFound(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "NoSuchBucket", "NotFound", "404":
		return true
	default:
		return false
	}
}

func isS3CredentialOrPermissionError(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "AccessDenied", "InvalidAccessKeyId", "InvalidToken", "SignatureDoesNotMatch":
		return true
	default:
		return false
	}
}

func deleteS3TestObject(t *testing.T, store *S3Store, key string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := store.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s3TestBucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("DeleteObject returned error: %v", err)
	}
}

func assertS3TestPresignedURL(t *testing.T, rawURL string, key string) {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("presigned url is invalid: %v", err)
	}
	if parsed.Host != "127.0.0.1:9000" {
		t.Fatalf("presigned url should use local endpoint, got %s", rawURL)
	}
	expectedPath := "/" + s3TestBucket + "/" + key
	if parsed.EscapedPath() != expectedPath {
		t.Fatalf("presigned url should use path-style object path %s, got %s", expectedPath, rawURL)
	}
	if parsed.Query().Get("X-Amz-Signature") == "" {
		t.Fatalf("presigned url should include X-Amz-Signature, got %s", rawURL)
	}
}

func assertS3TestPresignedURLContent(t *testing.T, rawURL string, want string) {
	t.Helper()

	client := http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(rawURL)
	if err != nil {
		t.Fatalf("GET presigned url returned error: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET presigned url returned status %d", response.StatusCode)
	}

	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll presigned body returned error: %v", err)
	}
	if string(content) != want {
		t.Fatalf("unexpected presigned object content: %q", content)
	}
}
