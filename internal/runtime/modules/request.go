package modules

import (
	"fmt"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

var RequestModule = &starlarkstruct.Module{
	Name: "request",
	Members: starlark.StringDict{
		"body_text": starlark.NewBuiltin("request.body_text", requestBodyText),
	},
}

func requestBodyText(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var body starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "body", &body); err != nil {
		return nil, err
	}
	switch v := body.(type) {
	case starlark.Bytes:
		return starlark.String(string(v)), nil
	case starlark.String:
		return v, nil
	default:
		return nil, fmt.Errorf("%s: got %s, want bytes or string", fn.Name(), body.Type())
	}
}
