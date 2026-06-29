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

func TestParseAllowsWASMModules(t *testing.T) {
	body := `wasm:
  modules:
    rules:
      path: plugins/rules.wasm
      abi: quack:json-v1
      retain_instances: 4
      limits:
        timeout_ms: 25
        memory_pages: 16
        max_input_bytes: 65536
        max_output_bytes: 65536
      imports:
        - clock.now
        - random.bytes
`
	got, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	module := got.WASM.Modules["rules"]
	if module.Path != "plugins/rules.wasm" || module.ABI != "quack:json-v1" || module.RetainInstances != 4 {
		t.Fatalf("wasm module = %+v, want rules module", module)
	}
	if module.Limits.MemoryPages != 16 || strings.Join(module.Imports, ",") != "clock.now,random.bytes" {
		t.Fatalf("wasm module = %+v, want limits and imports", module)
	}
}

func TestParseAllowsRouteLimits(t *testing.T) {
	body := `routes:
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
	got, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	limits := got.Routes[0].Limits
	if limits == nil {
		t.Fatal("route limits were not parsed")
	}
	if limits.MaxRequestBytes != 10485760 ||
		limits.MaxResponseBytes != 20971520 ||
		limits.MaxDurationMS != 2000 ||
		limits.MaxMemoryBytes != 67108864 ||
		limits.MaxConcurrency != 2 ||
		limits.MaxExecutionSteps != 100000 ||
		limits.MaxScriptBytes != 524288 {
		t.Fatalf("limits = %+v, want parsed route limits", limits)
	}
}

func TestParseRejectsNegativeRouteLimits(t *testing.T) {
	body := `routes:
  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    limits:
      max_request_bytes: -1
`
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil || !strings.Contains(err.Error(), "route.limits cannot contain negative values") {
		t.Fatalf("Parse error = %v, want negative route limits error", err)
	}
}

func TestParseAllowsQuackWASMABI(t *testing.T) {
	body := `wasm:
  modules:
    rules:
      path: plugins/rules.wasm
      abi: quack:wasm-v1
`
	got, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if got.WASM.Modules["rules"].ABI != "quack:wasm-v1" {
		t.Fatalf("abi = %q, want quack:wasm-v1", got.WASM.Modules["rules"].ABI)
	}
}

func TestParseRejectsUnsupportedWASMImport(t *testing.T) {
	body := `wasm:
  modules:
    rules:
      path: plugins/rules.wasm
      abi: quack:json-v1
      imports:
        - fs.read
`
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil || !strings.Contains(err.Error(), "unsupported wasm.modules") {
		t.Fatalf("Parse error = %v, want unsupported wasm import", err)
	}
}

func TestParseAllowsSerialByTopicEventConcurrency(t *testing.T) {
	body := `events:
  - selector: "room.*"
    concurrency: serial_by_topic
    on_event: app/room.star:on_event
`
	got, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if len(got.Events) != 1 || got.Events[0].Concurrency != "serial_by_topic" {
		t.Fatalf("events = %#v, want serial_by_topic", got.Events)
	}
}

func TestParseAllowsSelectorPipesWithTopicBounds(t *testing.T) {
	body := `pipes:
  - selector: "room.*"
    retain: 64
    overflow: drop_oldest
    key_by: topic
    max_topics: 256
    topic_overflow: evict_lru
  - selector: "notifications.*"
    retain: 512
    key_by: selector
`
	got, err := Parse(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if len(got.Pipes) != 2 || got.Pipes[0].Selector != "room.*" || got.Pipes[0].MaxTopics != 256 || got.Pipes[1].KeyBy != "selector" {
		t.Fatalf("pipes = %#v, want selector pipe policies", got.Pipes)
	}
}

func TestParseRejectsUnboundedWildcardTopicPipe(t *testing.T) {
	body := `pipes:
  - selector: "room.*"
    retain: 64
`
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil || !strings.Contains(err.Error(), "pipe.max_topics is required") {
		t.Fatalf("Parse error = %v, want max_topics requirement", err)
	}
}

func TestParseRejectsInvalidPipeSelectors(t *testing.T) {
	tests := []string{
		"pipes:\n  - selector: \"room.*.message\"\n    key_by: selector\n",
		"pipes:\n  - selector: \"*.message\"\n    key_by: selector\n",
		"pipes:\n  - selector: \"room*\"\n    key_by: selector\n",
		"pipes:\n  - selector: \"*\"\n    key_by: selector\n",
	}
	for _, body := range tests {
		t.Run(body, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(body), int64(len(body))); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseRejectsUnknownEventConcurrency(t *testing.T) {
	body := `events:
  - selector: "room.*"
    concurrency: single_file
    on_event: app/room.star:on_event
`
	_, err := Parse(strings.NewReader(body), int64(len(body)))
	if err == nil || !strings.Contains(err.Error(), "unsupported event.concurrency") {
		t.Fatalf("Parse error = %v, want unsupported concurrency", err)
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
