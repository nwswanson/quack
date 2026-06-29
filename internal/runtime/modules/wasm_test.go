package modules

import (
	"encoding/json"
	"strings"
	"testing"

	"quack/internal/manifest"

	"go.starlark.net/starlark"
)

func TestWASMInputEncodesBytesAsBase64ForQuackWASMABI(t *testing.T) {
	args := starlark.Tuple{starlark.NewDict(1)}
	if err := args[0].(*starlark.Dict).SetKey(starlark.String("input"), starlark.Bytes(string([]byte{0xff, 0x00, 'x'}))); err != nil {
		t.Fatal(err)
	}

	input, err := wasmInput(args, quackWASMABI)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) < 2 || input[0] != wasmFormatJSON || input[1] != 0 {
		t.Fatalf("input envelope = %v, want JSON format with zero flags", input[:2])
	}

	var payload map[string]string
	if err := json.Unmarshal(input[2:], &payload); err != nil {
		t.Fatal(err)
	}
	if payload["input"] != "/wB4" {
		t.Fatalf("input bytes = %q, want base64", payload["input"])
	}
}

func TestWASMInputKeepsBytesAsStringForQuackJSONABI(t *testing.T) {
	args := starlark.Tuple{starlark.Bytes("abc")}
	input, err := wasmInput(args, quackJSONABI)
	if err != nil {
		t.Fatal(err)
	}
	if string(input) != `"abc"` {
		t.Fatalf("input = %q, want legacy JSON string bytes", string(input))
	}
}

func TestWASMExecutionModeDefaultsToInterruptible(t *testing.T) {
	interruptible, fastRequested := wasmExecutionMode(manifest.WASMModule{}, true)
	if !interruptible || fastRequested {
		t.Fatalf("interruptible = %v fastRequested = %v, want default safe mode", interruptible, fastRequested)
	}
}

func TestWASMExecutionModeRequiresPolicyForFastMode(t *testing.T) {
	requested := false
	cfg := manifest.WASMModule{Execution: manifest.WASMExecution{Interruptible: &requested}}

	interruptible, fastRequested := wasmExecutionMode(cfg, false)
	if !interruptible || !fastRequested {
		t.Fatalf("interruptible = %v fastRequested = %v, want requested fast mode forced safe", interruptible, fastRequested)
	}

	interruptible, fastRequested = wasmExecutionMode(cfg, true)
	if interruptible || !fastRequested {
		t.Fatalf("interruptible = %v fastRequested = %v, want policy-allowed fast mode", interruptible, fastRequested)
	}
}

func TestWASMCacheKeyIncludesEffectiveExecutionMode(t *testing.T) {
	requested := false
	req := wasmLoadRequest{
		site: "site", version: 1, name: "rules",
		file: WASMFile{Path: "plugins/rules.wasm", BlobPath: "blob", FileSHA: "sha"},
		cfg: manifest.WASMModule{
			Path:      "plugins/rules.wasm",
			ABI:       "quack:json-v1",
			Execution: manifest.WASMExecution{Interruptible: &requested},
		},
	}
	limits := normalizeWASMLimits(manifest.WASMLimits{MemoryPages: 1})

	safeKey := wasmCacheKey(req, limits)
	req.fastExecutionAllowed = true
	fastKey := wasmCacheKey(req, limits)
	if safeKey == fastKey {
		t.Fatal("cache key did not separate safe and fast wasm execution modes")
	}
	if !strings.Contains(safeKey, "\x00true\x00") || !strings.Contains(fastKey, "\x00false\x00") {
		t.Fatalf("safe key = %q fast key = %q, want interruptible marker", safeKey, fastKey)
	}
}
