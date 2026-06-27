package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"document-archive/internal/config"
)

type preprocessFunc func(cfg config.Config, w http.ResponseWriter, r *http.Request) (handled bool)

func preprocessChainHandler(cfg config.Config, next http.Handler) http.Handler {
	preprocessors := []preprocessFunc{
		processAuth,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, process := range preprocessors {
			if handled := process(cfg, w, r); handled {
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

func processAuth(cfg config.Config, w http.ResponseWriter, r *http.Request) bool {
	if cfg.AuthToken == "" {
		return false
	}

	header := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(cfg.AuthToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return true
	}
	return false
}
