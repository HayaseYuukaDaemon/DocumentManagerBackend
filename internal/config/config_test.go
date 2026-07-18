package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"document-archive/internal/storage"

	"gopkg.in/yaml.v3"
)

func TestLoadCreatesAndUsesDefaultsWithoutConfigFile(t *testing.T) {
	dir := chdirTemp(t)

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
	if cfg.DeletedSweepInterval != 24*time.Hour {
		t.Fatalf("unexpected deleted sweep interval: %v", cfg.DeletedSweepInterval)
	}
	if len(cfg.AllowCORS) != 0 {
		t.Fatalf("allow_cors should default to empty, got %#v", cfg.AllowCORS)
	}
	if cfg.S3 != nil {
		t.Fatalf("s3 should default to nil, got %#v", cfg.S3)
	}

	info, err := os.Stat(filepath.Join(dir, configFileName))
	if err != nil {
		t.Fatalf("generated config file not found: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected generated config permissions: %o", info.Mode().Perm())
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("reload generated config returned error: %v", err)
	}
	if !reflect.DeepEqual(reloaded, cfg) {
		t.Fatalf("generated config should round-trip: got %#v, want %#v", reloaded, cfg)
	}
}

func TestLoadReadsConfigYAML(t *testing.T) {
	dir := chdirTemp(t)
	writeConfigFile(t, dir, `
addr: ":9090"
log_level: "debug"
default_storage: "s3"
document_store: "memory"
sqlite_path: "/tmp/archive.db"
deleted_sweep_interval: "12h"
allow_cors:
  - "http://localhost:5173"
  - "https://example.com"
s3:
  internal_endpoint: "http://minio:9000"
  endpoint: "http://127.0.0.1:9000"
  bucket: "archive"
  region: "us-east-1"
  access_key_id: "config-key"
  secret_access_key: "config-secret"
  session_token: "config-session"
  use_path_style: true
role:
  admin-token:
    name: "admin"
    admin: true
  contributor-token:
    name: "contributor"
    permissions:
      - "document:create"
      - "document:read"
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Addr != ":9090" {
		t.Fatalf("unexpected addr: %q", cfg.Addr)
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
	if cfg.DeletedSweepInterval != 12*time.Hour {
		t.Fatalf("unexpected deleted sweep interval: %v", cfg.DeletedSweepInterval)
	}
	if !slices.Equal(cfg.AllowCORS, []string{"http://localhost:5173", "https://example.com"}) {
		t.Fatalf("unexpected allow_cors: %#v", cfg.AllowCORS)
	}
	if cfg.S3 == nil {
		t.Fatal("s3 config should be loaded")
	}
	if cfg.S3.Endpoint != "http://127.0.0.1:9000" || cfg.S3.Bucket != "archive" || cfg.S3.Region != "us-east-1" {
		t.Fatalf("unexpected s3 endpoint/bucket/region: %#v", cfg)
	}
	if cfg.S3.InternalEndpoint != "http://minio:9000" {
		t.Fatalf("unexpected s3 internal endpoint: %q", cfg.S3.InternalEndpoint)
	}
	if cfg.S3.AccessKeyID != "config-key" || cfg.S3.SecretAccessKey != "config-secret" || cfg.S3.SessionToken != "config-session" {
		t.Fatalf("unexpected s3 credentials: %#v", cfg)
	}
	if !cfg.S3.UsePathStyle {
		t.Fatalf("s3 use path style should come from config")
	}
	admin, ok := cfg.Roles["admin-token"]
	if !ok || admin.Name != "admin" || !admin.Admin {
		t.Fatalf("unexpected admin role: %#v", admin)
	}
	if !admin.HasPermission(DocumentDelete) {
		t.Fatalf("admin role should have every permission")
	}
	contributor, ok := cfg.Roles["contributor-token"]
	if !ok || contributor.Name != "contributor" {
		t.Fatalf("unexpected contributor role: %#v", contributor)
	}
	if !contributor.HasPermission(DocumentCreate) || !contributor.HasPermission(DocumentRead) {
		t.Fatalf("contributor should have configured permissions: %#v", contributor.Permissions)
	}
	if contributor.HasPermission(DocumentDelete) {
		t.Fatalf("contributor should not have unconfigured permissions")
	}
}

func TestLoadIgnoresEnvironment(t *testing.T) {
	dir := chdirTemp(t)
	want := defaultConfig()
	want.Addr = ":9090"
	want.S3 = &storage.S3Config{UsePathStyle: true}
	writeCompleteConfig(t, dir, want)
	t.Setenv("ARCHIVE_ADDR", ":7070")
	t.Setenv("ARCHIVE_S3_USE_PATH_STYLE", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Addr != ":9090" {
		t.Fatalf("env should not override config addr, got %q", cfg.Addr)
	}
	if !cfg.S3.UsePathStyle {
		t.Fatalf("env should not override config bool")
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("environment should not change config: got %#v, want %#v", cfg, want)
	}
}

func TestLoadNormalizesConfigValues(t *testing.T) {
	dir := chdirTemp(t)
	want := defaultConfig()
	want.LogLevel = slog.LevelWarn
	want.DefaultStorageBackend = " S3 "
	want.DocumentStore = " MEMORY "
	want.DeletedSweepInterval = 30 * time.Minute
	writeCompleteConfig(t, dir, want)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.LogLevel != slog.LevelWarn {
		t.Fatalf("unexpected log level: %v", cfg.LogLevel)
	}
	if cfg.DefaultStorageBackend != storage.S3StorageName {
		t.Fatalf("unexpected normalized storage: %q", cfg.DefaultStorageBackend)
	}
	if cfg.DocumentStore != "memory" {
		t.Fatalf("unexpected normalized document store: %q", cfg.DocumentStore)
	}
	if cfg.DeletedSweepInterval != 30*time.Minute {
		t.Fatalf("unexpected deleted sweep interval: %v", cfg.DeletedSweepInterval)
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

func TestLoadReturnsErrorForInvalidDeletedSweepInterval(t *testing.T) {
	dir := chdirTemp(t)
	writeConfigFile(t, dir, "deleted_sweep_interval: invalid")

	_, err := Load()
	if err == nil {
		t.Fatalf("Load should return error for invalid deleted_sweep_interval")
	}
}

func TestLoadReturnsErrorForNegativeDeletedSweepInterval(t *testing.T) {
	dir := chdirTemp(t)
	cfg := defaultConfig()
	cfg.DeletedSweepInterval = -time.Second
	writeCompleteConfig(t, dir, cfg)

	_, err := Load()
	if err == nil {
		t.Fatalf("Load should return error for negative deleted_sweep_interval")
	}
}

func TestLoadReturnsErrorForMissingConfigField(t *testing.T) {
	dir := chdirTemp(t)
	cfg := defaultConfig()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	content := removeYAMLField(t, string(data), "allow_cors")
	writeConfigFile(t, dir, content)

	_, err = Load()
	if err == nil {
		t.Fatalf("Load should return error for missing config field")
	}
	if !strings.Contains(err.Error(), "allow_cors") {
		t.Fatalf("unexpected missing field error: %v", err)
	}
}

func TestLoadReturnsErrorForMissingS3ConfigField(t *testing.T) {
	dir := chdirTemp(t)
	cfg := defaultConfig()
	cfg.S3 = &storage.S3Config{}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	content := removeYAMLField(t, string(data), "internal_endpoint")
	writeConfigFile(t, dir, content)

	_, err = Load()
	if err == nil {
		t.Fatalf("Load should return error for missing s3 config field")
	}
	if !strings.Contains(err.Error(), "s3.internal_endpoint") {
		t.Fatalf("unexpected missing field error: %v", err)
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

func writeCompleteConfig(t *testing.T, dir string, cfg Config) {
	t.Helper()

	path := filepath.Join(dir, configFileName)
	if err := writeDefaultConfig(path, cfg); err != nil {
		t.Fatalf("writeDefaultConfig returned error: %v", err)
	}
}

func removeYAMLField(t *testing.T, content string, name string) string {
	t.Helper()

	lines := strings.SplitAfter(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), name+":") {
			return strings.Join(append(lines[:i], lines[i+1:]...), "")
		}
	}
	t.Fatalf("field %q not found in YAML", name)
	return ""
}
