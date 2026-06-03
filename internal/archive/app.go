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

func (a *App) onPageDownloaded(ctx context.Context, documentID int, page documents.Page) error {
	_, err := a.documents.Update(ctx, documentID, func(document *documents.Document) error {
		index := page.Index
		if index >= len(document.Pages) {
			if index >= cap(document.Pages) {
				newPages := make([]documents.Page, len(document.Pages), index+1)
				copy(newPages, document.Pages)
				document.Pages = newPages
			}
			document.Pages = document.Pages[:index+1]
		}
		document.Pages[index] = page
		document.Progress.Done++
		return nil
	})
	return err
}

func (a *App) RegisterSource(handler SourceHandler) error {
	err := handler.RegisterPageDownloadHook(a.onPageDownloaded)
	if err != nil {
		return err
	}
	a.sources[handler.Source()] = handler
	return nil
}

func (a *App) RegisterStorage(storage storage.ObjectStore) {
	a.storages[storage.StorageName()] = storage
}

func (a *App) RequestDocument(ctx context.Context, input documents.RequestDocumentInput) (documents.Document, error) {
	if input.Source == "" {
		return documents.Document{}, errors.New("source is required")
	}
	if len(input.SourceDocumentID) == 0 {
		return documents.Document{}, errors.New("source_document_id is required")
	}

	if _, err := a.getSource(input.Source); err != nil {
		return documents.Document{}, err
	}

	storageBackend := input.StorageBackend
	if storageBackend == "" {
		storageBackend = a.defaultStorage
	}
	if _, err := a.getStorage(storageBackend); err != nil {
		return documents.Document{}, err
	}

	document := documents.Document{
		Source:           input.Source,
		SourceDocumentID: input.SourceDocumentID,
		SourceMeta:       input.SourceMeta,
		StorageBackend:   storageBackend,
		ArchiveStatus:    documents.StatusQueued,
	}
	return a.documents.Create(ctx, document)
}

func (a *App) GetDocument(ctx context.Context, id int) (documents.Document, error) {
	return a.documents.Get(ctx, id)
}

func (a *App) GetPage(ctx context.Context, document documents.Document, pageIndex int) (PageResult, error) {
	storage, err := a.getStorage(document.StorageBackend)
	if err != nil {
		return PageResult{}, err
	}
	pagesLen := len(document.Pages)
	if pageIndex >= pagesLen {
		return PageResult{}, fmt.Errorf("page index out of bounds")
	}
	page := document.Pages[pageIndex]
	if storage.StorageName() == "memory" {
		pageObject, err := storage.GetObject(ctx, page.Key)
		if err != nil {
			return PageResult{}, fmt.Errorf("failed to get page object: %w", err)
		}
		return PageResult{
			Kind:   PageResultObject,
			Object: pageObject,
		}, nil
	}
	return PageResult{}, fmt.Errorf("unsupported storage backend")
}

func (a *App) QueryDocument(ctx context.Context, input documents.QueryInput) ([]documents.Document, error) {
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
		return []documents.Document{document}, nil
	default:
		return nil, fmt.Errorf("unsupported query mode: %s", input.Mode)
	}
}

func (a *App) RefreshDocument(ctx context.Context, id int, mode documents.RefreshMode) (documents.Document, error) {
	document, err := a.documents.Get(ctx, id)
	if err != nil {
		return documents.Document{}, err
	}

	switch mode {
	case documents.OnlyMetaData:
		_, err = a.documents.Update(ctx, id, func(document *documents.Document) error {
			document.ArchiveStatus = documents.StatusQueued
			return nil
		})
	case documents.All:
		_, err = a.documents.Update(ctx, id, func(document *documents.Document) error {
			document.ArchiveStatus = documents.StatusQueued
			document.Progress.Done = 0
			return nil
		})
	default:
		return documents.Document{}, fmt.Errorf("invalid refresh mode: %s", mode)
	}
	return document, err
}

func (a *App) RemoveDocument(ctx context.Context, id int) (documents.Document, error) {
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
		if _, err := a.processDocument(ctx, document.ID); err != nil {
			a.logger.Warn("process document archive failed", "document_id", document.ID, "error", err)
		}
		a.logger.Info("document process done", "document_id", document.ID)
	}
}

func (a *App) processDocument(ctx context.Context, id int) (documents.Document, error) {
	document, err := a.documents.Get(ctx, id)
	if err != nil {
		return documents.Document{}, err
	}

	handler, err := a.getSource(document.Source)
	if err != nil {
		return a.failDocument(ctx, id, err)
	}
	objectStorage, err := a.getStorage(document.StorageBackend)
	if err != nil {
		return a.failDocument(ctx, id, err)
	}

	_, err = a.documents.Update(ctx, id, func(document *documents.Document) error {
		document.ArchiveStatus = documents.StatusResolving
		return nil
	})
	if err != nil {
		return a.failDocument(ctx, id, err)
	}
	manifest, err := handler.ArchiveManifest(ctx, document, objectStorage)
	if err != nil {
		return a.failDocument(ctx, id, err)
	}

	if document.Progress.Done == 0 {
		document, err = a.documents.Update(ctx, id, func(d *documents.Document) error {
			d.ArchiveStatus = documents.StatusDownloading
			d.SourceMeta = manifest.SourceMeta
			return nil
		})
		if err != nil {
			return a.failDocument(ctx, id, err)
		}
		_, err = handler.ArchiveContent(ctx, document, objectStorage)
		if err != nil {
			return a.failDocument(ctx, id, err)
		}
		_, err := handler.ArchiveManifest(ctx, document, objectStorage)
		if err != nil {
			return a.failDocument(ctx, id, err)
		}
	}

	_, err = a.documents.Update(ctx, id, func(d *documents.Document) error {
		if manifest.Title != "" {
			d.Title = manifest.Title
		}
		d.ArchiveStatus = documents.StatusArchived
		d.Error = ""
		return nil
	})
	if err != nil {
		return documents.Document{}, err
	}
	return document, nil
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

func (a *App) failDocument(ctx context.Context, id int, cause error) (documents.Document, error) {
	document, err := a.documents.Update(ctx, id, func(d *documents.Document) error {
		d.ArchiveStatus = documents.StatusFailed
		d.Error = cause.Error()
		return nil
	})
	if err != nil {
		return document, err
	}
	a.logger.Warn("document archive failed", "document_id", document.ID, "error", cause)
	return document, cause
}
