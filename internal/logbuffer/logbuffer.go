package logbuffer

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultCapacity = 500

type Event struct {
	ID         int64             `json:"id"`
	Time       string            `json:"time"`
	Level      string            `json:"level"`
	Source     string            `json:"source"`
	Site       string            `json:"site,omitempty"`
	Version    int64             `json:"version,omitempty"`
	Route      string            `json:"route,omitempty"`
	Message    string            `json:"message"`
	Attributes map[string]string `json:"attributes,omitempty"`
	StackTrace string            `json:"stack_trace,omitempty"`
}

type Filter struct {
	Site          string
	IncludeSystem bool
}

type Service struct {
	mu          sync.RWMutex
	capacity    int
	nextID      int64
	events      []Event
	subscribers map[int]subscriber
	nextSubID   int
}

type subscriber struct {
	filter Filter
	ch     chan Event
}

func New(capacity int) *Service {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Service{
		capacity:    capacity,
		subscribers: map[int]subscriber{},
	}
}

func (s *Service) SetCapacity(capacity int) {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.capacity = capacity
	if len(s.events) > capacity {
		s.events = append([]Event(nil), s.events[len(s.events)-capacity:]...)
	}
}

func (s *Service) Add(event Event) Event {
	if s == nil {
		return event
	}
	if event.Time == "" {
		event.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	event.Level = normalizeLevel(event.Level)
	event.Site = strings.TrimSpace(event.Site)
	event.Source = strings.TrimSpace(event.Source)
	event.Route = strings.TrimSpace(event.Route)

	s.mu.Lock()
	s.nextID++
	event.ID = s.nextID
	if s.capacity > 0 {
		if len(s.events) == s.capacity {
			copy(s.events, s.events[1:])
			s.events[len(s.events)-1] = event
		} else {
			s.events = append(s.events, event)
		}
	}
	subs := make([]subscriber, 0, len(s.subscribers))
	for _, sub := range s.subscribers {
		if eventMatches(event, sub.filter) {
			subs = append(subs, sub)
		}
	}
	s.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub.ch <- event:
		default:
		}
	}
	return event
}

func (s *Service) Tail(filter Filter, limit int) []Event {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Event, 0, len(s.events))
	for _, event := range s.events {
		if eventMatches(event, filter) {
			out = append(out, cloneEvent(event))
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (s *Service) Subscribe(ctx context.Context, filter Filter) <-chan Event {
	ch := make(chan Event, 64)
	if s == nil {
		close(ch)
		return ch
	}
	s.mu.Lock()
	s.nextSubID++
	id := s.nextSubID
	s.subscribers[id] = subscriber{filter: filter, ch: ch}
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		delete(s.subscribers, id)
		s.mu.Unlock()
		close(ch)
	}()
	return ch
}

func eventMatches(event Event, filter Filter) bool {
	if filter.Site != "" {
		return event.Site == filter.Site
	}
	if filter.IncludeSystem {
		return true
	}
	return event.Site != ""
}

func cloneEvent(event Event) Event {
	if event.Attributes != nil {
		attrs := make(map[string]string, len(event.Attributes))
		for key, value := range event.Attributes {
			attrs[key] = value
		}
		event.Attributes = attrs
	}
	return event
}

func normalizeLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return "debug"
	case "info":
		return "info"
	case "warn", "warning":
		return "warn"
	case "error":
		return "error"
	default:
		return "info"
	}
}

type requestLogMetadata struct {
	mu      sync.RWMutex
	Site    string
	Version int64
	Route   string
}

type requestLogMetadataKey struct{}

func ContextWithRequestMetadata(ctx context.Context) context.Context {
	if _, ok := ctx.Value(requestLogMetadataKey{}).(*requestLogMetadata); ok {
		return ctx
	}
	return context.WithValue(ctx, requestLogMetadataKey{}, &requestLogMetadata{})
}

func SetRequestSite(ctx context.Context, site string, version int64, route string) {
	meta, ok := ctx.Value(requestLogMetadataKey{}).(*requestLogMetadata)
	if !ok || meta == nil {
		return
	}
	meta.mu.Lock()
	meta.Site = strings.TrimSpace(site)
	meta.Version = version
	meta.Route = strings.TrimSpace(route)
	meta.mu.Unlock()
}

func RequestSite(ctx context.Context) (site string, version int64, route string) {
	meta, ok := ctx.Value(requestLogMetadataKey{}).(*requestLogMetadata)
	if !ok || meta == nil {
		return "", 0, ""
	}
	meta.mu.RLock()
	defer meta.mu.RUnlock()
	return meta.Site, meta.Version, meta.Route
}

func Attrs(attrs ...slog.Attr) map[string]string {
	out := map[string]string{}
	for _, attr := range attrs {
		attr.Value = attr.Value.Resolve()
		switch attr.Value.Kind() {
		case slog.KindString:
			out[attr.Key] = attr.Value.String()
		case slog.KindInt64:
			out[attr.Key] = strconv.FormatInt(attr.Value.Int64(), 10)
		case slog.KindUint64:
			out[attr.Key] = strconv.FormatUint(attr.Value.Uint64(), 10)
		case slog.KindFloat64:
			out[attr.Key] = strconv.FormatFloat(attr.Value.Float64(), 'f', -1, 64)
		case slog.KindBool:
			out[attr.Key] = strconv.FormatBool(attr.Value.Bool())
		case slog.KindDuration:
			out[attr.Key] = attr.Value.Duration().String()
		case slog.KindTime:
			out[attr.Key] = attr.Value.Time().UTC().Format(time.RFC3339Nano)
		default:
			out[attr.Key] = fmt.Sprint(attr.Value.Any())
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
