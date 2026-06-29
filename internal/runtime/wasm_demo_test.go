package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"quack/internal/manifest"
)

func TestWASMAddDemo(t *testing.T) {
	wasmBytes, err := os.ReadFile("../../demos/wasm-add/plugins/add.wasm")
	if err != nil {
		t.Fatal(err)
	}
	executor := newTestStarlarkExecutor(t, map[string]string{
		"api/home.star": `
calculator = wasm.module("calculator")

def handle(req):
    result = calculator.add({"left": 20, "right": 22})
    return (200, {"content-type": "application/json"}, json.encode(result))
`,
		"plugins/add.wasm": string(wasmBytes),
	})

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "wasm-add", Version: 1,
		Routes: []Route{{Path: "/", Kind: RouteHTTP, Entrypoint: "api/home.star"}},
		Files: []BundleFile{
			{Path: "api/home.star", BlobPath: "api/home.star", FileSHA: "home"},
			{Path: "plugins/add.wasm", BlobPath: "plugins/add.wasm", FileSHA: "add"},
		},
		WASM: map[string]manifest.WASMModule{
			"calculator": {
				Path: "plugins/add.wasm",
				ABI:  "quack:json-v1",
				Limits: manifest.WASMLimits{
					TimeoutMS:      25,
					MemoryPages:    4,
					MaxInputBytes:  1024,
					MaxOutputBytes: 1024,
				},
			},
		},
	}, InvocationRequest{Method: http.MethodGet, Route: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if body := string(resp.Body); body != "42.0" && !strings.Contains(body, `"sum":42`) {
		t.Fatalf("body = %q, want wasm sum", body)
	}
}

func TestWASMAddABIDemo(t *testing.T) {
	wasmBytes, err := os.ReadFile("../../demos/wasm-add-abi/plugins/add.wasm")
	if err != nil {
		t.Fatal(err)
	}
	executor := newTestStarlarkExecutor(t, map[string]string{
		"api/home.star": `
calculator = wasm.module("calculator")

def handle(req):
    result = calculator.add({"left": 20, "right": 22})
    return (200, {"content-type": "application/json"}, json.encode({"result": result}))
`,
		"plugins/add.wasm": string(wasmBytes),
	})

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "wasm-add-abi", Version: 1,
		Routes: []Route{{Path: "/", Kind: RouteHTTP, Entrypoint: "api/home.star"}},
		Files: []BundleFile{
			{Path: "api/home.star", BlobPath: "api/home.star", FileSHA: "home"},
			{Path: "plugins/add.wasm", BlobPath: "plugins/add.wasm", FileSHA: "add"},
		},
		WASM: map[string]manifest.WASMModule{
			"calculator": {
				Path: "plugins/add.wasm",
				ABI:  "quack:wasm-v1",
				Limits: manifest.WASMLimits{
					TimeoutMS:      25,
					MemoryPages:    1,
					MaxInputBytes:  1024,
					MaxOutputBytes: 1024,
				},
			},
		},
	}, InvocationRequest{Method: http.MethodGet, Route: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if body := string(resp.Body); !strings.Contains(body, `"result":42`) {
		t.Fatalf("body = %q, want wasm result", body)
	}
}

func TestWASMSTBResizeDemo(t *testing.T) {
	wasmBytes, err := os.ReadFile("../../demos/wasm-stb-resize/plugins/image_resize.wasm")
	if err != nil {
		t.Fatal(err)
	}
	executor := newTestStarlarkExecutor(t, map[string]string{
		"api/resize.star": `
images = wasm.module("images")

def parse_size(query):
    width = 320
    height = 0
    parts = query.split("&") if query else []
    for part in parts:
        if part.startswith("w="):
            width = int(part[2:])
        elif part.startswith("h="):
            height = int(part[2:])
    return width, height

def handle(req):
    method, path, query, headers, body = req
    width, height = parse_size(query)
    result = images.resize_image({"input": body, "width": width, "height": height})
    return (200, {"content-type": "application/json"}, json.encode(result))
`,
		"plugins/image_resize.wasm": string(wasmBytes),
	})
	body, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFgwJ/l7z8WAAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "wasm-stb-resize", Version: 1,
		Routes: []Route{{Path: "/api/resize", Kind: RouteHTTP, Entrypoint: "api/resize.star"}},
		Files: []BundleFile{
			{Path: "api/resize.star", BlobPath: "api/resize.star", FileSHA: "resize"},
			{Path: "plugins/image_resize.wasm", BlobPath: "plugins/image_resize.wasm", FileSHA: "stb"},
		},
		WASM: map[string]manifest.WASMModule{
			"images": {
				Path: "plugins/image_resize.wasm",
				ABI:  "quack:wasm-v1",
				Limits: manifest.WASMLimits{
					TimeoutMS:      250,
					MemoryPages:    160,
					MaxInputBytes:  4 << 20,
					MaxOutputBytes: 4 << 20,
				},
			},
		},
	}, InvocationRequest{Method: http.MethodPost, Route: "/api/resize", Query: "w=2&h=2", Body: body})
	if err != nil {
		t.Fatal(err)
	}

	var result struct {
		OK          bool    `json:"ok"`
		ContentType string  `json:"content_type"`
		Width       float64 `json:"width"`
		Height      float64 `json:"height"`
		Output      string  `json:"output"`
	}
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.ContentType != "image/png" || result.Width != 2 || result.Height != 2 {
		t.Fatalf("result = %+v, want resized png metadata", result)
	}
	output, err := base64.StdEncoding.DecodeString(result.Output)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) < 8 || string(output[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("output header = %x, want png", output[:min(len(output), 8)])
	}
}
