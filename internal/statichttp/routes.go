package statichttp

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"quack/internal/domain"
	"quack/internal/protocol"
	appsettings "quack/internal/settings"
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
		h.applyCacheHeaders(w, r)
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

	h.applyCacheHeaders(w, r)
	if etag := fileETag(file); etag != "" {
		w.Header().Set("ETag", etag)
	}
	http.ServeContent(w, r, relativePath, time.Time{}, blob)
}

func (h handler) applyCacheHeaders(w http.ResponseWriter, r *http.Request) {
	settings, err := h.read.ServerSettings(r.Context())
	if err != nil {
		slog.WarnContext(r.Context(), "load static cache settings failed", "error", err)
		settings = defaultCacheSettings()
	}
	applyCacheHeaders(w.Header(), settings)
}

func applyCacheHeaders(header http.Header, settings domain.ServerSettings) {
	mode := appsettings.ParseHTTPCacheMode(settings.HTTPCacheMode)
	if mode == "" {
		mode = appsettings.ParseHTTPCacheMode(appsettings.Default(appsettings.SettingHTTPCacheMode))
	}
	maxAge := settings.HTTPCacheMaxAgeSeconds
	if maxAge <= 0 {
		maxAge = appsettings.DefaultHTTPCacheMaxAgeSeconds
	}

	switch mode {
	case "anti_cache":
		header.Set("Cache-Control", "no-store, no-cache, max-age=0, must-revalidate")
		header.Set("CDN-Cache-Control", "no-store")
		header.Set("Cloudflare-CDN-Cache-Control", "no-store")
		header.Set("Pragma", "no-cache")
		header.Set("Expires", "0")
	case "max_age":
		value := fmt.Sprintf("public, max-age=%d", maxAge)
		header.Set("Cache-Control", value)
		header.Set("CDN-Cache-Control", value)
		header.Set("Cloudflare-CDN-Cache-Control", value)
	default:
		header.Set("Cache-Control", "public, no-cache, must-revalidate")
		header.Set("CDN-Cache-Control", "no-cache")
		header.Set("Cloudflare-CDN-Cache-Control", "no-cache")
	}
}

func defaultCacheSettings() domain.ServerSettings {
	maxAge, _ := strconv.ParseInt(appsettings.Default(appsettings.SettingHTTPCacheMaxAgeSeconds), 10, 64)
	return domain.ServerSettings{
		HTTPCacheMode:          appsettings.Default(appsettings.SettingHTTPCacheMode),
		HTTPCacheMaxAgeSeconds: maxAge,
	}
}

func fileETag(file domain.UploadFileRecord) string {
	sha := strings.TrimSpace(file.FileSHA)
	if sha == "" {
		return ""
	}
	return `"` + strings.ReplaceAll(sha, `"`, `%22`) + `"`
}
