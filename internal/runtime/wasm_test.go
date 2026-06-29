package runtime

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"quack/internal/manifest"
)

func TestStarlarkWASMModuleQuackJSONABI(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{
		"app.star": `
rules = wasm.module("rules")

def handle(req):
    decision = rules.evaluate({"topic": "orders.created"})
    return (200, {}, "%s %s" % (decision["allow"], decision["reason"]))
`,
		"plugins/rules.wasm": string(testQuackJSONWASM([]byte(`{"allow":true,"reason":"ok"}`))),
	})

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "foo", Version: 1,
		Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
		Files: []BundleFile{
			{Path: "app.star", BlobPath: "app.star", FileSHA: "app"},
			{Path: "plugins/rules.wasm", BlobPath: "plugins/rules.wasm", FileSHA: "rules-v1"},
		},
		WASM: map[string]manifest.WASMModule{
			"rules": {
				Path:            "plugins/rules.wasm",
				ABI:             "quack:json-v1",
				RetainInstances: 1,
				Limits: manifest.WASMLimits{
					TimeoutMS:      25,
					MemoryPages:    1,
					MaxInputBytes:  1024,
					MaxOutputBytes: 1024,
				},
			},
		},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "True ok" {
		t.Fatalf("body = %q, want wasm decision", string(resp.Body))
	}
}

func TestStarlarkWASMModuleQuackWASMABI(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{
		"app.star": `
rules = wasm.module("rules")

def handle(req):
    decision = rules.evaluate({"topic": "orders.created"})
    return (200, {}, "%s %s" % (decision["allow"], decision["reason"]))
`,
		"plugins/rules.wasm": string(testQuackJSONWASM(envelopedJSON(0, []byte(`{"allow":true,"reason":"ok"}`)))),
	})

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "foo-errors", Version: 1,
		Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
		Files: []BundleFile{
			{Path: "app.star", BlobPath: "app.star", FileSHA: "app"},
			{Path: "plugins/rules.wasm", BlobPath: "plugins/rules.wasm", FileSHA: "rules-error-v1"},
		},
		WASM: map[string]manifest.WASMModule{
			"rules": {
				Path: "plugins/rules.wasm",
				ABI:  "quack:wasm-v1",
				Limits: manifest.WASMLimits{
					TimeoutMS:      25,
					MemoryPages:    1,
					MaxInputBytes:  1024,
					MaxOutputBytes: 1024,
				},
			},
		},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "True ok" {
		t.Fatalf("body = %q, want wasm decision", string(resp.Body))
	}
}

func TestStarlarkWASMModuleQuackWASMABIGuestError(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{
		"app.star": `
rules = wasm.module("rules")

def handle(req):
    rules.missing()
    return (200, {}, "unreachable")
`,
		"plugins/rules.wasm": string(testQuackJSONWASM(envelopedJSON(3, []byte(`"unknown function"`)))),
	})

	_, err := executor.Invoke(context.Background(), Bundle{
		Site: "foo", Version: 1,
		Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
		Files: []BundleFile{
			{Path: "app.star", BlobPath: "app.star", FileSHA: "app"},
			{Path: "plugins/rules.wasm", BlobPath: "plugins/rules.wasm", FileSHA: "rules-v1"},
		},
		WASM: map[string]manifest.WASMModule{
			"rules": {
				Path: "plugins/rules.wasm",
				ABI:  "quack:wasm-v1",
				Limits: manifest.WASMLimits{
					TimeoutMS:      25,
					MemoryPages:    1,
					MaxInputBytes:  1024,
					MaxOutputBytes: 1024,
				},
			},
		},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if err == nil || !strings.Contains(err.Error(), "wasm unknown function: unknown function") {
		t.Fatalf("Invoke error = %v, want wasm unknown function", err)
	}
}

func TestStarlarkWASMModuleUnknownNameFails(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{"app.star": `
def handle(req):
    wasm.module("missing")
    return (200, {}, "unreachable")
`})

	_, err := executor.Invoke(context.Background(), Bundle{
		Site: "foo", Version: 1,
		Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if err == nil || !strings.Contains(err.Error(), `unknown wasm module "missing"`) {
		t.Fatalf("Invoke error = %v, want unknown wasm module", err)
	}
}

func testQuackJSONWASM(output []byte) []byte {
	var wasm []byte
	wasm = append(wasm, 0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00)
	wasm = section(wasm, 1, vec(
		0x03,
		0x60, 0x01, 0x7f, 0x01, 0x7f,
		0x60, 0x02, 0x7f, 0x7f, 0x00,
		0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7e,
	))
	wasm = section(wasm, 3, vec(0x03, 0x00, 0x01, 0x02))
	wasm = section(wasm, 5, vec(0x01, 0x00, 0x01))
	wasm = section(wasm, 6, vec(0x01, 0x7f, 0x01, 0x41, 0x80, 0x08, 0x0b))
	wasm = section(wasm, 7, exports(
		export("memory", 0x02, 0x00),
		export("alloc", 0x00, 0x00),
		export("free", 0x00, 0x01),
		export("call", 0x00, 0x02),
	))
	allocBody := body(nil,
		0x23, 0x00,
		0x23, 0x00,
		0x20, 0x00,
		0x6a,
		0x24, 0x00,
		0x0b,
	)
	freeBody := body(nil, 0x0b)
	callCode := []byte{
		0x41, 0x80, 0x10,
		0xad,
		0x42, 0x20,
		0x86,
		0x42,
	}
	callCode = append(callCode, uleb(uint64(len(output)))...)
	callCode = append(callCode, 0x84, 0x0b)
	callBody := body(nil, callCode...)
	wasm = section(wasm, 10, codeSection(allocBody, freeBody, callBody))
	data := []byte{0x01, 0x00, 0x41, 0x80, 0x10, 0x0b}
	data = append(data, uleb(uint64(len(output)))...)
	data = append(data, output...)
	wasm = section(wasm, 11, data)
	return wasm
}

func envelopedJSON(status byte, payload []byte) []byte {
	out := []byte{status, 0}
	return append(out, payload...)
}

func section(dst []byte, id byte, payload []byte) []byte {
	dst = append(dst, id)
	dst = append(dst, uleb(uint64(len(payload)))...)
	return append(dst, payload...)
}

func vec(items ...byte) []byte {
	return items
}

func body(locals []byte, code ...byte) []byte {
	payload := append([]byte{0x00}, locals...)
	payload = append(payload, code...)
	out := uleb(uint64(len(payload)))
	return append(out, payload...)
}

func exports(values ...[]byte) []byte {
	out := uleb(uint64(len(values)))
	for _, value := range values {
		out = append(out, value...)
	}
	return out
}

func codeSection(bodies ...[]byte) []byte {
	out := uleb(uint64(len(bodies)))
	for _, body := range bodies {
		out = append(out, body...)
	}
	return out
}

func export(name string, kind byte, index uint64) []byte {
	out := uleb(uint64(len(name)))
	out = append(out, name...)
	out = append(out, kind)
	out = append(out, uleb(index)...)
	return out
}

func uleb(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			return out
		}
	}
}
