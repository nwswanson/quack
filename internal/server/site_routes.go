package server

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"quack/internal/protocol"
)

func (h *handler) siteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handleServeFile)
}

func (h *handler) handleServeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		protocol.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	site := siteFromHost(r.Host)
	if site == "" {
		http.NotFound(w, r)
		return
	}

	h.serveSiteFile(w, r, site, r.URL.Path, "")
}

func (h *handler) handleServeDisabled(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

func (h *handler) serveSiteFile(w http.ResponseWriter, r *http.Request, site string, urlPath string, redirectPrefix string) {
	decision, err := h.read.ServeSiteFile(r.Context(), site, urlPath)
	if err != nil {
		slog.ErrorContext(r.Context(), "resolve site file failed", "site", site, "path", urlPath, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	switch decision.Status {
	case ServeSiteFileSuspended:
		protocol.WriteError(w, http.StatusForbidden, "site suspended by administrator policy")
	case ServeSiteFileFound:
		h.serveBlob(w, r, decision.Site, decision.RelativePath, decision.File)
	case ServeSiteFileDirectoryRedirect:
		http.Redirect(w, r, directoryRedirectPath(r, redirectPrefix, urlPath), http.StatusMovedPermanently)
	case ServeSiteFileEmptyIndex:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
	case ServeSiteFileNotFound:
		http.NotFound(w, r)
	default:
		slog.ErrorContext(r.Context(), "unknown site file decision", "site", site, "path", urlPath, "status", decision.Status)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}

func (h *handler) serveBlob(w http.ResponseWriter, r *http.Request, site string, relativePath string, file UploadFileRecord) {
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
