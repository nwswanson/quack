package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"quack/internal/domain"
	"quack/internal/manifest"
	"quack/internal/policy"
)

type service struct {
	repo                Repository
	policies            policy.Loader
	executor            Executor
	wsExecutor          WebSocketExecutor
	eventExecutor       EventExecutor
	metrics             Metrics
	sem                 chan struct{}
	defaults            ResourceLimits
	settings            SettingsReader
	allowHTTPClientSelf bool
	executionOn         bool
}

func NewService(opts ServiceOptions) Service {
	if !opts.EnableExecution || opts.Repository == nil || opts.Executor == nil {
		return NewDisabledService()
	}
	metrics := opts.Metrics
	if metrics == nil {
		metrics = NoopMetrics{}
	}
	wsExecutor := opts.WebSocketExecutor
	if wsExecutor == nil {
		wsExecutor, _ = opts.Executor.(WebSocketExecutor)
	}
	eventExecutor := opts.EventExecutor
	if eventExecutor == nil {
		eventExecutor, _ = opts.Executor.(EventExecutor)
	}
	return &service{
		repo:                opts.Repository,
		policies:            opts.Policies,
		executor:            opts.Executor,
		wsExecutor:          wsExecutor,
		eventExecutor:       eventExecutor,
		metrics:             metrics,
		sem:                 make(chan struct{}, positiveOr(opts.MaxConcurrency, DefaultMaxConcurrentInvocations)),
		defaults:            opts.DefaultLimits.withDefaults(),
		settings:            opts.Settings,
		allowHTTPClientSelf: opts.AllowHTTPClientSelf,
		executionOn:         true,
	}
}

