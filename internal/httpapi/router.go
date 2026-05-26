package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"document-archive/internal/archive"
	"document-archive/internal/config"
	"document-archive/internal/documents"
)

type Router struct {
	app *archive.App
}

func NewRouter(cfg config.Config, app *archive.App) http.Handler {
	router := &Router{
		app: app,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", router.health)
	mux.Handle("POST /v1/documents/request", withAuth(cfg, http.HandlerFunc(router.requestDocument)))
	mux.Handle("POST /v1/documents/query", withAuth(cfg, http.HandlerFunc(router.queryDocument)))
	mux.Handle("GET /v1/documents/{document_id}", withAuth(cfg, http.HandlerFunc(router.getDocument)))
	mux.Handle("DELETE /v1/documents/{document_id}", withAuth(cfg, http.HandlerFunc(router.removeDocument)))
	mux.Handle("GET /v1/documents/{document_id}/manifest", withAuth(cfg, http.HandlerFunc(router.getManifest)))
	mux.Handle("GET /v1/documents/{document_id}/pages/{page_index}", withAuth(cfg, http.HandlerFunc(router.getPage)))
	return mux
}

func (r *Router) health(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (r *Router) requestDocument(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()

	var input documents.RequestDocumentInput
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	document, err := r.app.RequestDocument(req.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, document)
}

func (r *Router) queryDocument(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()

	var input documents.QueryInput
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	result, err := r.app.QueryDocument(req.Context(), input)
	if err != nil {
		if errors.Is(err, documents.ErrNotFound) {
			writeError(w, http.StatusNotFound, "document not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) getDocument(w http.ResponseWriter, req *http.Request) {
	document, err := r.app.GetDocument(req.Context(), req.PathValue("document_id"))
	if err != nil {
		if errors.Is(err, documents.ErrNotFound) {
			writeError(w, http.StatusNotFound, "document not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, document)
}

func (r *Router) removeDocument(w http.ResponseWriter, req *http.Request) {
	document, err := r.app.RemoveDocument(req.Context(), req.PathValue("document_id"))
	if err != nil {
		if errors.Is(err, documents.ErrNotFound) {
			writeError(w, http.StatusNotFound, "document not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, document)
}

func (r *Router) getManifest(w http.ResponseWriter, req *http.Request) {
	documentID := req.PathValue("document_id")
	writeError(w, http.StatusNotImplemented, "manifest api not implemented for document "+documentID)
}

func (r *Router) getPage(w http.ResponseWriter, req *http.Request) {
	documentID := req.PathValue("document_id")
	pageIndex := req.PathValue("page_index")
	writeError(w, http.StatusNotImplemented, "page api not implemented for document "+documentID+"/"+pageIndex)
}
