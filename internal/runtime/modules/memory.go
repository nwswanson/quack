package modules

import (
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"sort"
	"strconv"
	"sync"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

var globalMemory = newMemoryStore()

type MemoryPersistence interface {
	Save(site string, snapshot map[string]any) error
	Load(site string) (map[string]any, error)
}

func WipeMemorySite(site string) {
	globalMemory.wipeSite(site)
}

func WipeAllMemory() {
	globalMemory.wipeAll()
}

func MemoryUsage(site string) int64 {
	return globalMemory.memoryUsage(site)
}

func MemoryUsageBySite() map[string]int64 {
	return globalMemory.memoryUsageBySite()
}

type memoryStore struct {
	mu          sync.Mutex
	sites       map[string]*siteMemory
	persistence *memoryPersistenceManager
}

type siteMemory struct {
	mu     sync.Mutex
	loaded bool
	used   int64
	items  map[string]memoryEntry
}

type memoryEntry struct {
	kind  string
	value any
	bytes int64
}

type memoryModule struct {
	site  string
	quota int64
}

type memoryValue struct {
	kind string
	data any
	size int64
}

type zmember struct {
	value memoryValue
	score float64
}

func NewMemoryModule(site string, maxBytes int64) *starlarkstruct.Module {
	m := &memoryModule{site: site, quota: maxBytes}
	return &starlarkstruct.Module{
		Name: "memory",
		Members: starlark.StringDict{
			"usage":        starlark.NewBuiltin("memory.usage", m.usage),
			"quota":        starlark.NewBuiltin("memory.quota", m.quotaValue),
			"clear":        starlark.NewBuiltin("memory.clear", m.clear),
			"keys":         starlark.NewBuiltin("memory.keys", m.keys),
			"items":        starlark.NewBuiltin("memory.items", m.items),
			"type":         starlark.NewBuiltin("memory.type", m.typeOf),
			"get":          starlark.NewBuiltin("memory.get", m.get),
			"set":          starlark.NewBuiltin("memory.set", m.set),
			"delete":       starlark.NewBuiltin("memory.delete", m.delete),
			"list_push":    starlark.NewBuiltin("memory.list_push", m.listPush),
			"list_pop":     starlark.NewBuiltin("memory.list_pop", m.listPop),
			"list_len":     starlark.NewBuiltin("memory.list_len", m.listLen),
			"list_range":   starlark.NewBuiltin("memory.list_range", m.listRange),
			"set_add":      starlark.NewBuiltin("memory.set_add", m.setAdd),
			"set_remove":   starlark.NewBuiltin("memory.set_remove", m.setRemove),
			"set_members":  starlark.NewBuiltin("memory.set_members", m.setMembers),
			"set_contains": starlark.NewBuiltin("memory.set_contains", m.setContains),
			"zadd":         starlark.NewBuiltin("memory.zadd", m.zadd),
			"zremove":      starlark.NewBuiltin("memory.zremove", m.zremove),
			"zrange":       starlark.NewBuiltin("memory.zrange", m.zrange),
			"zscore":       starlark.NewBuiltin("memory.zscore", m.zscore),
			"incr":         starlark.NewBuiltin("memory.incr", m.incr),
			"decr":         starlark.NewBuiltin("memory.decr", m.decr),
		},
	}
}

func newMemoryStore() *memoryStore {
	return &memoryStore{sites: map[string]*siteMemory{}}
}

func (s *memoryStore) siteStore(site string) *siteMemory {
	site = normalizedMemorySite(site)
	s.mu.Lock()
	store := s.sites[site]
	if store == nil {
		store = &siteMemory{items: map[string]memoryEntry{}, loaded: s.persistence == nil}
		s.sites[site] = store
	}
	manager := s.persistence
	s.mu.Unlock()
	if manager != nil {
		if err := store.loadSnapshot(site, manager.p); err != nil {
			// Persistence should not make the Starlark memory module unavailable.
			// Keep serving with an empty in-memory store and surface the fault in logs.
			slog.Warn("memory snapshot load failed", "site", site, "error", err)
		}
	}
	return store
}

func (s *memoryStore) wipeSite(site string) {
	site = normalizedMemorySite(site)
	s.mu.Lock()
	delete(s.sites, site)
	manager := s.persistence
	s.mu.Unlock()
	if manager != nil {
		manager.forget(site)
		if err := manager.p.Remove(site); err != nil {
			slog.Warn("memory snapshot remove failed", "site", site, "error", err)
		}
	}
}

func (s *memoryStore) wipeAll() {
	s.mu.Lock()
	s.sites = map[string]*siteMemory{}
	manager := s.persistence
	s.mu.Unlock()
	if manager != nil {
		manager.forgetAll()
		if err := manager.p.RemoveAll(); err != nil {
			slog.Warn("memory snapshots remove all failed", "error", err)
		}
	}
}

func (m *memoryModule) store() *siteMemory { return globalMemory.siteStore(m.site) }

func normalizedMemorySite(site string) string {
	if site == "" {
		return "<unknown>"
	}
	return site
}

func (s *memoryStore) siteNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sites := make([]string, 0, len(s.sites))
	for site := range s.sites {
		sites = append(sites, site)
	}
	sort.Strings(sites)
	return sites
}

