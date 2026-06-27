package runtimehttp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"quack/internal/domain"
	"quack/internal/eventpipe"
	"quack/internal/manifest"
	appruntime "quack/internal/runtime"
	appsettings "quack/internal/settings"
	"quack/internal/sites"
)

const (
	websocketGUID               = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	websocketSendQueueDepth     = 64
	websocketWriteTimeout       = 5 * time.Second
	websocketBackpressureStatus = 1013
)

func (h Handler) ServeWebSocketRoute(w http.ResponseWriter, r *http.Request, req appruntime.WebSocketInvocationRequest) {
	if r.Method != http.MethodGet {
		http.Error(w, "websocket upgrade requires GET", http.StatusMethodNotAllowed)
		return
	}
	if err := validateWebSocketUpgrade(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateWebSocketOrigin(r, req.SiteHost); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	settings, err := h.websocketSettings(r.Context())
	if err != nil {
		http.Error(w, "runtime invocation failed", http.StatusInternalServerError)
		return
	}
	connID, reserved, err := h.sockets.reserve(req.Site, req.Version, req.Route, req.Query, publicHeaders(r.Header), connectionLimits(settings))
	if err != nil {
		if errors.Is(err, appruntime.ErrConnectionLimit) {
			http.Error(w, "runtime websocket connection limit reached", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "runtime invocation failed", http.StatusInternalServerError)
		return
	}
	defer func() {
		if reserved {
			h.sockets.unregister(connID)
		}
	}()
	req.ConnID = connID
	req.Headers = publicHeaders(r.Header)
	req.EventType = appruntime.WebSocketEventConnect
	effects, err := h.runtime.InvokeWebSocket(r.Context(), req)
	if err != nil {
		h.writeWebSocketRuntimeError(w, r, err)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket upgrade is unavailable", http.StatusInternalServerError)
		return
	}
	netConn, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if err := writeWebSocketHandshake(rw, r.Header.Get("Sec-WebSocket-Key")); err != nil {
		_ = netConn.Close()
		return
	}
	h.sockets.attach(connID, netConn)
	if err := h.applyEffects(r.Context(), req.Site, effects); err != nil {
		_ = writeCloseFrame(netConn, 1011, "runtime effect failed")
		_ = netConn.Close()
		return
	}
	reserved = false
	h.readWebSocketLoop(r.Context(), rw.Reader, connID, req)
}

func (h Handler) writeWebSocketRuntimeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, appruntime.ErrDisabled) {
		http.Error(w, "runtime execution is disabled", http.StatusNotImplemented)
		return
	}
	switch {
	case errors.Is(err, appruntime.ErrCapabilityDenied):
		http.Error(w, "runtime capability denied", http.StatusForbidden)
	case errors.Is(err, appruntime.ErrRequestTooLarge):
		http.Error(w, "runtime websocket message is too large", http.StatusRequestEntityTooLarge)
	case errors.Is(err, appruntime.ErrResponseTooLarge):
		http.Error(w, "runtime websocket response is too large", http.StatusBadGateway)
	case errors.Is(err, appruntime.ErrTimeout):
		http.Error(w, "runtime execution timed out", http.StatusGatewayTimeout)
	case errors.Is(err, appruntime.ErrConcurrencyLimit):
		http.Error(w, "runtime concurrency limit reached", http.StatusTooManyRequests)
	case errors.Is(err, appruntime.ErrConnectionLimit):
		http.Error(w, "runtime websocket connection limit reached", http.StatusTooManyRequests)
	case errors.Is(err, appruntime.ErrBackpressure):
		http.Error(w, "runtime websocket back pressure limit reached", http.StatusTooManyRequests)
	case errors.Is(err, appruntime.ErrRouteNotFound):
		http.NotFound(w, r)
	case errors.Is(err, appruntime.ErrInvocationFailure):
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		http.Error(w, "runtime invocation failed", http.StatusInternalServerError)
	}
}

