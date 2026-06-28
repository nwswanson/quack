package runtime

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
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

func (e *StarlarkExecutor) programCacheLen() int {
	e.programMu.Lock()
	defer e.programMu.Unlock()
	return len(e.programs)
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