func (s *memoryStore) markDirty(site string) {
	manager := s.persistenceManager()
	if manager != nil {
		manager.markDirty(normalizedMemorySite(site))
	}
}

func (s *memoryStore) memoryUsage(site string) int64 {
	site = normalizedMemorySite(site)
	s.mu.Lock()
	store := s.sites[site]
	manager := s.persistence
	if store == nil && manager == nil {
		s.mu.Unlock()
		return 0
	}
	s.mu.Unlock()
	store = s.siteStore(site)
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.used
}

func (s *memoryStore) memoryUsageBySite() map[string]int64 {
	sites := s.siteNames()
	out := make(map[string]int64, len(sites))
	for _, site := range sites {
		out[site] = s.memoryUsage(site)
	}
	return out
}

func (m *memoryModule) usage(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs); err != nil {
		return nil, err
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	return starlark.MakeInt64(store.used), nil
}

func (m *memoryModule) quotaValue(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs); err != nil {
		return nil, err
	}
	return starlark.MakeInt64(m.quota), nil
}

func (m *memoryModule) clear(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs); err != nil {
		return nil, err
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	n := len(store.items)
	if n == 0 {
		return starlark.MakeInt(n), nil
	}
	store.items = map[string]memoryEntry{}
	store.used = 0
	globalMemory.markDirty(m.site)
	return starlark.MakeInt(n), nil
}

func (m *memoryModule) keys(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs); err != nil {
		return nil, err
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	keys := make([]string, 0, len(store.items))
	for key := range store.items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]starlark.Value, 0, len(keys))
	for _, key := range keys {
		out = append(out, starlark.String(key))
	}
	return starlark.NewList(out), nil
}

func (m *memoryModule) items(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs); err != nil {
		return nil, err
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	keys := make([]string, 0, len(store.items))
	for key := range store.items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := starlark.NewDict(len(keys))
	for _, key := range keys {
		_ = out.SetKey(starlark.String(key), starlarkValue(store.items[key]))
	}
	return out, nil
}

func (m *memoryModule) typeOf(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key); err != nil {
		return nil, err
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	if entry, ok := store.items[key]; ok {
		return starlark.String(entry.kind), nil
	}
	return starlark.None, nil
}

func (m *memoryModule) get(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	defaultValue := starlark.Value(starlark.None)
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "default?", &defaultValue); err != nil {
		return nil, err
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	entry, ok := store.items[key]
	if !ok {
		return defaultValue, nil
	}
	if entry.kind != "value" && entry.kind != "counter" {
		return nil, wrongType(fn.Name(), key, entry.kind, "value")
	}
	return starlarkValue(entry), nil
}

func (m *memoryModule) set(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	var value starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "value", &value); err != nil {
		return nil, err
	}
	mv, err := freezeMemoryValue(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn.Name(), err)
	}
	return m.write(key, memoryEntry{kind: "value", value: mv, bytes: entryBytes(key, "value", mv.size)})
}

func (m *memoryModule) delete(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key); err != nil {
		return nil, err
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	entry, ok := store.items[key]
	if ok {
		delete(store.items, key)
		store.used -= entry.bytes
		globalMemory.markDirty(m.site)
	}
	return starlark.Bool(ok), nil
}

