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
	"sync"
	"testing"
	"time"

	"quack/internal/domain"
	"quack/internal/eventpipe"
	"quack/internal/logbuffer"
	"quack/internal/manifest"
	appruntime "quack/internal/runtime"
	appsettings "quack/internal/settings"
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

func TestHandlerHidesInvocationFailureDetailsByDefault(t *testing.T) {
	handler := New(&recordingRuntime{err: fmt.Errorf("%w:\nTraceback: kaboom", appruntime.ErrInvocationFailure)})
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTPRoute(rec, req, appruntime.InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Traceback: kaboom") {
		t.Fatalf("body = %q, should not expose invocation failure details", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "runtime invocation failed") {
		t.Fatalf("body = %q, want generic invocation failure", rec.Body.String())
	}
}

func TestHandlerReturnsInvocationFailureDetailsWhenEnabled(t *testing.T) {
	handler := New(&recordingRuntime{err: fmt.Errorf("%w:\nTraceback: kaboom", appruntime.ErrInvocationFailure)})
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTPRoute(rec, req, appruntime.InvocationRequest{
		Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet, ExposeRuntimeErrors: true,
	})

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

func TestHandlerWebSocketRejectsMissingOriginBeforeRuntime(t *testing.T) {
	runtime := &recordingRuntime{}
	handler := New(runtime)
	req := httptest.NewRequest(http.MethodGet, "/socket", nil)
	req.Host = "foo.example.com"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	rec := httptest.NewRecorder()

	handler.ServeWebSocketRoute(rec, req, appruntime.WebSocketInvocationRequest{Site: "foo", SiteHost: "foo.example.com", Version: 1, Route: "/socket"})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if len(runtime.websocketRequests) != 0 {
		t.Fatalf("websocket requests = %#v, want none", runtime.websocketRequests)
	}
}

func TestHandlerWebSocketRejectsCrossOriginBeforeRuntime(t *testing.T) {
	runtime := &recordingRuntime{}
	handler := New(runtime)
	req := httptest.NewRequest(http.MethodGet, "/socket", nil)
	req.Host = "foo.example.com"
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	rec := httptest.NewRecorder()

	handler.ServeWebSocketRoute(rec, req, appruntime.WebSocketInvocationRequest{Site: "foo", SiteHost: "foo.example.com", Version: 1, Route: "/socket"})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if len(runtime.websocketRequests) != 0 {
		t.Fatalf("websocket requests = %#v, want none", runtime.websocketRequests)
	}
}

func TestHandlerWebSocketAllowsSameOriginWithPort(t *testing.T) {
	runtime := &recordingRuntime{err: appruntime.ErrDisabled}
	handler := New(runtime)
	req := httptest.NewRequest(http.MethodGet, "/socket", nil)
	req.Host = "foo.example.com:8443"
	req.Header.Set("Origin", "https://foo.example.com:8443")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	rec := httptest.NewRecorder()

	handler.ServeWebSocketRoute(rec, req, appruntime.WebSocketInvocationRequest{Site: "foo", SiteHost: "foo.example.com", Version: 1, Route: "/socket"})

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want runtime disabled after origin passes; body=%s", rec.Code, rec.Body.String())
	}
	if len(runtime.websocketRequests) != 1 {
		t.Fatalf("websocket requests = %#v, want one runtime invocation", runtime.websocketRequests)
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
	if got := manager.activeBySite("foo"); got != 1 {
		t.Fatalf("activeBySite(foo) = %d, want 1", got)
	}
	if got := manager.activeBySite("bar"); got != 0 {
		t.Fatalf("activeBySite(bar) = %d, want 0", got)
	}
	manager.unregister(first)
	if got := manager.activeBySite("foo"); got != 0 {
		t.Fatalf("activeBySite(foo) after unregister = %d, want 0", got)
	}
}

func TestSocketManagerClosesConnectionWhenSendQueueIsFull(t *testing.T) {
	manager := newSocketManager()
	connID, reserved, err := manager.reserve("foo", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 10, maxPerSite: 10})
	if err != nil || !reserved {
		t.Fatalf("reserve = (%q, %v, %v), want success", connID, reserved, err)
	}
	conn := newBlockingConn()
	manager.attach(connID, conn)

	var sendErr error
	for i := 0; i < websocketSendQueueDepth+2; i++ {
		sendErr = manager.send(connID, []byte("slow"))
		if errors.Is(sendErr, appruntime.ErrBackpressure) {
			break
		}
	}
	if !errors.Is(sendErr, appruntime.ErrBackpressure) {
		t.Fatalf("send error = %v, want back pressure", sendErr)
	}
	if !conn.waitClosed(time.Second) {
		t.Fatal("slow connection was not closed after queue overflow")
	}
	manager.mu.Lock()
	_, stillRegistered := manager.connections[connID]
	manager.mu.Unlock()
	if stillRegistered {
		t.Fatal("slow connection remained registered after queue overflow")
	}
}

func TestSocketManagerBroadcastDoesNotBlockOnSlowSubscriber(t *testing.T) {
	manager := newSocketManager()
	slowID, _, err := manager.reserve("foo", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 10, maxPerSite: 10})
	if err != nil {
		t.Fatal(err)
	}
	slowConn := newBlockingConn()
	manager.attach(slowID, slowConn)
	manager.subscribe(slowID, "topic")
	for i := 0; i < websocketSendQueueDepth+2; i++ {
		if errors.Is(manager.send(slowID, []byte("fill")), appruntime.ErrBackpressure) {
			break
		}
	}

	fastID, _, err := manager.reserve("foo", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 10, maxPerSite: 10})
	if err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	manager.attach(fastID, serverConn)
	manager.subscribe(fastID, "topic")

	manager.broadcast("foo", "topic", []byte("fast"))

	frameCh := make(chan websocketFrame, 1)
	errCh := make(chan error, 1)
	go func() {
		frame, err := readServerFrame(bufio.NewReader(clientConn))
		if err != nil {
			errCh <- err
			return
		}
		frameCh <- frame
	}()
	select {
	case frame := <-frameCh:
		if frame.opcode != websocketOpcodeText || string(frame.payload) != "fast" {
			t.Fatalf("frame = %#v payload=%q, want fast broadcast", frame, frame.payload)
		}
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("fast subscriber did not receive broadcast")
	}
	manager.unregister(fastID)
}

