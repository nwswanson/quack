package modules

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const memorySnapshotVersion = 1

type MemorySaveRule struct {
	After   time.Duration
	Changes int64
}

type MemoryPersistenceConfig struct {
	Mode                 string
	Directory            string
	SaveRules            []MemorySaveRule
	MinInterval          time.Duration
	MaxConcurrency       int
	ShutdownFlushTimeout time.Duration
	CheckInterval        time.Duration
	Now                  func() time.Time
}

type memorySnapshot struct {
	Version int                            `json:"version"`
	Site    string                         `json:"site"`
	SiteSHA string                         `json:"site_sha"`
	Used    int64                          `json:"used"`
	SavedAt string                         `json:"saved_at"`
	Items   map[string]memorySnapshotEntry `json:"items"`
}

type memorySnapshotEntry struct {
	Kind    string                  `json:"kind"`
	Value   *memorySnapshotValue    `json:"value,omitempty"`
	Counter *int64                  `json:"counter,omitempty"`
	Values  []memorySnapshotValue   `json:"values,omitempty"`
	ZValues []memorySnapshotZMember `json:"z_values,omitempty"`
	Bytes   int64                   `json:"bytes"`
}

type memorySnapshotValue struct {
	Kind   string                `json:"kind"`
	Bool   bool                  `json:"bool,omitempty"`
	Int    string                `json:"int,omitempty"`
	Float  float64               `json:"float,omitempty"`
	String string                `json:"string,omitempty"`
	Bytes  string                `json:"bytes,omitempty"`
	Values []memorySnapshotValue `json:"values,omitempty"`
	Size   int64                 `json:"size"`
}

type memorySnapshotZMember struct {
	Value memorySnapshotValue `json:"value"`
	Score float64             `json:"score"`
}

type memorySnapshotPersistence struct {
	dir string
	now func() time.Time
}

type memoryPersistenceManager struct {
	store *memoryStore
	p     *memorySnapshotPersistence

	mu              sync.Mutex
	dirty           map[string]*memoryDirtyState
	rules           []MemorySaveRule
	minInterval     time.Duration
	checkEvery      time.Duration
	shutdownTimeout time.Duration
	sem             chan struct{}
	writers         sync.WaitGroup
	cancel          context.CancelFunc
	now             func() time.Time
}

type memoryDirtyState struct {
	changes    int64
	firstDirty time.Time
	lastSave   time.Time
	saving     bool
}

func defaultMemoryPersistenceConfig() MemoryPersistenceConfig {
	return MemoryPersistenceConfig{
		Mode:                 "off",
		SaveRules:            []MemorySaveRule{{After: time.Minute, Changes: 1}, {After: 15 * time.Second, Changes: 100}, {After: 10 * time.Second, Changes: 1000}},
		MinInterval:          10 * time.Second,
		MaxConcurrency:       1,
		ShutdownFlushTimeout: 5 * time.Second,
		CheckInterval:        time.Second,
		Now:                  time.Now,
	}
}

func ConfigureMemoryPersistence(config MemoryPersistenceConfig) error {
	return globalMemory.configurePersistence(config)
}

func ShutdownMemoryPersistence(ctx context.Context) error {
	return globalMemory.shutdownPersistence(ctx)
}

func FlushMemorySnapshots(ctx context.Context) error {
	return globalMemory.flushSnapshots(ctx)
}

