package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"quack/internal/domain"
	appsettings "quack/internal/settings"
)

func TestDisabledServiceDoesNotInvokeRuntime(t *testing.T) {
	service := NewDisabledService()

	_, err := service.InvokeHTTP(context.Background(), InvocationRequest{
		Site: "foo", Version: 1, Route: "/api", Method: "GET",
	})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("InvokeHTTP error = %v, want ErrDisabled", err)
	}
}

func TestStarlarkExecutorHandlesRequestTupleAndResponseTuple(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{"app.star": `
def handle(req):
    method, path, query, headers, body = req
    return (
        201,
        {"content-type": "text/plain", "x-seen": [method, path, query, headers["x-test"][0]]},
        "body=%s" % body,
    )
`})

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "foo", Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
	}, InvocationRequest{
		Method: http.MethodPost,
		Route:  "/api/echo",
		Query:  "a=1",
		Headers: map[string][]string{
			"X-Test": {"visible"},
		},
		Body: []byte("hello"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated || string(resp.Body) != `body=b"hello"` {
		t.Fatalf("response = %+v body=%q, want created byte body", resp, string(resp.Body))
	}
	if got := strings.Join(resp.Headers["X-Seen"], "|"); got != "POST|/echo|a=1|visible" {
		t.Fatalf("x-seen = %q, want tuple values from request", got)
	}
}

func TestStarlarkExecutorDoesNotExposeLoadFilesystemAccess(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{"app.star": `
def handle(req):
    load("secret.star", "secret")
    return (200, {}, secret)
`})

	_, err := executor.Invoke(context.Background(), Bundle{
		Site: "foo", Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if !errors.Is(err, ErrInvocationFailure) {
		t.Fatalf("Invoke error = %v, want invocation failure from unavailable load", err)
	}
}

func TestStarlarkExecutorReturnsBacktraceForScriptError(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{"app.star": `
def explode():
    fail("kaboom")

def handle(req):
    explode()
    return (200, {}, "never")
`})

	_, err := executor.Invoke(context.Background(), Bundle{
		Site: "foo", Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if !errors.Is(err, ErrInvocationFailure) {
		t.Fatalf("Invoke error = %v, want invocation failure", err)
	}
	for _, want := range []string{"Traceback", "in handle", "in explode", "kaboom"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Invoke error = %q, want %q in backtrace", err.Error(), want)
		}
	}
}

func TestStarlarkExecutorExposesReadOnlyBundleFS(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{
		"app.star": `
def handle(req):
    meta = fs.stat("message.txt")
    return (
        200,
        {"content-type": "text/plain", "x-size": str(meta["size"])},
        ",".join(fs.listdir(".")) + ":" + fs.read("/message.txt") + ":" + str(fs.exists("private.txt")),
    )
`,
		"data-blob": "hello from bundle",
	})

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "foo", Version: 1,
		Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star", FilesystemEnabled: true, FilesystemRoot: "data"}},
		Files: []BundleFile{
			{Path: "data/message.txt", BlobPath: "data-blob", FileSHA: "sha", Bytes: 17},
			{Path: "private.txt", BlobPath: "private-blob", FileSHA: "private-sha", Bytes: 7},
		},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "message.txt:hello from bundle:False" || resp.Headers["X-Size"][0] != "17" {
		t.Fatalf("response = %+v body=%q, want bundle fs read", resp, string(resp.Body))
	}
}

func TestStarlarkExecutorDoesNotExposeFSUnlessEnabled(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{"app.star": `
def handle(req):
    return (200, {}, str(fs.exists("message.txt")))
`})

	_, err := executor.Invoke(context.Background(), Bundle{
		Site: "foo", Version: 1,
		Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
		Files:  []BundleFile{{Path: "message.txt", BlobPath: "data-blob", FileSHA: "sha", Bytes: 17}},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if !errors.Is(err, ErrInvocationFailure) {
		t.Fatalf("Invoke error = %v, want invocation failure from disabled fs", err)
	}
}

func TestStarlarkExecutorExposesSiteScopedMemoryStore(t *testing.T) {
	site := "memory-site-a"
	executor := newTestStarlarkExecutor(t, map[string]string{"app.star": `
def handle(req):
    memory.clear()
    memory.set("plain", {"nested": [None, 0, b"x"]})
    memory.list_push("events", "first")
    memory.list_push("events", "second")
    memory.set_add("tags", "blue")
    memory.set_add("tags", "blue")
    memory.zadd("scores", 2.0, "b")
    memory.zadd("scores", 1.0, "a")
    count = memory.incr("count", 3)
    return (
        200,
        {"content-type": "text/plain"},
        "%s|%s|%s|%s|%s|%s|%s" % (
            memory.get("plain")["nested"][1],
            memory.list_range("events"),
            memory.set_members("tags"),
            memory.zrange("scores", with_scores=True),
            count,
            memory.type("count"),
            memory.usage() > 0,
        ),
    )
`})

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: site, Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if err != nil {
		t.Fatal(err)
	}
	want := `0|["first", "second"]|["blue"]|[("a", 1.0), ("b", 2.0)]|3|counter|True`
	if string(resp.Body) != want {
		t.Fatalf("body = %q, want %q", string(resp.Body), want)
	}
}

func TestStarlarkMemoryPersistsPerSiteOnly(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{"app.star": `
def handle(req):
    old = memory.get("seen", "missing")
    memory.set("seen", "yes")
    return (200, {}, old)
`})

	for _, site := range []string{"memory-site-b", "memory-site-c"} {
		resp, err := executor.Invoke(context.Background(), Bundle{
			Site: site, Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
		}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.Body) != "missing" {
			t.Fatalf("first body for %s = %q, want missing", site, string(resp.Body))
		}
	}
	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "memory-site-b", Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "yes" {
		t.Fatalf("second body = %q, want yes", string(resp.Body))
	}
}

func TestStarlarkMemoryQuotaRejectsWritesWithoutMutation(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{"app.star": `
def handle(req):
    memory.clear()
    first = memory.set("small", "ok")
    before = memory.usage()
    second = memory.set("large", "x" * 200)
    return (200, {}, "%s|%s|%s|%s|%s" % (first, second, memory.get("small"), memory.get("large", "missing"), memory.usage() == before))
`})

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "memory-quota-site", Version: 1,
		Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
		Limits: ResourceLimits{MaxMemoryBytes: 64},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(resp.Body), "True|False|ok|missing|True"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestStarlarkMemoryLoweredQuotaBlocksGrowthButAllowsShrink(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{
		"seed.star": `
def handle(req):
    memory.clear()
    memory.set("large", "x" * 100)
    return (200, {}, str(memory.usage()))
`,
		"lower.star": `
def handle(req):
    grow = memory.set("new", "y")
    shrink = memory.delete("large")
    after = memory.set("new", "y")
    return (200, {}, "%s|%s|%s" % (grow, shrink, after))
`,
	})
	site := "memory-lowered-quota-site"
	if _, err := executor.Invoke(context.Background(), Bundle{
		Site: site, Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "seed.star"}},
		Limits: ResourceLimits{MaxMemoryBytes: 1024},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"}); err != nil {
		t.Fatal(err)
	}
	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: site, Version: 2, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "lower.star"}},
		Limits: ResourceLimits{MaxMemoryBytes: 32},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(resp.Body), "False|True|True"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestStarlarkMemoryRejectsWrongTypeOperation(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{"app.star": `
def handle(req):
    memory.clear()
    memory.set("name", "not-a-list")
    memory.list_push("name", "boom")
    return (200, {}, "never")
`})

	_, err := executor.Invoke(context.Background(), Bundle{
		Site: "memory-wrong-type-site", Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if !errors.Is(err, ErrInvocationFailure) || !strings.Contains(err.Error(), `key "name" contains value, want list`) {
		t.Fatalf("Invoke error = %v, want wrong type invocation failure", err)
	}
}

func TestDemoStarlarkBundleExecutes(t *testing.T) {
	script, err := os.ReadFile("../../demos/starlark-basic/api/app.star")
	if err != nil {
		t.Fatal(err)
	}
	executor := newTestStarlarkExecutor(t, map[string]string{"demo.star": string(script)})

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "demo", Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "demo.star"}},
	}, InvocationRequest{
		Method:  http.MethodPost,
		Route:   "/api/report",
		Query:   "sample=true",
		Headers: map[string][]string{"User-Agent": {"quack-test"}},
		Body:    []byte("hello"),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.Headers["Content-Type"][0] != "application/json; charset=utf-8" {
		t.Fatalf("response = %+v body=%s, want JSON ok response", resp, body)
	}
	for _, want := range []string{`"runtime": "starlark"`, `"path": "/report"`, `"body_size": 5`, `"user_agent": "quack-test"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("demo body = %s, want %s", body, want)
		}
	}
}

func TestDemoStarlarkFSBundleExecutes(t *testing.T) {
	app, err := os.ReadFile("../../demos/starlark-fs/api/app.star")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := os.ReadFile("../../demos/starlark-fs/data/profile.txt")
	if err != nil {
		t.Fatal(err)
	}
	notes, err := os.ReadFile("../../demos/starlark-fs/data/notes.md")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../demos/starlark-fs/data/raw.bin")
	if err != nil {
		t.Fatal(err)
	}
	executor := newTestStarlarkExecutor(t, map[string]string{
		"api/app.star":     string(app),
		"data/profile.txt": string(profile),
		"data/notes.md":    string(notes),
		"data/raw.bin":     string(raw),
	})

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "demo", Version: 1,
		Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "api/app.star", FilesystemEnabled: true, FilesystemRoot: "data"}},
		Files: []BundleFile{
			{Path: "data/profile.txt", BlobPath: "data/profile.txt", FileSHA: "profile-sha", Bytes: int64(len(profile))},
			{Path: "data/notes.md", BlobPath: "data/notes.md", FileSHA: "notes-sha", Bytes: int64(len(notes))},
			{Path: "data/raw.bin", BlobPath: "data/raw.bin", FileSHA: "raw-sha", Bytes: int64(len(raw))},
		},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if err != nil {
		t.Fatal(err)
	}
	body := string(resp.Body)
	for _, want := range []string{
		`"message": "Hello from an uploaded file read by Starlark."`,
		`"data_dir": [`,
		`"notes.md"`,
		`"profile.txt"`,
		`"raw.bin"`,
		`"has_notes": true`,
		`"has_missing_file": false`,
		`"raw_size": 6`,
		`"raw_text": "QUACK\n"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("demo fs body = %s, want %s", body, want)
		}
	}
}