func TestSocketManagerScopesTopicsBySite(t *testing.T) {
	manager := newSocketManager()
	fooID, _, err := manager.reserve("foo", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 10, maxPerSite: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.unregister(fooID)
	barID, _, err := manager.reserve("bar", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 10, maxPerSite: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.unregister(barID)

	manager.subscribe(fooID, "topic")
	manager.subscribe(barID, "topic")

	fooSnapshots := manager.subscriberSnapshots("foo", "topic")
	if len(fooSnapshots) != 1 || fooSnapshots[0].id != fooID {
		t.Fatalf("foo snapshots = %#v, want only foo subscriber", fooSnapshots)
	}
	barSnapshots := manager.subscriberSnapshots("bar", "topic")
	if len(barSnapshots) != 1 || barSnapshots[0].id != barID {
		t.Fatalf("bar snapshots = %#v, want only bar subscriber", barSnapshots)
	}
}

func TestHandlerPublishDispatchesOnlyWithinSite(t *testing.T) {
	runtime := &recordingRuntime{}
	handler := New(runtime, WithSettings(eventSettingsFixture{manifests: []domain.CurrentSiteManifest{{
		Site:    "foo",
		SiteSHA: "foo-sha",
		Version: 3,
		Settings: map[string]string{
			appsettings.SettingRuntimePipes: `[{"name":"pixeldraw:canvas","retain":64}]`,
		},
	}}}))
	fooID, _, err := handler.sockets.reserve("foo", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 10, maxPerSite: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.sockets.unregister(fooID)
	barID, _, err := handler.sockets.reserve("bar", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 10, maxPerSite: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.sockets.unregister(barID)
	handler.sockets.subscribe(fooID, "pixeldraw:canvas")
	handler.sockets.subscribe(barID, "pixeldraw:canvas")

	err = handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
		Type:    appruntime.WebSocketEffectPublish,
		Topic:   "pixeldraw:canvas",
		Payload: []byte(`{"type":"pixels_updated"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}

	if len(runtime.websocketRequests) != 1 {
		t.Fatalf("websocket requests = %#v, want one same-site event", runtime.websocketRequests)
	}
	got := runtime.websocketRequests[0]
	if got.Site != "foo" || got.ConnID != fooID || got.EventType != appruntime.WebSocketEventEvent {
		t.Fatalf("event request = %#v, want foo event for foo subscriber", got)
	}
}

func TestHandlerPublishDispatchesSelectorEventHandler(t *testing.T) {
	runtime := &recordingRuntime{}
	handler := New(runtime, WithSettings(eventSettingsFixture{manifests: []domain.CurrentSiteManifest{{
		Site:    "foo",
		SiteSHA: "foo-sha",
		Version: 3,
		Settings: map[string]string{
			appsettings.SettingRuntimePipes:  `[{"name":"hardware.serial.rpi.read","retain":2}]`,
			appsettings.SettingRuntimeEvents: `[{"selector":"hardware.serial.*","on_event":"api/serial.star:on_serial"}]`,
		},
	}}}))

	err := handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
		Type:    appruntime.WebSocketEffectPublish,
		Topic:   "hardware.serial.rpi.read",
		Payload: []byte(`{"text":"BOOT\n"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}

	if len(runtime.eventRequests) != 1 {
		t.Fatalf("event requests = %#v, want one selector handler", runtime.eventRequests)
	}
	got := runtime.eventRequests[0]
	if got.Site != "foo" || got.Version != 3 || got.Entrypoint != "api/serial.star" || got.Handler != "on_serial" || got.Topic != "hardware.serial.rpi.read" {
		t.Fatalf("event request = %#v, want selector event invocation", got)
	}
}

func TestHandlerAppliesSiteTopicLimitFromServerSettings(t *testing.T) {
	handler := New(&recordingRuntime{}, WithSettings(eventSettingsFixture{
		settings: domain.ServerSettings{MaxTopicsPerSite: 1},
		manifests: []domain.CurrentSiteManifest{{
			Site:    "foo",
			SiteSHA: "foo-sha",
			Version: 3,
			Settings: map[string]string{
				appsettings.SettingRuntimePipes: `[{"selector":"chat.room.*","key_by":"selector","retain":4}]`,
			},
		}},
	}))

	if err := handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
		Type:    appruntime.WebSocketEffectPublish,
		Topic:   "chat.room.1",
		Payload: []byte(`{"room":1}`),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
		Type:    appruntime.WebSocketEffectPublish,
		Topic:   "chat.room.2",
		Payload: []byte(`{"room":2}`),
	}}); err != nil {
		t.Fatal(err)
	}

	recent := handler.pipes.Recent("foo", eventpipe.Config{Name: "chat.room.*"})
	if len(recent) != 1 || recent[0].Topic != "chat.room.1" {
		t.Fatalf("recent = %#v, want second topic rejected by site topic limit", recent)
	}
}

func TestHandlerDetectsRepeatedHandlerTopicPublishEdge(t *testing.T) {
	logs := logbuffer.New(10)
	runtime := &recordingRuntime{event: func(req appruntime.EventInvocationRequest) ([]appruntime.WebSocketEffect, error) {
		return []appruntime.WebSocketEffect{{
			Type:    appruntime.WebSocketEffectPublish,
			Topic:   req.Topic,
			Payload: req.Payload,
		}}, nil
	}}
	handler := New(runtime, WithLogBuffer(logs), WithSettings(eventSettingsFixture{manifests: []domain.CurrentSiteManifest{{
		Site:    "foo",
		SiteSHA: "foo-sha",
		Version: 3,
		Settings: map[string]string{
			appsettings.SettingRuntimePipes:  `[{"name":"loop","retain":64}]`,
			appsettings.SettingRuntimeEvents: `[{"selector":"loop","on_event":"app/loop.star:on_event"}]`,
		},
	}}}))

	err := handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
		Type:    appruntime.WebSocketEffectPublish,
		Topic:   "loop",
		Payload: []byte(`{"type":"loop"}`),
	}})
	if !errors.Is(err, errEventCycleDetected) {
		t.Fatalf("applyEffects error = %v, want cycle detection", err)
	}
	if len(runtime.eventRequests) != 2 {
		t.Fatalf("event requests = %#v, want initial handler and one nested handler before duplicate edge", runtime.eventRequests)
	}
	assertRuntimeGuardLog(t, logs, "runtime.event_cycle_detected")
}