func (s *service) InvokeHTTP(ctx context.Context, req InvocationRequest) (resp InvocationResponse, err error) {
	if !s.executionOn {
		return InvocationResponse{}, ErrDisabled
	}
	event, start := InvocationEvent{}, time.Now()
	defer func() {
		event.Duration = time.Since(start)
		if err != nil {
			event.Error = err.Error()
			event.ErrorKind = invocationErrorKind(err)
		}
		event.StatusCode = resp.StatusCode
		s.metrics.RecordInvocation(ctx, event)
	}()
	route, limits, err := s.prepareHTTPInvocation(ctx, req)
	if err != nil {
		return InvocationResponse{}, err
	}
	event = eventForRoute(route)
	if !s.acquire(ctx) {
		return InvocationResponse{}, ErrConcurrencyLimit
	}
	defer s.release()
	invokeCtx, cancel := context.WithTimeout(ctx, time.Duration(limits.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	bundle, err := s.runtimeBundle(invokeCtx, route, limits)
	if err != nil {
		return InvocationResponse{}, err
	}
	resp, err = s.executor.Invoke(invokeCtx, bundle, req)
	if err != nil {
		return InvocationResponse{}, invocationError(invokeCtx, err)
	}
	return validateResponse(resp, limits)
}

func (s *service) InvokeWebSocket(ctx context.Context, req WebSocketInvocationRequest) (effects []WebSocketEffect, err error) {
	if !s.executionOn || s.wsExecutor == nil {
		return nil, ErrDisabled
	}
	event, start := InvocationEvent{}, time.Now()
	defer func() {
		event.Duration = time.Since(start)
		if err != nil {
			event.Error = err.Error()
			event.ErrorKind = invocationErrorKind(err)
		}
		s.metrics.RecordInvocation(ctx, event)
	}()
	route, limits, err := s.prepareWebSocketInvocation(ctx, req)
	if err != nil {
		return nil, err
	}
	event = eventForRoute(route)
	if !s.acquire(ctx) {
		return nil, ErrConcurrencyLimit
	}
	defer s.release()
	invokeCtx, cancel := context.WithTimeout(ctx, time.Duration(limits.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	bundle, err := s.runtimeBundle(invokeCtx, route, limits)
	if err != nil {
		return nil, err
	}
	effects, err = s.wsExecutor.InvokeWebSocket(invokeCtx, bundle, WebSocketEvent{
		Site: req.Site, Version: req.Version, Route: req.Route, Query: req.Query,
		Headers: req.Headers, ConnID: req.ConnID, EventType: req.EventType,
		Message: req.Message, Event: req.Event,
	})
	if err != nil {
		return nil, invocationError(invokeCtx, err)
	}
	return validateWebSocketEffects(effects, limits)
}

func (s *service) InvokeEvent(ctx context.Context, req EventInvocationRequest) (effects []WebSocketEffect, err error) {
	if !s.executionOn || s.eventExecutor == nil {
		return nil, ErrDisabled
	}
	event, start := InvocationEvent{Site: req.Site, Version: req.Version, Route: req.Entrypoint, RuntimeKind: RuntimeStarlark}, time.Now()
	defer func() {
		event.Duration = time.Since(start)
		if err != nil {
			event.Error = err.Error()
			event.ErrorKind = invocationErrorKind(err)
		}
		s.metrics.RecordInvocation(ctx, event)
	}()
	limits := req.Limits.withFallback(s.defaults)
	if err := s.applyServerRuntimeSettings(ctx, &limits); err != nil {
		return nil, err
	}
	if int64(len(req.Payload)) > limits.MaxRequestBytes {
		return nil, ErrRequestTooLarge
	}
	if !s.acquire(ctx) {
		return nil, ErrConcurrencyLimit
	}
	defer s.release()
	invokeCtx, cancel := context.WithTimeout(ctx, time.Duration(limits.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	route, err := s.eventRoute(invokeCtx, req)
	if err != nil {
		return nil, err
	}
	bundle, err := s.runtimeBundle(invokeCtx, route, limits)
	if err != nil {
		return nil, err
	}
	effects, err = s.eventExecutor.InvokeEvent(invokeCtx, bundle, EventInvocation{
		Site: req.Site, Version: req.Version, Entrypoint: req.Entrypoint, Handler: req.Handler,
		Event: req.Event, Topic: req.Topic, Payload: req.Payload,
	})
	if err != nil {
		return nil, invocationError(invokeCtx, err)
	}
	return validateWebSocketEffects(effects, limits)
}

func (s *service) PumpWebSockets(ctx context.Context) error {
	if !s.executionOn {
		return ErrDisabled
	}
	return nil
}
func (s *service) runtimeBundle(ctx context.Context, route RouteMetadata, limits ResourceLimits) (Bundle, error) {
	files, _, err := s.repo.ListRuntimeBundleFiles(ctx, route.SiteSHA, route.Version)
	if err != nil {
		return Bundle{}, err
	}
	apiProxies, err := s.repo.ListRuntimeAPIProxies(ctx, route.SiteSHA, route.Version)
	if err != nil {
		return Bundle{}, err
	}
	bundle := route.bundle(limits, files)
	bundle.APIProxies = apiProxies
	if wasmRequestsFastExecution(bundle.WASM) {
		allowed, reason, err := policy.RuntimeWASMFastExecutionAllowed(ctx, s.policies, route.Site)
		if err != nil {
			return Bundle{}, err
		}
		bundle.WASMFastExecutionAllowed = allowed
		bundle.WASMFastExecutionDenyReason = reason
		if !allowed {
			slog.WarnContext(ctx, "wasm fast execution disabled by policy; using interruptible safe mode",
				"site", route.Site,
				"version", route.Version,
				"reason", reason,
			)
		}
	}
	return bundle, nil
}
func (s *service) prepareHTTPInvocation(ctx context.Context, req InvocationRequest) (RouteMetadata, ResourceLimits, error) {
	route, err := s.lookupRoute(ctx, req)
	if err != nil {
		return RouteMetadata{}, ResourceLimits{}, err
	}
	if err := validateHTTPRoute(route, req.Method); err != nil {
		return RouteMetadata{}, ResourceLimits{}, err
	}
	if err := s.checkCapabilities(ctx, route); err != nil {
		return RouteMetadata{}, ResourceLimits{}, err
	}
	limits := route.ResourceLimits.withFallback(s.defaults)
	if err := s.applyServerRuntimeSettings(ctx, &limits); err != nil {
		return RouteMetadata{}, ResourceLimits{}, err
	}
	if int64(len(req.Body)) > limits.MaxRequestBytes {
		return RouteMetadata{}, ResourceLimits{}, ErrRequestTooLarge
	}
	return route, limits, nil
}

func (s *service) prepareWebSocketInvocation(ctx context.Context, req WebSocketInvocationRequest) (RouteMetadata, ResourceLimits, error) {
	route, err := s.lookupWebSocketRoute(ctx, req)
	if err != nil {
		return RouteMetadata{}, ResourceLimits{}, err
	}
	if err := validateWebSocketRoute(route); err != nil {
		return RouteMetadata{}, ResourceLimits{}, err
	}
	if err := s.checkCapabilities(ctx, route); err != nil {
		return RouteMetadata{}, ResourceLimits{}, err
	}
	limits := route.ResourceLimits.withFallback(s.defaults)
	if err := s.applyServerRuntimeSettings(ctx, &limits); err != nil {
		return RouteMetadata{}, ResourceLimits{}, err
	}
	if req.EventType == WebSocketEventMessage && int64(len(req.Message)) > limits.MaxRequestBytes {
		return RouteMetadata{}, ResourceLimits{}, ErrRequestTooLarge
	}
	if int64(len(req.Event.Payload)) > limits.MaxRequestBytes {
		return RouteMetadata{}, ResourceLimits{}, ErrRequestTooLarge
	}
	return route, limits, nil
}

func (s *service) applyServerRuntimeSettings(ctx context.Context, limits *ResourceLimits) error {
	if s.settings == nil {
		return nil
	}
	settings, err := s.settings.GetServerSettings(ctx)
	if err != nil {
		return err
	}
	if settings.MaxRuntimeDurationMillis > 0 {
		limits.MaxDurationMillis = settings.MaxRuntimeDurationMillis
	}
	return nil
}
func validateHTTPRoute(route RouteMetadata, method string) error {
	switch {
	case route.RuntimeKind == RuntimeDisabled:
		return ErrDisabled
	case route.RuntimeKind != RuntimeStarlark:
		return fmt.Errorf("%w: unsupported runtime kind %s", ErrInvalidRuntime, route.RuntimeKind)
	case !methodAllowed(method, route.Methods):
		return ErrMethodNotAllowed
	default:
		return nil
	}
}

func validateWebSocketRoute(route RouteMetadata) error {
	switch {
	case route.RuntimeKind == RuntimeDisabled:
		return ErrDisabled
	case route.RuntimeKind != RuntimeStarlark:
		return fmt.Errorf("%w: unsupported runtime kind %s", ErrInvalidRuntime, route.RuntimeKind)
	case route.RouteKind != RouteWebSocket:
		return fmt.Errorf("%w: expected WebSocket route", ErrInvalidRuntime)
	default:
		return nil
	}
}
func invocationError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	return err
}
func invocationErrorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrDisabled):
		return "disabled"
	case errors.Is(err, ErrRouteNotFound):
		return "route_not_found"
	case errors.Is(err, ErrCapabilityDenied):
		return "capability_denied"
	case errors.Is(err, ErrMethodNotAllowed):
		return "method_not_allowed"
	case errors.Is(err, ErrRequestTooLarge):
		return "request_too_large"
	case errors.Is(err, ErrResponseTooLarge):
		return "response_too_large"
	case errors.Is(err, ErrTimeout):
		return "timeout"
	case errors.Is(err, ErrConcurrencyLimit):
		return "concurrency_limit"
	case errors.Is(err, ErrConnectionLimit):
		return "connection_limit"
	case errors.Is(err, ErrBackpressure):
		return "backpressure"
	case errors.Is(err, ErrInvalidRuntime):
		return "invalid_runtime"
	case errors.Is(err, ErrInvocationFailure):
		return "invocation_failure"
	default:
		return "other"
	}
}
func validateResponse(resp InvocationResponse, limits ResourceLimits) (InvocationResponse, error) {
	if int64(len(resp.Body)) > limits.MaxResponseBytes {
		return InvocationResponse{}, ErrResponseTooLarge
	}
	if resp.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	if resp.StatusCode < 100 || resp.StatusCode > 999 {
		return InvocationResponse{}, fmt.Errorf("%w: invalid status code %d", ErrInvocationFailure, resp.StatusCode)
	}
	return resp, nil
}

func validateWebSocketEffects(effects []WebSocketEffect, limits ResourceLimits) ([]WebSocketEffect, error) {
	for _, effect := range effects {
		if int64(len(effect.Payload)) > limits.MaxResponseBytes {
			return nil, ErrResponseTooLarge
		}
	}
	return effects, nil
}
func eventForRoute(route RouteMetadata) InvocationEvent {
	return InvocationEvent{Site: route.Site, Version: route.Version, Route: route.RoutePath, RuntimeKind: route.RuntimeKind}
}
func (r RouteMetadata) bundle(limits ResourceLimits, files []domain.UploadFileRecord) Bundle {
	return Bundle{
		Site:    r.Site,
		Version: r.Version,
		Routes: []Route{{
			Path:              r.RoutePath,
			Kind:              r.RouteKind,
			Entrypoint:        r.Entrypoint,
			ScriptKey:         r.BundleObjectKey,
			Methods:           append([]string(nil), r.Methods...),
			ExposeErrors:      r.ExposeErrors,
			FilesystemEnabled: r.FilesystemEnabled,
			FilesystemRoot:    r.FilesystemRoot,
		}},
		Files:  bundleFiles(files),
		WASM:   cloneWASMModules(r.WASM),
		Limits: limits,
	}
}

func cloneWASMModules(in map[string]manifest.WASMModule) map[string]manifest.WASMModule {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]manifest.WASMModule, len(in))
	for name, module := range in {
		module.Imports = append([]string(nil), module.Imports...)
		if module.Execution.Interruptible != nil {
			interruptible := *module.Execution.Interruptible
			module.Execution.Interruptible = &interruptible
		}
		out[name] = module
	}
	return out
}

