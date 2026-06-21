package statichttp

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"quack/internal/domain"
	"quack/internal/protocol"
	"quack/internal/sites"
	appstorage "quack/internal/storage"
)

type Request struct {
	Site       string
	URLPath    string
	RoutePath  string
	StaticRoot string
	StaticFile string
}

type Handler interface {
	ServeSiteFile(w http.ResponseWriter, r *http.Request, req Request)
}

type handler struct {
	store appstorage.Storage
	read  sites.SiteReadService
}

func New(store appstorage.Storage, read sites.SiteReadService) Handler {
	return handler{store: store, read: read}
}

func (h handler) ServeSiteFile(w http.ResponseWriter, r *http.Request, req Request) {
	decision, err := h.read.ServeSiteFile(r.Context(), req.Site, req.URLPath, req.RoutePath, req.StaticRoot, req.StaticFile)
	if err != nil {
		slog.ErrorContext(r.Context(), "resolve site file failed", "site", req.Site, "path", req.URLPath, "error", err)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	switch decision.Status {
	case sites.ServeSiteFileSuspended:
		protocol.WriteError(w, http.StatusForbidden, "site suspended by administrator policy")
	case sites.ServeSiteFileFound:
		h.serveBlob(w, r, decision.Site, decision.RelativePath, decision.File)
	case sites.ServeSiteFileDirectoryRedirect:
		http.Redirect(w, r, sites.DirectoryRedirectPath(r, "", req.URLPath), http.StatusMovedPermanently)
	case sites.ServeSiteFileEmptyIndex:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
	case sites.ServeSiteFileNotFound:
		http.NotFound(w, r)
	default:
		slog.ErrorContext(r.Context(), "unknown site file decision", "site", req.Site, "path", req.URLPath, "status", decision.Status)
		protocol.WriteError(w, http.StatusInternalServerError, "internal server error")
	}
}

func (h handler) serveBlob(w http.ResponseWriter, r *http.Request, site string, relativePath string, file domain.UploadFileRecord) {
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

	w.Header().Set("Cache-Control", "no-cache")
	if etag := fileETag(file); etag != "" {
		w.Header().Set("ETag", etag)
	}
	http.ServeContent(w, r, relativePath, time.Time{}, blob)
}

func fileETag(file domain.UploadFileRecord) string {
	sha := strings.TrimSpace(file.FileSHA)
	if sha == "" {
		return ""
	}
	return `"` + strings.ReplaceAll(sha, `"`, `%22`) + `"`
}
