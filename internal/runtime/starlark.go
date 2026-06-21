package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"quack/internal/runtime/modules"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkjson"
)

type StarlarkExecutor struct {
	loader ScriptLoader
	limits ResourceLimits
}

var predeclareds = starlark.StringDict{
	"json": starlarkjson.Module,
	"uuid": modules.UUIDModule,
}

func NewStarlarkExecutor(loader ScriptLoader, limits ResourceLimits) (*StarlarkExecutor, error) {
	if loader == nil {
		return nil, fmt.Errorf("script loader is required")
	}
	return &StarlarkExecutor{loader: loader, limits: limits.withDefaults()}, nil
}
func (e *StarlarkExecutor) Invoke(ctx context.Context, bundle Bundle, req InvocationRequest) (InvocationResponse, error) {
	route, err := singleHTTPRoute(bundle)
	if err != nil {
		return InvocationResponse{}, err
	}
	limits := bundle.Limits.withFallback(e.limits)
	script, err := e.readScript(ctx, route.Entrypoint, limits)
	if err != nil {
		return InvocationResponse{}, err
	}
	thread, stopCancel := starlarkThread(ctx, req.Method+" "+req.Route, limits.MaxExecutionSteps)
	defer stopCancel()
	globals, err := starlark.ExecFile(thread, route.Entrypoint, script, e.predeclareds(ctx, bundle, route, limits))
	if err != nil {
		return InvocationResponse{}, wrapStarlarkError(err)
	}
	handle, err := handleFromGlobals(globals)
	if err != nil {
		return InvocationResponse{}, err
	}
	globals.Freeze()
	result, err := starlark.Call(thread, handle, starlark.Tuple{requestTuple(req, route.Path)}, nil)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return InvocationResponse{}, ErrTimeout
		}
		return InvocationResponse{}, wrapStarlarkError(err)
	}
	return responseFromValue(result)
}
func (e *StarlarkExecutor) predeclareds(ctx context.Context, bundle Bundle, route Route, limits ResourceLimits) starlark.StringDict {
	out := make(starlark.StringDict, len(predeclareds)+2)
	for key, value := range predeclareds {
		out[key] = value
	}
	out["memory"] = modules.NewMemoryModule(bundle.Site, limits.MaxMemoryBytes)
	if route.FilesystemEnabled {
		out["fs"] = modules.NewFSModule(ctx, fsFiles(bundle.Files, route.FilesystemRoot), e.loader.OpenScript, limits.MaxScriptBytes)
	}
	return out
}
func fsFiles(files []BundleFile, root string) []modules.FSFile {
	out := make([]modules.FSFile, 0, len(files))
	for _, file := range files {
		rebased, ok := fsPathUnderRoot(file.Path, root)
		if !ok {
			continue
		}
		out = append(out, modules.FSFile{
			Path:    rebased,
			BlobKey: file.BlobPath,
			FileSHA: file.FileSHA,
			Bytes:   file.Bytes,
		})
	}
	return out
}
func fsPathUnderRoot(filePath string, root string) (string, bool) {
	filePath = strings.Trim(filePath, "/")
	root = strings.Trim(root, "/")
	if root == "" {
		return filePath, filePath != ""
	}
	if !strings.HasPrefix(filePath, root+"/") {
		return "", false
	}
	rebased := strings.TrimPrefix(filePath, root+"/")
	return rebased, rebased != ""
}
func (e *StarlarkExecutor) readScript(ctx context.Context, objectKey string, limits ResourceLimits) (string, error) {
	if objectKey == "" {
		return "", fmt.Errorf("%w: starlark bundle object key is required", ErrInvalidRuntime)
	}
	r, err := e.loader.OpenScript(ctx, objectKey)
	if err != nil {
		return "", fmt.Errorf("%w: open starlark script: %v", ErrInvalidRuntime, err)
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, limits.MaxScriptBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: read starlark script: %v", ErrInvalidRuntime, err)
	}
	if int64(len(data)) > limits.MaxScriptBytes {
		return "", fmt.Errorf("%w: starlark script exceeds %d bytes", ErrInvalidRuntime, limits.MaxScriptBytes)
	}
	return string(data), nil
}
func starlarkThread(ctx context.Context, name string, maxSteps uint64) (*starlark.Thread, func()) {
	thread := &starlark.Thread{Name: name}
	thread.SetMaxExecutionSteps(maxSteps)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			thread.Cancel(ctx.Err().Error())
		case <-done:
		}
	}()
	return thread, func() { close(done) }
}
func handleFromGlobals(globals starlark.StringDict) (starlark.Callable, error) {
	handle, ok := globals["handle"]
	if !ok {
		return nil, fmt.Errorf("%w: starlark entrypoint must define handle(req)", ErrInvalidRuntime)
	}
	callable, ok := handle.(starlark.Callable)
	if !ok {
		return nil, fmt.Errorf("%w: handle must be callable", ErrInvalidRuntime)
	}
	return callable, nil
}
func singleHTTPRoute(bundle Bundle) (Route, error) {
	if len(bundle.Routes) != 1 {
		return Route{}, fmt.Errorf("%w: bundle must contain exactly one HTTP route", ErrInvalidRuntime)
	}
	if route := bundle.Routes[0]; route.Kind == RouteHTTP {
		return route, nil
	}
	return Route{}, fmt.Errorf("%w: expected HTTP route", ErrInvalidRuntime)
}
func wrapStarlarkError(err error) error {
	var evalErr *starlark.EvalError
	if errors.As(err, &evalErr) {
		slog.Warn("starlark invocation failed", "backtrace", evalErr.Backtrace())
		return fmt.Errorf("%w: %v", ErrInvocationFailure, evalErr)
	}
	return fmt.Errorf("%w: %v", ErrInvocationFailure, err)
}