func (h Handler) readWebSocketLoop(ctx context.Context, reader *bufio.Reader, connID string, req appruntime.WebSocketInvocationRequest) {
	defer h.sockets.unregister(connID)
	maxRequestBytes := req.Limits.MaxRequestBytes
	if maxRequestBytes <= 0 {
		maxRequestBytes = appruntime.DefaultMaxRequestBytes
	}
	for {
		frame, err := readClientFrame(reader, maxRequestBytes)
		if err != nil {
			h.invokeDisconnect(ctx, req)
			return
		}
		switch frame.opcode {
		case websocketOpcodeText, websocketOpcodeBinary:
			req.ConnID = connID
			req.EventType = appruntime.WebSocketEventMessage
			req.Message = frame.payload
			effects, err := h.runtime.InvokeWebSocket(ctx, req)
			if err != nil {
				_ = h.sockets.close(connID, 1011, "runtime invocation failed")
				h.invokeDisconnect(ctx, req)
				return
			}
			if err := h.applyEffects(ctx, req.Site, effects); err != nil {
				_ = h.sockets.close(connID, 1011, "runtime effect failed")
				h.invokeDisconnect(ctx, req)
				return
			}
		case websocketOpcodePing:
			_ = h.sockets.sendControl(connID, websocketOpcodePong, frame.payload)
		case websocketOpcodePong:
		case websocketOpcodeClose:
			h.invokeDisconnect(ctx, req)
			_ = h.sockets.close(connID, 1000, "")
			return
		default:
			h.invokeDisconnect(ctx, req)
			_ = h.sockets.close(connID, 1003, "unsupported frame")
			return
		}
	}
}

func (h Handler) invokeDisconnect(ctx context.Context, req appruntime.WebSocketInvocationRequest) {
	req.EventType = appruntime.WebSocketEventDisconnect
	req.Message = nil
	req.Event = appruntime.WebSocketServerEvent{}
	effects, err := h.runtime.InvokeWebSocket(ctx, req)
	if err == nil {
		_ = h.applyEffects(ctx, req.Site, effects)
	}
}

func (h Handler) applyEffects(ctx context.Context, site string, effects []appruntime.WebSocketEffect) error {
	for _, effect := range effects {
		switch effect.Type {
		case appruntime.WebSocketEffectAccept:
		case appruntime.WebSocketEffectSend:
			if err := h.sockets.send(effect.ConnID, effect.Payload); err != nil {
				return err
			}
		case appruntime.WebSocketEffectBroadcast:
			h.sockets.broadcast(site, effect.Topic, effect.Payload)
		case appruntime.WebSocketEffectSubscribe:
			h.sockets.subscribe(effect.ConnID, effect.Topic)
		case appruntime.WebSocketEffectUnsubscribe:
			h.sockets.unsubscribe(effect.ConnID, effect.Topic)
		case appruntime.WebSocketEffectUnsubscribeAll:
			h.sockets.unsubscribeAll(effect.ConnID)
		case appruntime.WebSocketEffectPublish:
			if err := h.dispatchEvent(ctx, site, effect.Topic, effect.Payload); err != nil {
				return err
			}
		case appruntime.WebSocketEffectSetTimer:
			// Stub for the future heartbeat/background pump. Timers are accepted as
			// durable intents here, but scheduling will be implemented by the host.
		case appruntime.WebSocketEffectClose:
			code := effect.Code
			if code == 0 {
				code = 1000
			}
			if err := h.sockets.close(effect.ConnID, code, effect.Reason); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: unknown websocket effect %s", appruntime.ErrInvocationFailure, effect.Type)
		}
	}
	return nil
}