func (m *memoryModule) listPush(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	var value starlark.Value
	side := "right"
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "value", &value, "side?", &side); err != nil {
		return nil, err
	}
	mv, err := freezeMemoryValue(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn.Name(), err)
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	list, old, err := collection[[]memoryValue](fn.Name(), store, key, "list")
	if err != nil {
		return nil, err
	}
	next := append([]memoryValue(nil), list...)
	switch side {
	case "left":
		next = append([]memoryValue{mv}, next...)
	case "right":
		next = append(next, mv)
	default:
		return nil, fmt.Errorf("%s: side must be left or right", fn.Name())
	}
	entry := listEntry(key, next)
	if !m.replace(store, key, old, entry) {
		return starlark.False, nil
	}
	return starlark.MakeInt(len(next)), nil
}

func (m *memoryModule) listPop(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	side := "right"
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "side?", &side); err != nil {
		return nil, err
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	list, old, err := collection[[]memoryValue](fn.Name(), store, key, "list")
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return starlark.None, nil
	}
	var out memoryValue
	next := append([]memoryValue(nil), list...)
	switch side {
	case "left":
		out = next[0]
		next = next[1:]
	case "right":
		out = next[len(next)-1]
		next = next[:len(next)-1]
	default:
		return nil, fmt.Errorf("%s: side must be left or right", fn.Name())
	}
	m.mustReplace(store, key, old, listEntry(key, next))
	return valueToStarlark(out), nil
}

func (m *memoryModule) listLen(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key); err != nil {
		return nil, err
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	list, _, err := collection[[]memoryValue](fn.Name(), store, key, "list")
	if err != nil {
		return nil, err
	}
	return starlark.MakeInt(len(list)), nil
}

func (m *memoryModule) listRange(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	start, end := 0, -1
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "start?", &start, "end?", &end); err != nil {
		return nil, err
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	list, _, err := collection[[]memoryValue](fn.Name(), store, key, "list")
	if err != nil {
		return nil, err
	}
	lo, hi := bounds(start, end, len(list))
	out := make([]starlark.Value, 0, hi-lo)
	for _, value := range list[lo:hi] {
		out = append(out, valueToStarlark(value))
	}
	return starlark.NewList(out), nil
}

func (m *memoryModule) setAdd(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	var value starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "value", &value); err != nil {
		return nil, err
	}
	mv, err := freezeMemoryValue(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn.Name(), err)
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	set, old, err := collection[map[string]memoryValue](fn.Name(), store, key, "set")
	if err != nil {
		return nil, err
	}
	next := map[string]memoryValue{}
	for k, v := range set {
		next[k] = v
	}
	added := false
	if _, ok := next[mvKey(mv)]; !ok {
		next[mvKey(mv)] = mv
		added = true
	}
	if !added {
		return starlark.False, nil
	}
	entry := setEntry(key, next)
	if !m.replace(store, key, old, entry) {
		return starlark.False, nil
	}
	return starlark.Bool(added), nil
}

func (m *memoryModule) setRemove(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	var value starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "value", &value); err != nil {
		return nil, err
	}
	mv, err := freezeMemoryValue(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn.Name(), err)
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	set, old, err := collection[map[string]memoryValue](fn.Name(), store, key, "set")
	if err != nil {
		return nil, err
	}
	if old.kind == "" {
		return starlark.False, nil
	}
	next := map[string]memoryValue{}
	for k, v := range set {
		next[k] = v
	}
	_, removed := next[mvKey(mv)]
	if !removed {
		return starlark.False, nil
	}
	delete(next, mvKey(mv))
	m.mustReplace(store, key, old, setEntry(key, next))
	return starlark.Bool(removed), nil
}

func (m *memoryModule) setMembers(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key); err != nil {
		return nil, err
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	set, _, err := collection[map[string]memoryValue](fn.Name(), store, key, "set")
	if err != nil {
		return nil, err
	}
	keys := sortedKeys(set)
	out := make([]starlark.Value, 0, len(keys))
	for _, k := range keys {
		out = append(out, valueToStarlark(set[k]))
	}
	return starlark.NewList(out), nil
}

func (m *memoryModule) setContains(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	var value starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "value", &value); err != nil {
		return nil, err
	}
	mv, err := freezeMemoryValue(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn.Name(), err)
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	set, _, err := collection[map[string]memoryValue](fn.Name(), store, key, "set")
	if err != nil {
		return nil, err
	}
	_, ok := set[mvKey(mv)]
	return starlark.Bool(ok), nil
}

