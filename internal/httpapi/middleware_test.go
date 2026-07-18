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
		Roles: map[string]config.Role{
			"a": {
				Name:        "admin",
				Admin:       true,
				Permissions: []config.Permissions{},
			},
		},
		AllowCORS: []string{"http://localhost:5173"},
	}
	nextCalled := false
	ch := &ChainHandler{cfg: cfg}
	handler := corsHandler(cfg, ch.preprocess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusAccepted)
	}), RouteConfig{}))

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
		Roles: map[string]config.Role{
			"secret": {Name: "reader", Permissions: []config.Permissions{config.DocumentRead}},
		},
		AllowCORS: []string{"http://localhost:5173"},
	}
	ch := &ChainHandler{cfg: cfg}
	handler := corsHandler(cfg, ch.preprocess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("next handler should not be called for unauthorized request")
	}), RouteConfig{RequiredPermissions: []config.Permissions{config.DocumentRead}}))

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
		Roles: map[string]config.Role{
			"secret": {Name: "reader", Permissions: []config.Permissions{config.DocumentRead}},
		},
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

func TestProcessAuthAllowsRequestsWhenNoRolesConfigured(t *testing.T) {
	cfg := PreprocessConfig{
		RouteConfig: RouteConfig{RequiredPermissions: []config.Permissions{config.DocumentDelete}},
	}
	request := httptest.NewRequest(http.MethodDelete, "/v1/documents/1", nil)
	response := httptest.NewRecorder()

	if handled := processAuth(cfg, response, request); handled {
		t.Fatalf("authentication should be disabled when no roles are configured")
	}
}

func TestProcessAuthRejectsMissingAndUnknownTokens(t *testing.T) {
	cfg := PreprocessConfig{
		Config: config.Config{Roles: map[string]config.Role{
			"reader-token": {Name: "reader", Permissions: []config.Permissions{config.DocumentRead}},
		}},
		RouteConfig: RouteConfig{RequiredPermissions: []config.Permissions{config.DocumentRead}},
	}

	for _, authorization := range []string{"", "Bearer unknown-token"} {
		t.Run(authorization, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/documents/1", nil)
			if authorization != "" {
				request.Header.Set("Authorization", authorization)
			}
			response := httptest.NewRecorder()

			if handled := processAuth(cfg, response, request); !handled {
				t.Fatalf("invalid credentials should handle the request")
			}
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("unexpected status: %d", response.Code)
			}
		})
	}
}

func TestProcessAuthRejectsRoleWithoutRequiredPermission(t *testing.T) {
	cfg := PreprocessConfig{
		Config: config.Config{Roles: map[string]config.Role{
			"reader-token": {Name: "reader", Permissions: []config.Permissions{config.DocumentRead}},
		}},
		RouteConfig: RouteConfig{RequiredPermissions: []config.Permissions{config.DocumentDelete}},
	}
	request := httptest.NewRequest(http.MethodDelete, "/v1/documents/1", nil)
	request.Header.Set("Authorization", "Bearer reader-token")
	response := httptest.NewRecorder()

	if handled := processAuth(cfg, response, request); !handled {
		t.Fatalf("forbidden credentials should handle the request")
	}
	if response.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: %d", response.Code)
	}
}

func TestProcessAuthAllowsConfiguredPermissionAndAdmin(t *testing.T) {
	tests := map[string]config.Role{
		"reader-token": {Name: "reader", Permissions: []config.Permissions{config.DocumentRead}},
		"admin-token":  {Name: "admin", Admin: true},
	}

	for token, role := range tests {
		t.Run(role.Name, func(t *testing.T) {
			cfg := PreprocessConfig{
				Config: config.Config{Roles: map[string]config.Role{token: role}},
				RouteConfig: RouteConfig{RequiredPermissions: []config.Permissions{
					config.DocumentRead,
				}},
			}
			request := httptest.NewRequest(http.MethodGet, "/v1/documents/1", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()

			if handled := processAuth(cfg, response, request); handled {
				t.Fatalf("authorized credentials should continue to the next handler")
			}
		})
	}
}
