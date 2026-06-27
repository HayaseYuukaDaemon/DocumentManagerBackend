package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"document-archive/internal/storage"
)

func TestLoadUsesDefaultsWithoutConfigFile(t *testing.T) {
	chdirTemp(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Addr != ":8080" {
		t.Fatalf("unexpected addr: %q", cfg.Addr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected log level: %v", cfg.LogLevel)
	}
	if cfg.DefaultStorageBackend != storage.MemoryStorageName {
		t.Fatalf("unexpected default storage: %q", cfg.DefaultStorageBackend)
	}
	if cfg.DocumentStore != "sqlite" {
		t.Fatalf("unexpected document store: %q", cfg.DocumentStore)
	}
	if cfg.SQLitePath != "document-archive.db" {
		t.Fatalf("unexpected sqlite path: %q", cfg.SQLitePath)
	}
	if len(cfg.AllowCORS) != 0 {
		t.Fatalf("allow_cors should default to empty, got %#v", cfg.AllowCORS)
	}
	if cfg.S3UsePathStyle {
		t.Fatalf("s3 use path style should default to false")
	}
}

func TestLoadReadsConfigYAML(t *testing.T) {
	dir := chdirTemp(t)
	writeConfigFile(t, dir, `
addr: ":9090"
auth_token: "config-token"
log_level: "debug"
default_storage: "s3"
document_store: "memory"
sqlite_path: "/tmp/archive.db"
allow_cors:
  - "http://localhost:5173"
  - "https://example.com"
s3:
  endpoint: "http://127.0.0.1:9000"
  bucket: "archive"
  region: "us-east-1"
  access_key_id: "config-key"
  secret_access_key: "config-secret"
  session_token: "config-session"
  use_path_style: true
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Addr != ":9090" {
		t.Fatalf("unexpected addr: %q", cfg.Addr)
	}
	if cfg.AuthToken != "config-token" {
		t.Fatalf("unexpected auth token: %q", cfg.AuthToken)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("unexpected log level: %v", cfg.LogLevel)
	}
	if cfg.DefaultStorageBackend != storage.S3StorageName {
		t.Fatalf("unexpected default storage: %q", cfg.DefaultStorageBackend)
	}
	if cfg.DocumentStore != "memory" {
		t.Fatalf("unexpected document store: %q", cfg.DocumentStore)
	}
	if cfg.SQLitePath != "/tmp/archive.db" {
		t.Fatalf("unexpected sqlite path: %q", cfg.SQLitePath)
	}
	if !slices.Equal(cfg.AllowCORS, []string{"http://localhost:5173", "https://example.com"}) {
		t.Fatalf("unexpected allow_cors: %#v", cfg.AllowCORS)
	}
	if cfg.S3Endpoint != "http://127.0.0.1:9000" || cfg.S3Bucket != "archive" || cfg.S3Region != "us-east-1" {
		t.Fatalf("unexpected s3 endpoint/bucket/region: %#v", cfg)
	}
	if cfg.S3AccessKeyID != "config-key" || cfg.S3SecretAccessKey != "config-secret" || cfg.S3SessionToken != "config-session" {
		t.Fatalf("unexpected s3 credentials: %#v", cfg)
	}
	if !cfg.S3UsePathStyle {
		t.Fatalf("s3 use path style should come from config")
	}
}

func TestLoadIgnoresEnvironment(t *testing.T) {
	dir := chdirTemp(t)
	writeConfigFile(t, dir, `
addr: ":9090"
s3:
  use_path_style: true
`)
	t.Setenv("ARCHIVE_ADDR", ":7070")
	t.Setenv("ARCHIVE_S3_USE_PATH_STYLE", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Addr != ":9090" {
		t.Fatalf("env should not override config addr, got %q", cfg.Addr)
	}
	if !cfg.S3UsePathStyle {
		t.Fatalf("env should not override config bool")
	}
}

func TestLoadReturnsErrorForInvalidConfigYAML(t *testing.T) {
	dir := chdirTemp(t)
	writeConfigFile(t, dir, "s3:\n  use_path_style: [")

	_, err := Load()
	if err == nil {
		t.Fatalf("Load should return error for invalid config.yml")
	}
}

func TestLoadReturnsErrorForUnknownConfigField(t *testing.T) {
	dir := chdirTemp(t)
	writeConfigFile(t, dir, "unknown: true")

	_, err := Load()
	if err == nil {
		t.Fatalf("Load should return error for unknown config field")
	}
}

func chdirTemp(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
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
	return dir
}

func writeConfigFile(t *testing.T, dir string, content string) {
	t.Helper()

	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}
