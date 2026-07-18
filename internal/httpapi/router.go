package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

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
	ch := &ChainHandler{cfg: cfg}
	mux.Handle("POST /v1/documents/request", ch.preprocess(http.HandlerFunc(router.requestDocument), RouteConfig{
		RequiredPermissions: []config.Permissions{config.DocumentCreate},
	}))
	mux.Handle("POST /v1/documents/query", ch.preprocess(http.HandlerFunc(router.queryDocument), RouteConfig{
		RequiredPermissions: []config.Permissions{config.DocumentRead},
	}))
	mux.Handle("GET /v1/documents/{document_id}", ch.preprocess(http.HandlerFunc(router.getDocument), RouteConfig{
		RequiredPermissions: []config.Permissions{config.DocumentRead},
	}))
	mux.Handle("DELETE /v1/documents/{document_id}", ch.preprocess(http.HandlerFunc(router.removeDocument), RouteConfig{
		RequiredPermissions: []config.Permissions{config.DocumentDelete},
	}))
	mux.Handle("POST /v1/documents/{document_id}/refresh", ch.preprocess(http.HandlerFunc(router.refreshDocument), RouteConfig{
		RequiredPermissions: []config.Permissions{config.DocumentRefresh},
	}))
	mux.Handle("GET /v1/documents/{document_id}/pages/{page_index}", ch.preprocess(http.HandlerFunc(router.getPage), RouteConfig{
		RequiredPermissions: []config.Permissions{config.DocumentRead},
	}))
	return corsHandler(cfg, mux)
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

func (r *Router) refreshDocument(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	refreshMode := documents.RefreshMode(req.URL.Query().Get("mode"))
	docID, err := strconv.Atoi(req.PathValue("document_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid document ID")
		return
	}
	document, err := r.app.RefreshDocument(req.Context(), docID, refreshMode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, document)
}

func (r *Router) getDocument(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	docID, err := strconv.Atoi(req.PathValue("document_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid document ID")
		return
	}
	document, err := r.app.GetDocument(req.Context(), docID)
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
	defer req.Body.Close()
	docID, err := strconv.Atoi(req.PathValue("document_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid document ID")
		return
	}
	document, err := r.app.RemoveDocument(req.Context(), docID)
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
	defer req.Body.Close()
	documentID := req.PathValue("document_id")
	writeError(w, http.StatusNotImplemented, "manifest api not implemented for document "+documentID)
}

func (r *Router) getPage(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	documentID := req.PathValue("document_id")
	pageIndex, err := strconv.Atoi(req.PathValue("page_index"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid page index")
		return
	}
	docID, err := strconv.Atoi(documentID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid document ID")
		return
	}
	doc, err := r.app.GetDocument(req.Context(), docID)
	if err != nil {
		if errors.Is(err, documents.ErrNotFound) {
			writeError(w, http.StatusNotFound, "document not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pageResult, err := r.app.GetPage(req.Context(), doc, pageIndex)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch pageResult.Kind {
	case archive.PageResultRedirect:
		http.Redirect(w, req, pageResult.RedirectURL, http.StatusFound)
	case archive.PageResultObject:
		defer pageResult.Object.Body.Close()
		w.Header().Set("Content-Type", pageResult.Object.ContentType)
		w.Header().Set("ETag", pageResult.Object.ETag)
		w.Header().Set("Content-Length", strconv.FormatInt(pageResult.Object.Size, 10))
		w.WriteHeader(http.StatusOK)
		io.Copy(w, pageResult.Object.Body)
	}
}
