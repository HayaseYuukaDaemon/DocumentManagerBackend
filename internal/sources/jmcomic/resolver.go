package jmcomic

import (
	"bytes"
	"crypto/aes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	nativewebp "github.com/HugoSmits86/nativewebp"
)

const (
	appVersion     = "2.0.26"
	tokenSecret    = "185Hcomic3PAPP7R"
	imageSecret    = "185Hcomic3PAPP7R"
	scrambleSecret = "18comicAPPContent"

	mobileUA  = "Mozilla/5.0 (Linux; Android 9; V1938CT Build/PQ3A.190705.11211812; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/91.0.4472.114 Safari/537.36"
	desktopUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

var (
	fallbackAPIDomains = []string{
		"www.cdnhjk.net",
		"www.cdngwc.cc",
		"www.cdngwc.net",
		"www.cdngwc.club",
		"www.cdnutc.me",
	}
	imageDomains = []string{
		"cdn-msp.jmapiproxy1.cc",
		"cdn-msp.jmapiproxy2.cc",
		"cdn-msp2.jmapiproxy2.cc",
		"cdn-msp3.jmapiproxy2.cc",
		"cdn-msp.jmapinodeudzn.net",
		"cdn-msp3.jmapinodeudzn.net",
	}
	scramblePattern = regexp.MustCompile(`var\s+scramble_id\s*=\s*(\d+)`)
)

type ApiClient struct {
	http    *http.Client
	domains []string
}

type apiEnvelope struct {
	Code     int             `json:"code"`
	Data     json.RawMessage `json:"data"`
	ErrorMsg string          `json:"errorMsg"`
}

type Album struct {
	ID       flexString      `json:"id"`
	Name     string          `json:"name"`
	Authors  []string        `json:"author"`
	Tags     []string        `json:"tags"`
	Episodes []Episode       `json:"series"`
	Raw      json.RawMessage `json:"-"`
}

type Episode struct {
	PhotoID flexString `json:"id"`
	Index   flexString `json:"sort"`
	Title   string     `json:"name"`
}

type Photo struct {
	ID         flexString `json:"id"`
	Name       string     `json:"name"`
	SeriesID   flexString `json:"series_id"`
	ScrambleID int
	AddTime    flexString      `json:"addtime"`
	Images     []string        `json:"images"`
	Raw        json.RawMessage `json:"-"`
}

func (album *Album) UnmarshalJSON(data []byte) error {
	type albumJSON Album
	decoded := albumJSON{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	decoded.Raw = append(json.RawMessage(nil), data...)
	*album = Album(decoded)
	return nil
}

func (photo *Photo) UnmarshalJSON(data []byte) error {
	type photoJSON Photo
	decoded := photoJSON{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	decoded.Raw = append(json.RawMessage(nil), data...)
	*photo = Photo(decoded)
	return nil
}

// flexString 兼容 API 中同一 ID 有时是数字、有时是字符串的情况。
type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	if len(data) != 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		*s = flexString(text)
		return nil
	}
	*s = flexString(strings.TrimSpace(string(data)))
	return nil
}

func NewAPIClient(timeout time.Duration) (*ApiClient, error) {
	c := &ApiClient{
		http: &http.Client{
			Timeout: timeout,
		},
		domains: append([]string(nil), fallbackAPIDomains...),
	}
	return c, nil
}

func (c *ApiClient) GetAlbum(id string) (*Album, error) {
	data, err := c.requestAPI("/album", map[string]string{"id": id})
	if err != nil {
		return nil, err
	}
	var album Album
	if err := json.Unmarshal(data, &album); err != nil {
		return nil, err
	}
	if album.ID == "" {
		album.ID = flexString(id)
	}
	return &album, nil
}

func (c *ApiClient) GetPhoto(id string) (*Photo, error) {
	data, err := c.requestAPI("/chapter", map[string]string{"id": id})
	if err != nil {
		return nil, err
	}
	var photo Photo
	if err := json.Unmarshal(data, &photo); err != nil {
		return nil, err
	}
	if photo.ID == "" {
		photo.ID = flexString(id)
	}
	photo.ScrambleID, err = c.getScrambleID(string(photo.ID))
	if err != nil {
		return nil, err
	}
	return &photo, nil
}

func (c *ApiClient) requestAPI(apiPath string, query map[string]string) ([]byte, error) {
	var lastErr error
	for _, domain := range c.domains {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		u := url.URL{Scheme: "https", Host: domain, Path: apiPath}
		q := u.Query()
		for key, value := range query {
			q.Set(key, value)
		}
		u.RawQuery = q.Encode()

		req, err := http.NewRequest(http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		token, tokenParam := makeToken(ts, tokenSecret)
		req.Header.Set("user-agent", mobileUA)
		req.Header.Set("token", token)
		req.Header.Set("tokenparam", tokenParam)

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil || resp.StatusCode >= 400 {
			lastErr = fmt.Errorf("%s%s: http=%d read=%v", domain, apiPath, resp.StatusCode, readErr)
			continue
		}

		var envelope apiEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			lastErr = err
			continue
		}
		if envelope.Code != 200 {
			lastErr = fmt.Errorf("api code=%d: %s", envelope.Code, envelope.ErrorMsg)
			continue
		}
		decoded, err := decodeEnvelope(envelope.Data, ts)
		if err != nil {
			lastErr = err
			continue
		}
		return decoded, nil
	}
	return nil, fmt.Errorf("all API domains failed: %w", lastErr)
}

func (c *ApiClient) getScrambleID(photoID string) (int, error) {
	var lastErr error
	for _, domain := range c.domains {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		u := url.URL{Scheme: "https", Host: domain, Path: "/chapter_view_template"}
		q := u.Query()
		q.Set("id", photoID)
		q.Set("mode", "vertical")
		q.Set("page", "0")
		q.Set("app_img_shunt", "1")
		q.Set("express", "off")
		q.Set("v", ts)
		u.RawQuery = q.Encode()

		req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
		token, tokenParam := makeToken(ts, scrambleSecret)
		req.Header.Set("user-agent", mobileUA)
		req.Header.Set("token", token)
		req.Header.Set("tokenparam", tokenParam)
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil || resp.StatusCode >= 400 {
			lastErr = fmt.Errorf("scramble %s: http=%d read=%v", domain, resp.StatusCode, readErr)
			continue
		}
		match := scramblePattern.FindSubmatch(body)
		if len(match) < 2 {
			lastErr = fmt.Errorf("scramble_id not found from %s", domain)
			continue
		}
		return strconv.Atoi(string(match[1]))
	}
	return 0, lastErr
}

func (c *ApiClient) DownloadDecodedImage(photo *Photo, imageName string) (image.Image, error) {
	var lastErr error
	for _, domain := range imageDomains {
		u := url.URL{
			Scheme: "https",
			Host:   domain,
			Path:   path.Join("/media/photos", string(photo.ID), imageName),
		}
		// addtime 对同一章节稳定，避免每次使用当前时间戳主动击穿 CDN 缓存。
		if photo.AddTime != "" {
			q := u.Query()
			q.Set("v", string(photo.AddTime))
			u.RawQuery = q.Encode()
		}

		req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
		req.Header.Set("user-agent", desktopUA)
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil || resp.StatusCode >= 400 {
			lastErr = fmt.Errorf("image CDN %s: http=%d read=%v", domain, resp.StatusCode, readErr)
			continue
		}

		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			lastErr = err
			continue
		}
		aid, _ := strconv.Atoi(string(photo.ID))
		filename := strings.TrimSuffix(imageName, filepath.Ext(imageName))
		segments := segmentationNum(photo.ScrambleID, aid, filename)
		return rearrangeImage(img, segments), nil
	}
	return nil, lastErr
}

