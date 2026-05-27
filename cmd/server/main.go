package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

	documentStore := documents.NewMemoryStore()

	archiveApp := archive.NewApp(documentStore, logger, cfg.DefaultStorageBackend)
	archiveApp.RegisterSource(hitomi.NewHandler())
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