func TestDemoStarlarkMemoryRoutesExecute(t *testing.T) {
	scripts := map[string]string{}
	for _, name := range []string{"meta", "kv", "list", "set", "zset", "counter"} {
		src, err := os.ReadFile("../../demos/starlark-memory/api/" + name + ".star")
		if err != nil {
			t.Fatal(err)
		}
		scripts[name+".star"] = string(src)
	}
	executor := newTestStarlarkExecutor(t, scripts)
	site := "demo-memory-routes"
	call := func(entrypoint, body string) string {
		t.Helper()
		resp, err := executor.Invoke(context.Background(), Bundle{
			Site: site, Version: 1, Routes: []Route{{Path: "/api/" + strings.TrimSuffix(entrypoint, ".star"), Kind: RouteHTTP, Entrypoint: entrypoint}},
		}, InvocationRequest{Method: http.MethodPost, Route: "/api/" + strings.TrimSuffix(entrypoint, ".star"), Body: []byte(body)})
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || resp.Headers["Content-Type"][0] != "application/json; charset=utf-8" {
			t.Fatalf("response = %+v body=%s, want JSON ok", resp, string(resp.Body))
		}
		return string(resp.Body)
	}

	for _, step := range []struct {
		entrypoint string
		body       string
		want       string
	}{
		{"meta.star", `{"op":"clear"}`, `"cleared"`},
		{"kv.star", `{"op":"set","value":"hello from json"}`, `"value": "hello from json"`},
		{"kv.star", `{"op":"set_object","value":"structured"}`, `"blob_label": "QUACK"`},
		{"list.star", `{"op":"push_right","value":"launch"}`, `"length_after_push": 1`},
		{"list.star", `{"op":"pop_left"}`, `"popped": "launch"`},
		{"set.star", `{"op":"add","value":"demo"}`, `"added": true`},
		{"set.star", `{"op":"contains","value":"demo"}`, `"contains_value": true`},
		{"zset.star", `{"op":"add","value":"Ada","score":2}`, `"added": true`},
		{"zset.star", `{"op":"score","value":"Ada"}`, `"score": 2.0`},
		{"counter.star", `{"op":"incr","delta":5}`, `"value": 5`},
		{"counter.star", `{"op":"decr","delta":1}`, `"value": 4`},
	} {
		body := call(step.entrypoint, step.body)
		if !strings.Contains(body, step.want) {
			t.Fatalf("%s %s body = %s, want %s", step.entrypoint, step.body, body, step.want)
		}
	}
}

