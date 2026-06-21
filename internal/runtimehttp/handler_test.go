package runtimehttp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appruntime "quack/internal/runtime"
)

func TestHandlerReturnsDisabledWhenRuntimeIsNotConfigured(t *testing.T) {
	handler := New(nil)

	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTPRoute(rec, req, appruntime.InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
}

func TestHandlerCapsRequestBodyBeforeRuntime(t *testing.T) {
	runtime := &recordingRuntime{}
	handler := New(runtime)

	req := httptest.NewRequest(http.MethodPost, "/api", strings.NewReader("toolarge"))
	rec := httptest.NewRecorder()
	handler.ServeHTTPRoute(rec, req, appruntime.InvocationRequest{
		Site: "foo", Version: 1, Route: "/api", Method: http.MethodPost,
		Limits: appruntime.ResourceLimits{MaxRequestBytes: 3},
	})

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	if runtime.called {
		t.Fatal("runtime called for oversized body")
	}
}

func TestHandlerCopiesOnlyPublicHeaders(t *testing.T) {
	runtime := &recordingRuntime{resp: appruntime.InvocationResponse{StatusCode: http.StatusOK, Headers: map[string][]string{
		"Connection":   {"close"},
		"Content-Type": {"text/plain"},
	}, Body: []byte("ok")}}
	handler := New(runtime)

	req := httptest.NewRequest(http.MethodPost, "/api", strings.NewReader("hello"))
	req.Header.Set("X-Test", "visible")
	req.Header.Set("Authorization", "secret")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rec := httptest.NewRecorder()
	handler.ServeHTTPRoute(rec, req, appruntime.InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodPost})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := runtime.req.Headers["X-Test"]; len(got) != 1 || got[0] != "visible" {
		t.Fatalf("headers = %+v, want X-Test visible", runtime.req.Headers)
	}
	if _, ok := runtime.req.Headers["Authorization"]; ok {
		t.Fatalf("headers = %+v, authorization should be stripped", runtime.req.Headers)
	}
	if rec.Header().Get("Connection") != "" {
		t.Fatalf("response connection header = %q, want stripped", rec.Header().Get("Connection"))
	}
}

func TestHandlerMapsStructuredRuntimeErrors(t *testing.T) {
	tests := map[string]struct {
		err  error
		want int
	}{
		"denied":         {err: appruntime.ErrCapabilityDenied, want: http.StatusForbidden},
		"method":         {err: appruntime.ErrMethodNotAllowed, want: http.StatusMethodNotAllowed},
		"response large": {err: appruntime.ErrResponseTooLarge, want: http.StatusBadGateway},
		"timeout":        {err: appruntime.ErrTimeout, want: http.StatusGatewayTimeout},
		"concurrency":    {err: appruntime.ErrConcurrencyLimit, want: http.StatusTooManyRequests},
		"route missing":  {err: appruntime.ErrRouteNotFound, want: http.StatusNotFound},
		"generic":        {err: errors.New("boom"), want: http.StatusInternalServerError},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			handler := New(&recordingRuntime{err: tc.err})
			req := httptest.NewRequest(http.MethodGet, "/api", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTPRoute(rec, req, appruntime.InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestHandlerReturnsInvocationFailureDetails(t *testing.T) {
	handler := New(&recordingRuntime{err: fmt.Errorf("%w:\nTraceback: kaboom", appruntime.ErrInvocationFailure)})
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTPRoute(rec, req, appruntime.InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Traceback: kaboom") {
		t.Fatalf("body = %q, want invocation failure details", rec.Body.String())
	}
}

func TestHandlerWebSocketUpgradeAppliesConnectEffects(t *testing.T) {
	runtime := &recordingRuntime{}
	runtime.websocket = func(req appruntime.WebSocketInvocationRequest) ([]appruntime.WebSocketEffect, error) {
		if req.EventType != appruntime.WebSocketEventConnect {
			return nil, nil
		}
		return []appruntime.WebSocketEffect{{
			Type:    appruntime.WebSocketEffectSend,
			ConnID:  req.ConnID,
			Payload: []byte("welcome"),
		}}, nil
	}
	handler := New(runtime)
	conn, reader, done := websocketPipe(t, handler, appruntime.WebSocketInvocationRequest{Site: "foo", Version: 1, Route: "/socket"})
	defer func() { <-done }()
	defer conn.Close()

	frame, err := readServerFrame(reader)
	if err != nil {
		t.Fatal(err)
	}
	if frame.opcode != websocketOpcodeText || string(frame.payload) != "welcome" {
		t.Fatalf("frame = %#v payload=%q, want welcome text frame", frame, string(frame.payload))
	}
	if len(runtime.websocketRequests) == 0 || runtime.websocketRequests[0].ConnID == "" {
		t.Fatalf("websocket requests = %#v, want connect invocation with conn id", runtime.websocketRequests)
	}
}

func TestSocketManagerEnforcesConnectionLimits(t *testing.T) {
	manager := newSocketManager()
	first, reserved, err := manager.reserve("foo", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 1, maxPerSite: 10})
	if err != nil || !reserved {
		t.Fatalf("first reserve = (%q, %v, %v), want success", first, reserved, err)
	}
	if _, _, err := manager.reserve("bar", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 1, maxPerSite: 10}); !errors.Is(err, appruntime.ErrConnectionLimit) {
		t.Fatalf("second reserve error = %v, want total connection limit", err)
	}
	manager.unregister(first)

	first, reserved, err = manager.reserve("foo", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 10, maxPerSite: 1})
	if err != nil || !reserved {
		t.Fatalf("first site reserve = (%q, %v, %v), want success", first, reserved, err)
	}
	if _, _, err := manager.reserve("foo", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 10, maxPerSite: 1}); !errors.Is(err, appruntime.ErrConnectionLimit) {
		t.Fatalf("second site reserve error = %v, want per-site connection limit", err)
	}
	manager.unregister(first)
}

