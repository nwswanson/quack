package modules

import (
	"encoding/json"
	"testing"

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
