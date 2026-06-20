package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"quack/internal/domain"
	"quack/internal/policy"

	"go.starlark.net/starlark"
)

var ErrDisabled = errors.New("runtime execution is disabled")

var (
	ErrRouteNotFound     = errors.New("runtime route was not found")
	ErrCapabilityDenied  = errors.New("runtime capability denied")
	ErrMethodNotAllowed  = errors.New("runtime method is not allowed")
	ErrRequestTooLarge   = errors.New("runtime request body is too large")
	ErrResponseTooLarge  = errors.New("runtime response body is too large")
	ErrTimeout           = errors.New("runtime execution timed out")
	ErrConcurrencyLimit  = errors.New("runtime concurrency limit reached")
	ErrInvalidRuntime    = errors.New("invalid runtime configuration")
	ErrInvocationFailure = errors.New("runtime invocation failed")
)

const (
	RuntimeStarlark RuntimeKind = "starlark"

	// DefaultMaxRequestBytes is intentionally small for the first public runtime
	// pass. Admin-configurable runtime limits can raise this later per site/route.
	DefaultMaxRequestBytes int64 = 1 << 20
	// DefaultMaxResponseBytes caps buffered runtime responses before runtimehttp
	// writes headers. Streaming runtimes will need an equivalent streaming cap.
	DefaultMaxResponseBytes int64 = 1 << 20
	// DefaultMaxDuration keeps runaway scripts from tying up public request
	// workers. Starlark also gets a step limit as a second guard.
	DefaultMaxDuration = 250 * time.Millisecond
	// DefaultMaxMemoryBytes is recorded as a contract limit for future admin UI
	// configuration. Starlark-Go does not expose a hard memory limiter, so Phase
	// 12 enforces bounded inputs/outputs/steps/concurrency and keeps this clearly
	// denoted until a process/WASM isolation layer can enforce memory directly.
	DefaultMaxMemoryBytes int64 = 32 << 20
	// DefaultMaxConcurrentInvocations limits concurrent user-code executions per
	// server process until a configurable per-site/per-route limiter exists.
	DefaultMaxConcurrentInvocations int64  = 8
	DefaultMaxExecutionSteps        uint64 = 100_000
	DefaultMaxScriptBytes           int64  = 256 << 10
)

// Bundle is the runtime-facing view of a published release.
//
// The Starlark executor currently receives a single immutable script object key
// through Route.Entrypoint. Future process/WASM/external executors can extend
// this with integrity data or richer bundle layout without involving HTTP.
type Bundle struct {
	Site    string
	Version int64
	Routes  []Route
	Limits  ResourceLimits
}

type RouteKind string

const (
	RouteHTTP      RouteKind = "http"
	RouteWebSocket RouteKind = "websocket"
)

type Route struct {
	Path       string
	Kind       RouteKind
	Entrypoint string
	Methods    []string
}

type RuntimeKind string

const (
	RuntimeDisabled RuntimeKind = "disabled"
)

// ResourceLimits records limits declared for a runtime route.
//
// Limits are persisted with route metadata so the admin UI can expose them
// later. Request bytes are enforced in runtimehttp and again before executor
// invocation; response bytes, duration, steps, and concurrency are enforced in
// runtime service/executor code. MaxMemoryBytes is denoted but not hard-enforced
// by Starlark-Go.
type ResourceLimits struct {
	MaxRequestBytes   int64  `json:"max_request_bytes,omitempty"`
	MaxResponseBytes  int64  `json:"max_response_bytes,omitempty"`
	MaxDurationMillis int64  `json:"max_duration_millis,omitempty"`
	MaxMemoryBytes    int64  `json:"max_memory_bytes,omitempty"`
	MaxConcurrency    int64  `json:"max_concurrency,omitempty"`
	MaxExecutionSteps uint64 `json:"max_execution_steps,omitempty"`
	MaxScriptBytes    int64  `json:"max_script_bytes,omitempty"`
}