func (s *memoryStore) configurePersistence(config MemoryPersistenceConfig) error {
	defaults := defaultMemoryPersistenceConfig()
	if strings.TrimSpace(config.Mode) == "" {
		config.Mode = defaults.Mode
	}
	config.Mode = strings.ToLower(strings.TrimSpace(config.Mode))
	if config.Now == nil {
		config.Now = defaults.Now
	}
	if len(config.SaveRules) == 0 {
		config.SaveRules = defaults.SaveRules
	}
	if config.MinInterval < 0 {
		return fmt.Errorf("memory persistence min interval must be >= 0")
	}
	if config.MinInterval == 0 {
		config.MinInterval = defaults.MinInterval
	}
	if config.MaxConcurrency <= 0 {
		config.MaxConcurrency = defaults.MaxConcurrency
	}
	if config.CheckInterval <= 0 {
		config.CheckInterval = defaults.CheckInterval
	}
	if config.ShutdownFlushTimeout <= 0 {
		config.ShutdownFlushTimeout = defaults.ShutdownFlushTimeout
	}
	rules := normalizeMemorySaveRules(config.SaveRules)

	s.mu.Lock()
	old := s.persistence
	s.persistence = nil
	s.mu.Unlock()
	if old != nil {
		old.stop()
	}

	switch config.Mode {
	case "off", "disabled", "none":
		return nil
	case "snapshot", "rdb":
	default:
		return fmt.Errorf("unsupported memory persistence mode %q", config.Mode)
	}
	if strings.TrimSpace(config.Directory) == "" {
		return fmt.Errorf("memory persistence directory is required for snapshot mode")
	}
	if err := os.MkdirAll(config.Directory, 0o700); err != nil {
		return fmt.Errorf("create memory persistence directory: %w", err)
	}
	manager := &memoryPersistenceManager{
		store:           s,
		p:               &memorySnapshotPersistence{dir: config.Directory, now: config.Now},
		dirty:           map[string]*memoryDirtyState{},
		rules:           rules,
		minInterval:     config.MinInterval,
		checkEvery:      config.CheckInterval,
		shutdownTimeout: config.ShutdownFlushTimeout,
		sem:             make(chan struct{}, config.MaxConcurrency),
		now:             config.Now,
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager.cancel = cancel
	s.mu.Lock()
	s.persistence = manager
	s.mu.Unlock()
	go manager.loop(ctx)
	return nil
}

func (s *memoryStore) persistenceManager() *memoryPersistenceManager {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistence
}

func (s *memoryStore) shutdownPersistence(ctx context.Context) error {
	manager := s.persistenceManager()
	if manager == nil {
		return nil
	}
	manager.stop()
	return manager.flush(ctx)
}

func (s *memoryStore) flushSnapshots(ctx context.Context) error {
	manager := s.persistenceManager()
	if manager == nil {
		return nil
	}
	return manager.flush(ctx)
}

func (m *memoryPersistenceManager) stop() {
	if m.cancel != nil {
		m.cancel()
	}
	done := make(chan struct{})
	go func() {
		m.writers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(m.shutdownTimeout):
	}
}

func (m *memoryPersistenceManager) loop(ctx context.Context) {
	ticker := time.NewTicker(m.checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.checkDue()
		case <-ctx.Done():
			return
		}
	}
}

func (m *memoryPersistenceManager) markDirty(site string) {
	now := m.now()
	m.mu.Lock()
	state := m.dirty[site]
	if state == nil {
		state = &memoryDirtyState{}
		m.dirty[site] = state
	}
	if state.changes == 0 {
		state.firstDirty = now
	}
	state.changes++
	shouldSave := m.siteDueLocked(site, state, now)
	if shouldSave {
		state.saving = true
	}
	m.mu.Unlock()
	if shouldSave {
		m.saveAsync(site)
	}
}

func (m *memoryPersistenceManager) checkDue() {
	now := m.now()
	var due []string
	m.mu.Lock()
	for site, state := range m.dirty {
		if m.siteDueLocked(site, state, now) {
			state.saving = true
			due = append(due, site)
		}
	}
	m.mu.Unlock()
	for _, site := range due {
		m.saveAsync(site)
	}
}

func (m *memoryPersistenceManager) siteDueLocked(site string, state *memoryDirtyState, now time.Time) bool {
	_ = site
	if state == nil || state.saving || state.changes == 0 {
		return false
	}
	if !state.lastSave.IsZero() && now.Sub(state.lastSave) < m.minInterval {
		return false
	}
	for _, rule := range m.rules {
		if state.changes >= rule.Changes && now.Sub(state.firstDirty) >= rule.After {
			return true
		}
	}
	return false
}

func (m *memoryPersistenceManager) saveAsync(site string) {
	m.writers.Add(1)
	go func() {
		defer m.writers.Done()
		select {
		case m.sem <- struct{}{}:
			defer func() { <-m.sem }()
		default:
			m.sem <- struct{}{}
			defer func() { <-m.sem }()
		}
		err := m.saveSite(site)
		m.finishSave(site, err)
		if err != nil {
			slog.Warn("memory snapshot save failed", "site", site, "error", err)
		}
	}()
}

func (m *memoryPersistenceManager) finishSave(site string, err error) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.dirty[site]
	if state == nil {
		return
	}
	state.saving = false
	if err != nil {
		return
	}
	state.lastSave = now
	state.changes = 0
	state.firstDirty = time.Time{}
}

func (m *memoryPersistenceManager) forget(site string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dirty, site)
}

func (m *memoryPersistenceManager) forgetAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dirty = map[string]*memoryDirtyState{}
}

func (m *memoryPersistenceManager) flush(ctx context.Context) error {
	sites := m.store.siteNames()
	var errs []error
	for _, site := range sites {
		select {
		case <-ctx.Done():
			errs = append(errs, ctx.Err())
			return errors.Join(errs...)
		default:
		}
		if err := m.saveSite(site); err != nil {
			errs = append(errs, err)
		} else {
			m.finishSave(site, nil)
		}
	}
	return errors.Join(errs...)
}

func (m *memoryPersistenceManager) saveSite(site string) error {
	store := m.store.siteStore(site)
	snapshot, err := store.snapshot(site, m.now())
	if err != nil {
		return err
	}
	return m.p.Save(site, snapshot)
}