func (h Handler) dispatchEvent(ctx context.Context, site string, topic string, payload []byte) error {
	settings, err := h.siteEventSettings(ctx, site)
	if err != nil {
		return err
	}
	pipeName := strings.TrimSpace(topic)
	config := settings.pipe(pipeName)
	event, accepted := h.pipes.Publish(config, eventpipe.Event{
		Site: site, Pipe: pipeName, Topic: topic, SourceKind: "runtime", SourceName: "events.publish", Payload: payload,
	})
	if !accepted {
		return nil
	}
	for _, route := range settings.matchingRoutes(event.Topic) {
		entrypoint, handler, err := manifest.SplitEventHandler(route.OnEvent)
		if err != nil {
			return err
		}
		effects, err := h.runtime.InvokeEvent(ctx, appruntime.EventInvocationRequest{
			Site: site, Version: settings.version, Entrypoint: entrypoint, Handler: handler,
			Topic: event.Topic, Payload: event.Payload,
		})
		if err != nil {
			continue
		}
		if err := h.applyEffects(ctx, site, effects); err != nil {
			return err
		}
	}
	for _, snapshot := range h.sockets.subscriberSnapshots(site, topic) {
		effects, err := h.runtime.InvokeWebSocket(ctx, appruntime.WebSocketInvocationRequest{
			Site: snapshot.site, Version: snapshot.version, Route: snapshot.route, Query: snapshot.query,
			Headers: snapshot.headers, ConnID: snapshot.id, EventType: appruntime.WebSocketEventEvent,
			Event: appruntime.WebSocketServerEvent{Topic: topic, Payload: payload},
		})
		if err != nil {
			_ = h.sockets.close(snapshot.id, 1011, "runtime event invocation failed")
			continue
		}
		if err := h.applyEffects(ctx, snapshot.site, effects); err != nil {
			_ = h.sockets.close(snapshot.id, 1011, "runtime event effect failed")
			continue
		}
	}
	return nil
}

type eventSettings struct {
	version int64
	pipes   []manifest.Pipe
	events  []manifest.EventRoute
}

func (h Handler) siteEventSettings(ctx context.Context, site string) (eventSettings, error) {
	if h.events == nil {
		return eventSettings{}, nil
	}
	manifests, err := h.events.ListCurrentSiteManifests(ctx)
	if err != nil {
		return eventSettings{}, err
	}
	for _, current := range manifests {
		if current.Site != site {
			continue
		}
		settings := eventSettings{version: current.Version}
		if raw := strings.TrimSpace(current.Settings[appsettings.SettingRuntimePipes]); raw != "" {
			if err := json.Unmarshal([]byte(raw), &settings.pipes); err != nil {
				return eventSettings{}, err
			}
		}
		if raw := strings.TrimSpace(current.Settings[appsettings.SettingRuntimeEvents]); raw != "" {
			if err := json.Unmarshal([]byte(raw), &settings.events); err != nil {
				return eventSettings{}, err
			}
		}
		return settings, nil
	}
	return eventSettings{}, nil
}

func (s eventSettings) pipe(name string) eventpipe.Config {
	for _, pipe := range s.pipes {
		if pipe.Name == name {
			return eventpipe.Config{Name: pipe.Name, Retain: pipe.Retain, Unlimited: pipe.Unlimited, Overflow: pipe.Overflow}
		}
	}
	return eventpipe.Config{Name: name}
}

func (s eventSettings) matchingRoutes(topic string) []manifest.EventRoute {
	var out []manifest.EventRoute
	for _, route := range s.events {
		selector := strings.TrimSpace(route.Selector)
		if selector == "" {
			continue
		}
		if strings.HasSuffix(selector, "*") {
			prefix := strings.TrimSuffix(selector, "*")
			if strings.HasPrefix(topic, prefix) {
				out = append(out, route)
			}
			continue
		}
		if selector == topic {
			out = append(out, route)
		}
	}
	return out
}

