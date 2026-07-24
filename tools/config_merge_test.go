package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"document-archive/internal/config"
	"document-archive/internal/storage"

	"gopkg.in/yaml.v3"
)

func TestMergeConfigAppliesDefaults(t *testing.T) {
	merged, err := mergeConfig([]byte(`addr: ":9090"`))
	if err != nil {
		t.Fatalf("mergeConfig returned error: %v", err)
	}

	var got config.Config
	if err := yaml.Unmarshal(merged, &got); err != nil {
		t.Fatalf("unmarshal merged config: %v", err)
	}
	if got.Addr != ":9090" {
		t.Fatalf("unexpected overridden addr: %q", got.Addr)
	}
	if got.DocumentStore != "sqlite" || got.DefaultStorageName != "memory" {
		t.Fatalf("defaults were not applied: %#v", got)
	}
	if got.Storages["memory"].Type != storage.MemoryStorageType {
		t.Fatalf("default memory storage was not applied: %#v", got.Storages)
	}
}

func TestMergeConfigCompletesNamedS3Storage(t *testing.T) {
	merged, err := mergeConfig([]byte(`
default_storage: "minio"
storages:
  minio:
    type: "s3"
    s3:
      bucket: "archive"
`))
	if err != nil {
		t.Fatalf("mergeConfig returned error: %v", err)
	}

	var got config.Config
	if err := yaml.Unmarshal(merged, &got); err != nil {
		t.Fatalf("unmarshal merged config: %v", err)
	}
	minio := got.Storages["minio"]
	if minio.Type != storage.S3StorageType || minio.S3 == nil || minio.S3.Bucket != "archive" {
		t.Fatalf("unexpected minio config: %#v", minio)
	}
	if !strings.Contains(string(merged), "internal_endpoint:") || !strings.Contains(string(merged), "use_path_style:") {
		t.Fatalf("merged s3 config is incomplete:\n%s", merged)
	}
	if got.Storages["memory"].Type != storage.MemoryStorageType {
		t.Fatalf("default memory storage was not retained: %#v", got.Storages)
	}

	assertStrictLoaderAccepts(t, merged)
}

func TestMergeConfigRejectsUnknownFields(t *testing.T) {
	_, err := mergeConfig([]byte(`unknown: true`))
	if err == nil {
		t.Fatal("mergeConfig should reject unknown fields")
	}
}

func TestWriteOutputUsesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}
	if err := writeOutput(path, []byte("new")); err != nil {
		t.Fatalf("writeOutput returned error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected output permissions: %o", info.Mode().Perm())
	}
}

func assertStrictLoaderAccepts(t *testing.T, content []byte) {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), content, 0o600); err != nil {
		t.Fatalf("write merged config: %v", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore Chdir returned error: %v", err)
		}
	})

	if _, err := config.Load(); err != nil {
		t.Fatalf("strict config loader rejected merged config: %v\n%s", err, content)
	}
}
