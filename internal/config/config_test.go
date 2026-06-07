package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"document-archive/internal/storage"
)

var configEnvKeys = []string{
	"ARCHIVE_ADDR",
	"ARCHIVE_TOKEN",
	"ARCHIVE_LOG_LEVEL",
	"ARCHIVE_DEFAULT_STORAGE",
	"ARCHIVE_DOCUMENT_STORE",
	"ARCHIVE_SQLITE_PATH",
	"ARCHIVE_S3_ENDPOINT",
	"ARCHIVE_S3_BUCKET",
	"ARCHIVE_S3_REGION",
	"ARCHIVE_S3_ACCESS_KEY_ID",
	"ARCHIVE_S3_SECRET_ACCESS_KEY",
	"ARCHIVE_S3_SESSION_TOKEN",
	"ARCHIVE_S3_USE_PATH_STYLE",
}

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

func TestLoadEnvOverridesConfigYAML(t *testing.T) {
	dir := chdirTemp(t)
	writeConfigFile(t, dir, `
addr: ":9090"
auth_token: "config-token"
log_level: "debug"
default_storage: "s3"
document_store: "memory"
sqlite_path: "/tmp/config.db"
s3:
  endpoint: "http://config-endpoint"
  bucket: "config-bucket"
  region: "config-region"
  access_key_id: "config-key"
  secret_access_key: "config-secret"
  session_token: "config-session"
  use_path_style: true
`)
	t.Setenv("ARCHIVE_ADDR", ":7070")
	t.Setenv("ARCHIVE_TOKEN", "env-token")
	t.Setenv("ARCHIVE_LOG_LEVEL", "warn")
	t.Setenv("ARCHIVE_DEFAULT_STORAGE", "memory")
	t.Setenv("ARCHIVE_DOCUMENT_STORE", "sqlite")
	t.Setenv("ARCHIVE_SQLITE_PATH", "/tmp/env.db")
	t.Setenv("ARCHIVE_S3_ENDPOINT", "http://env-endpoint")
	t.Setenv("ARCHIVE_S3_BUCKET", "env-bucket")
	t.Setenv("ARCHIVE_S3_REGION", "env-region")
	t.Setenv("ARCHIVE_S3_ACCESS_KEY_ID", "env-key")
	t.Setenv("ARCHIVE_S3_SECRET_ACCESS_KEY", "env-secret")
	t.Setenv("ARCHIVE_S3_SESSION_TOKEN", "env-session")
	t.Setenv("ARCHIVE_S3_USE_PATH_STYLE", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Addr != ":7070" || cfg.AuthToken != "env-token" || cfg.LogLevel != slog.LevelWarn {
		t.Fatalf("top-level env values did not override config: %#v", cfg)
	}
	if cfg.DefaultStorageBackend != storage.MemoryStorageName || cfg.DocumentStore != "sqlite" || cfg.SQLitePath != "/tmp/env.db" {
		t.Fatalf("storage env values did not override config: %#v", cfg)
	}
	if cfg.S3Endpoint != "http://env-endpoint" || cfg.S3Bucket != "env-bucket" || cfg.S3Region != "env-region" {
		t.Fatalf("s3 env values did not override config: %#v", cfg)
	}
	if cfg.S3AccessKeyID != "env-key" || cfg.S3SecretAccessKey != "env-secret" || cfg.S3SessionToken != "env-session" {
		t.Fatalf("s3 credential env values did not override config: %#v", cfg)
	}
	if cfg.S3UsePathStyle {
		t.Fatalf("bool env value should override config")
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

func TestLoadReturnsErrorForInvalidEnvBool(t *testing.T) {
	chdirTemp(t)
	t.Setenv("ARCHIVE_S3_USE_PATH_STYLE", "sometimes")

	_, err := Load()
	if err == nil {
		t.Fatalf("Load should return error for invalid bool env")
	}
}

func chdirTemp(t *testing.T) string {
	t.Helper()
	clearConfigEnv(t)

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

func clearConfigEnv(t *testing.T) {
	t.Helper()

	previous := make(map[string]string, len(configEnvKeys))
	present := make(map[string]bool, len(configEnvKeys))
	for _, key := range configEnvKeys {
		value, ok := os.LookupEnv(key)
		if ok {
			previous[key] = value
			present[key] = true
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("Unsetenv(%s) returned error: %v", key, err)
		}
	}
	t.Cleanup(func() {
		for _, key := range configEnvKeys {
			var err error
			if present[key] {
				err = os.Setenv(key, previous[key])
			} else {
				err = os.Unsetenv(key)
			}
			if err != nil {
				t.Fatalf("restore env %s returned error: %v", key, err)
			}
		}
	})
}

func writeConfigFile(t *testing.T, dir string, content string) {
	t.Helper()

	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}
