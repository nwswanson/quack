package runtime

import (
	"fmt"
	"go.starlark.net/starlark"
	"net/http"
	"strings"
)

func requestTuple(req InvocationRequest, routePath string) starlark.Tuple {
	headers := starlark.NewDict(len(req.Headers))
	for key, values := range req.Headers {
		_ = headers.SetKey(starlark.String(strings.ToLower(key)), starlark.NewList(stringValues(values)))
	}
	return starlark.Tuple{
		starlark.String(req.Method),
		starlark.String(pathUnderRoute(req.Route, routePath)),
		starlark.String(req.Query),
		headers,
		starlark.Bytes(string(req.Body)),
	}
}
func responseFromValue(v starlark.Value) (InvocationResponse, error) {
	tuple, ok := v.(starlark.Tuple)
	if !ok || tuple.Len() != 3 {
		return InvocationResponse{}, fmt.Errorf("%w: response must be (status, headers, body)", ErrInvocationFailure)
	}
	status, err := starlark.AsInt32(tuple[0])
	if err != nil {
		return InvocationResponse{}, fmt.Errorf("%w: status must be int", ErrInvocationFailure)
	}
	headers, err := headersFromValue(tuple[1])
	if err != nil {
		return InvocationResponse{}, err
	}
	body, err := bodyFromValue(tuple[2])
	if err != nil {
		return InvocationResponse{}, err
	}
	return InvocationResponse{StatusCode: int(status), Headers: headers, Body: body}, nil
}
func headersFromValue(v starlark.Value) (map[string][]string, error) {
	out := map[string][]string{}
	if v == starlark.None {
		return out, nil
	}
	dict, ok := v.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("%w: headers must be dict", ErrInvocationFailure)
	}
	for _, item := range dict.Items() {
		key, ok := starlark.AsString(item[0])
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%w: header key must be string", ErrInvocationFailure)
		}
		values, err := headerValues(item[1])
		if err != nil {
			return nil, err
		}
		canonical := http.CanonicalHeaderKey(key)
		out[canonical] = append(out[canonical], values...)
	}
	return out, nil
}
func headerValues(v starlark.Value) ([]string, error) {
	if s, ok := starlark.AsString(v); ok {
		return []string{s}, nil
	}
	switch values := v.(type) {
	case starlark.Tuple:
		return tupleStrings(values)
	case *starlark.List:
		return listStrings(values)
	default:
		return nil, fmt.Errorf("%w: header value must be string/list/tuple", ErrInvocationFailure)
	}
}
func tupleStrings(values starlark.Tuple) ([]string, error) {
	out := make([]string, 0, values.Len())
	for _, value := range values {
		var err error
		out, err = appendStarlarkString(out, value)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func listStrings(values *starlark.List) ([]string, error) {
	out := make([]string, 0, values.Len())
	iter := values.Iterate()
	defer iter.Done()
	var value starlark.Value
	for iter.Next(&value) {
		var err error
		out, err = appendStarlarkString(out, value)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func appendStarlarkString(out []string, value starlark.Value) ([]string, error) {
	s, ok := starlark.AsString(value)
	if !ok {
		return nil, fmt.Errorf("%w: header values must be strings", ErrInvocationFailure)
	}
	return append(out, s), nil
}
func stringValues(values []string) []starlark.Value {
	out := make([]starlark.Value, 0, len(values))
	for _, value := range values {
		out = append(out, starlark.String(value))
	}
	return out
}
func bodyFromValue(v starlark.Value) ([]byte, error) {
	switch value := v.(type) {
	case starlark.String:
		return []byte(string(value)), nil
	case starlark.Bytes:
		return []byte(string(value)), nil
	case starlark.NoneType:
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: body must be string, bytes, or None", ErrInvocationFailure)
	}
}