func normalizeMemorySaveRules(rules []MemorySaveRule) []MemorySaveRule {
	out := make([]MemorySaveRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Changes <= 0 || rule.After < 0 {
			continue
		}
		out = append(out, rule)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].After == out[j].After {
			return out[i].Changes < out[j].Changes
		}
		return out[i].After < out[j].After
	})
	if len(out) == 0 {
		return defaultMemoryPersistenceConfig().SaveRules
	}
	return out
}

func (p *memorySnapshotPersistence) Save(site string, snapshot memorySnapshot) error {
	dir := filepath.Join(p.dir, "site:"+siteHash(site))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create memory snapshot site dir: %w", err)
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode memory snapshot: %w", err)
	}
	data = append(data, '\n')
	return writeFileAtomic(filepath.Join(dir, "snapshot.json"), data, 0o600)
}

func (p *memorySnapshotPersistence) Load(site string) (memorySnapshot, bool, error) {
	path := filepath.Join(p.dir, "site:"+siteHash(site), "snapshot.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return memorySnapshot{}, false, nil
		}
		return memorySnapshot{}, false, fmt.Errorf("read memory snapshot: %w", err)
	}
	var snapshot memorySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return memorySnapshot{}, false, fmt.Errorf("decode memory snapshot: %w", err)
	}
	if snapshot.Version != memorySnapshotVersion {
		return memorySnapshot{}, false, fmt.Errorf("unsupported memory snapshot version %d", snapshot.Version)
	}
	if snapshot.Site != "" && snapshot.Site != site {
		return memorySnapshot{}, false, fmt.Errorf("memory snapshot site mismatch: got %q want %q", snapshot.Site, site)
	}
	return snapshot, true, nil
}