func TestHandlerCapsDispatchDepth(t *testing.T) {
	runtime := &recordingRuntime{event: func(req appruntime.EventInvocationRequest) ([]appruntime.WebSocketEffect, error) {
		next := strings.TrimPrefix(req.Topic, "chain.")
		var n int
		if _, err := fmt.Sscanf(next, "%d", &n); err != nil {
			t.Fatal(err)
		}
		return []appruntime.WebSocketEffect{{
			Type:    appruntime.WebSocketEffectPublish,
			Topic:   fmt.Sprintf("chain.%d", n+1),
			Payload: req.Payload,
		}}, nil
	}}
	handler := New(runtime, WithSettings(eventSettingsFixture{manifests: []domain.CurrentSiteManifest{{
		Site:    "foo",
		SiteSHA: "foo-sha",
		Version: 3,
		Settings: map[string]string{
			appsettings.SettingRuntimePipes:  `[{"selector":"chain.*","key_by":"selector","retain":64}]`,
			appsettings.SettingRuntimeEvents: `[{"selector":"chain.*","on_event":"app/chain.star:on_event"}]`,
		},
	}}}))

	err := handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
		Type:    appruntime.WebSocketEffectPublish,
		Topic:   "chain.0",
		Payload: []byte(`{"type":"chain"}`),
	}})
	if !errors.Is(err, errEventDepthExceeded) {
		t.Fatalf("applyEffects error = %v, want depth limit", err)
	}
	if len(runtime.eventRequests) != dispatchMaxDepth {
		t.Fatalf("event requests = %d, want %d before depth rejection", len(runtime.eventRequests), dispatchMaxDepth)
	}
}

