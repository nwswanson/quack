package runtime

import (
	"context"
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
