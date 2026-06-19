package sitehttp

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"quack/internal/domain"
	"quack/internal/protocol"
	"quack/internal/sites"
	appstorage "quack/internal/storage"
)

type Handler struct {
	store appstorage.Storage
	read  sites.SiteReadService
}

func New(store appstorage.Storage, read sites.SiteReadService) Handler {
	return Handler{store: store, read: read}
}

func (h Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handleServeFile)
}

func (h Handler) handleServeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	site := sites.NameFromHost(r.Host)
	if site == "" {
		http.NotFound(w, r)
		return
	}

	h.serveSiteFile(w, r, site, r.URL.Path, "")
}

func (h Handler) serveSiteFile(w http.ResponseWriter, r *http.Request, site string, urlPath string, redirectPrefix string) {
	decision, err := h.read.ServeSiteFile(r.Context(), site, urlPath)
	if err != nil {
		slog.ErrorContext(r.Context(), "resolve site file failed", "site", site, "path", urlPath, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	switch decision.Status {
	case sites.ServeSiteFileSuspended:
		protocol.WriteError(w, http.StatusForbidden, "site suspended by administrator policy")
	case sites.ServeSiteFileFound:
		h.serveBlob(w, r, decision.Site, decision.RelativePath, decision.File)
	case sites.ServeSiteFileDirectoryRedirect:
		http.Redirect(w, r, sites.DirectoryRedirectPath(r, redirectPrefix, urlPath), http.StatusMovedPermanently)
	case sites.ServeSiteFileEmptyIndex:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
	case sites.ServeSiteFileNotFound:
		http.NotFound(w, r)
	default:
		slog.ErrorContext(r.Context(), "unknown site file decision", "site", site, "path", urlPath, "status", decision.Status)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}

func (h Handler) serveBlob(w http.ResponseWriter, r *http.Request, site string, relativePath string, file domain.UploadFileRecord) {
	blob, err := h.store.OpenBlob(r.Context(), file.BlobPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.WarnContext(r.Context(), "blob missing for current file", "site", site, "path", relativePath, "blob_path", file.BlobPath)
			http.NotFound(w, r)
			return
		}
		slog.ErrorContext(r.Context(), "open blob failed", "site", site, "path", relativePath, "blob_path", file.BlobPath, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer blob.Close()

	http.ServeContent(w, r, relativePath, time.Time{}, blob)
}