func TestHandlerCapsPublishesPerRootEvent(t *testing.T) {
	effects := make([]appruntime.WebSocketEffect, 0, dispatchMaxPublishes+1)
	for i := 0; i < dispatchMaxPublishes+1; i++ {
		effects = append(effects, appruntime.WebSocketEffect{
			Type:    appruntime.WebSocketEffectPublish,
			Topic:   fmt.Sprintf("bulk.%d", i),
			Payload: []byte(`{"type":"bulk"}`),
		})
	}
	handler := New(&recordingRuntime{}, WithSettings(eventSettingsFixture{manifests: []domain.CurrentSiteManifest{{
		Site:    "foo",
		SiteSHA: "foo-sha",
		Version: 3,
		Settings: map[string]string{
			appsettings.SettingRuntimePipes: `[{"selector":"bulk.*","key_by":"selector","retain":64}]`,
		},
	}}}))

	err := handler.applyEffects(context.Background(), "foo", effects)
	if !errors.Is(err, errEventPublishLimitExceeded) {
		t.Fatalf("applyEffects error = %v, want publish limit", err)
	}
}

func TestEventSettingsPipeUsesExactThenLongestPrefixSelector(t *testing.T) {
	settings := eventSettings{pipes: []manifest.Pipe{
		{Selector: "room.*", Retain: 64, KeyBy: "selector"},
		{Selector: "room.audit.*", Retain: 1024, KeyBy: "selector"},
		{Selector: "room.audit.created", Retain: 7},
	}}
	if got := mustPipe(t, settings, "room.audit.created"); got.Name != "room.audit.created" || got.Retain != 7 {
		t.Fatalf("exact pipe config = %+v, want exact selector", got)
	}
	if got := mustPipe(t, settings, "room.audit.deleted"); got.Name != "room.audit.*" || got.Retain != 1024 {
		t.Fatalf("audit pipe config = %+v, want longest prefix selector", got)
	}
	if got := mustPipe(t, settings, "room.chat"); got.Name != "room.*" || got.Retain != 64 {
		t.Fatalf("room pipe config = %+v, want broad selector", got)
	}
}

func TestEventSettingsPipeRejectsUndeclaredTopic(t *testing.T) {
	settings := eventSettings{pipes: []manifest.Pipe{{Selector: "room.1", Retain: 64}}}

	if _, err := settings.pipe("room.2"); !errors.Is(err, errEventPipeNotDeclared) || !strings.Contains(err.Error(), "site.yml pipes") {
		t.Fatalf("pipe error = %v, want undeclared pipe guidance", err)
	}
}

func TestHandlerRejectsPublishWithoutDeclaredPipe(t *testing.T) {
	handler := New(&recordingRuntime{}, WithSettings(eventSettingsFixture{manifests: []domain.CurrentSiteManifest{{
		Site:    "foo",
		SiteSHA: "foo-sha",
		Version: 3,
		Settings: map[string]string{
			appsettings.SettingRuntimePipes: `[{"name":"room.1","retain":64}]`,
		},
	}}}))

	err := handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
		Type:    appruntime.WebSocketEffectPublish,
		Topic:   "room.2",
		Payload: []byte(`{"room":2}`),
	}})
	if !errors.Is(err, errEventPipeNotDeclared) || !errors.Is(err, appruntime.ErrInvocationFailure) {
		t.Fatalf("applyEffects error = %v, want invocation failure for undeclared pipe", err)
	}
}