func (p *memorySnapshotPersistence) Remove(site string) error {
	err := os.RemoveAll(filepath.Join(p.dir, "site:"+siteHash(site)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDir(p.dir)
}

func (p *memorySnapshotPersistence) RemoveAll() error {
	entries, err := os.ReadDir(p.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "site:") {
			if err := os.RemoveAll(filepath.Join(p.dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return syncDir(p.dir)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name, err := tempSnapshotName(path)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, name)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	removeTmp = false
	if err := syncDir(dir); err != nil {
		return err
	}
	return nil
}

func tempSnapshotName(path string) (string, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf(".%s.tmp.%d.%s", filepath.Base(path), os.Getpid(), hex.EncodeToString(nonce[:])), nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func siteHash(site string) string {
	sum := sha256.Sum256([]byte(site))
	return hex.EncodeToString(sum[:])
}

func (s *siteMemory) snapshot(site string, now time.Time) (memorySnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make(map[string]memorySnapshotEntry, len(s.items))
	keys := sortedKeys(s.items)
	for _, key := range keys {
		entry, err := snapshotEntry(s.items[key])
		if err != nil {
			return memorySnapshot{}, fmt.Errorf("snapshot key %q: %w", key, err)
		}
		items[key] = entry
	}
	return memorySnapshot{
		Version: memorySnapshotVersion,
		Site:    site,
		SiteSHA: siteHash(site),
		Used:    s.used,
		SavedAt: now.UTC().Format(time.RFC3339Nano),
		Items:   items,
	}, nil
}

func (s *siteMemory) loadSnapshot(site string, p *memorySnapshotPersistence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return nil
	}
	s.loaded = true
	snapshot, ok, err := p.Load(site)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	items := make(map[string]memoryEntry, len(snapshot.Items))
	var used int64
	keys := make([]string, 0, len(snapshot.Items))
	for key := range snapshot.Items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry, err := memoryEntryFromSnapshot(key, snapshot.Items[key])
		if err != nil {
			return fmt.Errorf("load key %q: %w", key, err)
		}
		items[key] = entry
		used += entry.bytes
	}
	s.items = items
	s.used = used
	return nil
}

func snapshotEntry(entry memoryEntry) (memorySnapshotEntry, error) {
	out := memorySnapshotEntry{Kind: entry.kind, Bytes: entry.bytes}
	switch entry.kind {
	case "value":
		value, err := snapshotValue(entry.value.(memoryValue))
		if err != nil {
			return memorySnapshotEntry{}, err
		}
		out.Value = &value
	case "counter":
		n := entry.value.(int64)
		out.Counter = &n
	case "list":
		values, err := snapshotValues(entry.value.([]memoryValue))
		if err != nil {
			return memorySnapshotEntry{}, err
		}
		out.Values = values
	case "set":
		set := entry.value.(map[string]memoryValue)
		keys := sortedKeys(set)
		out.Values = make([]memorySnapshotValue, 0, len(keys))
		for _, key := range keys {
			value, err := snapshotValue(set[key])
			if err != nil {
				return memorySnapshotEntry{}, err
			}
			out.Values = append(out.Values, value)
		}
	case "zset":
		members := sortedZMembers(entry.value.(map[string]zmember))
		out.ZValues = make([]memorySnapshotZMember, 0, len(members))
		for _, member := range members {
			value, err := snapshotValue(member.value)
			if err != nil {
				return memorySnapshotEntry{}, err
			}
			out.ZValues = append(out.ZValues, memorySnapshotZMember{Value: value, Score: member.score})
		}
	default:
		return memorySnapshotEntry{}, fmt.Errorf("unsupported memory kind %q", entry.kind)
	}
	return out, nil
}

func snapshotValues(values []memoryValue) ([]memorySnapshotValue, error) {
	out := make([]memorySnapshotValue, 0, len(values))
	for _, value := range values {
		snapshot, err := snapshotValue(value)
		if err != nil {
			return nil, err
		}
		out = append(out, snapshot)
	}
	return out, nil
}

func snapshotValue(value memoryValue) (memorySnapshotValue, error) {
	out := memorySnapshotValue{Kind: value.kind, Size: value.size}
	switch value.kind {
	case "none":
	case "bool":
		out.Bool = value.data.(bool)
	case "int":
		out.Int = value.data.(string)
	case "float":
		out.Float = value.data.(float64)
	case "string":
		out.String = value.data.(string)
	case "bytes":
		out.Bytes = base64.StdEncoding.EncodeToString([]byte(value.data.(string)))
	case "list", "tuple", "dict":
		values, err := snapshotValues(value.data.([]memoryValue))
		if err != nil {
			return memorySnapshotValue{}, err
		}
		out.Values = values
	default:
		return memorySnapshotValue{}, fmt.Errorf("unsupported value kind %q", value.kind)
	}
	return out, nil
}

func memoryEntryFromSnapshot(key string, entry memorySnapshotEntry) (memoryEntry, error) {
	switch entry.Kind {
	case "value":
		if entry.Value == nil {
			return memoryEntry{}, fmt.Errorf("value entry missing value")
		}
		value, err := memoryValueFromSnapshot(*entry.Value)
		if err != nil {
			return memoryEntry{}, err
		}
		return memoryEntry{kind: "value", value: value, bytes: entryBytes(key, "value", value.size)}, nil
	case "counter":
		if entry.Counter == nil {
			return memoryEntry{}, fmt.Errorf("counter entry missing counter")
		}
		return counterEntry(key, *entry.Counter), nil
	case "list":
		values, err := memoryValuesFromSnapshot(entry.Values)
		if err != nil {
			return memoryEntry{}, err
		}
		return listEntry(key, values), nil
	case "set":
		values, err := memoryValuesFromSnapshot(entry.Values)
		if err != nil {
			return memoryEntry{}, err
		}
		set := make(map[string]memoryValue, len(values))
		for _, value := range values {
			set[mvKey(value)] = value
		}
		return setEntry(key, set), nil
	case "zset":
		zset := make(map[string]zmember, len(entry.ZValues))
		for _, snapshot := range entry.ZValues {
			value, err := memoryValueFromSnapshot(snapshot.Value)
			if err != nil {
				return memoryEntry{}, err
			}
			zset[mvKey(value)] = zmember{value: value, score: snapshot.Score}
		}
		return zsetEntry(key, zset), nil
	default:
		return memoryEntry{}, fmt.Errorf("unsupported memory kind %q", entry.Kind)
	}
}

func memoryValuesFromSnapshot(values []memorySnapshotValue) ([]memoryValue, error) {
	out := make([]memoryValue, 0, len(values))
	for _, value := range values {
		mv, err := memoryValueFromSnapshot(value)
		if err != nil {
			return nil, err
		}
		out = append(out, mv)
	}
	return out, nil
}

func memoryValueFromSnapshot(value memorySnapshotValue) (memoryValue, error) {
	out := memoryValue{kind: value.Kind, size: value.Size}
	switch value.Kind {
	case "none":
	case "bool":
		out.data = value.Bool
	case "int":
		out.data = value.Int
	case "float":
		out.data = value.Float
	case "string":
		out.data = value.String
	case "bytes":
		data, err := base64.StdEncoding.DecodeString(value.Bytes)
		if err != nil {
			return memoryValue{}, fmt.Errorf("decode bytes value: %w", err)
		}
		out.data = string(data)
	case "list", "tuple", "dict":
		values, err := memoryValuesFromSnapshot(value.Values)
		if err != nil {
			return memoryValue{}, err
		}
		out.data = values
	default:
		return memoryValue{}, fmt.Errorf("unsupported value kind %q", value.Kind)
	}
	return out, nil
}