func websocketPipe(t *testing.T, handler Handler, req appruntime.WebSocketInvocationRequest) (net.Conn, *bufio.Reader, <-chan struct{}) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	rec := &hijackRecorder{header: http.Header{}, conn: serverConn}
	httpReq := httptest.NewRequest(http.MethodGet, req.Route, nil)
	httpReq.Host = "foo.example.com"
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	httpReq.Header.Set("Connection", "Upgrade")
	httpReq.Header.Set("Upgrade", "websocket")
	httpReq.Header.Set("Sec-WebSocket-Version", "13")
	httpReq.Header.Set("Sec-WebSocket-Key", key)
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeWebSocketRoute(rec, httpReq, req)
	}()
	reader := bufio.NewReader(clientConn)
	status, err := reader.ReadString('\n')
	if err != nil {
		_ = clientConn.Close()
		t.Fatal(err)
	}
	if !strings.Contains(status, "101 Switching Protocols") {
		_ = clientConn.Close()
		t.Fatalf("handshake status = %q, want 101", status)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			_ = clientConn.Close()
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	return clientConn, reader, done
}

func readServerFrame(r *bufio.Reader) (websocketFrame, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return websocketFrame{}, err
	}
	if header[1]&0x80 != 0 {
		return websocketFrame{}, fmt.Errorf("server frame was masked")
	}
	opcode := header[0] & 0x0f
	length := int64(header[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return websocketFrame{}, err
		}
		length = int64(ext[0])<<8 | int64(ext[1])
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return websocketFrame{}, err
		}
		return websocketFrame{}, fmt.Errorf("unexpected large frame length: %x", ext)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return websocketFrame{}, err
	}
	return websocketFrame{opcode: opcode, payload: payload}, nil
}

type hijackRecorder struct {
	header http.Header
	conn   net.Conn
	status int
	body   strings.Builder
}

func (r *hijackRecorder) Header() http.Header {
	return r.header
}

func (r *hijackRecorder) Write(data []byte) (int, error) {
	return r.body.Write(data)
}

func (r *hijackRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
}

func (r *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	rw := bufio.NewReadWriter(bufio.NewReader(r.conn), bufio.NewWriter(r.conn))
	return r.conn, rw, nil
}

type recordingRuntime struct {
	called            bool
	req               appruntime.InvocationRequest
	resp              appruntime.InvocationResponse
	err               error
	websocket         func(appruntime.WebSocketInvocationRequest) ([]appruntime.WebSocketEffect, error)
	websocketRequests []appruntime.WebSocketInvocationRequest
}

func (r *recordingRuntime) InvokeHTTP(ctx context.Context, req appruntime.InvocationRequest) (appruntime.InvocationResponse, error) {
	r.called = true
	r.req = req
	return r.resp, r.err
}

func (r *recordingRuntime) InvokeWebSocket(ctx context.Context, req appruntime.WebSocketInvocationRequest) ([]appruntime.WebSocketEffect, error) {
	r.websocketRequests = append(r.websocketRequests, req)
	if r.websocket != nil {
		return r.websocket(req)
	}
	return nil, r.err
}

func (r *recordingRuntime) PumpWebSockets(ctx context.Context) error {
	return nil
}
