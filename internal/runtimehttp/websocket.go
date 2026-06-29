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
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"quack/internal/domain"
	"quack/internal/eventpipe"
	"quack/internal/logbuffer"
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
	websocketMaxEventFanout     = 1024
	dispatchMaxDepth            = 32
	dispatchMaxPublishes        = 256
)

var (
	errEventCycleDetected        = errors.New("runtime.event_cycle_detected")
	errEventDepthExceeded        = errors.New("runtime.event_depth_exceeded")
	errEventPublishLimitExceeded = errors.New("runtime.event_publish_limit_exceeded")
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
	if err := h.applyEffectsFromHandler(r.Context(), req.Site, effects, websocketHandlerEdge(req.Route, "on_connect")); err != nil {
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
			if err := h.applyEffectsFromHandler(ctx, req.Site, effects, websocketHandlerEdge(req.Route, "on_message")); err != nil {
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
		_ = h.applyEffectsFromHandler(ctx, req.Site, effects, websocketHandlerEdge(req.Route, "on_disconnect"))
	}
}

func (h Handler) applyEffects(ctx context.Context, site string, effects []appruntime.WebSocketEffect) error {
	return h.applyEffectsFromHandler(ctx, site, effects, "")
}

func (h Handler) applyEffectsFromHandler(ctx context.Context, site string, effects []appruntime.WebSocketEffect, handler string) error {
	if dispatchTraceFromContext(ctx) == nil {
		ctx = context.WithValue(ctx, dispatchTraceContextKey{}, newDispatchTrace())
	}
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
			if err := h.dispatchEventFromHandler(ctx, site, effect.Topic, effect.Payload, handler); err != nil {
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
	return h.dispatchEventFromHandler(ctx, site, topic, payload, "")
}

func (h Handler) dispatchEventFromHandler(ctx context.Context, site string, topic string, payload []byte, handler string) error {
	return h.dispatchEventWithSourceAndHandler(ctx, site, topic, payload, "runtime", "events.publish", nil, handler)
}

func (h Handler) dispatchEventWithSource(ctx context.Context, site string, topic string, payload []byte, sourceKind string, sourceName string, headers map[string]string) error {
	return h.dispatchEventWithSourceAndHandler(ctx, site, topic, payload, sourceKind, sourceName, headers, "")
}

func (h Handler) dispatchEventWithSourceAndHandler(ctx context.Context, site string, topic string, payload []byte, sourceKind string, sourceName string, headers map[string]string, handler string) error {
	settings, err := h.siteEventSettings(ctx, site)
	if err != nil {
		return err
	}
	topic = strings.TrimSpace(topic)
	trace := dispatchTraceFromContext(ctx)
	if trace == nil {
		trace = newDispatchTrace()
		ctx = context.WithValue(ctx, dispatchTraceContextKey{}, trace)
	}
	if err := trace.enter(); err != nil {
		h.logDispatchGuard(ctx, site, settings.version, trace, topic, handler, "runtime.event_depth_exceeded", err)
		return err
	}
	defer trace.leave()
	if err := trace.recordPublish(handler, topic); err != nil {
		kind := "runtime.event_publish_limit_exceeded"
		if errors.Is(err, errEventCycleDetected) {
			kind = "runtime.event_cycle_detected"
		}
		h.logDispatchGuard(ctx, site, settings.version, trace, topic, handler, kind, err)
		return err
	}
	config := settings.pipe(topic)
	event, accepted := h.pipes.Publish(config, eventpipe.Event{
		Site: site, Pipe: config.Name, Topic: topic, SourceKind: sourceKind, SourceName: sourceName, Payload: payload, Headers: headers,
	})
	if !accepted {
		return nil
	}
	trace.setRootEventID(event.ID)
	for _, route := range settings.matchingRoutes(event.Topic) {
		entrypoint, handler, err := manifest.SplitEventHandler(route.OnEvent)
		if err != nil {
			return err
		}
		var effects []appruntime.WebSocketEffect
		invoke := func() error {
			var invokeErr error
			effects, invokeErr = h.runtime.InvokeEvent(ctx, appruntime.EventInvocationRequest{
				Site: site, Version: settings.version, Entrypoint: entrypoint, Handler: handler,
				Topic: event.Topic, Payload: event.Payload,
			})
			return invokeErr
		}
		if strings.TrimSpace(route.Concurrency) == "serial_by_topic" {
			err = h.lanes.Do(ctx, eventLaneKey(site, settings.version, route, event.Topic), invoke)
		} else {
			err = invoke()
		}
		if err != nil {
			continue
		}
		if err := h.applyEffectsFromHandler(ctx, site, effects, route.OnEvent); err != nil {
			return err
		}
	}
	for _, snapshot := range h.sockets.subscriberSnapshots(site, event.Topic) {
		effects, err := h.runtime.InvokeWebSocket(ctx, appruntime.WebSocketInvocationRequest{
			Site: snapshot.site, Version: snapshot.version, Route: snapshot.route, Query: snapshot.query,
			Headers: snapshot.headers, ConnID: snapshot.id, EventType: appruntime.WebSocketEventEvent,
			Event: appruntime.WebSocketServerEvent{Topic: event.Topic, Payload: payload},
		})
		if err != nil {
			_ = h.sockets.close(snapshot.id, 1011, "runtime event invocation failed")
			continue
		}
		if err := h.applyEffectsFromHandler(ctx, snapshot.site, effects, websocketHandlerEdge(snapshot.route, "on_event")); err != nil {
			_ = h.sockets.close(snapshot.id, 1011, "runtime event effect failed")
			continue
		}
	}
	return nil
}

func websocketHandlerEdge(route string, handler string) string {
	route = strings.TrimSpace(route)
	handler = strings.TrimSpace(handler)
	if route == "" || handler == "" {
		return ""
	}
	return "websocket:" + route + ":" + handler
}

func (h Handler) logDispatchGuard(ctx context.Context, site string, version int64, trace *dispatchTrace, topic string, handler string, kind string, err error) {
	attrs := []slog.Attr{
		slog.String("site", site),
		slog.Int64("version", version),
		slog.String("topic", topic),
		slog.String("handler", handler),
		slog.String("root_event_id", trace.rootEventID),
		slog.Int("depth", trace.depth),
		slog.Int("publish_count", trace.publishCount),
		slog.String("error_kind", kind),
		slog.String("error", err.Error()),
	}
	slog.LogAttrs(ctx, slog.LevelError, kind, attrs...)
	if h.logs == nil {
		return
	}
	h.logs.Add(logbuffer.Event{
		Level:   "error",
		Source:  "runtime_error",
		Site:    site,
		Version: version,
		Message: kind,
		Attributes: map[string]string{
			"topic":         topic,
			"handler":       handler,
			"root_event_id": trace.rootEventID,
			"depth":         fmt.Sprintf("%d", trace.depth),
			"publish_count": fmt.Sprintf("%d", trace.publishCount),
			"error_kind":    kind,
			"error":         err.Error(),
		},
	})
}

func eventLaneKey(site string, version int64, route manifest.EventRoute, topic string) string {
	return fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s", site, version, strings.TrimSpace(route.Selector), strings.TrimSpace(route.OnEvent), topic)
}

func positiveOrInt64(value int64, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}

type eventSettings struct {
	version int64
	pipes   []manifest.Pipe
	events  []manifest.EventRoute
	limits  eventpipe.Limits
}

func (h Handler) siteEventSettings(ctx context.Context, site string) (eventSettings, error) {
	defaultSettings := eventSettings{limits: h.eventPipeLimits(ctx)}
	if h.events == nil {
		return defaultSettings, nil
	}
	manifests, err := h.events.ListCurrentSiteManifests(ctx)
	if err != nil {
		return eventSettings{}, err
	}
	for _, current := range manifests {
		if current.Site != site {
			continue
		}
		settings := eventSettings{version: current.Version, limits: h.eventPipeLimits(ctx)}
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
	return defaultSettings, nil
}

func (h Handler) eventPipeLimits(ctx context.Context) eventpipe.Limits {
	settings := domain.ServerSettings{
		MaxPipesPerSite:          appsettings.DefaultMaxPipesPerSite,
		MaxTopicsPerSite:         appsettings.DefaultMaxTopicsPerSite,
		MaxRetainedEventsPerSite: appsettings.DefaultMaxRetainedEventsPerSite,
		MaxRetainedBytesPerSite:  appsettings.DefaultMaxRetainedBytesPerSite,
	}
	if h.settings != nil {
		if current, err := h.settings.GetServerSettings(ctx); err == nil {
			settings = current
		}
	}
	return eventpipe.Limits{
		MaxPipes:          positiveOrInt64(settings.MaxPipesPerSite, appsettings.DefaultMaxPipesPerSite),
		MaxTopics:         positiveOrInt64(settings.MaxTopicsPerSite, appsettings.DefaultMaxTopicsPerSite),
		MaxRetainedEvents: positiveOrInt64(settings.MaxRetainedEventsPerSite, appsettings.DefaultMaxRetainedEventsPerSite),
		MaxRetainedBytes:  positiveOrInt64(settings.MaxRetainedBytesPerSite, appsettings.DefaultMaxRetainedBytesPerSite),
	}
}

func (s eventSettings) pipe(name string) eventpipe.Config {
	var best *manifest.Pipe
	bestLen := -1
	for _, pipe := range s.pipes {
		selector := pipeSelector(pipe)
		if selector == "" || !selectorMatches(selector, name) {
			continue
		}
		specificity := selectorSpecificity(selector)
		if specificity <= bestLen {
			continue
		}
		current := pipe
		best = &current
		bestLen = specificity
	}
	if best == nil {
		return eventpipe.Config{Name: name, SiteLimits: s.limits}
	}
	selector := pipeSelector(*best)
	keyBy := strings.TrimSpace(best.KeyBy)
	if keyBy == "" {
		keyBy = eventpipe.KeyByTopic
	}
	configName := name
	if keyBy == eventpipe.KeyBySelector {
		configName = selector
	}
	return eventpipe.Config{
		Name: configName, Selector: selector, Retain: best.Retain, Unlimited: best.Unlimited,
		Overflow: best.Overflow, KeyBy: keyBy, MaxTopics: best.MaxTopics,
		TopicOverflow: best.TopicOverflow, SiteLimits: s.limits,
	}
}

func (s eventSettings) matchingRoutes(topic string) []manifest.EventRoute {
	var out []manifest.EventRoute
	for _, route := range s.events {
		selector := strings.TrimSpace(route.Selector)
		if selector == "" {
			continue
		}
		if selectorMatches(selector, topic) {
			out = append(out, route)
		}
	}
	return out
}

func pipeSelector(pipe manifest.Pipe) string {
	selector := strings.TrimSpace(pipe.Selector)
	if selector == "" {
		selector = strings.TrimSpace(pipe.Name)
	}
	return selector
}

func selectorMatches(selector string, topic string) bool {
	selector = strings.TrimSpace(selector)
	topic = strings.TrimSpace(topic)
	if selector == "" || topic == "" {
		return false
	}
	if strings.HasSuffix(selector, ".*") {
		return strings.HasPrefix(topic, strings.TrimSuffix(selector, "*"))
	}
	return selector == topic
}

func selectorSpecificity(selector string) int {
	if strings.HasSuffix(selector, ".*") {
		return len(strings.TrimSuffix(selector, "*"))
	}
	return len(selector) + 1<<20
}

func selectorsIntersect(a string, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	aWildcard := strings.HasSuffix(a, ".*")
	bWildcard := strings.HasSuffix(b, ".*")
	switch {
	case !aWildcard && !bWildcard:
		return a == b
	case aWildcard && !bWildcard:
		return selectorMatches(a, b)
	case !aWildcard && bWildcard:
		return selectorMatches(b, a)
	default:
		aPrefix := strings.TrimSuffix(a, "*")
		bPrefix := strings.TrimSuffix(b, "*")
		return strings.HasPrefix(aPrefix, bPrefix) || strings.HasPrefix(bPrefix, aPrefix)
	}
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

func (m *socketManager) subscriberSnapshots(site string, topic string) []socketSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]struct{}{}
	var out []socketSnapshot
	for scoped, subscribers := range m.topics {
		subSite, selector, ok := splitScopedTopic(scoped)
		if !ok || subSite != site || !selectorsIntersect(selector, topic) {
			continue
		}
		for connID := range subscribers {
			if _, ok := seen[connID]; ok {
				continue
			}
			if len(out) >= websocketMaxEventFanout {
				return out
			}
			conn := m.connections[connID]
			if conn == nil {
				continue
			}
			seen[connID] = struct{}{}
			out = append(out, socketSnapshot{
				id: conn.id, site: conn.site, version: conn.version, route: conn.route,
				query: conn.query, headers: cloneHeaders(conn.headers),
			})
		}
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
	for _, snapshot := range m.subscriberSnapshots(site, topic) {
		_ = m.send(snapshot.id, payload)
	}
}

func scopedTopic(site string, topic string) string {
	return site + "\x00" + topic
}

func splitScopedTopic(scoped string) (string, string, bool) {
	site, topic, ok := strings.Cut(scoped, "\x00")
	return site, topic, ok
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
