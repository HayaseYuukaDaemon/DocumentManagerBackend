package httpapi

import (
	"net/http"
	"strings"

	"document-archive/internal/config"
)

type PreprocessConfig struct {
	Config      config.Config
	RouteConfig RouteConfig
}

type preprocessFunc func(cfg PreprocessConfig, w http.ResponseWriter, r *http.Request) (handled bool)

type ChainHandler struct {
	cfg config.Config
}

type RouteConfig struct {
	RequiredPermissions []config.Permissions
}

func (ch *ChainHandler) preprocess(next http.Handler, routeConfig RouteConfig) http.Handler {
	preprocessors := []preprocessFunc{
		processAuth,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, process := range preprocessors {
			if handled := process(PreprocessConfig{
				Config:      ch.cfg,
				RouteConfig: routeConfig,
			}, w, r); handled {
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func corsHandler(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handled := processCORS(cfg, w, r); handled {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func processCORS(cfg config.Config, w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || len(cfg.AllowCORS) == 0 {
		return false
	}

	allowOrigin, ok := matchCORSOrigin(cfg.AllowCORS, origin)
	if !ok {
		return false
	}

	w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
	if allowOrigin != "*" {
		w.Header().Add("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Max-Age", "86400")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

func matchCORSOrigin(allowList []string, origin string) (string, bool) {
	for _, allowed := range allowList {
		allowed = strings.TrimSpace(allowed)
		switch allowed {
		case "":
			continue
		case "*":
			return "*", true
		case origin:
			return origin, true
		}
	}
	return "", false
}

func processAuth(cfg PreprocessConfig, w http.ResponseWriter, r *http.Request) bool {
	if len(cfg.Config.Roles) == 0 {
		return false
	}

	header := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return true
	}
	role, ok := cfg.Config.Roles[token]
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return true
	}

	for _, required := range cfg.RouteConfig.RequiredPermissions {
		if !role.HasPermission(required) {
			writeError(w, http.StatusForbidden, "forbidden")
			return true
		}
	}
	return false
}