func (h Handler) websocketSettings(ctx context.Context) (domain.ServerSettings, error) {
	settings := domain.ServerSettings{
		MaxWebSocketConnections:        appsettings.DefaultMaxWebSocketConnections,
		MaxWebSocketConnectionsPerSite: appsettings.DefaultMaxWebSocketConnectionsPerSite,
	}
	if h.settings == nil {
		return settings, nil
	}
	got, err := h.settings.GetServerSettings(ctx)
	if err != nil {
		return domain.ServerSettings{}, err
	}
	if got.MaxWebSocketConnections <= 0 {
		got.MaxWebSocketConnections = appsettings.DefaultMaxWebSocketConnections
	}
	if got.MaxWebSocketConnectionsPerSite <= 0 {
		got.MaxWebSocketConnectionsPerSite = appsettings.DefaultMaxWebSocketConnectionsPerSite
	}
	return got, nil
}

type websocketConnectionLimits struct {
	maxTotal   int64
	maxPerSite int64
}

func connectionLimits(settings domain.ServerSettings) websocketConnectionLimits {
	return websocketConnectionLimits{
		maxTotal:   settings.MaxWebSocketConnections,
		maxPerSite: settings.MaxWebSocketConnectionsPerSite,
	}
}

type socketManager struct {
	mu          sync.Mutex
	connections map[string]*socketConnection
	bySite      map[string]int64
	topics      map[string]map[string]bool
	connTopics  map[string]map[string]bool
}

type socketConnection struct {
	id        string
	site      string
	version   int64
	route     string
	query     string
	headers   map[string][]string
	conn      net.Conn
	outbound  chan outboundFrame
	done      chan struct{}
	closeOnce sync.Once
}

type outboundFrame struct {
	opcode     byte
	payload    []byte
	closeAfter bool
}

type socketSnapshot struct {
	id      string
	site    string
	version int64
	route   string
	query   string
	headers map[string][]string
}

func newSocketManager() *socketManager {
	return &socketManager{
		connections: map[string]*socketConnection{},
		bySite:      map[string]int64{},
		topics:      map[string]map[string]bool{},
		connTopics:  map[string]map[string]bool{},
	}
}

func (m *socketManager) reserve(site string, version int64, route string, query string, headers map[string][]string, limits websocketConnectionLimits) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limits.maxTotal > 0 && int64(len(m.connections)) >= limits.maxTotal {
		return "", false, appruntime.ErrConnectionLimit
	}
	if limits.maxPerSite > 0 && m.bySite[site] >= limits.maxPerSite {
		return "", false, appruntime.ErrConnectionLimit
	}
	id := randomConnectionID()
	for m.connections[id] != nil {
		id = randomConnectionID()
	}
	m.connections[id] = &socketConnection{
		id: id, site: site, version: version, route: route, query: query, headers: cloneHeaders(headers),
		outbound: make(chan outboundFrame, websocketSendQueueDepth),
		done:     make(chan struct{}),
	}
	m.bySite[site]++
	return id, true, nil
}

func (m *socketManager) attach(connID string, conn net.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c := m.connections[connID]; c != nil {
		c.conn = conn
		go m.writeLoop(c)
	}
}

func (m *socketManager) unregister(connID string) {
	m.mu.Lock()
	conn := m.connections[connID]
	if conn != nil {
		delete(m.connections, connID)
		m.bySite[conn.site]--
		if m.bySite[conn.site] <= 0 {
			delete(m.bySite, conn.site)
		}
	}
	topics := m.connTopics[connID]
	delete(m.connTopics, connID)
	for topic := range topics {
		delete(m.topics[topic], connID)
		if len(m.topics[topic]) == 0 {
			delete(m.topics, topic)
		}
	}
	m.mu.Unlock()
	if conn != nil {
		conn.closeOnce.Do(func() {
			close(conn.done)
			if conn.conn != nil {
				_ = conn.conn.Close()
			}
		})
	}
}

func (m *socketManager) subscribe(connID string, topic string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	conn := m.connections[connID]
	if conn == nil || strings.TrimSpace(topic) == "" {
		return
	}
	scoped := scopedTopic(conn.site, topic)
	if m.topics[scoped] == nil {
		m.topics[scoped] = map[string]bool{}
	}
	if m.connTopics[connID] == nil {
		m.connTopics[connID] = map[string]bool{}
	}
	m.topics[scoped][connID] = true
	m.connTopics[connID][scoped] = true
}

