package server

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"quack/internal/logbuffer"
)

type httpRequestRecorder interface {
	RecordHTTPRequest(surface string, method string, status int, duration time.Duration)
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

func (w *loggingResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	if w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return hijacker.Hijack()
}

func requestLogger(next http.Handler) http.Handler {
	return requestLoggerWithMetrics(next, "", nil)
}

func requestLoggerWithMetrics(next http.Handler, surface string, metrics httpRequestRecorder) http.Handler {
	return requestLoggerWithMetricsAndLogs(next, surface, metrics, nil)
}

func requestLoggerWithMetricsAndLogs(next http.Handler, surface string, metrics httpRequestRecorder, logs *logbuffer.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w}
		ctx := logbuffer.ContextWithRequestMetadata(r.Context())
		r = r.WithContext(ctx)

		next.ServeHTTP(lrw, r)

		status := lrw.status
		if status == 0 {
			status = http.StatusOK
		}
		duration := time.Since(start)
		if metrics != nil {
			metrics.RecordHTTPRequest(surface, r.Method, status, duration)
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
			slog.Duration("duration", duration),
		)
		if logs != nil {
			site, version, route := logbuffer.RequestSite(r.Context())
			logs.Add(logbuffer.Event{
				Level:   level.String(),
				Source:  "access",
				Site:    site,
				Version: version,
				Route:   route,
				Message: "http request",
				Attributes: logbuffer.Attrs(
					slog.String("surface", surface),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("host", r.Host),
					slog.String("remote_addr", r.RemoteAddr),
					slog.Int("status", status),
					slog.Int64("bytes", lrw.bytes),
					slog.Duration("duration", duration),
				),
			})
		}
	})
}
