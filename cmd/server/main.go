package main

import (
	"context"
	"errors"
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
	"document-archive/internal/storage"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))

	documentStore, closeDocumentStore, err := newDocumentStore(context.Background(), cfg, logger)
	if err != nil {
		logger.Error("failed to open document store", "error", err)
		os.Exit(1)
	}
	defer closeDocumentStore()

	archiveApp := archive.NewApp(documentStore, logger, cfg.DefaultStorageBackend)
	err = archiveApp.RegisterSource(hitomi.NewHandler())
	if err != nil {
		logger.Error("failed to register source", "error", err)
		os.Exit(1)
	}
	archiveApp.RegisterStorage(storage.NewMemoryStore())

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
