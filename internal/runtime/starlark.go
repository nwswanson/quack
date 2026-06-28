package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"

	"quack/internal/domain"
	"quack/internal/logbuffer"
	"quack/internal/runtime/modules"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkjson"
	"go.starlark.net/syntax"
)

type StarlarkExecutor struct {
	loader              ScriptLoader
	limits              ResourceLimits
	logs                *logbuffer.Service
	policies            policyLoader
	settings            SettingsReader
	secrets             modules.SecretGetter
	hardware            modules.HardwareService
	allowHTTPClientSelf bool
	cachePrograms       bool
	programMu           sync.Mutex
	programs            map[programCacheKey]*starlark.Program
}

type policyLoader interface {
	LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error)
}

var predeclareds = starlark.StringDict{
	"json":    starlarkjson.Module,
	"request": modules.RequestModule,
	"uuid":    modules.UUIDModule,
}

const maxProgramCacheEntries = 128

func NewStarlarkExecutor(loader ScriptLoader, limits ResourceLimits) (*StarlarkExecutor, error) {
	if loader == nil {
		return nil, fmt.Errorf("script loader is required")
	}
	return &StarlarkExecutor{
		loader:        loader,
		limits:        limits.withDefaults(),
		cachePrograms: true,
		programs:      map[programCacheKey]*starlark.Program{},
	}, nil
}

func (e *StarlarkExecutor) SetLogBuffer(logs *logbuffer.Service) {
	e.logs = logs
}

func (e *StarlarkExecutor) SetHTTPClientPolicy(policies policyLoader, settings SettingsReader, allowSelf bool) {
	e.policies = policies
	e.settings = settings
	e.allowHTTPClientSelf = allowSelf
}

func (e *StarlarkExecutor) SetSecretStore(secrets modules.SecretGetter) {
	e.secrets = secrets
}

func (e *StarlarkExecutor) SetHardwareService(hardware modules.HardwareService) {
	e.hardware = hardware
}

func (e *StarlarkExecutor) SetProgramCacheEnabled(enabled bool) {
	e.programMu.Lock()
	defer e.programMu.Unlock()
	e.cachePrograms = enabled
	if !enabled {
		e.programs = map[programCacheKey]*starlark.Program{}
	}
}

func (e *StarlarkExecutor) Invoke(ctx context.Context, bundle Bundle, req InvocationRequest) (InvocationResponse, error) {
	route, err := singleHTTPRoute(bundle)
	if err != nil {
		return InvocationResponse{}, err
	}
	limits := bundle.Limits.withFallback(e.limits)
	scriptKey := route.ScriptKey
	if scriptKey == "" {
		scriptKey = route.Entrypoint
	}
	thread, stopCancel := starlarkThread(ctx, req.Method+" "+req.Route, limits.MaxExecutionSteps)
	defer stopCancel()
	predeclared := e.predeclareds(ctx, bundle, route, limits)
	program, err := e.program(ctx, bundle, route, scriptKey, limits, predeclared)
	if err != nil {
		return InvocationResponse{}, e.wrapStarlarkError(bundle, route, err)
	}
	globals, err := program.Init(thread, predeclared)
	if err != nil {
		return InvocationResponse{}, e.wrapStarlarkError(bundle, route, err)
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
		return InvocationResponse{}, e.wrapStarlarkError(bundle, route, err)
	}
	return responseFromValue(result)
}

type programCacheKey struct {
	site      string
	version   int64
	scriptKey string
	filename  string
	sourceID  string
	limits    ResourceLimits
	predefs   string
}