func TestHandlerSubscribesOnlyToDeclaredPipeTopics(t *testing.T) {
	handler := New(&recordingRuntime{}, WithSettings(eventSettingsFixture{manifests: []domain.CurrentSiteManifest{{
		Site:    "foo",
		SiteSHA: "foo-sha",
		Version: 3,
		Settings: map[string]string{
			appsettings.SettingRuntimePipes: `[{"selector":"room.*","key_by":"topic","max_topics":10,"retain":64}]`,
		},
	}}}))
	connID, _, err := handler.sockets.reserve("foo", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 10, maxPerSite: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.sockets.unregister(connID)

	err = handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
		Type:   appruntime.WebSocketEffectSubscribe,
		ConnID: connID,
		Topic:  "room.2",
	}})
	if err != nil {
		t.Fatalf("applyEffects subscribe error = %v", err)
	}
	if snapshots := handler.sockets.subscriberSnapshots("foo", "room.2"); len(snapshots) != 1 || snapshots[0].id != connID {
		t.Fatalf("room.2 snapshots = %#v, want subscribed conn", snapshots)
	}
}

func TestHandlerRejectsSubscribeWithoutDeclaredPipe(t *testing.T) {
	handler := New(&recordingRuntime{}, WithSettings(eventSettingsFixture{manifests: []domain.CurrentSiteManifest{{
		Site:    "foo",
		SiteSHA: "foo-sha",
		Version: 3,
		Settings: map[string]string{
			appsettings.SettingRuntimePipes: `[{"name":"room.1","retain":64}]`,
		},
	}}}))
	connID, _, err := handler.sockets.reserve("foo", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: 10, maxPerSite: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.sockets.unregister(connID)

	err = handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
		Type:   appruntime.WebSocketEffectSubscribe,
		ConnID: connID,
		Topic:  "room.2",
	}})
	if !errors.Is(err, errEventPipeNotDeclared) || !errors.Is(err, appruntime.ErrInvocationFailure) {
		t.Fatalf("applyEffects subscribe error = %v, want invocation failure for undeclared pipe", err)
	}
	if snapshots := handler.sockets.subscriberSnapshots("foo", "room.2"); len(snapshots) != 0 {
		t.Fatalf("room.2 snapshots = %#v, want no subscription", snapshots)
	}
}

func mustPipe(t *testing.T, settings eventSettings, topic string) eventpipe.Config {
	t.Helper()
	config, err := settings.pipe(topic)
	if err != nil {
		t.Fatalf("pipe(%q) error = %v", topic, err)
	}
	return config
}

func assertRuntimeGuardLog(t *testing.T, logs *logbuffer.Service, message string) {
	t.Helper()
	for _, event := range logs.Tail(logbuffer.Filter{Site: "foo"}, 10) {
		if event.Message == message {
			return
		}
	}
	t.Fatalf("runtime logs = %#v, want message %q", logs.Tail(logbuffer.Filter{Site: "foo"}, 10), message)
}

func TestHandlerSelectorPipeCanRetainBySelector(t *testing.T) {
	handler := New(&recordingRuntime{}, WithSettings(eventSettingsFixture{manifests: []domain.CurrentSiteManifest{{
		Site:    "foo",
		SiteSHA: "foo-sha",
		Version: 3,
		Settings: map[string]string{
			appsettings.SettingRuntimePipes: `[{"selector":"notifications.*","retain":2,"key_by":"selector"}]`,
		},
	}}}))

	for _, topic := range []string{"notifications.email", "notifications.sms"} {
		if err := handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
			Type: appruntime.WebSocketEffectPublish, Topic: topic, Payload: []byte(topic),
		}}); err != nil {
			t.Fatal(err)
		}
	}

	recent := handler.pipes.Recent("foo", eventpipe.Config{Name: "notifications.*"})
	if len(recent) != 2 || recent[0].Topic != "notifications.email" || recent[1].Topic != "notifications.sms" {
		t.Fatalf("recent = %#v, want aggregate selector retention", recent)
	}
}

