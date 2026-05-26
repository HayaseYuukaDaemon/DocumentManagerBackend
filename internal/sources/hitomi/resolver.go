package hitomi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	metadataDomain = "ltn.gold-usergeneratedcontent.net"
	imageDomain    = "gold-usergeneratedcontent.net"
	hitomiReferer  = "https://hitomi.la"
)

var ErrComicNotFound = errors.New("hitomi comic not found")

type Resolver struct {
	client *http.Client
}

func NewResolver(client *http.Client) *Resolver {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Resolver{client: client}
}

type ResolvedComic struct {
	Comic Comic
	Pages []DownloadPage
}

type Comic struct {
	ID                StringID   `json:"id"`
	Title             string     `json:"title"`
	Type              string     `json:"type"`
	Language          string     `json:"language"`
	LanguageLocalName string     `json:"language_localname"`
	Date              string     `json:"date"`
	GalleryURL        string     `json:"galleryurl"`
	Blocked           int        `json:"blocked"`
	Files             []PageInfo `json:"files"`
}

type StringID string

func (id *StringID) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		*id = StringID(asString)
		return nil
	}

	var asNumber json.Number
	if err := json.Unmarshal(data, &asNumber); err != nil {
		return err
	}
	*id = StringID(asNumber.String())
	return nil
}

