package eventpipe

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRetain = 64
	DropOldest    = "drop_oldest"
	DropNew       = "drop_new"
	KeyByTopic    = "topic"
	KeyBySelector = "selector"
	EvictLRU      = "evict_lru"
)

type Config struct {
	Name          string `json:"name"`
	Selector      string `json:"selector,omitempty"`
	Retain        int    `json:"retain,omitempty"`
	Unlimited     bool   `json:"unlimited,omitempty"`
	Overflow      string `json:"overflow,omitempty"`
	KeyBy         string `json:"key_by,omitempty"`
	MaxTopics     int    `json:"max_topics,omitempty"`
	TopicOverflow string `json:"topic_overflow,omitempty"`
	SiteLimits    Limits `json:"-"`
}

type Limits struct {
	MaxPipes          int64
	MaxTopics         int64
	MaxRetainedEvents int64
	MaxRetainedBytes  int64
}

type Event struct {
	ID            string            `json:"id"`
	Site          string            `json:"site,omitempty"`
	Version       int64             `json:"version,omitempty"`
	Pipe          string            `json:"pipe"`
	Topic         string            `json:"topic"`
	Type          string            `json:"type"`
	Source        string            `json:"source"`
	SourceKind    string            `json:"source_kind,omitempty"`
	SourceName    string            `json:"source_name,omitempty"`
	Time          time.Time         `json:"time"`
	Seq           uint64            `json:"seq"`
	CausationID   string            `json:"causation_id,omitempty"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	Payload       []byte            `json:"payload,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
}

type PublishStats struct {
	Accepted       bool
	DroppedEvents  int64
	RetainedEvents int64
	RetainedBytes  int64
}

type Store struct {
	mu       sync.Mutex
	pipes    map[string]*pipe
	policies map[string]*topicIndex
	sites    map[string]*siteIndex
}

type topicIndex struct {
	topics map[string]struct{}
	lru    []string
}

type siteIndex struct {
	pipes  map[string]struct{}
	topics map[string]struct{}
}

type pipe struct {
	mu     sync.Mutex
	config Config
	next   uint64
	events []Event
	start  int
	count  int
}

func NewStore() *Store {
	return &Store{pipes: map[string]*pipe{}, policies: map[string]*topicIndex{}, sites: map[string]*siteIndex{}}
}

func (s *Store) Publish(config Config, event Event) (Event, bool) {
	event, stats := s.PublishWithStats(config, event)
	return event, stats.Accepted
}

func (s *Store) PublishWithStats(config Config, event Event) (Event, PublishStats) {
	if s == nil {
		return event, PublishStats{}
	}
	config = normalizeConfig(config)
	if config.Name == "" {
		config.Name = event.Pipe
	}
	if config.Name == "" {
		return event, PublishStats{}
	}
	event.Site = strings.TrimSpace(event.Site)
	event.Topic = nonEmpty(event.Topic, config.Name)
	key := scopedKey(event.Site, config.Name)
	s.mu.Lock()
	if !s.admitSiteLocked(event.Site, config.Name, event.Topic, config.SiteLimits) {
		s.mu.Unlock()
		return event, PublishStats{DroppedEvents: 1}
	}
	if !s.admitTopicLocked(event.Site, config, strings.TrimSpace(event.Topic)) {
		s.rollbackSiteLocked(event.Site, config.Name, event.Topic)
		s.mu.Unlock()
		return event, PublishStats{DroppedEvents: 1}
	}
	p := s.pipes[key]
	if p == nil {
		p = &pipe{config: config}
		s.pipes[key] = p
	}
	s.mu.Unlock()

	p.mu.Lock()
	if p.config != config {
		p.setConfig(config)
	}
	if !p.config.Unlimited && p.config.Overflow == DropNew && p.count >= p.config.Retain {
		p.mu.Unlock()
		s.mu.Lock()
		s.rollbackSiteLocked(event.Site, config.Name, event.Topic)
		s.mu.Unlock()
		return event, PublishStats{DroppedEvents: 1}
	}
	p.next++
	event.Pipe = config.Name
	event.Seq = p.next
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	if event.ID == "" {
		event.ID = randomEventID(event.Site, event.Pipe, event.Seq)
	}
	if event.Type == "" {
		event.Type = inferEventType(event.Topic, event.Payload)
	}
	if event.Source == "" {
		event.Source = eventSource(event.SourceKind, event.SourceName)
	}
	event.Payload = append([]byte(nil), event.Payload...)
	event.Headers = cloneHeaders(event.Headers)
	stats := PublishStats{Accepted: true}
	if p.append(event) {
		stats.DroppedEvents++
	}
	limits := p.config.SiteLimits
	stats.RetainedEvents, stats.RetainedBytes = p.retainedUsageLocked()
	p.mu.Unlock()
	stats.DroppedEvents += s.pruneSite(event.Site, limits)
	stats.RetainedEvents, stats.RetainedBytes = s.siteRetainedUsage(event.Site)
	return event, stats
}