func makeToken(ts, secret string) (string, string) {
	return md5Hex(ts + secret), ts + "," + appVersion
}

func decodeEnvelope(raw json.RawMessage, ts string) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []byte("{}"), nil
	}
	var encrypted string
	if err := json.Unmarshal(raw, &encrypted); err == nil {
		return decryptPayload(encrypted, ts, imageSecret)
	}
	return raw, nil
}

func decryptPayload(data, ts, secret string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher([]byte(md5Hex(ts + secret)))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid encrypted payload length: %d", len(raw))
	}
	out := make([]byte, len(raw))
	for offset := 0; offset < len(raw); offset += aes.BlockSize {
		block.Decrypt(out[offset:offset+aes.BlockSize], raw[offset:offset+aes.BlockSize])
	}
	padding := int(out[len(out)-1])
	if padding <= 0 || padding > aes.BlockSize || padding > len(out) {
		return nil, fmt.Errorf("invalid PKCS#7 padding")
	}
	return out[:len(out)-padding], nil
}

func segmentationNum(scrambleID, aid int, filename string) int {
	if aid < scrambleID {
		return 0
	}
	if aid < 268850 {
		return 10
	}
	x := 10
	if aid >= 421926 {
		x = 8
	}
	hash := md5.Sum([]byte(strconv.Itoa(aid) + filename))
	hexDigest := hex.EncodeToString(hash[:])
	return (int(hexDigest[len(hexDigest)-1])%x)*2 + 2
}

func rearrangeImage(src image.Image, segments int) image.Image {
	if segments <= 0 {
		return src
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	over := height % segments
	piece := height / segments
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < segments; i++ {
		move := piece
		sourceY := height - piece*(i+1) - over
		destinationY := piece * i
		if i == 0 {
			move += over
		} else {
			destinationY += over
		}
		sourceRect := image.Rect(0, sourceY, width, sourceY+move)
		destinationRect := image.Rect(0, destinationY, width, destinationY+move)
		draw.Draw(dst, destinationRect, src, sourceRect.Min, draw.Src)
	}
	return dst
}

func SaveImage(filename string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	return encodeImage(file, filename, img)
}

func imageContentType(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".webp":
		return "image/webp"
	case ".png":
		return "image/png"
	default:
		return "image/jpeg"
	}
}

func encodeImage(w io.Writer, filename string, img image.Image) error {
	switch imageContentType(filename) {
	case "image/webp":
		return nativewebp.Encode(w, img, &nativewebp.Options{CompressionLevel: nativewebp.BestSpeed})
	case "image/png":
		return png.Encode(w, img)
	default:
		return jpeg.Encode(w, img, &jpeg.Options{Quality: 95})
	}
}

func md5Hex(value string) string {
	hash := md5.Sum([]byte(value))
	return hex.EncodeToString(hash[:])
}