func wasmRequestsFastExecution(modules map[string]manifest.WASMModule) bool {
	for _, module := range modules {
		if module.Execution.Interruptible != nil && !*module.Execution.Interruptible {
			return true
		}
	}
	return false
}

func (s *service) eventRoute(ctx context.Context, req EventInvocationRequest) (RouteMetadata, error) {
	if strings.TrimSpace(req.Entrypoint) == "" || strings.TrimSpace(req.Handler) == "" {
		return RouteMetadata{}, fmt.Errorf("%w: event entrypoint and handler are required", ErrInvalidRuntime)
	}
	routes, err := s.repo.ListCurrentRuntimeRoutes(ctx)
	if err != nil {
		return RouteMetadata{}, err
	}
	var siteSHA string
	for _, route := range routes {
		if route.Site == req.Site && route.Version == req.Version {
			siteSHA = route.SiteSHA
			break
		}
	}
	if siteSHA == "" {
		return RouteMetadata{}, ErrRouteNotFound
	}
	files, _, err := s.repo.ListRuntimeBundleFiles(ctx, siteSHA, req.Version)
	if err != nil {
		return RouteMetadata{}, err
	}
	entrypoint := strings.Trim(req.Entrypoint, "/")
	for _, file := range files {
		if file.RelativePath == entrypoint {
			return RouteMetadata{
				Site: req.Site, SiteSHA: siteSHA, Version: req.Version,
				RuntimeKind: RuntimeStarlark, RouteKind: RouteWebSocket,
				RoutePath: "event:" + entrypoint, Entrypoint: entrypoint,
				BundleObjectKey: file.BlobPath,
				ResourceLimits: ResourceLimits{
					MaxRequestBytes:   DefaultMaxRequestBytes,
					MaxResponseBytes:  DefaultMaxResponseBytes,
					MaxDurationMillis: DefaultMaxDuration.Milliseconds(),
					MaxMemoryBytes:    DefaultMaxMemoryBytes,
					MaxConcurrency:    DefaultMaxConcurrentInvocations,
					MaxExecutionSteps: DefaultMaxExecutionSteps,
					MaxScriptBytes:    DefaultMaxScriptBytes,
				},
			}, nil
		}
	}
	return RouteMetadata{}, ErrRouteNotFound
}
func bundleFiles(files []domain.UploadFileRecord) []BundleFile {
	out := make([]BundleFile, 0, len(files))
	for _, file := range files {
		out = append(out, BundleFile{Path: file.RelativePath, BlobPath: file.BlobPath, FileSHA: file.FileSHA, Bytes: file.Bytes})
	}
	return out
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
func (s *service) release() { <-s.sem }

type DisabledService struct{}

func NewDisabledService() DisabledService { return DisabledService{} }
func (DisabledService) InvokeHTTP(context.Context, InvocationRequest) (InvocationResponse, error) {
	return InvocationResponse{}, ErrDisabled
}
func (DisabledService) InvokeWebSocket(context.Context, WebSocketInvocationRequest) ([]WebSocketEffect, error) {
	return nil, ErrDisabled
}
func (DisabledService) InvokeEvent(context.Context, EventInvocationRequest) ([]WebSocketEffect, error) {
	return nil, ErrDisabled
}
func (DisabledService) PumpWebSockets(context.Context) error { return ErrDisabled }
