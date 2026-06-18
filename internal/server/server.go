package server

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultMaxUploadBytes int64 = 512 << 20
	DefaultMaxUploadFiles int64 = 10000
)

type Options struct {
	AdminHost            string
	AllowUnauthenticated bool
}

func DefaultOptions() Options {
	return Options{}
}

func New(addr string, token string, store Storage, db Database, opts Options) *http.Server {
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	source := NewPassthroughHotDataReader(db)
	// hot := NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{})
	hot := NewOtterHotDataReader(source, OtterHotDataReaderOptions{})
	read := NewSiteReadService(hot)
	write := NewSiteWriteService(db, hot, hot)
	h := &handler{
		token:                token,
		store:                store,
		db:                   db,
		read:                 read,
		write:                write,
		allowUnauthenticated: opts.AllowUnauthenticated,
	}
	h.adminRoutes(mux)

	siteMux := http.NewServeMux()
	h.siteRoutes(siteMux)

	router := adminHostRouter{
		adminHost: normalizeAdminHost(opts.AdminHost),
		admin:     mux,
		site:      siteMux,
	}

	return &http.Server{
		Addr:    addr,
		Handler: requestLogger(router),
	}
}

type adminHostRouter struct {
	adminHost string
	admin     http.Handler
	site      http.Handler
}

func (r adminHostRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if isAdminPath(req.URL.Path) {
		if r.adminHost != "" && !r.isAdminHost(req.Host) {
			http.NotFound(w, req)
			return
		}
		r.admin.ServeHTTP(w, req)
		return
	}
	if r.isAdminHost(req.Host) {
		r.admin.ServeHTTP(w, req)
		return
	}
	r.site.ServeHTTP(w, req)
}

func (r adminHostRouter) isAdminHost(host string) bool {
	return r.adminHost != "" && normalizeAdminHost(host) == r.adminHost
}

func isAdminPath(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/")
}

func normalizeAdminHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
			value = parsed.Host
		}
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(value, ".")
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w}

		next.ServeHTTP(lrw, r)

		status := lrw.status
		if status == 0 {
			status = http.StatusOK
		}

		level := slog.LevelInfo
		switch {
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
		case status >= http.StatusBadRequest && status != http.StatusNotFound:
			level = slog.LevelWarn
		}

		slog.LogAttrs(r.Context(), level, "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("host", r.Host),
			slog.String("remote_addr", r.RemoteAddr),
			slog.Int("status", status),
			slog.Int64("bytes", lrw.bytes),
			slog.Duration("duration", time.Since(start)),
		)
	})
}
