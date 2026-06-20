package manifest

import (
	"strings"
	"testing"
)

func TestParseEmptyManifest(t *testing.T) {
	got, err := Parse(strings.NewReader(" \n"), 2)
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if got.Features.Database.Enabled || got.Features.Database.Required || len(got.Routes) != 0 {
		t.Fatalf("Parse = %+v, want default manifest", got)
	}
}

func TestParseRejectsTopLevelStaticRoot(t *testing.T) {
	body := "static:\n  root: public\n"
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "field static not found") {
		t.Fatalf("error = %q, want top-level static field rejected", err.Error())
	}
}

func TestParseDatabaseFeatureEnabled(t *testing.T) {
	got, err := Parse(strings.NewReader("features:\n  database:\n    enabled: true\n"), 43)
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if !got.Features.Database.Enabled || got.Features.Database.Required {
		t.Fatalf("database feature = %+v, want enabled optional", got.Features.Database)
	}
}

func TestParseDatabaseFeatureRequired(t *testing.T) {
	got, err := Parse(strings.NewReader("features:\n  database:\n    enabled: true\n    required: true\n"), 62)
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if !got.Features.Database.Enabled || !got.Features.Database.Required {
		t.Fatalf("database feature = %+v, want enabled required", got.Features.Database)
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	_, err := Parse(strings.NewReader("features:\n  mystery: true\n"), 26)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "field mystery not found") {
		t.Fatalf("error = %q, want unknown field detail", err.Error())
	}
}

func TestParseRejectsInvalidDatabaseRequirement(t *testing.T) {
	_, err := Parse(strings.NewReader("features:\n  database:\n    required: true\n"), 45)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "database.required cannot be true") {
		t.Fatalf("error = %q, want database requirement detail", err.Error())
	}
}

func TestParseRejectsInvalidRouteDeclaration(t *testing.T) {
	_, err := Parse(strings.NewReader("routes:\n  - kind: http\n"), 23)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "route.path is required") {
		t.Fatalf("error = %q, want route path detail", err.Error())
	}
}

func TestParseRejectsEmptyRouteMethod(t *testing.T) {
	body := "routes:\n  - path: /api\n    kind: http\n    methods: [GET, \"\"]\n"
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "route.methods cannot contain an empty method") {
		t.Fatalf("error = %q, want route method detail", err.Error())
	}
}

func TestParseAllowsStaticRouteRoot(t *testing.T) {
	body := "routes:\n  - path: /\n    kind: static\n    root: public assets\\dist/\n"
	manifest, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Routes) != 1 || manifest.Routes[0].Root != "public_assets/dist" {
		t.Fatalf("routes = %#v, want sanitized static route root", manifest.Routes)
	}
}

func TestParseRejectsRootOnHTTPRoute(t *testing.T) {
	body := "routes:\n  - path: /api\n    kind: http\n    root: public\n"
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil || !strings.Contains(err.Error(), "route.root is only supported for static routes") {
		t.Fatalf("Parse error = %v, want route root kind error", err)
	}
}

func TestParseAllowsStarlarkHTTPRoute(t *testing.T) {
	body := "routes:\n  - path: /api\n    kind: http\n    runtime: starlark\n    entrypoint: app.star\n"
	manifest, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Routes) != 1 || manifest.Routes[0].Runtime != "starlark" {
		t.Fatalf("routes = %#v, want starlark runtime", manifest.Routes)
	}
}

func TestParseRejectsRuntimeWithoutEntrypoint(t *testing.T) {
	body := "routes:\n  - path: /api\n    kind: http\n    runtime: starlark\n"
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil || !strings.Contains(err.Error(), "route.entrypoint") {
		t.Fatalf("Parse error = %v, want entrypoint error", err)
	}
}

func TestParseRejectsRuntimeOnStaticRoute(t *testing.T) {
	body := "routes:\n  - path: /\n    kind: static\n    runtime: starlark\n    entrypoint: app.star\n"
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil || !strings.Contains(err.Error(), "route.runtime is only supported") {
		t.Fatalf("Parse error = %v, want static runtime error", err)
	}
}