func (e *StarlarkExecutor) program(ctx context.Context, bundle Bundle, route Route, scriptKey string, limits ResourceLimits, predeclared starlark.StringDict) (*starlark.Program, error) {
	key := programCacheKey{
		site:      bundle.Site,
		version:   bundle.Version,
		scriptKey: scriptKey,
		filename:  route.Entrypoint,
		sourceID:  scriptSourceID(bundle, route, scriptKey),
		limits:    limits,
		predefs:   predeclaredSignature(predeclared),
	}
	if key.sourceID != "" {
		if program := e.cachedProgram(key); program != nil {
			return program, nil
		}
	}

	script, err := e.readScript(ctx, scriptKey, limits)
	if err != nil {
		return nil, err
	}
	if key.sourceID == "" {
		sum := sha256.Sum256([]byte(script))
		key.sourceID = "sha256:" + hex.EncodeToString(sum[:])
		if program := e.cachedProgram(key); program != nil {
			return program, nil
		}
	}
	_, program, err := starlark.SourceProgramOptions(syntax.LegacyFileOptions(), route.Entrypoint, script, predeclared.Has)
	if err != nil {
		return nil, err
	}
	e.programMu.Lock()
	defer e.programMu.Unlock()
	if e.cachePrograms {
		if existing := e.programs[key]; existing != nil {
			return existing, nil
		}
		if len(e.programs) >= maxProgramCacheEntries {
			e.programs = map[programCacheKey]*starlark.Program{}
		}
		e.programs[key] = program
	}
	return program, nil
}

func (e *StarlarkExecutor) cachedProgram(key programCacheKey) *starlark.Program {
	e.programMu.Lock()
	defer e.programMu.Unlock()
	if !e.cachePrograms {
		return nil
	}
	return e.programs[key]
}

func scriptSourceID(bundle Bundle, route Route, scriptKey string) string {
	for _, file := range bundle.Files {
		if file.FileSHA == "" {
			continue
		}
		if file.BlobPath == scriptKey || file.Path == route.Entrypoint {
			return "file:" + file.FileSHA
		}
	}
	return ""
}

func predeclaredSignature(predeclared starlark.StringDict) string {
	keys := make([]string, 0, len(predeclared))
	for key := range predeclared {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, "\x00")
}

func (e *StarlarkExecutor) predeclareds(ctx context.Context, bundle Bundle, route Route, limits ResourceLimits) starlark.StringDict {
	out := make(starlark.StringDict, len(predeclareds)+2)
	for key, value := range predeclareds {
		out[key] = value
	}
	out["memory"] = modules.NewMemoryModule(bundle.Site, limits.MaxMemoryBytes)
	out["log"] = modules.NewLogModule(ctx, modules.LogModuleOptions{
		Buffer:  e.logs,
		Site:    bundle.Site,
		Version: bundle.Version,
		Route:   route.Path,
	})
	out["http"] = e.newHTTPModule(ctx, bundle, route)
	out["secret"] = modules.NewSecretModule(ctx, bundle.Site, e.secrets)
	if e.hardware != nil {
		out["camera"] = modules.NewCameraModule(ctx, bundle.Site, e.hardware)
		out["serial"] = modules.NewSerialModule(ctx, bundle.Site, e.hardware)
	}
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
	thread.SetLocal("context", ctx)
	if maxSteps != 0 && maxSteps != math.MaxUint64 {
		thread.SetMaxExecutionSteps(maxSteps)
	}
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
		backtrace := evalErr.Backtrace()
		return fmt.Errorf("%w:\n%s", ErrInvocationFailure, backtrace)
	}
	return fmt.Errorf("%w: %v", ErrInvocationFailure, err)
}

func (e *StarlarkExecutor) wrapStarlarkError(bundle Bundle, route Route, err error) error {
	wrapped := wrapStarlarkError(err)
	var evalErr *starlark.EvalError
	stack := ""
	if errors.As(err, &evalErr) {
		stack = evalErr.Backtrace()
	}
	if route.ScriptKey != "" && route.Entrypoint != "" && route.ScriptKey != route.Entrypoint {
		stack = strings.ReplaceAll(stack, route.ScriptKey, route.Entrypoint)
		if stack != "" {
			wrapped = fmt.Errorf("%w:\n%s", ErrInvocationFailure, stack)
		}
	}
	if e.logs != nil {
		event := logbuffer.Event{
			Level:      "error",
			Source:     "starlark_error",
			Site:       bundle.Site,
			Version:    bundle.Version,
			Route:      route.Path,
			Message:    "starlark invocation failed",
			StackTrace: stack,
			Attributes: map[string]string{
				"error": wrapped.Error(),
			},
		}
		if stack == "" {
			event.StackTrace = wrapped.Error()
		}
		e.logs.Add(event)
	}
	slog.Error("starlark invocation failed", "site", bundle.Site, "version", bundle.Version, "route", route.Path, "error", wrapped, "backtrace", stack)
	return wrapped
}
