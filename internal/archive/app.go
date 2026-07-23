package archive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

const pagePresignTTL = 24 * time.Hour
const deletedPurgeBatchSize = 5

type App struct {
	documents      documents.Store
	storages       map[storage.StorageName]storage.ObjectStore
	defaultStorage storage.StorageName
	deletedSweep   time.Duration
	sources        map[sources.SourceType]SourceHandler
	logger         *slog.Logger
}

func NewApp(documentStore documents.Store, logger *slog.Logger, defaultStorage storage.StorageName, deletedSweep time.Duration) *App {
	return &App{
		documents:      documentStore,
		storages:       make(map[storage.StorageName]storage.ObjectStore),
		defaultStorage: defaultStorage,
		deletedSweep:   deletedSweep,
		sources:        make(map[sources.SourceType]SourceHandler),
		logger:         logger,
	}
}

func (a *App) onPageDownloaded(ctx context.Context, documentID int, page documents.Page) error {
	return a.documents.AddPage(ctx, documentID, documents.Page{
		Index:       page.Index,
		Key:         page.Key,
		ContentType: page.ContentType,
		Size:        page.Size,
		Hash:        page.Hash,
	})
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
		StorageBackend:   storageBackend,
	}
	return a.documents.Create(ctx, document)
}

func (a *App) GetDocument(ctx context.Context, id int) (documents.Document, error) {
	return a.documents.Get(ctx, id)
}

func (a *App) GetPage(ctx context.Context, document documents.Document, pageIndex int) (PageResult, error) {
	storageBackend, err := a.getStorage(document.StorageBackend)
	if err != nil {
		return PageResult{}, err
	}
	pagesLen := len(document.Pages)
	if pageIndex < 0 || pageIndex >= pagesLen {
		return PageResult{}, fmt.Errorf("page index out of bounds")
	}
	page := document.Pages[pageIndex]
	if page.Key == "" {
		return PageResult{}, fmt.Errorf("page not archived")
	}
	switch storageBackend.StorageName() {
	case storage.MemoryStorageName:
		pageObject, err := storageBackend.GetObject(ctx, page.Key)
		if err != nil {
			return PageResult{}, fmt.Errorf("failed to get page object: %w", err)
		}
		return PageResult{
			Kind:   PageResultObject,
			Object: pageObject,
		}, nil
	case storage.S3StorageName:
		redirectURL, err := storageBackend.PresignGetObject(ctx, page.Key, pagePresignTTL)
		if err != nil {
			return PageResult{}, fmt.Errorf("failed to presign page object: %w", err)
		}
		return PageResult{
			Kind:        PageResultRedirect,
			RedirectURL: redirectURL,
		}, nil
	}
	return PageResult{}, fmt.Errorf("unsupported storage backend: %s", storageBackend.StorageName())
}

func (a *App) QueryDocument(ctx context.Context, input documents.QueryInput) ([]documents.Document, error) {
	qb := documents.QueryBuilder{}
	switch input.Mode {
	case documents.QueryBySourceDocumentID:
		var params struct {
			Source           sources.SourceType `json:"source"`
			SourceDocumentID string             `json:"source_document_id"`
		}
		if err := json.Unmarshal(input.Params, &params); err != nil {
			return nil, fmt.Errorf("decode query params: %w", err)
		}
		qb = qb.BySourceDocumentID(params.Source, params.SourceDocumentID)

	case documents.QueryByStatus:
		var params struct {
			Status documents.DocumentStatus `json:"status"`
		}
		if err := json.Unmarshal(input.Params, &params); err != nil {
			return nil, fmt.Errorf("decode query params: %w", err)
		}
		qb = qb.ByStatus(params.Status)
	case documents.QueryAll:
		// No additional filtering needed for QueryAll
	default:
		return nil, fmt.Errorf("unsupported query mode: %s", input.Mode)
	}
	query, err := qb.OrderBy(input.OrderBy).Order(input.Order).Limit(input.Limit).Offset(input.Offset).Build()
	if err != nil {
		return nil, err
	}
	documents, err := a.documents.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	return documents, nil
}

func (a *App) RefreshDocument(ctx context.Context, id int, mode documents.RefreshMode) (documents.Document, error) {
	switch mode {
	case documents.OnlyMetaData:
		if err := a.documents.TransitionTo(ctx, id, documents.StatusQueued); err != nil {
			return documents.Document{}, err
		}
		return a.documents.Get(ctx, id)
	case documents.All:
		if err := a.documents.TransitionTo(ctx, id, documents.StatusQueued); err != nil {
			return documents.Document{}, err
		}
		return a.documents.ResetPages(ctx, id)
	case documents.Restore:
		return a.documents.Restore(ctx, id)
	default:
		return documents.Document{}, fmt.Errorf("invalid refresh mode: %s", mode)
	}
}

func (a *App) RemoveDocument(ctx context.Context, id int) (documents.Document, error) {
	return a.documents.Delete(ctx, id)
}

