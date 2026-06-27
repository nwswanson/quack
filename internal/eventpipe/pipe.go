package eventpipe

import (
	"strings"
	"sync"
	"time"
)

const (
	DefaultRetain = 64
	DropOldest    = "drop_oldest"
	DropNew       = "drop_new"
)

type Config struct {
	Name      string `json:"name"`
	Retain    int    `json:"retain,omitempty"`
	Unlimited bool   `json:"unlimited,omitempty"`
	Overflow  string `json:"overflow,omitempty"`
}

type Event struct {
	ID         string            `json:"id"`
	Site       string            `json:"site"`
	Pipe       string            `json:"pipe"`
	Topic      string            `json:"topic"`
	SourceKind string            `json:"source_kind,omitempty"`
	SourceName string            `json:"source_name,omitempty"`
	Payload    []byte            `json:"payload,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	Seq        uint64            `json:"seq"`
}

type Store struct {
	mu    sync.Mutex
	pipes map[string]*pipe
}

type pipe struct {
	config Config
	next   uint64
	events []Event
}

func NewStore() *Store {
	return &Store{pipes: map[string]*pipe{}}
}

func (s *Store) Publish(config Config, event Event) (Event, bool) {
	if s == nil {
		return event, false
	}
	config = normalizeConfig(config)
	if config.Name == "" {
		config.Name = event.Pipe
	}
	if config.Name == "" {
		return event, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := scopedKey(event.Site, config.Name)
	p := s.pipes[key]
	if p == nil {
		p = &pipe{config: config}
		s.pipes[key] = p
	} else {
		p.config = config
	}
	if !p.config.Unlimited && p.config.Overflow == DropNew && len(p.events) >= p.config.Retain {
		return event, false
	}
	p.next++
	event.Site = strings.TrimSpace(event.Site)
	event.Pipe = config.Name
	event.Topic = nonEmpty(event.Topic, config.Name)
	event.Seq = p.next
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.ID == "" {
		event.ID = event.Site + ":" + event.Pipe + ":" + uintString(event.Seq)
	}
	event.Payload = append([]byte(nil), event.Payload...)
	event.Headers = cloneHeaders(event.Headers)
	p.events = append(p.events, event)
	if !p.config.Unlimited && p.config.Retain > 0 && len(p.events) > p.config.Retain {
		p.events = append([]Event(nil), p.events[len(p.events)-p.config.Retain:]...)
	}
	return event, true
}

func (s *Store) Recent(site string, config Config) []Event {
	if s == nil {
		return nil
	}
	config = normalizeConfig(config)
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pipes[scopedKey(site, config.Name)]
	if p == nil {
		return nil
	}
	out := make([]Event, len(p.events))
	for i, event := range p.events {
		out[i] = cloneEvent(event)
	}
	return out
}

func normalizeConfig(config Config) Config {
	config.Name = strings.TrimSpace(config.Name)
	if config.Overflow == "" {
		config.Overflow = DropOldest
	}
	if config.Retain < 0 {
		config.Retain = 0
	}
	if !config.Unlimited && config.Retain == 0 && config.Overflow != DropNew {
		config.Retain = DefaultRetain
	}
	return config
}

func scopedKey(site string, name string) string {
	return strings.TrimSpace(site) + "\x00" + strings.TrimSpace(name)
}

func nonEmpty(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func uintString(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		out[key] = value
	}
	return out
}

func cloneEvent(event Event) Event {
	event.Payload = append([]byte(nil), event.Payload...)
	event.Headers = cloneHeaders(event.Headers)
	return event
}