func (s *Store) admitSiteLocked(site string, pipeName string, topic string, limits Limits) bool {
	index := s.sites[site]
	if index == nil {
		index = &siteIndex{pipes: map[string]struct{}{}, topics: map[string]struct{}{}}
		s.sites[site] = index
	}
	if _, ok := index.pipes[pipeName]; !ok && limits.MaxPipes > 0 && int64(len(index.pipes)) >= limits.MaxPipes {
		return false
	}
	if _, ok := index.topics[topic]; !ok && limits.MaxTopics > 0 && int64(len(index.topics)) >= limits.MaxTopics {
		return false
	}
	index.pipes[pipeName] = struct{}{}
	index.topics[topic] = struct{}{}
	return true
}

func (s *Store) rollbackSiteLocked(site string, pipeName string, topic string) {
	index := s.sites[site]
	if index == nil {
		return
	}
	key := scopedKey(site, pipeName)
	if _, ok := s.pipes[key]; !ok {
		delete(index.pipes, pipeName)
	}
	if !s.topicRetainedLocked(site, topic) {
		delete(index.topics, topic)
	}
}

func (s *Store) topicRetainedLocked(site string, topic string) bool {
	for key, p := range s.pipes {
		if !strings.HasPrefix(key, site+"\x00") {
			continue
		}
		p.mu.Lock()
		events := p.orderedEvents()
		found := false
		for _, event := range events {
			if event.Topic == topic {
				found = true
				break
			}
		}
		p.mu.Unlock()
		if found {
			return true
		}
	}
	return false
}

func (s *Store) pruneSite(site string, limits Limits) int64 {
	var dropped int64
	if limits.MaxRetainedEvents <= 0 && limits.MaxRetainedBytes <= 0 {
		return 0
	}
	for {
		events, bytes := s.siteRetainedUsage(site)
		if (limits.MaxRetainedEvents <= 0 || events <= limits.MaxRetainedEvents) &&
			(limits.MaxRetainedBytes <= 0 || bytes <= limits.MaxRetainedBytes) {
			return dropped
		}
		if !s.dropOldestSiteEvent(site) {
			return dropped
		}
		dropped++
	}
}

func (s *Store) siteRetainedUsage(site string) (int64, int64) {
	var events int64
	var bytes int64
	for _, p := range s.sitePipes(site) {
		p.mu.Lock()
		for _, event := range p.orderedEvents() {
			events++
			bytes += retainedEventBytes(event)
		}
		p.mu.Unlock()
	}
	return events, bytes
}

func (s *Store) dropOldestSiteEvent(site string) bool {
	var oldestPipe *pipe
	var oldest Event
	for _, p := range s.sitePipes(site) {
		p.mu.Lock()
		events := p.orderedEvents()
		if len(events) > 0 && (oldestPipe == nil || events[0].Time.Before(oldest.Time) || (events[0].Time.Equal(oldest.Time) && events[0].Seq < oldest.Seq)) {
			oldestPipe = p
			oldest = events[0]
		}
		p.mu.Unlock()
	}
	if oldestPipe == nil {
		return false
	}
	oldestPipe.mu.Lock()
	defer oldestPipe.mu.Unlock()
	return oldestPipe.dropOldest()
}

func (s *Store) sitePipes(site string) []*pipe {
	prefix := strings.TrimSpace(site) + "\x00"
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*pipe, 0, len(s.pipes))
	for key, p := range s.pipes {
		if strings.HasPrefix(key, prefix) {
			out = append(out, p)
		}
	}
	return out
}

func retainedEventBytes(event Event) int64 {
	n := len(event.Payload) + len(event.ID) + len(event.Site) + len(event.Pipe) + len(event.Topic) + len(event.Type) + len(event.Source) + len(event.SourceKind) + len(event.SourceName) + len(event.CausationID) + len(event.CorrelationID)
	for key, value := range event.Headers {
		n += len(key) + len(value)
	}
	return int64(n)
}

func (p *pipe) dropOldest() bool {
	if p.count == 0 {
		return false
	}
	if p.config.Unlimited {
		copy(p.events, p.events[1:])
		p.events = p.events[:len(p.events)-1]
		p.count = len(p.events)
		return true
	}
	p.events[p.start] = Event{}
	p.start = (p.start + 1) % len(p.events)
	p.count--
	return true
}

