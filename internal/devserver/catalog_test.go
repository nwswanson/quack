package devserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSiteSourceAppliesRouteLimits(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	siteYAML := `routes:
  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    limits:
      max_request_bytes: 10485760
      max_response_bytes: 20971520
      max_duration_ms: 2000
      max_memory_bytes: 67108864
      max_concurrency: 2
      max_execution_steps: 100000
      max_script_bytes: 524288
`
	if err := os.WriteFile(filepath.Join(root, "site.yml"), []byte(siteYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "api", "app.star"), []byte("def handle(req): return (200, {}, 'ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	source, err := LoadSiteSource(context.Background(), root, "limits-demo", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Routes) != 1 {
		t.Fatalf("routes = %#v, want one route", source.Routes)
	}
	limits := source.Routes[0].ResourceLimits
	if limits.MaxRequestBytes != 10485760 ||
		limits.MaxResponseBytes != 20971520 ||
		limits.MaxDurationMillis != 2000 ||
		limits.MaxMemoryBytes != 67108864 ||
		limits.MaxConcurrency != 2 ||
		limits.MaxExecutionSteps != 100000 ||
		limits.MaxScriptBytes != 524288 {
		t.Fatalf("resource limits = %#v, want manifest route limits", limits)
	}
}
