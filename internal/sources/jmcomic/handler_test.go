package jmcomic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"document-archive/internal/documents"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestResolveDocumentRejectsMultiChapterAlbumBeforeFetchingPhoto(t *testing.T) {
	requests := 0
	client := &ApiClient{
		http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			if request.URL.Path != "/album" {
				t.Fatalf("unexpected request after multi-chapter album: %s", request.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"code": 200,
					"data": {
						"id": "1430149",
						"name": "multi-chapter",
						"series": [{"id": "1430150", "sort": "1", "name": "chapter 1"}]
					}
				}`)),
			}, nil
		})},
		domains: []string{"api.test"},
	}
	handler := &Handler{client: client}

	_, err := handler.ResolveDocument(context.Background(), documents.Document{SourceDocumentID: "1430149"})
	if !errors.Is(err, ErrMultiChapterAlbum) {
		t.Fatalf("expected ErrMultiChapterAlbum, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected only the album request, got %d requests", requests)
	}
}

func TestResolveDocumentStoresRawPhotoMetadata(t *testing.T) {
	client := &ApiClient{
		http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			var body string
			switch request.URL.Path {
			case "/album":
				body = `{"code":200,"data":{"id":"42","name":"single","series":[]}}`
			case "/chapter":
				body = `{"code":200,"data":{"id":"42","name":"photo","addtime":"123","images":["00001.webp","00002.png"]}}`
			case "/chapter_view_template":
				body = `var scramble_id = 99`
			default:
				t.Fatalf("unexpected request: %s", request.URL.Path)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
		domains: []string{"api.test"},
	}
	handler := &Handler{client: client}

	document, err := handler.ResolveDocument(context.Background(), documents.Document{SourceDocumentID: "42"})
	if err != nil {
		t.Fatalf("ResolveDocument returned error: %v", err)
	}
	photo := Photo{}
	if err := json.Unmarshal(document.SourceMeta, &photo); err != nil {
		t.Fatalf("decode source meta: %v", err)
	}
	if string(photo.Raw) != string(document.SourceMeta) {
		t.Fatalf("source meta is not the original photo JSON: %s", document.SourceMeta)
	}
	if len(document.Pages) != 2 || document.Pages[0].ContentType != "image/webp" || document.Pages[1].ContentType != "image/png" {
		t.Fatalf("unexpected pages: %#v", document.Pages)
	}
}