func (m *memoryModule) zadd(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	var score float64
	var value starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "score", &score, "value", &value); err != nil {
		return nil, err
	}
	if math.IsInf(score, 0) || math.IsNaN(score) {
		return nil, fmt.Errorf("%s: score must be finite", fn.Name())
	}
	mv, err := freezeMemoryValue(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn.Name(), err)
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	zset, old, err := collection[map[string]zmember](fn.Name(), store, key, "zset")
	if err != nil {
		return nil, err
	}
	next := map[string]zmember{}
	for k, v := range zset {
		next[k] = v
	}
	_, existed := next[mvKey(mv)]
	next[mvKey(mv)] = zmember{value: mv, score: score}
	entry := zsetEntry(key, next)
	if !m.replace(store, key, old, entry) {
		return starlark.False, nil
	}
	return starlark.Bool(!existed), nil
}

func (m *memoryModule) zremove(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	var value starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "value", &value); err != nil {
		return nil, err
	}
	mv, err := freezeMemoryValue(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn.Name(), err)
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	zset, old, err := collection[map[string]zmember](fn.Name(), store, key, "zset")
	if err != nil {
		return nil, err
	}
	if old.kind == "" {
		return starlark.False, nil
	}
	next := map[string]zmember{}
	for k, v := range zset {
		next[k] = v
	}
	_, removed := next[mvKey(mv)]
	if !removed {
		return starlark.False, nil
	}
	delete(next, mvKey(mv))
	m.mustReplace(store, key, old, zsetEntry(key, next))
	return starlark.Bool(removed), nil
}

func (m *memoryModule) zrange(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	start, end := 0, -1
	withScores := false
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "start?", &start, "end?", &end, "with_scores?", &withScores); err != nil {
		return nil, err
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	zset, _, err := collection[map[string]zmember](fn.Name(), store, key, "zset")
	if err != nil {
		return nil, err
	}
	members := sortedZMembers(zset)
	lo, hi := bounds(start, end, len(members))
	out := make([]starlark.Value, 0, hi-lo)
	for _, member := range members[lo:hi] {
		value := valueToStarlark(member.value)
		if withScores {
			value = starlark.Tuple{value, starlark.Float(member.score)}
		}
		out = append(out, value)
	}
	return starlark.NewList(out), nil
}

func (m *memoryModule) zscore(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	var value starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "value", &value); err != nil {
		return nil, err
	}
	mv, err := freezeMemoryValue(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn.Name(), err)
	}
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	zset, _, err := collection[map[string]zmember](fn.Name(), store, key, "zset")
	if err != nil {
		return nil, err
	}
	if member, ok := zset[mvKey(mv)]; ok {
		return starlark.Float(member.score), nil
	}
	return starlark.None, nil
}

func (m *memoryModule) incr(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	delta := int64(1)
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "delta?", &delta); err != nil {
		return nil, err
	}
	return m.addCounter(fn.Name(), key, delta)
}

func (m *memoryModule) decr(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key string
	delta := int64(1)
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "delta?", &delta); err != nil {
		return nil, err
	}
	return m.addCounter(fn.Name(), key, -delta)
}

func (m *memoryModule) addCounter(name, key string, delta int64) (starlark.Value, error) {
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	var current int64
	old := memoryEntry{}
	if entry, ok := store.items[key]; ok {
		if entry.kind != "counter" {
			return nil, wrongType(name, key, entry.kind, "counter")
		}
		old = entry
		current = entry.value.(int64)
	}
	if (delta > 0 && current > math.MaxInt64-delta) || (delta < 0 && current < math.MinInt64-delta) {
		return nil, fmt.Errorf("%s: counter overflow", name)
	}
	next := current + delta
	entry := counterEntry(key, next)
	if !m.replace(store, key, old, entry) {
		return starlark.None, nil
	}
	return starlark.MakeInt64(next), nil
}

func (m *memoryModule) write(key string, entry memoryEntry) (starlark.Value, error) {
	store := m.store()
	store.mu.Lock()
	defer store.mu.Unlock()
	old := store.items[key]
	if !m.replace(store, key, old, entry) {
		return starlark.False, nil
	}
	return starlark.True, nil
}

