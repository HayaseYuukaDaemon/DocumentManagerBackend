package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"document-archive/internal/config"
)

func TestCORSHandlerAddsHeadersForAllowedOrigin(t *testing.T) {
	cfg := config.Config{AllowCORS: []string{"http://localhost:5173"}}
	nextCalled := false
	handler := corsHandler(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/documents/1", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, req)

	if !nextCalled {
		t.Fatalf("next handler should be called for non-preflight request")
	}
	if response.Code != http.StatusAccepted {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("unexpected allow origin: %q", got)
	}
	if got := response.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("unexpected vary header: %q", got)
	}
}

func TestCORSHandlerHandlesPreflightBeforeAuth(t *testing.T) {
	cfg := config.Config{
		AuthToken: "secret",
		AllowCORS: []string{"http://localhost:5173"},
	}
	nextCalled := false
	handler := corsHandler(cfg, preprocessChainHandler(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusAccepted)
	})))

	req := httptest.NewRequest(http.MethodOptions, "/v1/documents/query", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, req)

	if nextCalled {
		t.Fatalf("next handler should not be called for handled preflight")
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("unexpected allow origin: %q", got)
	}
}

func TestCORSHandlerAddsHeadersToUnauthorizedResponse(t *testing.T) {
	cfg := config.Config{
		AuthToken: "secret",
		AllowCORS: []string{"http://localhost:5173"},
	}
	handler := corsHandler(cfg, preprocessChainHandler(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("next handler should not be called for unauthorized request")
	})))

	req := httptest.NewRequest(http.MethodGet, "/v1/documents/1", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, req)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("unauthorized response should still include CORS header, got %q", got)
	}
}

func TestCORSHandlerSkipsDisallowedOrigin(t *testing.T) {
	cfg := config.Config{AllowCORS: []string{"https://example.com"}}
	handler := corsHandler(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/documents/1", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, req)

	if response.Code != http.StatusAccepted {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed origin should not receive CORS header, got %q", got)
	}
}

func TestCORSHandlerSupportsWildcard(t *testing.T) {
	cfg := config.Config{AllowCORS: []string{"*"}}
	handler := corsHandler(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/documents/1", nil)
	req.Header.Set("Origin", "https://any.example")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, req)

	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("unexpected wildcard allow origin: %q", got)
	}
	if got := response.Header().Get("Vary"); got != "" {
		t.Fatalf("wildcard CORS should not set Vary: Origin, got %q", got)
	}
}

func TestRouterHandlesBusinessPreflightWithoutExplicitOptionsRoutes(t *testing.T) {
	cfg := config.Config{
		AuthToken: "secret",
		AllowCORS: []string{"http://localhost:5173"},
	}
	router := NewRouter(cfg, nil)

	req := httptest.NewRequest(http.MethodOptions, "/v1/documents/query", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, req)

	if response.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("unexpected allow origin: %q", got)
	}
}
