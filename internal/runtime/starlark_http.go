package runtime

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"quack/internal/domain"
	"quack/internal/policy"
	"quack/internal/runtime/httpclient"
	appsettings "quack/internal/settings"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

func (e *StarlarkExecutor) newHTTPModule(ctx context.Context, bundle Bundle, route Route) *starlarkstruct.Module {
	client := e.httpClient(ctx, bundle, route)
	request := starlark.NewBuiltin("http.request", httpRequestBuiltin(client))
	members := starlark.StringDict{"request": request}
	for _, method := range []string{"get", "head", "post", "put", "patch", "delete"} {
		method := method
		members[method] = starlark.NewBuiltin("http."+method, func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			return httpVerbBuiltin(thread, client, strings.ToUpper(method), fn, args, kwargs)
		})
	}
	return &starlarkstruct.Module{Name: "http", Members: members}
}

func (e *StarlarkExecutor) httpClient(ctx context.Context, bundle Bundle, route Route) httpclient.Client {
	settings := domain.ServerSettings{
		HTTPClientMaxBytes:     appsettings.DefaultHTTPClientMaxBytes,
		HTTPClientMaxTimeoutMS: appsettings.DefaultHTTPClientMaxTimeoutMS,
	}
	if e.settings != nil {
		if loaded, err := e.settings.GetServerSettings(ctx); err == nil {
			settings = loaded
		}
	}
	allowed, reason, err := policy.RuntimeHTTPClientAllowed(ctx, e.policies, bundle.Site)
	if err != nil {
		allowed = false
		reason = err.Error()
	}
	return httpclient.Client{
		Policy: httpclient.Policy{
			Proxies:              bundle.APIProxies,
			MaxBytes:             settings.HTTPClientMaxBytes,
			MaxTimeout:           time.Duration(settings.HTTPClientMaxTimeoutMS) * time.Millisecond,
			AllowedPrivateCIDRs:  settings.HTTPClientAllowedCIDRs,
			AllowSelf:            e.allowHTTPClientSelf,
			AllowInsecureSSL:     settings.HTTPClientAllowInsecureSSL,
			HTTPClientAllowed:    allowed,
			HTTPClientDenyReason: reason,
		},
		Logs:    e.logs,
		Site:    bundle.Site,
		Version: bundle.Version,
		Route:   route.Path,
	}
}

func httpRequestBuiltin(client httpclient.Client) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var method string
		var urlValue string
		var headers starlark.Value = starlark.None
		var body starlark.Value = starlark.None
		var options starlark.Value = starlark.None
		if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "method", &method, "url", &urlValue, "headers?", &headers, "body?", &body, "options?", &options); err != nil {
			return nil, err
		}
		req, err := starlarkHTTPRequest(method, urlValue, headers, body, options)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(threadContext(thread), req)
		if err != nil {
			return nil, err
		}
		return starlarkHTTPResponse(resp)
	}
}

func httpVerbBuiltin(thread *starlark.Thread, client httpclient.Client, method string, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var urlValue string
	var headers starlark.Value = starlark.None
	var body starlark.Value = starlark.None
	var options starlark.Value = starlark.None
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "url", &urlValue, "headers?", &headers, "body?", &body, "options?", &options); err != nil {
		return nil, err
	}
	req, err := starlarkHTTPRequest(method, urlValue, headers, body, options)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(threadContext(thread), req)
	if err != nil {
		return nil, err
	}
	return starlarkHTTPResponse(resp)
}

func threadContext(thread *starlark.Thread) context.Context {
	if thread == nil {
		return context.Background()
	}
	if ctx, ok := thread.Local("context").(context.Context); ok && ctx != nil {
		return ctx
	}
	return context.Background()
}

func starlarkHTTPRequest(method, urlValue string, headersValue starlark.Value, bodyValue starlark.Value, optionsValue starlark.Value) (httpclient.Request, error) {
	headers, err := starlarkHTTPHeaders(headersValue)
	if err != nil {
		return httpclient.Request{}, err
	}
	body, err := starlarkHTTPBody(bodyValue)
	if err != nil {
		return httpclient.Request{}, err
	}
	timeout, followRedirects, err := starlarkHTTPOptions(optionsValue)
	if err != nil {
		return httpclient.Request{}, err
	}
	return httpclient.Request{Method: method, URL: urlValue, Headers: headers, Body: body, Timeout: timeout, FollowRedirects: followRedirects}, nil
}

func starlarkHTTPHeaders(value starlark.Value) (map[string][]string, error) {
	out := map[string][]string{}
	if value == nil || value == starlark.None {
		return out, nil
	}
	dict, ok := value.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("http headers must be a dict")
	}
	for _, item := range dict.Items() {
		key, ok := starlark.AsString(item[0])
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("http header keys must be strings")
		}
		values, err := headerValues(item[1])
		if err != nil {
			return nil, err
		}
		out[http.CanonicalHeaderKey(key)] = values
	}
	return out, nil
}

func starlarkHTTPBody(value starlark.Value) ([]byte, error) {
	if value == nil || value == starlark.None {
		return nil, nil
	}
	switch v := value.(type) {
	case starlark.String:
		return []byte(string(v)), nil
	case starlark.Bytes:
		return []byte(string(v)), nil
	default:
		return nil, fmt.Errorf("http body must be string, bytes, or None")
	}
}

func starlarkHTTPOptions(value starlark.Value) (time.Duration, bool, error) {
	if value == nil || value == starlark.None {
		return 0, false, nil
	}
	dict, ok := value.(*starlark.Dict)
	if !ok {
		return 0, false, fmt.Errorf("http options must be a dict")
	}
	var timeout time.Duration
	followRedirects := false
	for _, item := range dict.Items() {
		key, ok := starlark.AsString(item[0])
		if !ok {
			return 0, false, fmt.Errorf("http option keys must be strings")
		}
		switch key {
		case "timeout":
			s, ok := starlark.AsString(item[1])
			if !ok {
				return 0, false, fmt.Errorf("http options.timeout must be a duration string")
			}
			parsed, err := time.ParseDuration(s)
			if err != nil {
				return 0, false, fmt.Errorf("http options.timeout is invalid: %w", err)
			}
			timeout = parsed
		case "follow_redirects":
			b, ok := item[1].(starlark.Bool)
			if !ok {
				return 0, false, fmt.Errorf("http options.follow_redirects must be bool")
			}
			followRedirects = bool(b)
		default:
			return 0, false, fmt.Errorf("unsupported http option %q", key)
		}
	}
	return timeout, followRedirects, nil
}

func starlarkHTTPResponse(resp httpclient.Response) (starlark.Value, error) {
	headers := starlark.NewDict(len(resp.Headers))
	for key, values := range resp.Headers {
		_ = headers.SetKey(starlark.String(strings.ToLower(key)), starlark.NewList(stringValues(values)))
	}
	text := string(resp.Body)
	if !utf8.Valid(resp.Body) {
		text = strings.ToValidUTF8(text, "\uFFFD")
	}
	return starlarkstruct.FromStringDict(starlark.String("http.response"), starlark.StringDict{
		"status_code": starlark.MakeInt(resp.StatusCode),
		"headers":     headers,
		"body":        starlark.Bytes(string(resp.Body)),
		"text":        starlark.String(text),
	}), nil
}