func collection[T any](name string, store *siteMemory, key, kind string) (T, memoryEntry, error) {
	var zero T
	entry, ok := store.items[key]
	if !ok {
		return zero, memoryEntry{}, nil
	}
	if entry.kind != kind {
		return zero, memoryEntry{}, wrongType(name, key, entry.kind, kind)
	}
	return entry.value.(T), entry, nil
}

func (m *memoryModule) replace(store *siteMemory, key string, old, next memoryEntry) bool {
	newUsed := store.used - old.bytes + next.bytes
	if m.quota >= 0 && newUsed > m.quota {
		return false
	}
	store.items[key] = next
	store.used = newUsed
	globalMemory.markDirty(m.site)
	return true
}

func (m *memoryModule) mustReplace(store *siteMemory, key string, old, next memoryEntry) {
	store.items[key] = next
	store.used = store.used - old.bytes + next.bytes
	globalMemory.markDirty(m.site)
}

func freezeMemoryValue(value starlark.Value) (memoryValue, error) {
	switch v := value.(type) {
	case starlark.NoneType:
		return memoryValue{kind: "none", size: 1}, nil
	case starlark.Bool:
		return memoryValue{kind: "bool", data: bool(v), size: 2}, nil
	case starlark.Int:
		s := v.String()
		return memoryValue{kind: "int", data: s, size: int64(len(s)) + 1}, nil
	case starlark.Float:
		f := float64(v)
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return memoryValue{}, fmt.Errorf("floats must be finite")
		}
		return memoryValue{kind: "float", data: f, size: 9}, nil
	case starlark.String:
		s := string(v)
		return memoryValue{kind: "string", data: s, size: int64(len(s)) + 1}, nil
	case starlark.Bytes:
		b := string(v)
		return memoryValue{kind: "bytes", data: b, size: int64(len(b)) + 1}, nil
	case *starlark.List:
		out := make([]memoryValue, 0, v.Len())
		var elem starlark.Value
		var size int64 = 1
		for i := 0; i < v.Len(); i++ {
			elem = v.Index(i)
			mv, err := freezeMemoryValue(elem)
			if err != nil {
				return memoryValue{}, err
			}
			out = append(out, mv)
			size += mv.size + 1
		}
		return memoryValue{kind: "list", data: out, size: size}, nil
	case starlark.Tuple:
		out := make([]memoryValue, 0, len(v))
		var size int64 = 1
		for _, elem := range v {
			mv, err := freezeMemoryValue(elem)
			if err != nil {
				return memoryValue{}, err
			}
			out = append(out, mv)
			size += mv.size + 1
		}
		return memoryValue{kind: "tuple", data: out, size: size}, nil
	case *starlark.Dict:
		pairs := make([]memoryValue, 0, v.Len()*2)
		var size int64 = 1
		for _, item := range v.Items() {
			k, err := freezeMemoryValue(item[0])
			if err != nil {
				return memoryValue{}, err
			}
			val, err := freezeMemoryValue(item[1])
			if err != nil {
				return memoryValue{}, err
			}
			pairs = append(pairs, k, val)
			size += k.size + val.size + 2
		}
		return memoryValue{kind: "dict", data: pairs, size: size}, nil
	default:
		return memoryValue{}, fmt.Errorf("unsupported value type %s", value.Type())
	}
}

func starlarkValue(entry memoryEntry) starlark.Value {
	switch entry.kind {
	case "value":
		return valueToStarlark(entry.value.(memoryValue))
	case "counter":
		return starlark.MakeInt64(entry.value.(int64))
	case "list":
		return memoryListToStarlark(entry.value.([]memoryValue))
	case "set":
		return memorySetToStarlark(entry.value.(map[string]memoryValue))
	case "zset":
		return memoryZSetToStarlark(entry.value.(map[string]zmember))
	default:
		return starlark.None
	}
}

