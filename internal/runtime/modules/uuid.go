package modules

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

var UUIDModule = &starlarkstruct.Module{
	Name: "uuid",
	Members: starlark.StringDict{
		"uuid4": starlark.NewBuiltin("uuid.uuid4", uuid4),
	},
}

func uuid4(
	thread *starlark.Thread,
	fn *starlark.Builtin,
	args starlark.Tuple,
	kwargs []starlark.Tuple,
) (starlark.Value, error) {
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs); err != nil {
		return nil, err
	}

	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("%s: %w", fn.Name(), err)
	}

	// RFC 4122 / UUID v4 bits:
	// Version: 0100
	// Variant: 10xxxxxx
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])

	return starlark.String(out[:]), nil
}