func (a *App) RunWorker(ctx context.Context) {
	queuedTicker := time.NewTicker(time.Second)
	defer queuedTicker.Stop()

	var deletedTicker *time.Ticker
	var deletedCh <-chan time.Time
	if a.deletedSweep > 0 {
		deletedTicker = time.NewTicker(a.deletedSweep)
		defer deletedTicker.Stop()
		deletedCh = deletedTicker.C
	}

	a.processDeleted(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-queuedTicker.C:
			a.processQueued(ctx)
		case <-deletedCh:
			a.processDeleted(ctx)
		}
	}
}

func (a *App) processDeleted(ctx context.Context) {
	for {
		qb := documents.QueryBuilder{}
		query, err := qb.ByStatus(documents.StatusDeleted).Limit(deletedPurgeBatchSize).Build()
		if err != nil {
			a.logger.Error("build deleted documents query failed", "error", err)
			return
		}
		deleted, err := a.documents.Query(ctx, query)
		if err != nil {
			a.logger.Error("list deleted documents failed", "error", err)
			return
		}
		if len(deleted) == 0 {
			return
		}

		purgedCount := 0
		for _, document := range deleted {
			a.logger.Info("purging document archive", "document_id", document.ID)
			storageBackend, err := a.getStorage(document.StorageBackend)
			if err != nil {
				a.logger.Warn("get storage backend failed", "document_id", document.ID, "error", err)
				continue
			}

			prefix := DocumentObjectPrefix(strconv.Itoa(document.ID))
			if err := storageBackend.DeletePrefix(ctx, prefix); err != nil {
				if !errors.Is(err, storage.ErrObjectNotFound) {
					a.logger.Warn("delete document objects failed", "document_id", document.ID, "prefix", prefix, "error", err)
					a.logger.Warn("purge document skipped because object deletion did not complete", "document_id", document.ID)
					continue
				}
			}
			if _, err := a.documents.Purge(ctx, document.ID); err != nil {
				a.logger.Warn("purge document failed", "document_id", document.ID, "error", err)
			} else {
				purgedCount++
				a.logger.Info("document archive removed", "document_id", document.ID)
			}
		}

		if len(deleted) < deletedPurgeBatchSize || purgedCount == 0 {
			return
		}
	}
}

func (a *App) processQueued(ctx context.Context) {
	qb := documents.QueryBuilder{}
	query, err := qb.ByStatus(documents.StatusQueued).Limit(5).Build()
	if err != nil {
		a.logger.Error("build queued documents query failed", "error", err)
		return
	}
	queued, err := a.documents.Query(ctx, query)
	if err != nil {
		a.logger.Error("list queued documents failed", "error", err)
		return
	}

	for _, document := range queued {
		a.logger.Info("processing document archive", "document_id", document.ID, "source", document.Source)
		if _, err := a.processDocument(ctx, document.ID); err != nil {
			a.logger.Warn("process document archive failed", "document_id", document.ID, "error", err)
			continue
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

	err = a.documents.TransitionTo(ctx, id, documents.StatusResolving)
	if err != nil {
		return a.failDocument(ctx, id, err)
	}
	resolved, err := handler.ResolveDocument(ctx, document)
	if err != nil {
		return a.failDocument(ctx, id, err)
	}
	document, err = a.documents.UpdateMeta(ctx, id, func(d *documents.DocumentMeta) error {
		d.SourceMeta = resolved.SourceMeta
		d.Title = resolved.Title
		if resolved.Pages != nil {
			d.Progress.Total = len(resolved.Pages)
		}
		d.Error = ""
		return nil
	})
	if err != nil {
		return a.failDocument(ctx, id, err)
	}

	if document.Progress.Done < document.Progress.Total {
		document, err = a.documents.ResetPages(ctx, id)
		if err != nil {
			return a.failDocument(ctx, id, err)
		}
		if err := a.documents.TransitionTo(ctx, id, documents.StatusDownloading); err != nil {
			return a.failDocument(ctx, id, err)
		}
		_, err = handler.ArchiveContent(ctx, document, objectStorage)
		if err != nil {
			return a.failDocument(ctx, id, err)
		}
	}

	document, err = a.documents.Get(ctx, id)
	if err != nil {
		return a.failDocument(ctx, id, err)
	}
	if err := handler.ArchiveManifest(ctx, document, objectStorage); err != nil {
		return a.failDocument(ctx, id, err)
	}
	if err := a.documents.TransitionTo(ctx, id, documents.StatusArchived); err != nil {
		return a.failDocument(ctx, id, err)
	}
	return a.documents.Get(ctx, id)
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
	document, err := a.documents.UpdateMeta(ctx, id, func(d *documents.DocumentMeta) error {
		d.Error = cause.Error()
		return nil
	})
	if err != nil {
		return document, err
	}
	if err := a.documents.TransitionTo(ctx, id, documents.StatusFailed); err != nil {
		return document, err
	}
	a.logger.Warn("document archive failed", "document_id", document.ID, "error", cause)
	return document, cause
}