func TestStarlarkExecutorWebSocketHandlersReturnHostEffects(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{"socket.star": `
def on_connect(ctx):
    return [
        ws.accept(),
        ws.subscribe(ctx.conn_id, "doc:123"),
        ws.send(ctx.conn_id, {"type": "connected", "path": ctx.path}),
        timers.set(key="heartbeat:" + ctx.conn_id, after="30s", event={"type": "heartbeat"}),
    ]

def on_message(ctx, msg):
    return [events.publish("doc:" + msg["doc_id"], {"type": msg["type"], "by": ctx.conn_id})]

def on_event(ctx, event):
    return [ws.send(ctx.conn_id, {"type": "event", "topic": event.topic, "payload": event.payload})]
`})

	effects, err := executor.InvokeWebSocket(context.Background(), Bundle{
		Site: "foo", Version: 1, Routes: []Route{{Path: "/api/somesocket", Kind: RouteWebSocket, Entrypoint: "socket.star"}},
	}, WebSocketEvent{
		Site: "foo", Version: 1, Route: "/api/somesocket/room", ConnID: "c1", EventType: WebSocketEventConnect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 4 || effects[1].Type != WebSocketEffectSubscribe || effects[1].Topic != "doc:123" {
		t.Fatalf("connect effects = %#v, want subscribe effect", effects)
	}
	if effects[2].Type != WebSocketEffectSend || string(effects[2].Payload) != `{"path":"/room","type":"connected"}` {
		t.Fatalf("send effect = %#v payload=%s, want JSON connected payload", effects[2], effects[2].Payload)
	}
	if effects[3].Type != WebSocketEffectSetTimer || effects[3].Key != "heartbeat:c1" || effects[3].After != "30s" {
		t.Fatalf("timer effect = %#v, want durable timer intent", effects[3])
	}

	effects, err = executor.InvokeWebSocket(context.Background(), Bundle{
		Site: "foo", Version: 1, Routes: []Route{{Path: "/api/somesocket", Kind: RouteWebSocket, Entrypoint: "socket.star"}},
	}, WebSocketEvent{
		Site: "foo", Version: 1, Route: "/api/somesocket", ConnID: "c1", EventType: WebSocketEventMessage,
		Message: []byte(`{"type":"edit","doc_id":"123"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Type != WebSocketEffectPublish || effects[0].Topic != "doc:123" || string(effects[0].Payload) != `{"by":"c1","type":"edit"}` {
		t.Fatalf("message effects = %#v payload=%s, want publish effect", effects, effects[0].Payload)
	}

	effects, err = executor.InvokeWebSocket(context.Background(), Bundle{
		Site: "foo", Version: 1, Routes: []Route{{Path: "/api/somesocket", Kind: RouteWebSocket, Entrypoint: "socket.star"}},
	}, WebSocketEvent{
		Site: "foo", Version: 1, Route: "/api/somesocket", ConnID: "c1", EventType: WebSocketEventEvent,
		Event: WebSocketServerEvent{Topic: "doc:123", Payload: []byte(`{"ok":true}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Type != WebSocketEffectSend || string(effects[0].Payload) != `{"payload":{"ok":true},"topic":"doc:123","type":"event"}` {
		t.Fatalf("event effects = %#v payload=%s, want send effect", effects, effects[0].Payload)
	}
}

func TestDemoPixeldrawWebSocketExecutes(t *testing.T) {
	src, err := os.ReadFile("../../demos/pixeldraw/api/pixels.star")
	if err != nil {
		t.Fatal(err)
	}
	colorsSrc, err := os.ReadFile("../../demos/pixeldraw/api/colors.star")
	if err != nil {
		t.Fatal(err)
	}
	executor := newTestStarlarkExecutor(t, map[string]string{
		"pixels.star": string(src),
		"colors.star": string(colorsSrc),
	})
	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "demo-pixeldraw", Version: 1,
		Routes: []Route{{Path: "/api/colors", Kind: RouteHTTP, Entrypoint: "colors.star"}},
	}, InvocationRequest{
		Site: "demo-pixeldraw", Version: 1, Route: "/api/colors", Method: http.MethodGet,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"colors"`, `"id": "red"`, `"code": 5`, `"hex": "#2469d8"`} {
		if !strings.Contains(string(resp.Body), want) {
			t.Fatalf("colors payload = %s, want %s", resp.Body, want)
		}
	}

	bundle := Bundle{
		Site: "demo-pixeldraw", Version: 1,
		Routes: []Route{{Path: "/ws", Kind: RouteWebSocket, Entrypoint: "pixels.star"}},
	}

	effects, err := executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: "demo-pixeldraw", Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventConnect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 3 || effects[0].Type != WebSocketEffectSubscribe || effects[0].Topic != "pixeldraw:canvas" {
		t.Fatalf("connect effects = %#v, want subscribe, ready, and snapshot", effects)
	}
	if got := string(effects[2].Payload); !strings.Contains(got, `"type":"canvas_snapshot"`) || !strings.Contains(got, `"width":48`) {
		t.Fatalf("snapshot payload = %s, want pixeldraw canvas snapshot", got)
	}

	effects, err = executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: "demo-pixeldraw", Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventMessage,
		Message: []byte(`{"type":"draw_pixels","pixels":[{"x":1,"y":2,"color":"red"},{"x":2,"y":2,"color":"blue"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Type != WebSocketEffectPublish || effects[0].Topic != "pixeldraw:canvas" {
		t.Fatalf("draw effects = %#v, want publish effect", effects)
	}
	update := string(effects[0].Payload)
	for _, want := range []string{`"type":"pixels_updated"`, `"revision":1`, `"drawing_id":`, `"color":5`, `"color":13`} {
		if !strings.Contains(update, want) {
			t.Fatalf("draw payload = %s, want %s", update, want)
		}
	}

	effects, err = executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: "demo-pixeldraw", Version: 1, Route: "/ws", ConnID: "c2", EventType: WebSocketEventEvent,
		Event: WebSocketServerEvent{Topic: "pixeldraw:canvas", Payload: []byte(update)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Type != WebSocketEffectSend || string(effects[0].Payload) != update {
		t.Fatalf("event effects = %#v, want update forwarded", effects)
	}
}

func TestDemoPixeldrawIgnoresLegacyCanvasKeys(t *testing.T) {
	site := "demo-pixeldraw-legacy-noise"
	src, err := os.ReadFile("../../demos/pixeldraw/api/pixels.star")
	if err != nil {
		t.Fatal(err)
	}
	executor := newTestStarlarkExecutor(t, map[string]string{
		"pixels.star": string(src),
		"seed.star": `
def handle(req):
    memory.clear()
    memory.set("pixeldraw:drawings", ["old-drawing"])
    for i in range(48 * 48):
        memory.set("pixeldraw:px:" + str(i), "red")
    memory.set("pixeldraw:revision", 9)
    return (200, {}, "seeded")
`,
	})

	_, err = executor.Invoke(context.Background(), Bundle{
		Site: site, Version: 1,
		Routes: []Route{{Path: "/seed", Kind: RouteHTTP, Entrypoint: "seed.star"}},
		Limits: ResourceLimits{MaxExecutionSteps: 1_000_000},
	}, InvocationRequest{Site: site, Version: 1, Route: "/seed", Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}

	effects, err := executor.InvokeWebSocket(context.Background(), Bundle{
		Site: site, Version: 1,
		Routes: []Route{{Path: "/ws", Kind: RouteWebSocket, Entrypoint: "pixels.star"}},
	}, WebSocketEvent{
		Site: site, Version: 1, Route: "/ws", ConnID: "legacy", EventType: WebSocketEventConnect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 3 {
		t.Fatalf("connect effects = %#v, want subscribe, ready, and snapshot", effects)
	}
	snapshot := string(effects[2].Payload)
	for _, want := range []string{`"type":"canvas_snapshot"`, `"revision":0`, `"pixels":[]`} {
		if !strings.Contains(snapshot, want) {
			t.Fatalf("snapshot payload = %s, want %s", snapshot, want)
		}
	}
}

func TestDemoPixeldrawDrawIgnoresPlainRevisionValue(t *testing.T) {
	site := "demo-pixeldraw-plain-revision"
	src, err := os.ReadFile("../../demos/pixeldraw/api/pixels.star")
	if err != nil {
		t.Fatal(err)
	}
	executor := newTestStarlarkExecutor(t, map[string]string{
		"pixels.star": string(src),
		"seed.star": `
def handle(req):
    memory.clear()
    memory.set("pixeldraw:drawings", ["old-drawing"])
    memory.set("pixeldraw:drawing:old-drawing:revision", 9)
    return (200, {}, "seeded")
`,
	})

	_, err = executor.Invoke(context.Background(), Bundle{
		Site: site, Version: 1,
		Routes: []Route{{Path: "/seed", Kind: RouteHTTP, Entrypoint: "seed.star"}},
	}, InvocationRequest{Site: site, Version: 1, Route: "/seed", Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}

	effects, err := executor.InvokeWebSocket(context.Background(), Bundle{
		Site: site, Version: 1,
		Routes: []Route{{Path: "/ws", Kind: RouteWebSocket, Entrypoint: "pixels.star"}},
	}, WebSocketEvent{
		Site: site, Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventMessage,
		Message: []byte(`{"type":"draw_pixels","drawing_id":"old-drawing","pixels":[{"x":1,"y":2,"color":"red"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Type != WebSocketEffectPublish {
		t.Fatalf("draw effects = %#v, want publish effect", effects)
	}
	update := string(effects[0].Payload)
	for _, want := range []string{`"type":"pixels_updated"`, `"revision":1`, `"drawing_id":"old-drawing"`, `"color":5`} {
		if !strings.Contains(update, want) {
			t.Fatalf("draw payload = %s, want %s", update, want)
		}
	}
}

func TestDemoPixeldrawSnapshotIncludesPersistedPixels(t *testing.T) {
	site := "demo-pixeldraw-persisted-pixels"
	src, err := os.ReadFile("../../demos/pixeldraw/api/pixels.star")
	if err != nil {
		t.Fatal(err)
	}
	executor := newTestStarlarkExecutor(t, map[string]string{"pixels.star": string(src)})
	bundle := Bundle{
		Site: site, Version: 1,
		Routes: []Route{{Path: "/ws", Kind: RouteWebSocket, Entrypoint: "pixels.star"}},
	}

	effects, err := executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: site, Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventConnect,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := string(effects[2].Payload)
	drawingID := jsonStringField(t, snapshot, "drawing_id")
	if drawingID == "" {
		t.Fatalf("snapshot payload = %s, want drawing_id", snapshot)
	}

	_, err = executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: site, Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventMessage,
		Message: []byte(`{"type":"draw_pixels","drawing_id":"` + drawingID + `","pixels":[{"x":1,"y":2,"color":"red"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	effects, err = executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: site, Version: 1, Route: "/ws", ConnID: "c2", EventType: WebSocketEventConnect,
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted := string(effects[2].Payload)
	for _, want := range []string{`"drawing_id":"` + drawingID + `"`, `"revision":1`, `"i":97`, `"color":5`} {
		if !strings.Contains(persisted, want) {
			t.Fatalf("snapshot payload = %s, want %s", persisted, want)
		}
	}
}

func TestDemoPixeldrawSnapshotIgnoresOversizedPixelKeys(t *testing.T) {
	site := "demo-pixeldraw-oversized-pixel-key"
	src, err := os.ReadFile("../../demos/pixeldraw/api/pixels.star")
	if err != nil {
		t.Fatal(err)
	}
	executor := newTestStarlarkExecutor(t, map[string]string{
		"pixels.star": string(src),
		"seed.star": `
def handle(req):
    memory.clear()
    memory.set("pixeldraw:drawings", ["old-drawing"])
    memory.set("pixeldraw:drawing:old-drawing:pixels", {
        "97": "red",
        "9" * 20000: 5,
    })
    return (200, {}, "seeded")
`,
	})

	_, err = executor.Invoke(context.Background(), Bundle{
		Site: site, Version: 1,
		Routes: []Route{{Path: "/seed", Kind: RouteHTTP, Entrypoint: "seed.star"}},
	}, InvocationRequest{Site: site, Version: 1, Route: "/seed", Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}

	effects, err := executor.InvokeWebSocket(context.Background(), Bundle{
		Site: site, Version: 1,
		Routes: []Route{{Path: "/ws", Kind: RouteWebSocket, Entrypoint: "pixels.star"}},
		Limits: ResourceLimits{MaxExecutionSteps: 1_000_000},
	}, WebSocketEvent{
		Site: site, Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventConnect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 3 {
		t.Fatalf("connect effects = %#v, want subscribe, ready, and snapshot", effects)
	}
	snapshot := string(effects[2].Payload)
	for _, want := range []string{`"type":"canvas_snapshot"`, `"drawing_id":"old-drawing"`, `"i":97`, `"color":5`} {
		if !strings.Contains(snapshot, want) {
			t.Fatalf("snapshot payload = %s, want %s", snapshot, want)
		}
	}
}

func jsonStringField(t *testing.T, body, field string) string {
	t.Helper()
	needle := `"` + field + `":"`
	start := strings.Index(body, needle)
	if start < 0 {
		return ""
	}
	start += len(needle)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		return ""
	}
	return body[start : start+end]
}

func TestServiceInvokesStarlarkBehindPolicyGate(t *testing.T) {
	svc := NewService(ServiceOptions{
		Repository: newRuntimeRepo(RouteMetadata{
			Site: "foo", Version: 3, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark,
			BundleObjectKey: "app.star", Methods: []string{http.MethodPost}, RequiredCapabilities: []string{"runtime.http"},
		}),
		Policies:        runtimePolicyLoader{policies: []domain.PolicyRecord{{ScopeType: domain.ScopeSystem, Key: appsettings.SettingRuntimeHTTPFeature, Mode: "allow"}}},
		Executor:        newTestStarlarkExecutor(t, map[string]string{"app.star": `def handle(req): return (200, {"content-type": "text/plain"}, "ok")`}),
		EnableExecution: true,
	})

	resp, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 3, Route: "/api", Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(resp.Body) != "ok" {
		t.Fatalf("response = %+v body=%q, want ok", resp, string(resp.Body))
	}
}

func TestServiceInvokesWebSocketStarlarkBehindPolicyGate(t *testing.T) {
	svc := NewService(ServiceOptions{
		Repository: newRuntimeRepo(RouteMetadata{
			Site: "foo", Version: 3, RoutePath: "/api/somesocket", RouteKind: RouteWebSocket, RuntimeKind: RuntimeStarlark,
			BundleObjectKey: "socket.star", RequiredCapabilities: []string{"runtime.websocket"},
		}),
		Policies:        allowRuntimeWebSocketPolicy(),
		Executor:        newTestStarlarkExecutor(t, map[string]string{"socket.star": `def on_connect(ctx): return [ws.send(ctx.conn_id, "ok")]`}),
		EnableExecution: true,
	})

	effects, err := svc.InvokeWebSocket(context.Background(), WebSocketInvocationRequest{
		Site: "foo", Version: 3, Route: "/api/somesocket", ConnID: "c1", EventType: WebSocketEventConnect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Type != WebSocketEffectSend || string(effects[0].Payload) != "ok" {
		t.Fatalf("effects = %#v, want send ok", effects)
	}
}

func TestServiceDeniesWebSocketWithoutPolicy(t *testing.T) {
	svc := NewService(ServiceOptions{
		Repository: newRuntimeRepo(RouteMetadata{
			Site: "foo", Version: 3, RoutePath: "/api/somesocket", RouteKind: RouteWebSocket, RuntimeKind: RuntimeStarlark,
			BundleObjectKey: "socket.star", RequiredCapabilities: []string{"runtime.websocket"},
		}),
		Policies:        runtimePolicyLoader{},
		Executor:        newTestStarlarkExecutor(t, map[string]string{"socket.star": `def on_connect(ctx): return []`}),
		EnableExecution: true,
	})

	_, err := svc.InvokeWebSocket(context.Background(), WebSocketInvocationRequest{
		Site: "foo", Version: 3, Route: "/api/somesocket", ConnID: "c1", EventType: WebSocketEventConnect,
	})
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("InvokeWebSocket error = %v, want capability denial", err)
	}
}

func TestServicePassesRuntimeBundleFilesToExecutor(t *testing.T) {
	executor := &recordingExecutor{resp: InvocationResponse{StatusCode: http.StatusOK, Body: []byte("ok")}}
	repo := newRuntimeRepo(RouteMetadata{
		Site: "foo", SiteSHA: "foo-sha", Version: 3, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark,
		BundleObjectKey: "app.star", Methods: []string{http.MethodGet}, RequiredCapabilities: []string{"runtime.http"},
	})
	repo.files = []domain.UploadFileRecord{{RelativePath: "data.txt", BlobPath: "data-blob", FileSHA: "sha", Bytes: 4}}
	svc := NewService(ServiceOptions{
		Repository:      repo,
		Policies:        allowRuntimeHTTPPolicy(),
		Executor:        executor,
		EnableExecution: true,
	})

	if _, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 3, Route: "/api", Method: http.MethodGet}); err != nil {
		t.Fatal(err)
	}
	if len(executor.bundle.Files) != 1 || executor.bundle.Files[0].Path != "data.txt" || executor.bundle.Files[0].BlobPath != "data-blob" {
		t.Fatalf("bundle files = %#v, want runtime upload files", executor.bundle.Files)
	}
}

func TestServiceDeniesRuntimeWithoutPolicy(t *testing.T) {
	svc := NewService(ServiceOptions{
		Repository: newRuntimeRepo(RouteMetadata{
			Site: "foo", Version: 3, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark,
			BundleObjectKey: "app.star", RequiredCapabilities: []string{"runtime.http"},
		}),
		Policies:        runtimePolicyLoader{},
		Executor:        newTestStarlarkExecutor(t, map[string]string{"app.star": `def handle(req): return (200, {}, "ok")`}),
		EnableExecution: true,
	})

	_, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 3, Route: "/api", Method: http.MethodGet})
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("InvokeHTTP error = %v, want capability denial", err)
	}
}

func TestServiceRejectsOversizedRequestBeforeExecutor(t *testing.T) {
	executor := &recordingExecutor{}
	svc := NewService(ServiceOptions{
		Repository: newRuntimeRepo(RouteMetadata{
			Site: "foo", Version: 1, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark,
			BundleObjectKey: "app.star", ResourceLimits: ResourceLimits{MaxRequestBytes: 3},
		}),
		Policies:        allowRuntimeHTTPPolicy(),
		Executor:        executor,
		EnableExecution: true,
	})

	_, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet, Body: []byte("toolarge")})
	if !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("InvokeHTTP error = %v, want request too large", err)
	}
	if executor.called {
		t.Fatal("executor was called for oversized request")
	}
}

func TestServiceRejectsOversizedResponse(t *testing.T) {
	svc := NewService(ServiceOptions{
		Repository: newRuntimeRepo(RouteMetadata{
			Site: "foo", Version: 1, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark,
			BundleObjectKey: "app.star", ResourceLimits: ResourceLimits{MaxResponseBytes: 3},
		}),
		Policies:        allowRuntimeHTTPPolicy(),
		Executor:        &recordingExecutor{resp: InvocationResponse{StatusCode: http.StatusOK, Body: []byte("toolarge")}},
		EnableExecution: true,
	})

	_, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("InvokeHTTP error = %v, want response too large", err)
	}
}

func TestServiceTimesOutRunawayStarlark(t *testing.T) {
	svc := NewService(ServiceOptions{
		Repository: newRuntimeRepo(RouteMetadata{
			Site: "foo", Version: 1, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark,
			BundleObjectKey: "app.star", ResourceLimits: ResourceLimits{MaxDurationMillis: 25, MaxExecutionSteps: 1000},
		}),
		Policies: allowRuntimeHTTPPolicy(),
		Executor: newTestStarlarkExecutor(t, map[string]string{"app.star": `
def handle(req):
    while True:
        pass
    return (200, {}, "never")
`}),
		EnableExecution: true,
	})

	_, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})
	if !errors.Is(err, ErrInvocationFailure) && !errors.Is(err, ErrTimeout) {
		t.Fatalf("InvokeHTTP error = %v, want timeout or step-limit invocation failure", err)
	}
}

func TestServiceLimitsConcurrentInvocations(t *testing.T) {
	executor := &blockingExecutor{started: make(chan struct{}), release: make(chan struct{})}
	svc := NewService(ServiceOptions{
		Repository:      newRuntimeRepo(RouteMetadata{Site: "foo", Version: 1, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark, BundleObjectKey: "app.star"}),
		Policies:        allowRuntimeHTTPPolicy(),
		Executor:        executor,
		MaxConcurrency:  1,
		EnableExecution: true,
	})

	started := make(chan struct{})
	done := make(chan error)
	go func() {
		close(started)
		_, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})
		done <- err
	}()
	<-started
	executor.waitStarted(t)

	_, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})
	if !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("second InvokeHTTP error = %v, want concurrency limit", err)
	}
	close(executor.release)
	if err := <-done; err != nil {
		t.Fatalf("first InvokeHTTP error = %v", err)
	}
}

func newTestStarlarkExecutor(t *testing.T, scripts map[string]string) *StarlarkExecutor {
	t.Helper()
	executor, err := NewStarlarkExecutor(scriptMap(scripts), ResourceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

type scriptMap map[string]string

func (m scriptMap) OpenScript(ctx context.Context, objectKey string) (io.ReadCloser, error) {
	script, ok := m[objectKey]
	if !ok {
		return nil, errors.New("missing script")
	}
	return io.NopCloser(strings.NewReader(script)), nil
}

type runtimeRepo struct {
	routes []RouteMetadata
	files  []domain.UploadFileRecord
}

func newRuntimeRepo(routes ...RouteMetadata) runtimeRepo {
	return runtimeRepo{routes: routes}
}

func (r runtimeRepo) ListRuntimeRoutes(ctx context.Context, siteSHA string, version int64) ([]RouteMetadata, error) {
	return append([]RouteMetadata(nil), r.routes...), nil
}

func (r runtimeRepo) ListCurrentRuntimeRoutes(ctx context.Context) ([]RouteMetadata, error) {
	return append([]RouteMetadata(nil), r.routes...), nil
}

func (r runtimeRepo) ListRuntimeBundleFiles(ctx context.Context, siteSHA string, version int64) ([]domain.UploadFileRecord, bool, error) {
	return append([]domain.UploadFileRecord(nil), r.files...), true, nil
}

type runtimePolicyLoader struct {
	policies []domain.PolicyRecord
}

func allowRuntimeHTTPPolicy() runtimePolicyLoader {
	return runtimePolicyLoader{policies: []domain.PolicyRecord{{ScopeType: domain.ScopeSystem, Key: appsettings.SettingRuntimeHTTPFeature, Mode: "allow"}}}
}

func allowRuntimeWebSocketPolicy() runtimePolicyLoader {
	return runtimePolicyLoader{policies: []domain.PolicyRecord{{ScopeType: domain.ScopeSystem, Key: appsettings.SettingRuntimeWebSocketFeature, Mode: "allow"}}}
}

func (l runtimePolicyLoader) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	var out []domain.PolicyRecord
	for _, p := range l.policies {
		for _, scope := range scopes {
			if p.ScopeType == scope.Type && p.ScopeID == scope.ID {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

type recordingExecutor struct {
	called bool
	bundle Bundle
	resp   InvocationResponse
	err    error
}

func (e *recordingExecutor) Invoke(ctx context.Context, bundle Bundle, req InvocationRequest) (InvocationResponse, error) {
	e.called = true
	e.bundle = bundle
	return e.resp, e.err
}

type blockingExecutor struct {
	startOnce sync.Once
	started   chan struct{}
	release   chan struct{}
}

func (e *blockingExecutor) Invoke(ctx context.Context, bundle Bundle, req InvocationRequest) (InvocationResponse, error) {
	e.startOnce.Do(func() {
		close(e.started)
	})
	select {
	case <-e.release:
		return InvocationResponse{StatusCode: http.StatusOK}, nil
	case <-ctx.Done():
		return InvocationResponse{}, ctx.Err()
	}
}

func (e *blockingExecutor) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-e.started:
	case <-time.After(time.Second):
		t.Fatal("blocking executor did not start")
	}
}