// RouteMetadata is persisted release metadata, not an executable runtime.
//
// The runtime service loads this metadata into a Bundle and verifies the route's
// RequiredCapabilities against policy immediately before invocation. Upload-time
// validation is not enough because policy can change after a release is
// published.
type RouteMetadata struct {
	Site                 string
	SiteSHA              string
	Version              int64
	RuntimeKind          RuntimeKind
	RouteKind            RouteKind
	Entrypoint           string
	BundleObjectKey      string
	RoutePath            string
	Methods              []string
	RequiredCapabilities []string
	ResourceLimits       ResourceLimits
	CreatedAt            string
}

// InvocationRequest is the transport-neutral request passed to runtime code.
//
// Only deliberately exposed HTTP details live here. Remote address, host,
// cookies, trailers, and internal proxy/control headers are omitted for now; add
// them deliberately and test that sensitive headers cannot leak into user code.
type InvocationRequest struct {
	Site    string
	Version int64
	Route   string
	Method  string
	Query   string
	Headers map[string][]string
	Body    []byte
	Limits  ResourceLimits
}

// InvocationResponse is the transport-neutral response from runtime code.
//
// Status codes and response size are validated before runtimehttp writes them.
// runtimehttp also filters hop-by-hop response headers.
type InvocationResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

// Executor is the narrow boundary where untrusted code execution will live.
//
// Keep sandbox setup, filesystem isolation, network policy, timeout handling,
// and resource accounting behind this interface. The runtime package should
// orchestrate those concerns but must not import HTTP adapters, SQLite, or
// concrete storage implementations.
type Executor interface {
	Invoke(ctx context.Context, bundle Bundle, req InvocationRequest) (InvocationResponse, error)
}

type Repository interface {
	ListRuntimeRoutes(ctx context.Context, siteSHA string, version int64) ([]RouteMetadata, error)
	ListCurrentRuntimeRoutes(ctx context.Context) ([]RouteMetadata, error)
}

type ScriptLoader interface {
	OpenScript(ctx context.Context, objectKey string) (io.ReadCloser, error)
}

type ScriptLoaderFunc func(ctx context.Context, objectKey string) (io.ReadCloser, error)

func (f ScriptLoaderFunc) OpenScript(ctx context.Context, objectKey string) (io.ReadCloser, error) {
	return f(ctx, objectKey)
}

type Metrics interface {
	RecordInvocation(ctx context.Context, event InvocationEvent)
}

type InvocationEvent struct {
	Site        string
	Version     int64
	Route       string
	RuntimeKind RuntimeKind
	StatusCode  int
	Duration    time.Duration
	Error       string
}

type NoopMetrics struct{}

func (NoopMetrics) RecordInvocation(ctx context.Context, event InvocationEvent) {}

type ServiceOptions struct {
	Repository      Repository
	Policies        policy.Loader
	Executor        Executor
	MaxConcurrency  int64
	DefaultLimits   ResourceLimits
	Metrics         Metrics
	EnableExecution bool
}

// Service owns runtime invocation policy and orchestration.
//
// Keep the disabled implementation available for tests and for deployments that
// do not opt into runtime execution.
type Service interface {
	InvokeHTTP(ctx context.Context, req InvocationRequest) (InvocationResponse, error)
}

type service struct {
	repo        Repository
	policies    policy.Loader
	executor    Executor
	metrics     Metrics
	sem         chan struct{}
	defaults    ResourceLimits
	executionOn bool
}

func NewService(opts ServiceOptions) Service {
	if !opts.EnableExecution || opts.Repository == nil || opts.Executor == nil {
		return NewDisabledService()
	}
	maxConcurrency := opts.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrentInvocations
	}
	defaults := opts.DefaultLimits
	defaults = defaults.withDefaults()
	metrics := opts.Metrics
	if metrics == nil {
		metrics = NoopMetrics{}
	}
	return &service{
		repo:        opts.Repository,
		policies:    opts.Policies,
		executor:    opts.Executor,
		metrics:     metrics,
		sem:         make(chan struct{}, maxConcurrency),
		defaults:    defaults,
		executionOn: true,
	}
}

