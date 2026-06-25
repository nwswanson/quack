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

func TestParseAllowsLogicalCameraCapabilities(t *testing.T) {
	body := `capabilities:
  camera:
    front_door:
      required: false
      description: Front door camera
      permissions:
        capture:
          roles: [admin, staff]
      limits:
        max_width: 640
        max_height: 480
        max_fps: 2
        max_duration_seconds: 10
        max_captures_per_minute: 10
`
	got, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	camera := got.Capabilities.Camera["front_door"]
	if camera.Description != "Front door camera" || camera.Limits.MaxWidth != 640 {
		t.Fatalf("camera capability = %+v, want parsed logical policy", camera)
	}
	if got := strings.Join(camera.Permissions["capture"].Roles, ","); got != "admin,staff" {
		t.Fatalf("capture roles = %q, want admin,staff", got)
	}
}

func TestParseRejectsPhysicalCameraCapabilityAlias(t *testing.T) {
	body := "capabilities:\n  camera:\n    /dev/video0:\n      required: true\n"
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil || !strings.Contains(err.Error(), "logical alias") {
		t.Fatalf("Parse error = %v, want logical alias error", err)
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

func TestParseAllowsExcludePatterns(t *testing.T) {
	body := "exclude:\n  - \"*.swp\"\n  - \"node_modules/\"\n"
	manifest, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(manifest.Exclude, ","), "*.swp,node_modules/**"; got != want {
		t.Fatalf("exclude = %q, want %q", got, want)
	}
}

func TestParseRejectsInvalidExcludePattern(t *testing.T) {
	tests := []string{
		"exclude:\n  - \"\"\n",
		"exclude:\n  - \"/tmp\"\n",
		"exclude:\n  - \"../secret\"\n",
		"exclude:\n  - \"[\"\n",
	}
	for _, body := range tests {
		t.Run(body, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(body), int64(len(body))); err == nil {
				t.Fatal("expected error")
			}
		})
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

func TestParseAllowsStaticRouteFile(t *testing.T) {
	body := "routes:\n  - path: /favicon.ico\n    kind: static\n    file: media/favicon ico.ico\n"
	manifest, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Routes) != 1 || manifest.Routes[0].File != "media/favicon_ico.ico" {
		t.Fatalf("routes = %#v, want sanitized static route file", manifest.Routes)
	}
}

func TestParseRejectsRootAndFileOnSameRoute(t *testing.T) {
	body := "routes:\n  - path: /favicon.ico\n    kind: static\n    root: public\n    file: media/favicon.ico\n"
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil || !strings.Contains(err.Error(), "route.root and route.file cannot both be set") {
		t.Fatalf("Parse error = %v, want root/file mutual exclusion error", err)
	}
}

func TestParseRejectsRootOnHTTPRoute(t *testing.T) {
	body := "routes:\n  - path: /api\n    kind: http\n    root: public\n"
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil || !strings.Contains(err.Error(), "route.root is only supported for static routes") {
		t.Fatalf("Parse error = %v, want route root kind error", err)
	}
}

func TestParseRejectsFileOnHTTPRoute(t *testing.T) {
	body := "routes:\n  - path: /api\n    kind: http\n    file: media/api.json\n"
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil || !strings.Contains(err.Error(), "route.file is only supported for static routes") {
		t.Fatalf("Parse error = %v, want route file kind error", err)
	}
}

func TestParseAllowsStarlarkHTTPRoute(t *testing.T) {
	body := "routes:\n  - path: /api\n    kind: http\n    runtime: starlark\n    entrypoint: app.star\n    expose_errors: true\n    filesystem:\n      root: /data\\files/\n"
	manifest, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Routes) != 1 ||
		manifest.Routes[0].Runtime != "starlark" ||
		manifest.Routes[0].ExposeErrors == nil ||
		!*manifest.Routes[0].ExposeErrors ||
		manifest.Routes[0].Filesystem == nil ||
		manifest.Routes[0].Filesystem.Root != "data/files" {
		t.Fatalf("routes = %#v, want starlark runtime with sanitized filesystem root", manifest.Routes)
	}
}

