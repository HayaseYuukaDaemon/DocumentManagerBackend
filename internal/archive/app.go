package archive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

type App struct {
	documents      documents.Store
	storages       map[storage.StorageName]storage.ObjectStore
	defaultStorage storage.StorageName
	sources        map[sources.SourceType]SourceHandler
	logger         *slog.Logger
}

func NewApp(documentStore documents.Store, logger *slog.Logger, defaultStorage storage.StorageName) *App {
	return &App{
		documents:      documentStore,
		storages:       make(map[storage.StorageName]storage.ObjectStore),
		defaultStorage: defaultStorage,
		sources:        make(map[sources.SourceType]SourceHandler),
		logger:         logger,
	}
}

func (a *App) RegisterSource(handler SourceHandler) {
	a.sources[handler.Source()] = handler
}

func (a *App) RegisterStorage(storage storage.ObjectStore) {
	a.storages[storage.StorageName()] = storage
}

func (a *App) RequestDocument(ctx context.Context, input documents.RequestDocumentInput) (*documents.Document, error) {
	if input.Source == "" {
		return nil, errors.New("source is required")
	}
	if len(input.SourceDocumentID) == 0 {
		return nil, errors.New("source_document_id is required")
	}

	if _, err := a.getSource(input.Source); err != nil {
		return nil, err
	}

	storageBackend := input.StorageBackend
	if storageBackend == "" {
		storageBackend = a.defaultStorage
	}
	if _, err := a.getStorage(storageBackend); err != nil {
		return nil, err
	}

	document := documents.Document{
		Source:           input.Source,
		SourceDocumentID: input.SourceDocumentID,
		SourceMeta:       input.SourceMeta,
		StorageBackend:   storageBackend,
		ArchiveStatus:    documents.StatusQueued,
	}
	return a.documents.Create(ctx, &document)
}

func (a *App) GetDocument(ctx context.Context, id int) (*documents.Document, error) {
	return a.documents.Get(ctx, id)
}

func (a *App) GetPage(ctx context.Context, document *documents.Document, pageIndex int) (PageResult, error) {
	return PageResult{}, nil
}

func (a *App) QueryDocument(ctx context.Context, input documents.QueryInput) ([]*documents.Document, error) {
	switch input.Mode {
	case documents.QueryBySourceDocumentID:
		var params documents.QueryBySourceDocumentIDParams
		if err := json.Unmarshal(input.Params, &params); err != nil {
			return nil, fmt.Errorf("decode query params: %w", err)
		}
		document, err := a.documents.GetBySourceDocumentID(ctx, params.Source, params.SourceDocumentID)
		if err != nil {
			return nil, err
		}
		return []*documents.Document{document}, nil
	default:
		return nil, fmt.Errorf("unsupported query mode: %s", input.Mode)
	}
}

func (a *App) RemoveDocument(ctx context.Context, id int) (*documents.Document, error) {
	return a.documents.Remove(ctx, id)
}

func (a *App) RunWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.processQueued(ctx)
		}
	}
}

func (a *App) processQueued(ctx context.Context) {
	queued, err := a.documents.ListByStatus(ctx, documents.StatusQueued, 5)
	if err != nil {
		a.logger.Error("list queued documents failed", "error", err)
		return
	}

	for _, document := range queued {
		a.logger.Info("processing document archive", "document_id", document.ID, "source", document.Source)
		if _, err := a.processDocument(ctx, document); err != nil {
			a.logger.Warn("process document archive failed", "document_id", document.ID, "error", err)
		}
	}
}

func (a *App) processDocument(ctx context.Context, document *documents.Document) (*documents.Document, error) {
	handler, err := a.getSource(document.Source)
	if err != nil {
		return a.failDocument(ctx, document, err)
	}

	document.ArchiveStatus = documents.StatusResolving
	document, err = a.documents.Update(ctx, document)
	if err != nil {
		return document, err
	}

	objectStorage, err := a.getStorage(document.StorageBackend)
	if err != nil {
		return a.failDocument(ctx, document, err)
	}

	manifest, err := handler.Archive(ctx, document, objectStorage)
	if err != nil {
		return a.failDocument(ctx, document, err)
	}

	document.ArchiveStatus = documents.StatusArchived
	document.Progress.Done = manifest.PageCount
	document.Progress.Total = manifest.PageCount
	document.PageCount = manifest.PageCount
	document.Error = ""
	if manifest.Title != "" {
		document.Title = manifest.Title
	}
	return a.documents.Update(ctx, document)
}

func (a *App) getSource(source sources.SourceType) (SourceHandler, error) {
	handler, ok := a.sources[source]
	if !ok {
		return nil, fmt.Errorf("source handler not found: %s", source)
	}
	return handler, nil
}

func (a *App) getStorage(storageBackend storage.StorageName) (storage.ObjectStore, error) {
	objectStorage := a.storages[storageBackend]
	if objectStorage == nil {
		return nil, fmt.Errorf("storage backend not found: %s", storageBackend)
	}
	return objectStorage, nil
}

func (a *App) failDocument(ctx context.Context, document *documents.Document, cause error) (*documents.Document, error) {
	document.ArchiveStatus = documents.StatusFailed
	document.Error = cause.Error()
	updated, err := a.documents.Update(ctx, document)
	if err != nil {
		return document, err
	}
	a.logger.Warn("document archive failed", "document_id", document.ID, "error", cause)
	return updated, cause
}
