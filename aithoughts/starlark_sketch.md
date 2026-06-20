`HTTP request -> Starlark request tuple -> handle(req) -> response tuple -> HTTP response`

Use Starlark-Go via `go.starlark.net/starlark`; its documented embedding path is `ExecFile` to load a script and `starlark.Call` to invoke a function from Go. ([Go Packages][1]) Register both `/api` and `/api/`: Go’s `ServeMux` dispatches to the most specific matching pattern, and `/api/` is the subtree-style route. ([Go Packages][2])

## Contract

Starlark receives:

```python
(method, path, query, headers, body)
```

Where:

```text
method  = "GET", "POST", ...
path    = path under /api, e.g. /api/users -> /users, /api -> /
query   = raw query string
headers = dict[str, list[str]]
body    = bytes
```

Starlark returns:

```python
(status, headers, body)
```

Where:

```text
status  = int
headers = dict[str, str | list[str]]
body    = str | bytes | None
```

Example `app.star`:

```python
def handle(req):
    method, path, query, headers, body = req

    if method == "GET" and path == "/health":
        return (
            200,
            {"content-type": "application/json"},
            '{"ok": true}\n',
        )

    if method == "POST" and path == "/echo":
        return (
            200,
            {"content-type": "application/octet-stream"},
            body,
        )

    return (
        200,
        {"content-type": "text/plain"},
        "handled by starlark: %s %s?%s\n" % (method, path, query),
    )
```

## Sketch: `server.go`

