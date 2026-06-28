package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"quack/internal/runtime/modules"
)

func TestStarlarkExecutorCachesCompiledProgram(t *testing.T) {
	loader := &mutableScriptLoader{script: `def handle(req): return (200, {}, "cached")`}
	executor, err := NewStarlarkExecutor(loader, ResourceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	bundle := Bundle{Site: "cache-site", Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}}}
	for i := 0; i < 2; i++ {
		resp, err := executor.Invoke(context.Background(), bundle, InvocationRequest{Method: http.MethodGet, Route: "/api"})
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.Body) != "cached" {
			t.Fatalf("body = %q, want cached", string(resp.Body))
		}
	}
	if got := executor.programCacheLen(); got != 1 {
		t.Fatalf("program cache len = %d, want 1", got)
	}
}

func TestStarlarkExecutorProgramCacheUsesBundleFileSHAWithoutRereading(t *testing.T) {
	loader := &mutableScriptLoader{script: `def handle(req): return (200, {}, "cached")`}
	executor, err := NewStarlarkExecutor(loader, ResourceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	bundle := Bundle{
		Site:    "cache-site",
		Version: 1,
		Routes:  []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star", ScriptKey: "app-blob"}},
		Files:   []BundleFile{{Path: "app.star", BlobPath: "app-blob", FileSHA: "script-sha", Bytes: 42}},
	}
	for i := 0; i < 2; i++ {
		resp, err := executor.Invoke(context.Background(), bundle, InvocationRequest{Method: http.MethodGet, Route: "/api"})
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.Body) != "cached" {
			t.Fatalf("body = %q, want cached", string(resp.Body))
		}
	}
	if got := loader.reads(); got != 1 {
		t.Fatalf("loader reads = %d, want 1 cache miss read", got)
	}
	if got := executor.programCacheLen(); got != 1 {
		t.Fatalf("program cache len = %d, want 1", got)
	}
}

