package server

import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"quack/internal/logbuffer"
)

func TestLoggingResponseWriterPreservesHijacker(t *testing.T) {
	inner := &hijackableResponseWriter{}
	lrw := &loggingResponseWriter{ResponseWriter: inner}

	if _, ok := any(lrw).(http.Hijacker); !ok {
		t.Fatal("loggingResponseWriter does not implement http.Hijacker")
	}
	conn, _, err := lrw.Hijack()
	if err != nil {
		t.Fatalf("Hijack error = %v", err)
	}
	defer conn.Close()
	if !inner.hijacked {
		t.Fatal("underlying response writer was not hijacked")
	}
	if lrw.status != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want %d", lrw.status, http.StatusSwitchingProtocols)
	}
}

func TestLoggingResponseWriterPreservesFlusher(t *testing.T) {
	inner := &flushableResponseWriter{}
	lrw := &loggingResponseWriter{ResponseWriter: inner}

	flusher, ok := any(lrw).(http.Flusher)
	if !ok {
		t.Fatal("loggingResponseWriter does not implement http.Flusher")
	}
	flusher.Flush()
	if !inner.flushed {
		t.Fatal("underlying response writer was not flushed")
	}
	if lrw.status != http.StatusOK {
		t.Fatalf("status = %d, want %d", lrw.status, http.StatusOK)
	}
}

func TestRequestLoggerWritesRouteMetadataToStdoutLog(t *testing.T) {
	var level slog.LevelVar
	level.Set(slog.LevelDebug)
	var out bytes.Buffer
	slog.SetDefault(NewLoggerWithLevel(&out, &level))
	t.Cleanup(func() {
		slog.SetDefault(NewLogger(io.Discard))
	})

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logbuffer.SetRequestSite(r.Context(), "trello", 2, "/api/boards")
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/boards", nil)
	req.Host = "trello.quackapps.dev"
	rec := httptest.NewRecorder()

	requestLoggerWithMetricsAndLogs(next, "public", nil, nil).ServeHTTP(rec, req)

	line := out.String()
	if !strings.Contains(line, " error access [trello@v2 /api/boards] ") {
		t.Fatalf("log line = %q, want access metadata", line)
	}
	if !strings.Contains(line, "method=GET") || !strings.Contains(line, "path=/api/boards") || !strings.Contains(line, "status=500") {
		t.Fatalf("log line = %q, want request attrs", line)
	}
}

type hijackableResponseWriter struct {
	hijacked bool
}

func (w *hijackableResponseWriter) Header() http.Header {
	return http.Header{}
}

func (w *hijackableResponseWriter) Write(b []byte) (int, error) {
	return len(b), nil
}

func (w *hijackableResponseWriter) WriteHeader(statusCode int) {}

func (w *hijackableResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	server, client := net.Pipe()
	_ = client.Close()
	return server, bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)), nil
}

type flushableResponseWriter struct {
	flushed bool
}

func (w *flushableResponseWriter) Header() http.Header {
	return http.Header{}
}

func (w *flushableResponseWriter) Write(b []byte) (int, error) {
	return len(b), nil
}

func (w *flushableResponseWriter) WriteHeader(statusCode int) {}

func (w *flushableResponseWriter) Flush() {
	w.flushed = true
}
