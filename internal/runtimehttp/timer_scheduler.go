package runtimehttp

import (
	"container/heap"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"hash/fnv"
	"log/slog"
	"strings"
	"sync"
	"time"

	appruntime "quack/internal/runtime"
)

const timerShardCount = 64

type timerScheduler struct {
	dispatch func(context.Context, scheduledTimer) error
	shards   [timerShardCount]*timerShard
}

type scheduledTimer struct {
	ID       string
	Site     string
	Topic    string
	Payload  []byte
	Key      string
	ActionID string
	Interval time.Duration
	Jitter   time.Duration
}

type timerShard struct {
	mu       sync.Mutex
	entries  timerHeap
	byID     map[string]*timerEntry
	byKey    map[string]map[string]*timerEntry
	coalesce map[string]*timerEntry
	wake     chan struct{}
}

type timerEntry struct {
	timer    scheduledTimer
	deadline time.Time
	index    int
}

func newTimerScheduler(dispatch func(context.Context, scheduledTimer) error) *timerScheduler {
	s := &timerScheduler{dispatch: dispatch}
	for i := range s.shards {
		shard := &timerShard{
			byID:     map[string]*timerEntry{},
			byKey:    map[string]map[string]*timerEntry{},
			coalesce: map[string]*timerEntry{},
			wake:     make(chan struct{}, 1),
		}
		s.shards[i] = shard
		go s.runShard(shard)
	}
	return s
}

func (s *timerScheduler) schedule(site string, effect appruntime.WebSocketEffect, now time.Time) {
	if s == nil {
		return
	}
	id := strings.TrimSpace(effect.ID)
	if id == "" {
		id = randomTimerID()
	}
	mode := strings.TrimSpace(effect.Mode)
	if mode == "" {
		mode = "new"
	}
	key := strings.TrimSpace(effect.Key)
	timer := scheduledTimer{
		ID: id, Site: strings.TrimSpace(site), Topic: strings.TrimSpace(effect.Topic),
		Payload: append([]byte(nil), effect.Payload...), Key: key, ActionID: strings.TrimSpace(effect.ActionID),
	}
	var deadline time.Time
	switch effect.Type {
	case appruntime.WebSocketEffectTimerAt:
		deadline = time.UnixMilli(effect.UnixMS).UTC()
	case appruntime.WebSocketEffectTimerEvery:
		timer.Interval = time.Duration(effect.MS) * time.Millisecond
		timer.Jitter = time.Duration(effect.JitterMS) * time.Millisecond
		deadline = now.Add(timer.intervalWithJitter())
	default:
		deadline = now.Add(time.Duration(effect.MS) * time.Millisecond)
	}
	s.shardFor(site, key, id).schedule(timerEntry{timer: timer, deadline: deadline}, mode)
}

func (s *timerScheduler) cancel(site string, key string, id string) {
	if s == nil {
		return
	}
	site = strings.TrimSpace(site)
	key = strings.TrimSpace(key)
	id = strings.TrimSpace(id)
	if id != "" {
		for _, shard := range s.shards {
			shard.cancelID(id)
		}
	}
	if key != "" {
		scopedKey := scopedTimerKey(site, key)
		for _, shard := range s.shards {
			shard.cancelKey(scopedKey)
		}
	}
}