func (m *socketManager) unsubscribe(connID string, topic string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	conn := m.connections[connID]
	if conn == nil {
		return
	}
	scoped := scopedTopic(conn.site, topic)
	delete(m.connTopics[connID], scoped)
	delete(m.topics[scoped], connID)
	if len(m.topics[scoped]) == 0 {
		delete(m.topics, scoped)
	}
}

func (m *socketManager) unsubscribeAll(connID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for topic := range m.connTopics[connID] {
		delete(m.topics[topic], connID)
		if len(m.topics[topic]) == 0 {
			delete(m.topics, topic)
		}
	}
	delete(m.connTopics, connID)
}

func (m *socketManager) subscribers(topic string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.topics[topic]))
	for connID := range m.topics[topic] {
		out = append(out, connID)
	}
	return out
}

func (m *socketManager) subscriberSnapshots(site string, topic string) []socketSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	scoped := scopedTopic(site, topic)
	out := make([]socketSnapshot, 0, len(m.topics[scoped]))
	for connID := range m.topics[scoped] {
		conn := m.connections[connID]
		if conn == nil {
			continue
		}
		out = append(out, socketSnapshot{
			id: conn.id, site: conn.site, version: conn.version, route: conn.route,
			query: conn.query, headers: cloneHeaders(conn.headers),
		})
	}
	return out
}

func (m *socketManager) activeBySite(site string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bySite[site]
}

func (m *socketManager) activeTotal() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.connections))
}

func (m *socketManager) activeBySiteSnapshot() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.bySite))
	for site, count := range m.bySite {
		out[site] = count
	}
	return out
}

func (m *socketManager) send(connID string, payload []byte) error {
	return m.sendFrame(connID, websocketOpcodeText, payload)
}

func (m *socketManager) sendControl(connID string, opcode byte, payload []byte) error {
	return m.sendFrame(connID, opcode, payload)
}

func (m *socketManager) close(connID string, code int, reason string) error {
	err := m.enqueueFrame(connID, outboundFrame{opcode: websocketOpcodeClose, payload: closePayload(code, reason), closeAfter: true})
	if errors.Is(err, appruntime.ErrBackpressure) {
		m.unregister(connID)
	}
	return err
}

func (m *socketManager) sendFrame(connID string, opcode byte, payload []byte) error {
	return m.enqueueFrame(connID, outboundFrame{opcode: opcode, payload: append([]byte(nil), payload...)})
}

func (m *socketManager) broadcast(site string, topic string, payload []byte) {
	for _, connID := range m.subscribers(scopedTopic(site, topic)) {
		_ = m.send(connID, payload)
	}
}

func scopedTopic(site string, topic string) string {
	return site + "\x00" + topic
}

func (m *socketManager) enqueueFrame(connID string, frame outboundFrame) error {
	m.mu.Lock()
	conn := m.connections[connID]
	m.mu.Unlock()
	if conn == nil || conn.conn == nil {
		return nil
	}
	select {
	case conn.outbound <- frame:
		return nil
	case <-conn.done:
		return nil
	default:
		m.unregister(connID)
		return appruntime.ErrBackpressure
	}
}

func (m *socketManager) writeLoop(conn *socketConnection) {
	for {
		select {
		case frame := <-conn.outbound:
			if conn.conn == nil {
				continue
			}
			if err := conn.conn.SetWriteDeadline(time.Now().Add(websocketWriteTimeout)); err != nil {
				m.unregister(conn.id)
				return
			}
			if err := writeFrame(conn.conn, frame.opcode, frame.payload); err != nil {
				m.unregister(conn.id)
				return
			}
			if frame.closeAfter {
				m.unregister(conn.id)
				return
			}
		case <-conn.done:
			return
		}
	}
}