func TestStarlarkExecutorProgramCacheInvalidatesWhenSourceChanges(t *testing.T) {
	loader := &mutableScriptLoader{script: `def handle(req): return (200, {}, "first")`}
	executor, err := NewStarlarkExecutor(loader, ResourceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	bundle := Bundle{Site: "cache-site", Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}}}
	resp, err := executor.Invoke(context.Background(), bundle, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "first" {
		t.Fatalf("first body = %q", string(resp.Body))
	}

	loader.set(`def handle(req): return (200, {}, "second")`)
	resp, err = executor.Invoke(context.Background(), bundle, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "second" {
		t.Fatalf("second body = %q, want updated source", string(resp.Body))
	}
	if got := executor.programCacheLen(); got != 2 {
		t.Fatalf("program cache len = %d, want 2 source-hash entries", got)
	}
}

func TestStarlarkExecutorProgramCacheCanBeDisabled(t *testing.T) {
	loader := &mutableScriptLoader{script: `def handle(req): return (200, {}, "uncached")`}
	executor, err := NewStarlarkExecutor(loader, ResourceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	executor.SetProgramCacheEnabled(false)
	bundle := Bundle{Site: "cache-site", Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}}}
	for i := 0; i < 2; i++ {
		if _, err := executor.Invoke(context.Background(), bundle, InvocationRequest{Method: http.MethodGet, Route: "/api"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := executor.programCacheLen(); got != 0 {
		t.Fatalf("program cache len = %d, want 0 when disabled", got)
	}
}

func TestStarlarkExecutorMemoryModuleCacheKeyedBySiteAndQuota(t *testing.T) {
	executor, err := NewStarlarkExecutor(&mutableScriptLoader{}, ResourceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	first := executor.memoryModule("site-a", 100)
	if again := executor.memoryModule("site-a", 100); again != first {
		t.Fatal("same site/quota returned different memory module")
	}
	if otherSite := executor.memoryModule("site-b", 100); otherSite == first {
		t.Fatal("different site reused memory module")
	}
	if otherQuota := executor.memoryModule("site-a", 200); otherQuota == first {
		t.Fatal("different quota reused memory module")
	}
	if got := executor.memoryModuleCacheLen(); got != 3 {
		t.Fatalf("memory module cache len = %d, want 3", got)
	}
}

func TestStarlarkExecutorCachedMemoryModulesKeepSitesSeparate(t *testing.T) {
	const script = `
def on_event(ctx, event):
    previous = memory.get("shared", "missing")
    memory.set("shared", event.payload["value"])
    return events.publish("seen", {
        "site": ctx.site,
        "previous": previous,
        "current": memory.get("shared"),
    })
`
	modules.WipeMemorySite("cache-site-a")
	modules.WipeMemorySite("cache-site-b")
	t.Cleanup(func() {
		modules.WipeMemorySite("cache-site-a")
		modules.WipeMemorySite("cache-site-b")
	})

	executor, err := NewStarlarkExecutor(&mutableScriptLoader{script: script}, ResourceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	assertCacheSafetyEvent(t, executor, "cache-site-a", "alpha", "seen", map[string]string{
		"site":     "cache-site-a",
		"previous": "missing",
		"current":  "alpha",
	})
	assertCacheSafetyEvent(t, executor, "cache-site-b", "beta", "seen", map[string]string{
		"site":     "cache-site-b",
		"previous": "missing",
		"current":  "beta",
	})
	assertCacheSafetyEvent(t, executor, "cache-site-a", "gamma", "seen", map[string]string{
		"site":     "cache-site-a",
		"previous": "alpha",
		"current":  "gamma",
	})
}

func TestStarlarkExecutorSharedWebSocketModulesAreReusableAcrossInvocations(t *testing.T) {
	if websocketModule() != websocketModule() {
		t.Fatal("websocket module was rebuilt")
	}
	if eventsModule() != eventsModule() {
		t.Fatal("events module was rebuilt")
	}
	if timersModule() != timersModule() {
		t.Fatal("timers module was rebuilt")
	}

	const script = `
def on_event(ctx, event):
    return events.publish("seen." + ctx.site, {
        "site": ctx.site,
        "value": event.payload["value"],
    })
`
	executor, err := NewStarlarkExecutor(&mutableScriptLoader{script: script}, ResourceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	assertCacheSafetyEvent(t, executor, "cache-shared-modules-a", "first", "seen.cache-shared-modules-a", map[string]string{
		"site":  "cache-shared-modules-a",
		"value": "first",
	})
	assertCacheSafetyEvent(t, executor, "cache-shared-modules-b", "second", "seen.cache-shared-modules-b", map[string]string{
		"site":  "cache-shared-modules-b",
		"value": "second",
	})
}

func (e *StarlarkExecutor) programCacheLen() int {
	e.programMu.Lock()
	defer e.programMu.Unlock()
	return len(e.programs)
}

func (e *StarlarkExecutor) memoryModuleCacheLen() int {
	e.moduleMu.RLock()
	defer e.moduleMu.RUnlock()
	return len(e.memoryModules)
}

func assertCacheSafetyEvent(t *testing.T, executor *StarlarkExecutor, site string, value string, topic string, want map[string]string) {
	t.Helper()
	effects, err := executor.InvokeEvent(context.Background(), Bundle{
		Site:    site,
		Version: 1,
		Routes:  []Route{{Path: "event:cache.star", Kind: RouteWebSocket, Entrypoint: "cache.star"}},
		Files:   []BundleFile{{Path: "cache.star", BlobPath: "cache.star", FileSHA: "cache-script", Bytes: 1}},
	}, EventInvocation{
		Site:       site,
		Version:    1,
		Entrypoint: "cache.star",
		Handler:    "on_event",
		Topic:      "cache.test",
		Payload:    []byte(`{"value":"` + value + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Type != WebSocketEffectPublish || effects[0].Topic != topic {
		t.Fatalf("effects = %+v, want one publish to %s", effects, topic)
	}
	var got map[string]any
	if err := json.Unmarshal(effects[0].Payload, &got); err != nil {
		t.Fatalf("effect payload = %q: %v", effects[0].Payload, err)
	}
	for key, wantValue := range want {
		if gotValue := starlarkCacheTestString(got[key]); gotValue != wantValue {
			t.Fatalf("payload[%s] = %q, want %q in payload %v", key, gotValue, wantValue, got)
		}
	}
}

func starlarkCacheTestString(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case float64:
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.0f", value), ".0"), ".")
	default:
		return fmt.Sprint(value)
	}
}

type mutableScriptLoader struct {
	mu     sync.Mutex
	script string
	read   int
}

func (m *mutableScriptLoader) set(script string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.script = script
}

func (m *mutableScriptLoader) OpenScript(context.Context, string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.read++
	return io.NopCloser(strings.NewReader(m.script)), nil
}

func (m *mutableScriptLoader) reads() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.read
}
