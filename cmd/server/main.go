package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"document-archive/internal/archive"
	"document-archive/internal/config"
	"document-archive/internal/documents"
	"document-archive/internal/httpapi"
	"document-archive/internal/sources/hitomi"
	"document-archive/internal/sources/jmcomic"
	"document-archive/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))

	documentStore, closeDocumentStore, err := newDocumentStore(context.Background(), cfg, logger)
	if err != nil {
		logger.Error("failed to open document store", "error", err)
		os.Exit(1)
	}
	defer closeDocumentStore()

	archiveApp := archive.NewApp(documentStore, logger, cfg.DefaultStorageName, cfg.DeletedSweepInterval)
	err = archiveApp.RegisterSourceFactory(hitomi.NewFactory())
	if err != nil {
		logger.Error("failed to register source factory", "error", err, "source", hitomi.SourceTypeHitomi)
		os.Exit(1)
	}
	jf, err := jmcomic.NewFactory()
	if err != nil {
		logger.Error("failed to create jmcomic handler factory", "error", err)
		os.Exit(1)
	}
	err = archiveApp.RegisterSourceFactory(jf)
	if err != nil {
		logger.Error("failed to register source factory", "error", err, "source", jmcomic.SourceTypeJmcomic)
		os.Exit(1)
	}
	if err := registerObjectStores(archiveApp, cfg, logger); err != nil {
		logger.Error("failed to register object stores", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go archiveApp.RunWorker(ctx)

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.NewRouter(cfg, archiveApp),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("archive service listening", "addr", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown failed", "error", err)
	}
}

func newDocumentStore(ctx context.Context, cfg config.Config, logger *slog.Logger) (documents.Store, func(), error) {
	switch strings.ToLower(strings.TrimSpace(cfg.DocumentStore)) {
	case "", "memory":
		logger.Info("using memory document store")
		return documents.NewMemoryStore(), func() {}, nil
	case "sqlite":
		store, err := documents.NewSQLiteStore(ctx, cfg.SQLitePath)
		if err != nil {
			return nil, nil, err
		}
		logger.Info("using sqlite document store", "path", cfg.SQLitePath)
		return store, func() {
			if err := store.Close(); err != nil {
				logger.Error("failed to close sqlite document store", "error", err)
			}
		}, nil
	default:
		return nil, nil, errors.New("unsupported document store: " + cfg.DocumentStore)
	}
}

func registerObjectStores(app *archive.App, cfg config.Config, logger *slog.Logger) error {
	for name, storageConfig := range cfg.Storages {
		var objectStorage storage.ObjectStore
		switch storageConfig.Type {
		case storage.MemoryStorageType:
			objectStorage = storage.NewMemoryStore(name)
		case storage.S3StorageType:
			if storageConfig.S3 == nil {
				return fmt.Errorf("s3 config is required for object storage %q", name)
			}
			store, err := storage.NewS3Store(name, *storageConfig.S3)
			if err != nil {
				return fmt.Errorf("create object storage %q: %w", name, err)
			}
			objectStorage = store
		default:
			return fmt.Errorf("unsupported object storage type %q for %q", storageConfig.Type, name)
		}
		if err := app.RegisterStorage(objectStorage); err != nil {
			return err
		}
		logger.Info("registered object storage", "name", name, "type", storageConfig.Type)
	}
	return nil
}