func cloneHeaders(headers map[string][]string) map[string][]string {
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func randomConnectionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("conn-%p", &b)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func validateWebSocketUpgrade(r *http.Request) error {
	if !headerTokenContains(r.Header, "Connection", "upgrade") {
		return fmt.Errorf("missing websocket connection upgrade")
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return fmt.Errorf("missing websocket upgrade header")
	}
	if strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key")) == "" {
		return fmt.Errorf("missing websocket key")
	}
	if version := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Version")); version != "13" {
		return fmt.Errorf("unsupported websocket version")
	}
	return nil
}

func validateWebSocketOrigin(r *http.Request, siteHost string) error {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return fmt.Errorf("websocket origin is required")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("websocket origin is invalid")
	}
	originHost := sites.NormalizeHost(u.Host)
	if originHost == "" {
		return fmt.Errorf("websocket origin is invalid")
	}
	expectedHost := sites.NormalizeHost(siteHost)
	if expectedHost == "" {
		expectedHost = sites.NormalizeHost(r.Host)
	}
	// TODO: Replace this same-host check with a shared origin/CORS policy
	// resolver when Quack supports explicit cross-host hotlinking.
	if expectedHost == "" || originHost != expectedHost {
		return fmt.Errorf("websocket origin is not allowed")
	}
	return nil
}

func headerTokenContains(header http.Header, key string, want string) bool {
	for _, value := range header.Values(key) {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), want) {
				return true
			}
		}
	}
	return false
}

func writeWebSocketHandshake(rw *bufio.ReadWriter, key string) error {
	accept := websocketAccept(key)
	if _, err := fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		return err
	}
	return rw.Flush()
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(key) + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

const (
	websocketOpcodeContinuation byte = 0x0
	websocketOpcodeText         byte = 0x1
	websocketOpcodeBinary       byte = 0x2
	websocketOpcodeClose        byte = 0x8
	websocketOpcodePing         byte = 0x9
	websocketOpcodePong         byte = 0xA
)

type websocketFrame struct {
	opcode  byte
	payload []byte
}

func readClientFrame(r *bufio.Reader, maxPayload int64) (websocketFrame, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return websocketFrame{}, err
	}
	fin := header[0]&0x80 != 0
	opcode := header[0] & 0x0f
	if !fin || opcode == websocketOpcodeContinuation {
		return websocketFrame{}, fmt.Errorf("fragmented websocket messages are not supported")
	}
	masked := header[1]&0x80 != 0
	if !masked {
		return websocketFrame{}, fmt.Errorf("client websocket frames must be masked")
	}
	length := int64(header[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return websocketFrame{}, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return websocketFrame{}, err
		}
		u := binary.BigEndian.Uint64(ext[:])
		if u > uint64(maxPayload) {
			return websocketFrame{}, appruntime.ErrRequestTooLarge
		}
		length = int64(u)
	}
	if maxPayload > 0 && length > maxPayload {
		return websocketFrame{}, appruntime.ErrRequestTooLarge
	}
	var mask [4]byte
	if _, err := io.ReadFull(r, mask[:]); err != nil {
		return websocketFrame{}, err
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return websocketFrame{}, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return websocketFrame{opcode: opcode, payload: payload}, nil
}

func writeFrame(w io.Writer, opcode byte, payload []byte) error {
	var header bytes.Buffer
	header.WriteByte(0x80 | opcode)
	switch {
	case len(payload) < 126:
		header.WriteByte(byte(len(payload)))
	case len(payload) <= 0xffff:
		header.WriteByte(126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(len(payload)))
		header.Write(ext[:])
	default:
		header.WriteByte(127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(len(payload)))
		header.Write(ext[:])
	}
	if _, err := w.Write(header.Bytes()); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func writeCloseFrame(w io.Writer, code int, reason string) error {
	return writeFrame(w, websocketOpcodeClose, closePayload(code, reason))
}

func closePayload(code int, reason string) []byte {
	if code == 0 {
		code = 1000
	}
	payload := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(payload[:2], uint16(code))
	copy(payload[2:], reason)
	return payload
}
