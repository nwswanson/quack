package server

import (
	"log/slog"
	"net/http"
	"time"
)

const (
	DefaultMaxUploadBytes int64 = 512 << 20
	DefaultMaxUploadFiles int64 = 10000
)

type Options struct {
	MaxUploadBytes int64
	MaxUploadFiles int64
}

func DefaultOptions() Options {
	return Options{
		MaxUploadBytes: DefaultMaxUploadBytes,
		MaxUploadFiles: DefaultMaxUploadFiles,
	}
}

func New(addr string, token string, store Storage, db Database, opts Options) *http.Server {
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	h := &handler{
		token:          token,
		store:          store,
		db:             db,
		maxUploadBytes: opts.MaxUploadBytes,
		maxUploadFiles: opts.MaxUploadFiles,
	}
	h.routes(mux)

	return &http.Server{
		Addr:    addr,
		Handler: requestLogger(mux),
	}
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