type PageInfo struct {
	Hash    string `json:"hash"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Name    string `json:"name"`
	HasAVIF int    `json:"hasavif"`
}

type DownloadPage struct {
	Index       int
	Name        string
	URL         string
	Referer     string
	Hash        string
	Width       int
	Height      int
	ContentType string
}

type GGInfo struct {
	Mapping map[int]int
	Base    string
	Default int
}

func (r *Resolver) Resolve(ctx context.Context, galleryID string) (ResolvedComic, error) {
	comic, err := r.FetchComic(ctx, galleryID)
	if err != nil {
		return ResolvedComic{}, err
	}

	gg, err := r.FetchGG(ctx)
	if err != nil {
		return ResolvedComic{}, err
	}

	pages, err := DecodeDownloadPages(comic, gg)
	if err != nil {
		return ResolvedComic{}, err
	}

	return ResolvedComic{
		Comic: comic,
		Pages: pages,
	}, nil
}

func (r *Resolver) FetchComic(ctx context.Context, galleryID string) (Comic, error) {
	galleryID = strings.TrimSpace(galleryID)
	if galleryID == "" {
		return Comic{}, errors.New("gallery id is required")
	}

	raw, err := r.getText(ctx, fmt.Sprintf("https://%s/galleries/%s.js", metadataDomain, galleryID), nil)
	if err != nil {
		return Comic{}, err
	}
	return ParseGalleryInfo(raw)
}

func (r *Resolver) FetchGG(ctx context.Context) (GGInfo, error) {
	url := fmt.Sprintf("https://%s/gg.js?_=%d", metadataDomain, time.Now().UnixMilli())
	raw, err := r.getText(ctx, url, nil)
	if err != nil {
		return GGInfo{}, err
	}
	return ParseGG(raw)
}

func ParseGalleryInfo(raw string) (Comic, error) {
	if !strings.Contains(raw, "galleryinfo") {
		return Comic{}, errors.New("galleryinfo not found")
	}

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return Comic{}, errors.New("galleryinfo json object not found")
	}

	var comic Comic
	if err := json.Unmarshal([]byte(raw[start:end+1]), &comic); err != nil {
		return Comic{}, fmt.Errorf("decode galleryinfo: %w", err)
	}
	if len(comic.Files) == 0 {
		return Comic{}, errors.New("galleryinfo has no files")
	}
	return comic, nil
}

func ParseGG(raw string) (GGInfo, error) {
	info := GGInfo{Mapping: make(map[int]int)}

	caseRe := regexp.MustCompile(`case\s+(\d+):(?:\s*o\s*=\s*(\d+))?`)
	pendingKeys := make([]int, 0)
	for _, match := range caseRe.FindAllStringSubmatch(raw, -1) {
		key, err := strconv.Atoi(match[1])
		if err != nil {
			return GGInfo{}, fmt.Errorf("parse gg case key: %w", err)
		}
		pendingKeys = append(pendingKeys, key)

		if match[2] == "" {
			continue
		}
		value, err := strconv.Atoi(match[2])
		if err != nil {
			return GGInfo{}, fmt.Errorf("parse gg case value: %w", err)
		}
		for _, pendingKey := range pendingKeys {
			info.Mapping[pendingKey] = value
		}
		pendingKeys = pendingKeys[:0]
	}

	ifRe := regexp.MustCompile(`if\s*\(g\s*={2,3}\s*(\d+)\)[\s{]*o\s*=\s*(\d+)`)
	for _, match := range ifRe.FindAllStringSubmatch(raw, -1) {
		key, err := strconv.Atoi(match[1])
		if err != nil {
			return GGInfo{}, fmt.Errorf("parse gg if key: %w", err)
		}
		value, err := strconv.Atoi(match[2])
		if err != nil {
			return GGInfo{}, fmt.Errorf("parse gg if value: %w", err)
		}
		info.Mapping[key] = value
	}

	defaultRe := regexp.MustCompile(`(?:var\s+|default:)\s*o\s*=\s*(\d+)`)
	if match := defaultRe.FindStringSubmatch(raw); match != nil {
		value, err := strconv.Atoi(match[1])
		if err != nil {
			return GGInfo{}, fmt.Errorf("parse gg default: %w", err)
		}
		info.Default = value
	}

	baseRe := regexp.MustCompile(`b:\s*["']([^"']+)["']`)
	match := baseRe.FindStringSubmatch(raw)
	if match == nil {
		return GGInfo{}, errors.New("gg base path not found")
	}
	info.Base = strings.Trim(match[1], "/")

	return info, nil
}

func DecodeDownloadPages(comic Comic, gg GGInfo) ([]DownloadPage, error) {
	pages := make([]DownloadPage, 0, len(comic.Files))
	for index, file := range comic.Files {
		url, err := DownloadURL(file.Hash, gg, "webp")
		if err != nil {
			return nil, fmt.Errorf("decode page %d: %w", index, err)
		}
		pages = append(pages, DownloadPage{
			Index:       index,
			Name:        webpName(file.Name),
			URL:         url,
			Referer:     hitomiReferer + comic.GalleryURL,
			Hash:        file.Hash,
			Width:       file.Width,
			Height:      file.Height,
			ContentType: "image/webp",
		})
	}
	return pages, nil
}

func DownloadURL(hash string, gg GGInfo, ext string) (string, error) {
	hash = strings.TrimSpace(hash)
	ext = strings.TrimPrefix(strings.TrimSpace(ext), ".")
	if ext == "" {
		ext = "webp"
	}
	if len(hash) < 3 {
		return "", errors.New("image hash is too short")
	}
	if gg.Base == "" {
		return "", errors.New("gg base path is empty")
	}

	inumRaw := string(hash[len(hash)-1]) + hash[len(hash)-3:len(hash)-1]
	inum64, err := strconv.ParseInt(inumRaw, 16, 64)
	if err != nil {
		return "", fmt.Errorf("parse image hash shard: %w", err)
	}
	inum := int(inum64)

	shard := gg.Default
	if mapped, ok := gg.Mapping[inum]; ok {
		shard = mapped
	}

	subdomain := fmt.Sprintf("%s%d", string(ext[0]), shard+1)
	return fmt.Sprintf("https://%s.%s/%s/%d/%s.%s", subdomain, imageDomain, gg.Base, inum, hash, ext), nil
}

func webpName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "page.webp"
	}
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		return name[:dot] + ".webp"
	}
	return name + ".webp"
}

func (r *Resolver) getText(ctx context.Context, url string, headers map[string]string) (string, error) {
	body, err := r.getBytes(ctx, url, headers)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (r *Resolver) getBytes(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "document-archive/0.1")
		req.Header.Set("Referer", hitomiReferer+"/")
		for key, value := range headers {
			req.Header.Set(key, value)
		}

		resp, err := r.client.Do(req)
		if err != nil {
			lastErr = err
			sleepBeforeRetry(ctx, attempt)
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			sleepBeforeRetry(ctx, attempt)
			continue
		}
		if closeErr != nil {
			lastErr = closeErr
			sleepBeforeRetry(ctx, attempt)
			continue
		}

		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrComicNotFound
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, nil
		}

		lastErr = fmt.Errorf("GET %s returned %s", url, resp.Status)
		sleepBeforeRetry(ctx, attempt)
	}

	if lastErr == nil {
		lastErr = errors.New("request failed")
	}
	return nil, lastErr
}

func sleepBeforeRetry(ctx context.Context, attempt int) {
	timer := time.NewTimer(time.Duration(attempt+1) * 500 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
