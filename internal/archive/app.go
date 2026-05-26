package archive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"document-archive/internal/documents"
	"document-archive/internal/storage"
)

type App struct {
	documents documents.Store
	objects   storage.ObjectStore
	sources   map[SourceName]SourceHandler
	logger    *slog.Logger
}

func NewApp(documentStore documents.Store, objects storage.ObjectStore, logger *slog.Logger) *App {
	return &App{
		documents: documentStore,
		objects:   objects,
		sources:   make(map[SourceName]SourceHandler),
		logger:    logger,
	}
}

func (a *App) RegisterSource(handler SourceHandler) {
	a.sources[normalizeSource(handler.Source())] = handler
}

func (a *App) RequestDocument(ctx context.Context, input documents.RequestDocumentInput) (documents.Document, error) {
	input.Source = strings.TrimSpace(input.Source)
	if input.Source == "" {
		return documents.Document{}, errors.New("source is required")
	}
	if len(input.SourceIdentity) == 0 {
		return documents.Document{}, errors.New("source_identity is required")
	}

	if _, err := a.getSource(input.Source); err != nil {
		return documents.Document{}, err
	}

	document := documents.Document{
		ID:             newDocumentID(),
		Source:         input.Source,
		SourceIdentity: input.SourceIdentity,
		SourceMeta:     input.SourceMeta,
		ArchiveStatus:  documents.StatusQueued,
	}
	return a.documents.Create(ctx, document)
}

func (a *App) GetDocument(ctx context.Context, id string) (documents.Document, error) {
	return a.documents.Get(ctx, id)
}

func (a *App) QueryDocument(ctx context.Context, input documents.QueryInput) ([]documents.Document, error) {
	switch input.Mode {
	case documents.QueryBySourceIdentity:
		var params documents.QueryBySourceIdentityParams
		if err := json.Unmarshal(input.Params, &params); err != nil {
			return nil, fmt.Errorf("decode query params: %w", err)
		}
		document, err := a.documents.GetBySourceIdentity(ctx, params.Source, params.SourceIdentity)
		if err != nil {
			return nil, err
		}
		return []documents.Document{document}, nil
	default:
		return nil, fmt.Errorf("unsupported query mode: %s", input.Mode)
	}
}

func (a *App) RemoveDocument(ctx context.Context, id string) (documents.Document, error) {
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

func (a *App) processDocument(ctx context.Context, document documents.Document) (documents.Document, error) {
	handler, err := a.getSource(document.Source)
	if err != nil {
		return a.failDocument(ctx, document, err)
	}

	document.ArchiveStatus = documents.StatusResolving
	document, err = a.documents.Update(ctx, document)
	if err != nil {
		return document, err
	}

	manifest, err := handler.Archive(ctx, document, a.objects)
	if err != nil {
		return a.failDocument(ctx, document, err)
	}

	document.ArchiveStatus = documents.StatusArchived
	document.Progress.Done = manifest.PageCount
	document.Progress.Total = manifest.PageCount
	document.PageCount = manifest.PageCount
	document.ManifestKey = ManifestObjectKey(document.ID)
	document.Error = ""
	if manifest.Title != "" {
		document.Title = manifest.Title
	}
	return a.documents.Update(ctx, document)
}

func (a *App) getSource(source string) (SourceHandler, error) {
	handler, ok := a.sources[normalizeSource(source)]
	if !ok {
		return nil, fmt.Errorf("source handler not found: %s", source)
	}
	return handler, nil
}

func (a *App) failDocument(ctx context.Context, document documents.Document, cause error) (documents.Document, error) {
	document.ArchiveStatus = documents.StatusFailed
	document.Error = cause.Error()
	updated, err := a.documents.Update(ctx, document)
	if err != nil {
		return document, err
	}
	a.logger.Warn("document archive failed", "document_id", document.ID, "error", cause)
	return updated, cause
}

func normalizeSource(source string) SourceName {
	return SourceName(strings.ToLower(strings.TrimSpace(source)))
}

func newDocumentID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