func (s *service) InvokeHTTP(ctx context.Context, req InvocationRequest) (InvocationResponse, error) {
	if !s.executionOn {
		return InvocationResponse{}, ErrDisabled
	}
	start := time.Now()
	var event InvocationEvent
	defer func() {
		event.Duration = time.Since(start)
		s.metrics.RecordInvocation(ctx, event)
	}()

	route, err := s.lookupRoute(ctx, req)
	if err != nil {
		event.Error = err.Error()
		return InvocationResponse{}, err
	}
	event = InvocationEvent{Site: route.Site, Version: route.Version, Route: route.RoutePath, RuntimeKind: route.RuntimeKind}
	if route.RuntimeKind == RuntimeDisabled {
		event.Error = ErrDisabled.Error()
		return InvocationResponse{}, ErrDisabled
	}
	if route.RuntimeKind != RuntimeStarlark {
		err := fmt.Errorf("%w: unsupported runtime kind %s", ErrInvalidRuntime, route.RuntimeKind)
		event.Error = err.Error()
		return InvocationResponse{}, err
	}
	if !methodAllowed(req.Method, route.Methods) {
		event.Error = ErrMethodNotAllowed.Error()
		return InvocationResponse{}, ErrMethodNotAllowed
	}
	if err := s.checkCapabilities(ctx, route); err != nil {
		event.Error = err.Error()
		return InvocationResponse{}, err
	}

	limits := route.ResourceLimits.withFallback(s.defaults)
	if int64(len(req.Body)) > limits.MaxRequestBytes {
		event.Error = ErrRequestTooLarge.Error()
		return InvocationResponse{}, ErrRequestTooLarge
	}
	if !s.acquire(ctx) {
		event.Error = ErrConcurrencyLimit.Error()
		return InvocationResponse{}, ErrConcurrencyLimit
	}
	defer s.release()

	timeout := time.Duration(limits.MaxDurationMillis) * time.Millisecond
	invokeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := s.executor.Invoke(invokeCtx, Bundle{
		Site:    route.Site,
		Version: route.Version,
		Routes:  []Route{{Path: route.RoutePath, Kind: route.RouteKind, Entrypoint: route.BundleObjectKey, Methods: append([]string(nil), route.Methods...)}},
		Limits:  limits,
	}, req)
	if err != nil {
		if errors.Is(invokeCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			event.Error = ErrTimeout.Error()
			return InvocationResponse{}, ErrTimeout
		}
		event.Error = err.Error()
		return InvocationResponse{}, err
	}
	if int64(len(resp.Body)) > limits.MaxResponseBytes {
		event.Error = ErrResponseTooLarge.Error()
		return InvocationResponse{}, ErrResponseTooLarge
	}
	if resp.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	if resp.StatusCode < 100 || resp.StatusCode > 999 {
		err := fmt.Errorf("%w: invalid status code %d", ErrInvocationFailure, resp.StatusCode)
		event.Error = err.Error()
		return InvocationResponse{}, err
	}
	event.StatusCode = resp.StatusCode
	return resp, nil
}

func (s *service) lookupRoute(ctx context.Context, req InvocationRequest) (RouteMetadata, error) {
	routes, err := s.repo.ListCurrentRuntimeRoutes(ctx)
	if err != nil {
		return RouteMetadata{}, err
	}
	var best RouteMetadata
	for _, route := range routes {
		if route.Site != req.Site || route.Version != req.Version || route.RouteKind != RouteHTTP {
			continue
		}
		if !routeMatches(req.Route, route.RoutePath) {
			continue
		}
		if best.RoutePath == "" || len(route.RoutePath) > len(best.RoutePath) {
			best = route
		}
	}
	if best.RoutePath == "" {
		return RouteMetadata{}, ErrRouteNotFound
	}
	return best, nil
}