func TestParseAllowsStarlarkWebSocketRoute(t *testing.T) {
	body := "routes:\n  - path: /api/somesocket\n    kind: websocket\n    runtime: starlark\n    entrypoint: api/somesocket.star\n"
	manifest, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Routes) != 1 ||
		manifest.Routes[0].Kind != RouteWebSocket ||
		manifest.Routes[0].Runtime != "starlark" ||
		manifest.Routes[0].Entrypoint != "api/somesocket.star" {
		t.Fatalf("routes = %#v, want starlark websocket runtime", manifest.Routes)
	}
}

func TestParseAllowsStarlarkFilesystemAtTarballRoot(t *testing.T) {
	body := "routes:\n  - path: /api\n    kind: http\n    runtime: starlark\n    entrypoint: app.star\n    filesystem:\n      root: /\n"
	manifest, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Routes[0].Filesystem == nil || manifest.Routes[0].Filesystem.Root != "" {
		t.Fatalf("filesystem = %#v, want enabled at tarball root", manifest.Routes[0].Filesystem)
	}
}

func TestParseRejectsInvalidStarlarkFilesystem(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "static route",
			body: "routes:\n  - path: /\n    kind: static\n    filesystem:\n      root: data\n",
			want: "route.filesystem is only supported",
		},
		{
			name: "traversal",
			body: "routes:\n  - path: /api\n    kind: http\n    runtime: starlark\n    entrypoint: app.star\n    filesystem:\n      root: ../data\n",
			want: "filesystem.root cannot contain ..",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.body), int64(len(tc.body)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParseAPIProxiesValidatesAndNormalizes(t *testing.T) {
	body := `api_proxies:
  - name: my_api
    domain: API.Example.COM
  - name: local_api
    path_fixed: http://192.168.1.50:8080/api/v1/widget
    methods: [post, GET]
`
	manifest, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.APIProxies[0]; got.Name != "my_api" || got.Domain != "api.example.com" || strings.Join(got.Methods, ",") != "GET" {
		t.Fatalf("domain proxy = %+v, want normalized GET proxy", got)
	}
	if got := manifest.APIProxies[1]; got.PathFixed != "http://192.168.1.50:8080/api/v1/widget" || strings.Join(got.Methods, ",") != "POST,GET" {
		t.Fatalf("fixed proxy = %+v, want normalized path/methods", got)
	}
}

func TestParseRejectsInvalidAPIProxies(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "both domain and fixed path",
			body: "api_proxies:\n  - name: p\n    domain: api.example.com\n    path_fixed: https://api.example.com/v1\n",
			want: "cannot set both domain and path_fixed",
		},
		{
			name: "neither domain nor fixed path",
			body: "api_proxies:\n  - name: p\n",
			want: "must set exactly one",
		},
		{
			name: "duplicate name",
			body: "api_proxies:\n  - name: p\n    domain: api.example.com\n  - name: p\n    domain: other.example.com\n",
			want: "duplicates",
		},
		{
			name: "userinfo",
			body: "api_proxies:\n  - name: p\n    path_fixed: https://user:pass@api.example.com/v1\n",
			want: "userinfo is not allowed",
		},
		{
			name: "fragment",
			body: "api_proxies:\n  - name: p\n    path_fixed: https://api.example.com/v1#frag\n",
			want: "fragment is not allowed",
		},
		{
			name: "bad name",
			body: "api_proxies:\n  - name: ../p\n    domain: api.example.com\n",
			want: "must contain only",
		},
		{
			name: "domain scheme",
			body: "api_proxies:\n  - name: p\n    domain: https://api.example.com\n",
			want: "must not include a scheme",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.body), int64(len(tt.body)))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse error = %v, want %q", err, tt.want)
			}
		})
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
