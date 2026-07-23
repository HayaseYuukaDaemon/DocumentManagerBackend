package hitomi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"document-archive/internal/archive"
	"document-archive/internal/documents"
	"document-archive/internal/sources"
	"document-archive/internal/storage"
)

const SourceTypeHitomi sources.SourceType = "hitomi"

type Handler struct {
	resolver         *Resolver
	pageDownloadHook func(ctx context.Context, documentID int, page documents.Page) error
}

func NewHandler() *Handler {
	return &Handler{resolver: NewResolver(nil)}
}

func (h *Handler) Source() sources.SourceType {
	return SourceTypeHitomi
}

type CocourrencyScheduler struct {
	sem chan struct{}
}

func (h *Handler) ArchiveManifest(ctx context.Context, document documents.Document, objects storage.ObjectStore) error {
	body, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("marshal document snapshot: %w", err)
	}
	_, err = objects.PutObject(ctx, storage.ObjectInfo{
		Key:         archive.ManifestObjectKey(strconv.Itoa(document.ID)),
		Size:        int64(len(body)),
		ContentType: "application/json",
	}, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("put document snapshot: %w", err)
	}
	return nil
}

func (h *Handler) downloadPage(ctx context.Context, page DownloadPage, objects storage.ObjectStore, document documents.Document) error {
	key, err := archive.PageObjectKey(strconv.Itoa(document.ID), page.Hash, page.ContentType)
	if err != nil {
		return fmt.Errorf("build page %d object key: %w", page.Index, err)
	}

	objectInfo, err := objects.HeadObject(ctx, key)
	if err == nil {
		if objectInfo.ETag != "" && objectInfo.ETag != page.Hash {
			return fmt.Errorf("page %d object hash mismatch: expected %s, got %s", page.Index, page.Hash, objectInfo.ETag)
		}
		docPage := documents.Page{
			Index:       page.Index,
			Key:         key,
			ContentType: page.ContentType,
			Size:        objectInfo.Size,
			Hash:        page.Hash,
		}
		if err := h.recordPage(ctx, document.ID, docPage); err != nil {
			return err
		}
	}
	if !errors.Is(err, storage.ErrObjectNotFound) {
		return fmt.Errorf("head page %d object: %w", page.Index, err)
	}

	buf := bytes.Buffer{}
	err = h.resolver.DownloadPage(ctx, page, &buf)
	if err != nil {
		return fmt.Errorf("failed to download page %d: %w", page.Index, err)
	}

	size := int64(buf.Len())
	objectInfo, err = objects.PutObject(ctx, storage.ObjectInfo{
		Key:         key,
		Size:        size,
		ContentType: page.ContentType,
		ETag:        page.Hash,
	}, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return fmt.Errorf("failed to put object: %w", err)
	}
	docPage := documents.Page{
		Index:       page.Index,
		Key:         key,
		ContentType: page.ContentType,
		Size:        size,
		Hash:        page.Hash,
	}
	if err := h.recordPage(ctx, document.ID, docPage); err != nil {
		return fmt.Errorf("failed to record page: %w", err)
	}
	return nil
}

func (h *Handler) ArchiveContent(ctx context.Context, document documents.Document, objects storage.ObjectStore) ([]documents.Page, error) {
	comic, err := DeserializeGalleryInfo(document.SourceMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize gallery info: %w", err)
	}
	resolvedComic, err := h.resolver.ResolveComic(ctx, comic)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve comic: %w", err)
	}
	archivedPages := make([]documents.Page, 0, len(resolvedComic.Pages))
	for _, page := range resolvedComic.Pages {
		// 这里就用并发了, 别忘了先检查一下downloadPage的并发安全性
		// Hitomi的限流策略知之甚少, 只知道如下特征: 有限流, 但不知道具体是多少, 只知道限流了再请求会503, 解除限流也不知道多长时间, 怎么样才能最大化带宽?
		// 干脆用一个全局的调度器, 就像上边那个 CocourrencyScheduler, 整个handler都用这个调度器, 只要有一个请求被限流了, 所有请求都能接收到调度信号
		// 再变态一点的话还可以起好几个mihomo, 对每个mihomo都用一个调度器, 这样就可以解除单IP限流
		// 那如果有多个调度器的话, 只需要在一开始根据调度器的数量把所有任务平均分片, 然后每个调度器只处理自己分片的任务, 这样就可以最大化带宽了
		if err := h.downloadPage(ctx, page, objects, document); err != nil {
			return nil, fmt.Errorf("failed to download page %d: %w", page.Index, err)
		}
	}
	return archivedPages, nil
}

func (h *Handler) ResolveDocument(ctx context.Context, document documents.Document) (documents.Document, error) {
	comic, err := h.resolver.ResolveID(ctx, document.SourceDocumentID)
	if err != nil {
		return documents.Document{}, err
	}
	pages := make([]documents.Page, len(comic.Pages))
	for index, page := range comic.Pages {
		pages[index] = documents.Page{
			Index:       index,
			ContentType: page.ContentType,
			Hash:        page.Hash,
		}
	}
	return documents.Document{
		Source:           SourceTypeHitomi,
		SourceMeta:       comic.RawJSON,
		SourceDocumentID: string(comic.Comic.ID),
		Title:            comic.Comic.Title,
		Pages:            pages,
	}, nil
}

func (h *Handler) recordPage(ctx context.Context, documentID int, page documents.Page) error {
	if h.pageDownloadHook == nil {
		return nil
	}
	if err := h.pageDownloadHook(ctx, documentID, page); err != nil {
		return fmt.Errorf("failed to execute page download hook: %w", err)
	}
	return nil
}

func (h *Handler) RegisterPageDownloadHook(hook func(ctx context.Context, documentID int, page documents.Page) error) error {
	h.pageDownloadHook = hook
	return nil
}
