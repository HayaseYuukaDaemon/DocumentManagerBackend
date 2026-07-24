package hitomi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maruel/natural"
)

const (
	metadataDomain = "ltn.gold-usergeneratedcontent.net"
	imageDomain    = "gold-usergeneratedcontent.net"
	hitomiReferer  = "https://hitomi.la"
)

var ErrComicNotFound = errors.New("hitomi comic not found")
var ErrHitomiRateLimited = errors.New("hitomi rate limited")

type HitomiRateLimitError struct {
	URL        string
	StatusCode int
	RetryAfter time.Duration
}

func (e *HitomiRateLimitError) Error() string {
	return fmt.Sprintf("GET %s returned %d: %v", e.URL, e.StatusCode, ErrHitomiRateLimited)
}

func (e *HitomiRateLimitError) Unwrap() error {
	return ErrHitomiRateLimited
}

type Resolver struct {
	client *http.Client
}

func NewResolver(client *http.Client) *Resolver {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Resolver{client: client}
}

type Comic struct {
	Comic   rawComic
	RawJSON json.RawMessage
	Pages   []DownloadPage
}

type rawComic struct {
	ID         StringID
	Title      string
	GalleryURL string
	Files      []PageInfo
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
	Hash    string
	Width   int
	Height  int
	Name    string
	HasAVIF int
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

func (r *Resolver) DownloadPage(ctx context.Context, page DownloadPage, w io.Writer) error {
	var lastErr error
	for attempt := range 5 {
		if err := ctx.Err(); err != nil {
			lastErr = err
			sleepBeforeRetry(ctx, attempt)
			continue
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, page.URL, nil)
		if err != nil {
			lastErr = err
			sleepBeforeRetry(ctx, attempt)
			continue
		}
		req.Header.Set("User-Agent", "document-archive/0.1")
		if page.Referer != "" {
			req.Header.Set("Referer", page.Referer)
		} else {
			req.Header.Set("Referer", hitomiReferer+"/")
		}

		resp, err := r.client.Do(req)
		if err != nil {
			lastErr = err
			sleepBeforeRetry(ctx, attempt)
			continue
		}

		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return fmt.Errorf("page not found: %s", page.URL)
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
			resp.Body.Close()
			return &HitomiRateLimitError{
				URL:        page.URL,
				StatusCode: resp.StatusCode,
				RetryAfter: retryAfter,
			}
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer resp.Body.Close()
			_, err = io.Copy(w, resp.Body)
			if err != nil {
				lastErr = err
				sleepBeforeRetry(ctx, attempt)
				continue
			}
			return nil
		}

		lastErr = fmt.Errorf("GET %s returned %s", page.URL, resp.Status)
		resp.Body.Close()
		sleepBeforeRetry(ctx, attempt)
	}

	if lastErr == nil {
		lastErr = errors.New("request failed")
	}
	return lastErr
}

func (r *Resolver) ResolveComic(ctx context.Context, comic Comic) (Comic, error) {
	gg, err := r.FetchGG(ctx)
	if err != nil {
		return Comic{}, err
	}

	pages, err := DecodeDownloadPages(comic.Comic, gg)
	if err != nil {
		return Comic{}, err
	}
	slices.SortFunc(pages, func(page1, page2 DownloadPage) int {
		return natural.Compare(page1.Name, page2.Name)
	})
	comic.Pages = pages
	return comic, nil
}

func (r *Resolver) ResolveID(ctx context.Context, galleryID string) (Comic, error) {
	comic, err := r.fetchComic(ctx, galleryID)
	if err != nil {
		return Comic{}, err
	}
	return r.ResolveComic(ctx, comic)
}

func (r *Resolver) fetchComic(ctx context.Context, galleryID string) (Comic, error) {
	galleryID = strings.TrimSpace(galleryID)
	if galleryID == "" {
		return Comic{}, errors.New("gallery id is required")
	}

	raw, err := r.getText(ctx, fmt.Sprintf("https://%s/galleries/%s.js", metadataDomain, galleryID), nil)
	if err != nil {
		return Comic{}, err
	}
	rawJSON, err := parseGalleryInfo(raw)
	if err != nil {
		return Comic{}, err
	}
	return DeserializeGalleryInfo(rawJSON)
}

func (r *Resolver) FetchGG(ctx context.Context) (GGInfo, error) {
	url := fmt.Sprintf("https://%s/gg.js?_=%d", metadataDomain, time.Now().UnixMilli())
	raw, err := r.getText(ctx, url, nil)
	if err != nil {
		return GGInfo{}, err
	}
	return ParseGG(raw)
}

func DeserializeGalleryInfo(raw json.RawMessage) (Comic, error) {
	comic := Comic{}
	rC := rawComic{}
	if err := json.Unmarshal(raw, &rC); err != nil {
		return Comic{}, fmt.Errorf("decode galleryinfo: %w", err)
	}
	comic.RawJSON = raw
	comic.Comic = rC
	if len(comic.Comic.Files) == 0 {
		return Comic{}, errors.New("galleryinfo has no files")
	}
	return comic, nil
}

func parseGalleryInfo(raw string) (json.RawMessage, error) {
	if !strings.Contains(raw, "galleryinfo") {
		return json.RawMessage{}, errors.New("galleryinfo not found")
	}

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return json.RawMessage{}, errors.New("galleryinfo json object not found")
	}
	return json.RawMessage(raw[start : end+1]), nil
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

func DecodeDownloadPages(comic rawComic, gg GGInfo) ([]DownloadPage, error) {
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

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}