```go
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"go.starlark.net/starlark"
)

type App struct {
	handle   starlark.Value
	maxBody  int64
	maxSteps uint64
}

func LoadApp(scriptPath string, maxBody int64, maxSteps uint64) (*App, error) {
	src, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, err
	}

	thread := &starlark.Thread{Name: "load " + scriptPath}

	globals, err := starlark.ExecFile(thread, scriptPath, string(src), nil)
	if err != nil {
		return nil, err
	}

	handle, ok := globals["handle"]
	if !ok {
		return nil, fmt.Errorf("%s must define def handle(req)", scriptPath)
	}

	if _, ok := handle.(starlark.Callable); !ok {
		return nil, fmt.Errorf("handle must be callable, got %s", handle.Type())
	}

	// Freeze globals so request executions cannot mutate shared module state.
	globals.Freeze()

	return &App{
		handle:   handle,
		maxBody:  maxBody,
		maxSteps: maxSteps,
	}, nil
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api" && !strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, a.maxBody))
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}

	req, err := requestTuple(r, body)
	if err != nil {
		http.Error(w, "bad request tuple", http.StatusInternalServerError)
		return
	}

	thread := &starlark.Thread{
		Name: r.Method + " " + r.URL.Path,
	}
	thread.SetMaxExecutionSteps(a.maxSteps)

	done := make(chan struct{})
	go func() {
		select {
		case <-r.Context().Done():
			thread.Cancel("request cancelled")
		case <-done:
		}
	}()

	result, err := starlark.Call(thread, a.handle, starlark.Tuple{req}, nil)
	close(done)

	if err != nil {
		logStarlarkError(err)
		http.Error(w, "starlark handler error", http.StatusInternalServerError)
		return
	}

	status, headers, respBody, err := parseResponse(result)
	if err != nil {
		log.Printf("bad starlark response: %v", err)
		http.Error(w, "bad starlark response", http.StatusInternalServerError)
		return
	}

	for key, values := range headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}

	w.WriteHeader(status)
	_, _ = w.Write(respBody)
}

func requestTuple(r *http.Request, body []byte) (starlark.Tuple, error) {
	headers := starlark.NewDict(len(r.Header))

	for key, values := range r.Header {
		starlarkValues := make([]starlark.Value, 0, len(values))
		for _, value := range values {
			starlarkValues = append(starlarkValues, starlark.String(value))
		}

		err := headers.SetKey(
			starlark.String(strings.ToLower(key)),
			starlark.NewList(starlarkValues),
		)
		if err != nil {
			return nil, err
		}
	}

	path := strings.TrimPrefix(r.URL.Path, "/api")
	if path == "" {
		path = "/"
	}

	return starlark.Tuple{
		starlark.String(r.Method),
		starlark.String(path),
		starlark.String(r.URL.RawQuery),
		headers,
		starlark.Bytes(string(body)),
	}, nil
}

func parseResponse(v starlark.Value) (int, http.Header, []byte, error) {
	tuple, ok := v.(starlark.Tuple)
	if !ok {
		return 0, nil, nil, fmt.Errorf("response must be tuple, got %s", v.Type())
	}

	if tuple.Len() != 3 {
		return 0, nil, nil, fmt.Errorf("response must be (status, headers, body)")
	}

	status, err := starlark.AsInt32(tuple[0])
	if err != nil {
		return 0, nil, nil, fmt.Errorf("status must be int: %w", err)
	}

	if status < 100 || status > 999 {
		return 0, nil, nil, fmt.Errorf("invalid HTTP status: %d", status)
	}

	headers, err := headersFromValue(tuple[1])
	if err != nil {
		return 0, nil, nil, err
	}

	body, err := bodyFromValue(tuple[2])
	if err != nil {
		return 0, nil, nil, err
	}

	return status, headers, body, nil
}

func headersFromValue(v starlark.Value) (http.Header, error) {
	h := http.Header{}

	if v == starlark.None {
		return h, nil
	}

	dict, ok := v.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("headers must be dict, got %s", v.Type())
	}

	for _, item := range dict.Items() {
		key, ok := starlark.AsString(item[0])
		if !ok {
			return nil, fmt.Errorf("header key must be string, got %s", item[0].Type())
		}

		canonicalKey := http.CanonicalHeaderKey(key)

		switch value := item[1].(type) {
		case starlark.String:
			h.Add(canonicalKey, string(value))

		case starlark.Tuple:
			for _, elem := range value {
				s, ok := starlark.AsString(elem)
				if !ok {
					return nil, fmt.Errorf("header value list must contain strings")
				}
				h.Add(canonicalKey, s)
			}

		case *starlark.List:
			iter := value.Iterate()
			var elem starlark.Value
			for iter.Next(&elem) {
				s, ok := starlark.AsString(elem)
				if !ok {
					iter.Done()
					return nil, fmt.Errorf("header value list must contain strings")
				}
				h.Add(canonicalKey, s)
			}
			iter.Done()

		default:
			return nil, fmt.Errorf("header value must be string/list/tuple, got %s", value.Type())
		}
	}

	return h, nil
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
		return nil, fmt.Errorf("body must be string, bytes, or None, got %s", v.Type())
	}
}

func logStarlarkError(err error) {
	var evalErr *starlark.EvalError
	if errors.As(err, &evalErr) {
		log.Printf("starlark error:\n%s", evalErr.Backtrace())
		return
	}

	log.Printf("starlark error: %v", err)
}

func main() {
	app, err := LoadApp("app.star", 1<<20, 100_000)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()

	// /api handles exactly /api.
	// /api/ handles the subtree, e.g. /api/users, /api/v1/foo.
	mux.Handle("/api", app)
	mux.Handle("/api/", app)

	server := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Println("listening on :8080")
	log.Fatal(server.ListenAndServe())
}
```

## Why this shape works

The Go side owns HTTP correctness: routing, body limits, status validation, header normalization, cancellation, and error reporting. The Starlark side only owns application logic.

Freezing the loaded globals is important because Starlark’s `Freeze` makes a value and transitively reachable values immutable and safe to publish to concurrently running interpreters. ([Go Packages][3]) A fresh `starlark.Thread` per request gives you request-local execution state, and `SetMaxExecutionSteps` gives you a basic CPU/runaway-script guard. ([Go Packages][3]) The body limit uses `http.MaxBytesReader`, which is specifically intended to prevent large request bodies from wasting server resources. ([Go Packages][2])

The first production extensions I’d add are hot reload via `atomic.Value`, structured logging with request IDs, a JSON helper exposed into Starlark, and optionally a richer response form later:

```python
return {
    "status": 200,
    "headers": {"content-type": "application/json"},
    "body": '{"ok": true}',
}
```

But for a first embedded Rack-like boundary, the tuple contract is clean and easy to validate.

[1]: https://pkg.go.dev/go.starlark.net "go.starlark.net module - go.starlark.net - Go Packages"
[2]: https://pkg.go.dev/net/http "http package - net/http - Go Packages"
[3]: https://pkg.go.dev/go.starlark.net/starlark "starlark package - go.starlark.net/starlark - Go Packages"