func (s *service) checkCapabilities(ctx context.Context, route RouteMetadata) error {
	if s.policies == nil {
		return ErrCapabilityDenied
	}
	capabilities := append([]string(nil), route.RequiredCapabilities...)
	if route.RouteKind == RouteHTTP && !containsCapability(capabilities, policy.CapabilityRuntimeHTTP) {
		capabilities = append(capabilities, policy.CapabilityRuntimeHTTP)
	}
	policies, err := s.policies.LoadPolicies(ctx, policy.ScopesFor(domain.AdminUser{}, route.Site))
	if err != nil {
		return err
	}
	requests := make([]policy.CapabilityRequest, 0, len(capabilities))
	for _, cap := range capabilities {
		if cap != policy.CapabilityRuntimeHTTP {
			return fmt.Errorf("%w: unsupported runtime capability %s", ErrCapabilityDenied, cap)
		}
		requests = append(requests, policy.CapabilityRequest{Key: cap, Required: true, Value: "true"})
	}
	eval := policy.Evaluate(policies, requests)
	if eval.Allowed {
		return nil
	}
	if len(eval.Violations) > 0 && eval.Violations[0].Reason != "" {
		return fmt.Errorf("%w: %s", ErrCapabilityDenied, eval.Violations[0].Reason)
	}
	return ErrCapabilityDenied
}

func containsCapability(capabilities []string, want string) bool {
	for _, cap := range capabilities {
		if cap == want {
			return true
		}
	}
	return false
}

func (s *service) acquire(ctx context.Context) bool {
	select {
	case s.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	default:
		return false
	}
}

func (s *service) release() {
	<-s.sem
}

type DisabledService struct{}

func NewDisabledService() DisabledService {
	return DisabledService{}
}

func (DisabledService) InvokeHTTP(ctx context.Context, req InvocationRequest) (InvocationResponse, error) {
	return InvocationResponse{}, ErrDisabled
}