func TestHandlerSelectorPipeRetainByTopicBoundsCardinality(t *testing.T) {
	handler := New(&recordingRuntime{}, WithSettings(eventSettingsFixture{manifests: []domain.CurrentSiteManifest{{
		Site:    "foo",
		SiteSHA: "foo-sha",
		Version: 3,
		Settings: map[string]string{
			appsettings.SettingRuntimePipes: `[{"selector":"room.*","retain":1,"key_by":"topic","max_topics":2,"topic_overflow":"evict_lru"}]`,
		},
	}}}))

	for _, topic := range []string{"room.1", "room.2", "room.1", "room.3"} {
		if err := handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
			Type: appruntime.WebSocketEffectPublish, Topic: topic, Payload: []byte(topic),
		}}); err != nil {
			t.Fatal(err)
		}
	}

	if recent := handler.pipes.Recent("foo", eventpipe.Config{Name: "room.2"}); len(recent) != 0 {
		t.Fatalf("room.2 recent = %#v, want evicted topic", recent)
	}
	if recent := handler.pipes.Recent("foo", eventpipe.Config{Name: "room.1"}); len(recent) != 1 || recent[0].Topic != "room.1" {
		t.Fatalf("room.1 recent = %#v, want retained topic", recent)
	}
	if recent := handler.pipes.Recent("foo", eventpipe.Config{Name: "room.3"}); len(recent) != 1 || recent[0].Topic != "room.3" {
		t.Fatalf("room.3 recent = %#v, want retained topic", recent)
	}
}

func TestSocketManagerSelectorSubscriptionsDedupeAndCapFanout(t *testing.T) {
	manager := newSocketManager()
	first, _, err := manager.reserve("foo", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: websocketMaxEventFanout + 10, maxPerSite: websocketMaxEventFanout + 10})
	if err != nil {
		t.Fatal(err)
	}
	manager.subscribe(first, "room.*")
	manager.subscribe(first, "room.1")
	for i := 0; i < websocketMaxEventFanout+5; i++ {
		id, _, err := manager.reserve("foo", 1, "/socket", "", nil, websocketConnectionLimits{maxTotal: websocketMaxEventFanout + 10, maxPerSite: websocketMaxEventFanout + 10})
		if err != nil {
			t.Fatal(err)
		}
		manager.subscribe(id, fmt.Sprintf("room.%d", i))
	}

	exact := manager.subscriberSnapshots("foo", "room.1")
	if len(exact) != 2 {
		t.Fatalf("exact snapshots = %d, want deduped wildcard plus exact subscribers", len(exact))
	}
	selector := manager.subscriberSnapshots("foo", "room.*")
	if len(selector) != websocketMaxEventFanout {
		t.Fatalf("selector snapshots = %d, want fanout cap %d", len(selector), websocketMaxEventFanout)
	}
}

func TestHandlerSerialByTopicPreventsSameTopicConcurrentHandlers(t *testing.T) {
	var mu sync.Mutex
	active := map[string]int{}
	violations := 0
	runtime := &recordingRuntime{event: func(req appruntime.EventInvocationRequest) ([]appruntime.WebSocketEffect, error) {
		mu.Lock()
		active[req.Topic]++
		if active[req.Topic] > 1 {
			violations++
		}
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		mu.Lock()
		active[req.Topic]--
		mu.Unlock()
		return nil, nil
	}}
	handler := New(runtime, WithSettings(eventSettingsFixture{manifests: []domain.CurrentSiteManifest{{
		Site:    "foo",
		SiteSHA: "foo-sha",
		Version: 3,
		Settings: map[string]string{
			appsettings.SettingRuntimePipes:  `[{"selector":"room.*","key_by":"selector","retain":64}]`,
			appsettings.SettingRuntimeEvents: `[{"selector":"room.*","concurrency":"serial_by_topic","on_event":"app/room.star:on_event"}]`,
		},
	}}}))

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
				Type:    appruntime.WebSocketEffectPublish,
				Topic:   "room.123",
				Payload: []byte(`{"type":"join"}`),
			}})
			if err != nil {
				t.Errorf("applyEffects error = %v", err)
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if violations != 0 {
		t.Fatalf("same-topic concurrent handler violations = %d, want 0", violations)
	}
}

