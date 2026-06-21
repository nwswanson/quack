package server

import (
	"bufio"
	"net"
	"net/http"
	"testing"
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