type StarlarkExecutor struct {
	loader ScriptLoader
	limits ResourceLimits
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
	thread := &starlark.Thread{Name: req.Method + " " + req.Route}
	thread.SetMaxExecutionSteps(limits.MaxExecutionSteps)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			thread.Cancel(ctx.Err().Error())
		case <-done:
		}
	}()
	defer close(done)

	globals, err := starlark.ExecFile(thread, route.Entrypoint, script, nil)
	if err != nil {
		return InvocationResponse{}, wrapStarlarkError(err)
	}
	handle, ok := globals["handle"]
	if !ok {
		return InvocationResponse{}, fmt.Errorf("%w: starlark entrypoint must define handle(req)", ErrInvalidRuntime)
	}
	if _, ok := handle.(starlark.Callable); !ok {
		return InvocationResponse{}, fmt.Errorf("%w: handle must be callable", ErrInvalidRuntime)
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

func singleHTTPRoute(bundle Bundle) (Route, error) {
	if len(bundle.Routes) != 1 {
		return Route{}, fmt.Errorf("%w: bundle must contain exactly one HTTP route", ErrInvalidRuntime)
	}
	route := bundle.Routes[0]
	if route.Kind != RouteHTTP {
		return Route{}, fmt.Errorf("%w: expected HTTP route", ErrInvalidRuntime)
	}
	return route, nil
}

func requestTuple(req InvocationRequest, routePath string) starlark.Tuple {
	headers := starlark.NewDict(len(req.Headers))
	for key, values := range req.Headers {
		list := make([]starlark.Value, 0, len(values))
		for _, value := range values {
			list = append(list, starlark.String(value))
		}
		_ = headers.SetKey(starlark.String(strings.ToLower(key)), starlark.NewList(list))
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
		out[http.CanonicalHeaderKey(key)] = append(out[http.CanonicalHeaderKey(key)], values...)
	}
	return out, nil
}

func headerValues(v starlark.Value) ([]string, error) {
	if s, ok := starlark.AsString(v); ok {
		return []string{s}, nil
	}
	switch values := v.(type) {
	case starlark.Tuple:
		out := make([]string, 0, values.Len())
		for _, elem := range values {
			s, ok := starlark.AsString(elem)
			if !ok {
				return nil, fmt.Errorf("%w: header values must be strings", ErrInvocationFailure)
			}
			out = append(out, s)
		}
		return out, nil
	case *starlark.List:
		out := make([]string, 0, values.Len())
		iter := values.Iterate()
		defer iter.Done()
		var elem starlark.Value
		for iter.Next(&elem) {
			s, ok := starlark.AsString(elem)
			if !ok {
				return nil, fmt.Errorf("%w: header values must be strings", ErrInvocationFailure)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: header value must be string/list/tuple", ErrInvocationFailure)
	}
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

func wrapStarlarkError(err error) error {
	var evalErr *starlark.EvalError
	if errors.As(err, &evalErr) {
		slog.Warn("starlark invocation failed", "backtrace", evalErr.Backtrace())
		return fmt.Errorf("%w: %v", ErrInvocationFailure, evalErr)
	}
	return fmt.Errorf("%w: %v", ErrInvocationFailure, err)
}

func methodAllowed(method string, methods []string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, candidate := range methods {
		if strings.EqualFold(method, candidate) {
			return true
		}
	}
	return false
}

func routeMatches(urlPath string, routePath string) bool {
	cleanRoute := strings.TrimRight(routePath, "/")
	if cleanRoute == "" {
		cleanRoute = "/"
	}
	return cleanRoute == "/" || urlPath == cleanRoute || strings.HasPrefix(urlPath, cleanRoute+"/")
}

func pathUnderRoute(urlPath string, routePath string) string {
	cleanRoute := strings.TrimRight(routePath, "/")
	if cleanRoute == "" || cleanRoute == "/" {
		if urlPath == "" {
			return "/"
		}
		return urlPath
	}
	if urlPath == cleanRoute {
		return "/"
	}
	out := strings.TrimPrefix(urlPath, cleanRoute)
	if out == "" {
		return "/"
	}
	return out
}

func (l ResourceLimits) withDefaults() ResourceLimits {
	defaults := ResourceLimits{
		MaxRequestBytes:   DefaultMaxRequestBytes,
		MaxResponseBytes:  DefaultMaxResponseBytes,
		MaxDurationMillis: DefaultMaxDuration.Milliseconds(),
		MaxMemoryBytes:    DefaultMaxMemoryBytes,
		MaxConcurrency:    DefaultMaxConcurrentInvocations,
		MaxExecutionSteps: DefaultMaxExecutionSteps,
		MaxScriptBytes:    DefaultMaxScriptBytes,
	}
	return l.withFallback(defaults)
}

func (l ResourceLimits) withFallback(defaults ResourceLimits) ResourceLimits {
	if l.MaxRequestBytes <= 0 {
		l.MaxRequestBytes = defaults.MaxRequestBytes
	}
	if l.MaxResponseBytes <= 0 {
		l.MaxResponseBytes = defaults.MaxResponseBytes
	}
	if l.MaxDurationMillis <= 0 {
		l.MaxDurationMillis = defaults.MaxDurationMillis
	}
	if l.MaxMemoryBytes <= 0 {
		l.MaxMemoryBytes = defaults.MaxMemoryBytes
	}
	if l.MaxConcurrency <= 0 {
		l.MaxConcurrency = defaults.MaxConcurrency
	}
	if l.MaxExecutionSteps == 0 {
		l.MaxExecutionSteps = defaults.MaxExecutionSteps
	}
	if l.MaxScriptBytes <= 0 {
		l.MaxScriptBytes = defaults.MaxScriptBytes
	}
	return l
}
