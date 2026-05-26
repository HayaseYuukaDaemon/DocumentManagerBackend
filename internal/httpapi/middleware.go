package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"document-archive/internal/config"
)

func withAuth(cfg config.Config, next http.Handler) http.Handler {
	if cfg.AuthToken == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(cfg.AuthToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}