func valueToStarlark(v memoryValue) starlark.Value {
	switch v.kind {
	case "none":
		return starlark.None
	case "bool":
		return starlark.Bool(v.data.(bool))
	case "int":
		i, ok := new(big.Int).SetString(v.data.(string), 10)
		if !ok {
			return starlark.MakeInt(0)
		}
		return starlark.MakeBigInt(i)
	case "float":
		return starlark.Float(v.data.(float64))
	case "string":
		return starlark.String(v.data.(string))
	case "bytes":
		return starlark.Bytes(v.data.(string))
	case "list":
		return memoryListToStarlark(v.data.([]memoryValue))
	case "tuple":
		values := v.data.([]memoryValue)
		out := make(starlark.Tuple, 0, len(values))
		for _, value := range values {
			out = append(out, valueToStarlark(value))
		}
		return out
	case "dict":
		pairs := v.data.([]memoryValue)
		out := starlark.NewDict(len(pairs) / 2)
		for i := 0; i+1 < len(pairs); i += 2 {
			_ = out.SetKey(valueToStarlark(pairs[i]), valueToStarlark(pairs[i+1]))
		}
		return out
	default:
		return starlark.None
	}
}

func memoryListToStarlark(values []memoryValue) *starlark.List {
	out := make([]starlark.Value, 0, len(values))
	for _, value := range values {
		out = append(out, valueToStarlark(value))
	}
	return starlark.NewList(out)
}

func memorySetToStarlark(values map[string]memoryValue) *starlark.Set {
	out := starlark.NewSet(len(values))
	for _, key := range sortedKeys(values) {
		_ = out.Insert(valueToStarlark(values[key]))
	}
	return out
}

func memoryZSetToStarlark(values map[string]zmember) *starlark.List {
	members := sortedZMembers(values)
	out := make([]starlark.Value, 0, len(members))
	for _, member := range members {
		out = append(out, starlark.Tuple{valueToStarlark(member.value), starlark.Float(member.score)})
	}
	return starlark.NewList(out)
}

func entryBytes(key, kind string, valueBytes int64) int64 {
	return int64(len(key)+len(kind)) + valueBytes + 2
}

func listEntry(key string, values []memoryValue) memoryEntry {
	var size int64 = 1
	for _, value := range values {
		size += value.size + 1
	}
	return memoryEntry{kind: "list", value: values, bytes: entryBytes(key, "list", size)}
}

func setEntry(key string, values map[string]memoryValue) memoryEntry {
	var size int64 = 1
	for _, value := range values {
		size += value.size + 1
	}
	return memoryEntry{kind: "set", value: values, bytes: entryBytes(key, "set", size)}
}

func zsetEntry(key string, values map[string]zmember) memoryEntry {
	var size int64 = 1
	for _, value := range values {
		size += value.value.size + 9
	}
	return memoryEntry{kind: "zset", value: values, bytes: entryBytes(key, "zset", size)}
}

func counterEntry(key string, value int64) memoryEntry {
	s := strconv.FormatInt(value, 10)
	return memoryEntry{kind: "counter", value: value, bytes: entryBytes(key, "counter", int64(len(s))+1)}
}

func mvKey(value memoryValue) string {
	switch value.kind {
	case "none":
		return "n:"
	case "bool":
		if value.data.(bool) {
			return "b:1"
		}
		return "b:0"
	case "int", "string", "bytes":
		return value.kind + ":" + value.data.(string)
	case "float":
		return "f:" + strconv.FormatFloat(value.data.(float64), 'g', -1, 64)
	case "list", "tuple":
		values := value.data.([]memoryValue)
		out := value.kind + ":["
		for _, elem := range values {
			out += mvKey(elem) + ","
		}
		return out + "]"
	case "dict":
		values := value.data.([]memoryValue)
		out := "dict:{"
		for _, elem := range values {
			out += mvKey(elem) + ","
		}
		return out + "}"
	default:
		return value.kind + ":"
	}
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedZMembers(values map[string]zmember) []zmember {
	keys := sortedKeys(values)
	members := make([]zmember, 0, len(keys))
	for _, key := range keys {
		members = append(members, values[key])
	}
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].score == members[j].score {
			return mvKey(members[i].value) < mvKey(members[j].value)
		}
		return members[i].score < members[j].score
	})
	return members
}

func bounds(start, end, length int) (int, int) {
	if start < 0 {
		start = length + start
	}
	if end < 0 {
		end = length + end
	}
	if start < 0 {
		start = 0
	}
	if end >= length {
		end = length - 1
	}
	if length == 0 || start > end || start >= length {
		return 0, 0
	}
	return start, end + 1
}

func wrongType(name, key, got, want string) error {
	return fmt.Errorf("%s: key %q contains %s, want %s", name, key, got, want)
}