func TestHandlerSerialByTopicAllowsDifferentTopicsConcurrentHandlers(t *testing.T) {
	var mu sync.Mutex
	activeTotal := 0
	maxActiveTotal := 0
	runtime := &recordingRuntime{event: func(req appruntime.EventInvocationRequest) ([]appruntime.WebSocketEffect, error) {
		mu.Lock()
		activeTotal++
		if activeTotal > maxActiveTotal {
			maxActiveTotal = activeTotal
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		activeTotal--
		mu.Unlock()
		return nil, nil
	}}
	handler := New(runtime, WithSettings(eventSettingsFixture{manifests: []domain.CurrentSiteManifest{{
		Site:    "foo",
		SiteSHA: "foo-sha",
		Version: 3,
		Settings: map[string]string{
			appsettings.SettingRuntimePipes:  `[{"selector":"room.*","key_by":"selector","retain":64}]`,
			appsettings.SettingRuntimeEvents: `[{"selector":"room.*","concurrency":"serial_by_topic","on_event":"app/room.star:on_event"}]`,
		},
	}}}))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		topic := fmt.Sprintf("room.%d", i%5)
		go func() {
			defer wg.Done()
			err := handler.applyEffects(context.Background(), "foo", []appruntime.WebSocketEffect{{
				Type:    appruntime.WebSocketEffectPublish,
				Topic:   topic,
				Payload: []byte(`{"type":"join"}`),
			}})
			if err != nil {
				t.Errorf("applyEffects error = %v", err)
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if maxActiveTotal < 2 {
		t.Fatalf("max concurrent handlers = %d, want different topics to overlap", maxActiveTotal)
	}
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
	httpReq.Header.Set("Origin", "https://foo.example.com")
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
	mu                sync.Mutex
	called            bool
	req               appruntime.InvocationRequest
	resp              appruntime.InvocationResponse
	err               error
	websocket         func(appruntime.WebSocketInvocationRequest) ([]appruntime.WebSocketEffect, error)
	event             func(appruntime.EventInvocationRequest) ([]appruntime.WebSocketEffect, error)
	websocketRequests []appruntime.WebSocketInvocationRequest
	eventRequests     []appruntime.EventInvocationRequest
}

type eventSettingsFixture struct {
	settings  domain.ServerSettings
	manifests []domain.CurrentSiteManifest
}

func (f eventSettingsFixture) GetServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	return f.settings, nil
}

func (f eventSettingsFixture) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	return append([]domain.CurrentSiteManifest(nil), f.manifests...), nil
}

func (r *recordingRuntime) InvokeHTTP(ctx context.Context, req appruntime.InvocationRequest) (appruntime.InvocationResponse, error) {
	r.mu.Lock()
	r.called = true
	r.req = req
	r.mu.Unlock()
	return r.resp, r.err
}

func (r *recordingRuntime) InvokeWebSocket(ctx context.Context, req appruntime.WebSocketInvocationRequest) ([]appruntime.WebSocketEffect, error) {
	r.mu.Lock()
	r.websocketRequests = append(r.websocketRequests, req)
	r.mu.Unlock()
	if r.websocket != nil {
		return r.websocket(req)
	}
	return nil, r.err
}

func (r *recordingRuntime) InvokeEvent(ctx context.Context, req appruntime.EventInvocationRequest) ([]appruntime.WebSocketEffect, error) {
	r.mu.Lock()
	r.eventRequests = append(r.eventRequests, req)
	r.mu.Unlock()
	if r.event != nil {
		return r.event(req)
	}
	return nil, r.err
}

func (r *recordingRuntime) PumpWebSockets(ctx context.Context) error {
	return nil
}

type blockingConn struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingConn() *blockingConn {
	return &blockingConn{closed: make(chan struct{})}
}

func (c *blockingConn) Read([]byte) (int, error) {
	<-c.closed
	return 0, net.ErrClosed
}

func (c *blockingConn) Write([]byte) (int, error) {
	<-c.closed
	return 0, net.ErrClosed
}

func (c *blockingConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *blockingConn) LocalAddr() net.Addr { return fakeAddr("local") }

func (c *blockingConn) RemoteAddr() net.Addr { return fakeAddr("remote") }

func (c *blockingConn) SetDeadline(time.Time) error { return nil }

func (c *blockingConn) SetReadDeadline(time.Time) error { return nil }

func (c *blockingConn) SetWriteDeadline(time.Time) error { return nil }

func (c *blockingConn) waitClosed(timeout time.Duration) bool {
	select {
	case <-c.closed:
		return true
	case <-time.After(timeout):
		return false
	}
}

type fakeAddr string

func (a fakeAddr) Network() string { return string(a) }

func (a fakeAddr) String() string { return string(a) }
