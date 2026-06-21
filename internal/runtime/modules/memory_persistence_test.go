package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.starlark.net/starlark"
)

func TestMemorySnapshotRoundTripAllKinds(t *testing.T) {
	resetGlobalMemory(t)
	dir := t.TempDir()
	if err := ConfigureMemoryPersistence(MemoryPersistenceConfig{
		Mode:        "snapshot",
		Directory:   dir,
		SaveRules:   []MemorySaveRule{{After: time.Hour, Changes: 1}},
		MinInterval: time.Nanosecond,
	}); err != nil {
		t.Fatal(err)
	}

	site := "example.com"
	m := &memoryModule{site: site, quota: 1 << 20}
	callMemory(t, m.set, "memory.set", starlark.String("plain"), starlark.Tuple{
		starlark.None,
		starlark.MakeInt(42),
		starlark.Bytes("raw"),
	})
	rawBytes := string([]byte{0xff, 0x00, 'x'})
	callMemory(t, m.set, "memory.set", starlark.String("raw_bytes"), starlark.Bytes(rawBytes))
	callMemory(t, m.listPush, "memory.list_push", starlark.String("events"), starlark.String("first"))
	callMemory(t, m.listPush, "memory.list_push", starlark.String("events"), starlark.String("second"))
	callMemory(t, m.setAdd, "memory.set_add", starlark.String("tags"), starlark.String("blue"))
	callMemory(t, m.setAdd, "memory.set_add", starlark.String("tags"), starlark.String("green"))
	callMemory(t, m.zadd, "memory.zadd", starlark.String("scores"), starlark.Float(2), starlark.String("b"))
	callMemory(t, m.zadd, "memory.zadd", starlark.String("scores"), starlark.Float(1), starlark.String("a"))
	callMemory(t, m.incr, "memory.incr", starlark.String("count"), starlark.MakeInt64(3))

	if err := FlushMemorySnapshots(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(dir, "site:"+siteHash(site), "snapshot.json")
	if _, err := os.Stat(snapshotPath); err != nil {
		t.Fatalf("snapshot stat error = %v", err)
	}

	globalMemory = newMemoryStore()
	if err := ConfigureMemoryPersistence(MemoryPersistenceConfig{
		Mode:        "snapshot",
		Directory:   dir,
		SaveRules:   []MemorySaveRule{{After: time.Hour, Changes: 1}},
		MinInterval: time.Nanosecond,
	}); err != nil {
		t.Fatal(err)
	}
	loaded := globalMemory.siteStore(site)
	loaded.mu.Lock()
	defer loaded.mu.Unlock()
	if got, want := len(loaded.items), 6; got != want {
		t.Fatalf("loaded item count = %d, want %d", got, want)
	}
	if got := valueToStarlark(loaded.items["plain"].value.(memoryValue)).String(); got != `(None, 42, b"raw")` {
		t.Fatalf("plain = %s", got)
	}
	if got := starlarkValue(loaded.items["events"]).String(); got != `["first", "second"]` {
		t.Fatalf("events = %s", got)
	}
	if got := starlarkValue(loaded.items["tags"]).String(); got != `set(["blue", "green"])` {
		t.Fatalf("tags = %s", got)
	}
	if got := starlarkValue(loaded.items["scores"]).String(); got != `[("a", 1.0), ("b", 2.0)]` {
		t.Fatalf("scores = %s", got)
	}
	if got := loaded.items["count"].value.(int64); got != 3 {
		t.Fatalf("count = %d, want 3", got)
	}
	if got := loaded.items["raw_bytes"].value.(memoryValue).data.(string); got != rawBytes {
		t.Fatalf("raw bytes = %q, want %q", got, rawBytes)
	}
	if loaded.used <= 0 {
		t.Fatalf("loaded used = %d, want positive", loaded.used)
	}
}

func TestMemorySnapshotDirtyRuleWritesInBackground(t *testing.T) {
	resetGlobalMemory(t)
	dir := t.TempDir()
	if err := ConfigureMemoryPersistence(MemoryPersistenceConfig{
		Mode:          "snapshot",
		Directory:     dir,
		SaveRules:     []MemorySaveRule{{After: 0, Changes: 1}},
		MinInterval:   time.Nanosecond,
		CheckInterval: time.Hour,
	}); err != nil {
		t.Fatal(err)
	}

	m := &memoryModule{site: "dirty.example", quota: 1 << 20}
	callMemory(t, m.set, "memory.set", starlark.String("key"), starlark.String("value"))

	path := filepath.Join(dir, "site:"+siteHash("dirty.example"), "snapshot.json")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("snapshot %s was not written", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMemoryPersistenceDoesNotDirtyFailedOrNoopMutations(t *testing.T) {
	resetGlobalMemory(t)
	dir := t.TempDir()
	if err := ConfigureMemoryPersistence(MemoryPersistenceConfig{
		Mode:        "snapshot",
		Directory:   dir,
		SaveRules:   []MemorySaveRule{{After: time.Hour, Changes: 1}},
		MinInterval: time.Nanosecond,
	}); err != nil {
		t.Fatal(err)
	}
	manager := globalMemory.persistenceManager()
	site := "noop.example"
	m := &memoryModule{site: site, quota: 16}

	if got := callMemory(t, m.delete, "memory.delete", starlark.String("missing")); got != starlark.False {
		t.Fatalf("delete missing = %s, want False", got)
	}
	if got := callMemory(t, m.clear, "memory.clear"); got.String() != "0" {
		t.Fatalf("clear empty = %s, want 0", got)
	}
	if got := callMemory(t, m.set, "memory.set", starlark.String("large"), starlark.String("this is too large")); got != starlark.False {
		t.Fatalf("large set = %s, want False", got)
	}
	if dirtyChanges(manager, site) != 0 {
		t.Fatalf("dirty changes after failed/no-op mutations = %d, want 0", dirtyChanges(manager, site))
	}

	m.quota = 1 << 20
	if got := callMemory(t, m.setAdd, "memory.set_add", starlark.String("tags"), starlark.String("blue")); got != starlark.True {
		t.Fatalf("set_add first = %s, want True", got)
	}
	if got := callMemory(t, m.setAdd, "memory.set_add", starlark.String("tags"), starlark.String("blue")); got != starlark.False {
		t.Fatalf("set_add duplicate = %s, want False", got)
	}
	if got := dirtyChanges(manager, site); got != 1 {
		t.Fatalf("dirty changes after duplicate set_add = %d, want 1", got)
	}
	if got := callMemory(t, m.setRemove, "memory.set_remove", starlark.String("tags"), starlark.String("green")); got != starlark.False {
		t.Fatalf("set_remove absent = %s, want False", got)
	}
	if got := dirtyChanges(manager, site); got != 1 {
		t.Fatalf("dirty changes after absent set_remove = %d, want 1", got)
	}
}

func TestWipeMemorySiteRemovesSnapshot(t *testing.T) {
	resetGlobalMemory(t)
	dir := t.TempDir()
	if err := ConfigureMemoryPersistence(MemoryPersistenceConfig{
		Mode:        "snapshot",
		Directory:   dir,
		SaveRules:   []MemorySaveRule{{After: time.Hour, Changes: 1}},
		MinInterval: time.Nanosecond,
	}); err != nil {
		t.Fatal(err)
	}
	site := "wipe.example"
	m := &memoryModule{site: site, quota: 1 << 20}
	callMemory(t, m.set, "memory.set", starlark.String("key"), starlark.String("value"))
	if got := MemoryUsage(site); got <= 0 {
		t.Fatalf("MemoryUsage(%q) = %d, want positive", site, got)
	}
	if err := FlushMemorySnapshots(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "site:"+siteHash(site), "snapshot.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot stat error = %v", err)
	}

	WipeMemorySite(site)
	if got := MemoryUsage(site); got != 0 {
		t.Fatalf("MemoryUsage(%q) after wipe = %d, want 0", site, got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot stat after wipe error = %v, want not exist", err)
	}
}

func TestWriteFileAtomicReplacesAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("file content = %q, want new", string(data))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "snapshot.json" {
		t.Fatalf("entries = %v, want only snapshot.json", entries)
	}
}

func callMemory(t *testing.T, fn func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error), name string, args ...starlark.Value) starlark.Value {
	t.Helper()
	value, err := fn(&starlark.Thread{Name: "test"}, starlark.NewBuiltin(name, fn), starlark.Tuple(args), nil)
	if err != nil {
		t.Fatalf("%s error = %v", name, err)
	}
	return value
}

func dirtyChanges(manager *memoryPersistenceManager, site string) int64 {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.dirty[site] == nil {
		return 0
	}
	return manager.dirty[site].changes
}

func resetGlobalMemory(t *testing.T) {
	t.Helper()
	_ = ConfigureMemoryPersistence(MemoryPersistenceConfig{Mode: "off"})
	globalMemory = newMemoryStore()
	t.Cleanup(func() {
		_ = ConfigureMemoryPersistence(MemoryPersistenceConfig{Mode: "off"})
		globalMemory = newMemoryStore()
	})
}