func (s *Store) admitTopicLocked(site string, config Config, topic string) bool {
	if config.Selector == "" || config.KeyBy != KeyByTopic || config.MaxTopics <= 0 {
		return true
	}
	if topic == "" {
		topic = config.Name
	}
	policyKey := scopedKey(site, config.Selector)
	index := s.policies[policyKey]
	if index == nil {
		index = &topicIndex{topics: map[string]struct{}{}}
		s.policies[policyKey] = index
	}
	if _, ok := index.topics[topic]; ok {
		index.touch(topic)
		return true
	}
	if len(index.topics) >= config.MaxTopics {
		if config.TopicOverflow == DropNew {
			return false
		}
		evicted := index.evictLRU()
		if evicted != "" {
			delete(s.pipes, scopedKey(site, evicted))
			if siteIndex := s.sites[site]; siteIndex != nil {
				delete(siteIndex.pipes, evicted)
			}
		}
	}
	index.topics[topic] = struct{}{}
	index.lru = append(index.lru, topic)
	return true
}

func (s *Store) Recent(site string, config Config) []Event {
	if s == nil {
		return nil
	}
	config = normalizeConfig(config)
	s.mu.Lock()
	p := s.pipes[scopedKey(site, config.Name)]
	if p == nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	events := p.orderedEvents()
	out := make([]Event, len(events))
	for i, event := range events {
		out[i] = cloneEvent(event)
	}
	return out
}

func (p *pipe) setConfig(config Config) {
	if p.config == config {
		return
	}
	events := p.orderedEvents()
	p.config = config
	p.start = 0
	p.count = 0
	if p.config.Unlimited {
		p.events = append([]Event(nil), events...)
		p.count = len(p.events)
		return
	}
	if p.config.Retain <= 0 {
		p.events = nil
		return
	}
	if len(events) > p.config.Retain {
		events = events[len(events)-p.config.Retain:]
	}
	p.events = make([]Event, p.config.Retain)
	copy(p.events, events)
	p.count = len(events)
}

func (p *pipe) append(event Event) bool {
	if p.config.Unlimited {
		p.events = append(p.events, event)
		p.start = 0
		p.count = len(p.events)
		return false
	}
	if p.config.Retain <= 0 {
		return false
	}
	if len(p.events) != p.config.Retain {
		p.events = make([]Event, p.config.Retain)
		p.start = 0
		p.count = 0
	}
	if p.count < p.config.Retain {
		p.events[(p.start+p.count)%len(p.events)] = event
		p.count++
		return false
	}
	p.events[p.start] = event
	p.start = (p.start + 1) % len(p.events)
	return true
}

func (p *pipe) retainedUsageLocked() (int64, int64) {
	var events int64
	var bytes int64
	for _, event := range p.orderedEvents() {
		events++
		bytes += retainedEventBytes(event)
	}
	return events, bytes
}

func (p *pipe) orderedEvents() []Event {
	if p.count == 0 {
		return nil
	}
	if p.config.Unlimited || p.start == 0 {
		return p.events[:p.count]
	}
	out := make([]Event, p.count)
	for i := 0; i < p.count; i++ {
		out[i] = p.events[(p.start+i)%len(p.events)]
	}
	return out
}

func normalizeConfig(config Config) Config {
	config.Name = strings.TrimSpace(config.Name)
	config.Selector = strings.TrimSpace(config.Selector)
	if config.Overflow == "" {
		config.Overflow = DropOldest
	}
	if config.KeyBy == "" {
		config.KeyBy = KeyByTopic
	}
	if config.TopicOverflow == "" {
		config.TopicOverflow = EvictLRU
	}
	if config.Retain < 0 {
		config.Retain = 0
	}
	if !config.Unlimited && config.Retain == 0 && config.Overflow != DropNew {
		config.Retain = DefaultRetain
	}
	return config
}

func randomEventID(site string, pipe string, seq uint64) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "evt_" + base64.RawURLEncoding.EncodeToString(b[:])
	}
	return "evt_" + strings.ReplaceAll(site+":"+pipe+":"+uintString(seq), " ", "_")
}

func inferEventType(topic string, payload []byte) string {
	var object map[string]any
	if len(payload) > 0 && json.Unmarshal(payload, &object) == nil {
		if value, ok := object["type"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return "event"
	}
	return topic
}

func eventSource(kind string, name string) string {
	kind = strings.TrimSpace(kind)
	name = strings.TrimSpace(name)
	switch {
	case kind != "" && name != "":
		return kind + ":" + name
	case kind != "":
		return kind
	case name != "":
		return name
	default:
		return "runtime"
	}
}

func (i *topicIndex) touch(topic string) {
	for pos, current := range i.lru {
		if current != topic {
			continue
		}
		copy(i.lru[pos:], i.lru[pos+1:])
		i.lru[len(i.lru)-1] = topic
		return
	}
	i.lru = append(i.lru, topic)
}

func (i *topicIndex) evictLRU() string {
	for len(i.lru) > 0 {
		topic := i.lru[0]
		i.lru = i.lru[1:]
		if _, ok := i.topics[topic]; !ok {
			continue
		}
		delete(i.topics, topic)
		return topic
	}
	return ""
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