func (s *timerScheduler) shardFor(site string, key string, id string) *timerShard {
	h := fnv.New32a()
	if key != "" {
		_, _ = h.Write([]byte(strings.TrimSpace(site)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(key))
	} else {
		_, _ = h.Write([]byte(id))
	}
	return s.shards[h.Sum32()%timerShardCount]
}

func (s *timerScheduler) runShard(shard *timerShard) {
	var timer *time.Timer
	for {
		wait, ok := shard.nextWait(time.Now().UTC())
		if !ok {
			if timer != nil {
				timer.Stop()
				timer = nil
			}
			<-shard.wake
			continue
		}
		if timer == nil {
			timer = time.NewTimer(wait)
		} else {
			timer.Reset(wait)
		}
		select {
		case <-timer.C:
			due := shard.popDue(time.Now().UTC())
			for _, entry := range due {
				if err := s.dispatch(context.Background(), entry.timer); err != nil {
					slog.Warn("timer dispatch failed", "site", entry.timer.Site, "topic", entry.timer.Topic, "key", entry.timer.Key, "error", err)
				}
				if entry.timer.Interval > 0 {
					entry.deadline = time.Now().UTC().Add(entry.timer.intervalWithJitter())
					shard.schedule(*entry, "replace")
				}
			}
		case <-shard.wake:
			if timer != nil && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}
}

func (s scheduledTimer) intervalWithJitter() time.Duration {
	if s.Jitter <= 0 {
		return s.Interval
	}
	return s.Interval + time.Duration(randomUint64()%uint64(s.Jitter+1))
}

func (s *timerShard) schedule(entry timerEntry, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scopedKey := scopedTimerKey(entry.timer.Site, entry.timer.Key)
	if entry.timer.Key != "" && mode != "new" {
		if existing := s.coalesce[scopedKey]; existing != nil {
			existing.timer.Topic = entry.timer.Topic
			existing.timer.Payload = entry.timer.Payload
			existing.timer.Interval = entry.timer.Interval
			existing.timer.Jitter = entry.timer.Jitter
			if mode != "keep_existing" {
				existing.deadline = entry.deadline
				heap.Fix(&s.entries, existing.index)
			}
			s.signal()
			return
		}
	}
	copy := entry
	copy.index = len(s.entries)
	heap.Push(&s.entries, &copy)
	s.byID[copy.timer.ID] = &copy
	if copy.timer.Key != "" {
		group := s.byKey[scopedKey]
		if group == nil {
			group = map[string]*timerEntry{}
			s.byKey[scopedKey] = group
		}
		group[copy.timer.ID] = &copy
		if mode != "new" {
			s.coalesce[scopedKey] = &copy
		}
	}
	s.signal()
}

func (s *timerShard) cancelID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.byID[id]
	if entry == nil {
		return
	}
	s.remove(entry)
	s.signal()
}

func (s *timerShard) cancelKey(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.byKey[key] {
		s.remove(entry)
	}
	delete(s.byKey, key)
	delete(s.coalesce, key)
	s.signal()
}

func (s *timerShard) nextWait(now time.Time) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) == 0 {
		return 0, false
	}
	wait := s.entries[0].deadline.Sub(now)
	if wait < 0 {
		wait = 0
	}
	return wait, true
}

func (s *timerShard) popDue(now time.Time) []*timerEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []*timerEntry
	for len(s.entries) > 0 && !s.entries[0].deadline.After(now) {
		entry := heap.Pop(&s.entries).(*timerEntry)
		s.forget(entry)
		due = append(due, entry)
	}
	return due
}

func (s *timerShard) remove(entry *timerEntry) {
	if entry.index < 0 || entry.index >= len(s.entries) || s.entries[entry.index] != entry {
		return
	}
	heap.Remove(&s.entries, entry.index)
	s.forget(entry)
}

func (s *timerShard) forget(entry *timerEntry) {
	delete(s.byID, entry.timer.ID)
	if entry.timer.Key != "" {
		scopedKey := scopedTimerKey(entry.timer.Site, entry.timer.Key)
		if group := s.byKey[scopedKey]; group != nil {
			delete(group, entry.timer.ID)
			if len(group) == 0 {
				delete(s.byKey, scopedKey)
			}
		}
		if s.coalesce[scopedKey] == entry {
			delete(s.coalesce, scopedKey)
		}
	}
	entry.index = -1
}

func scopedTimerKey(site string, key string) string {
	return strings.TrimSpace(site) + "\x00" + strings.TrimSpace(key)
}

func (s *timerShard) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

type timerHeap []*timerEntry

func (h timerHeap) Len() int { return len(h) }
func (h timerHeap) Less(i, j int) bool {
	return h[i].deadline.Before(h[j].deadline)
}
func (h timerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *timerHeap) Push(x any) {
	entry := x.(*timerEntry)
	entry.index = len(*h)
	*h = append(*h, entry)
}
func (h *timerHeap) Pop() any {
	old := *h
	n := len(old)
	entry := old[n-1]
	old[n-1] = nil
	entry.index = -1
	*h = old[:n-1]
	return entry
}

func randomTimerID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "tmr_runtime_fallback"
	}
	return "tmr_runtime_" + base64.RawURLEncoding.EncodeToString(b[:])
}

func randomUint64() uint64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint64(time.Now().UnixNano())
	}
	return binary.LittleEndian.Uint64(b[:])
}
