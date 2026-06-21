package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"quack/internal/domain"
	"quack/internal/policy"
)

type service struct {
	repo        Repository
	policies    policy.Loader
	executor    Executor
	wsExecutor  WebSocketExecutor
	metrics     Metrics
	sem         chan struct{}
	defaults    ResourceLimits
	executionOn bool
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
	return &service{
		repo:        opts.Repository,
		policies:    opts.Policies,
		executor:    opts.Executor,
		wsExecutor:  wsExecutor,
		metrics:     metrics,
		sem:         make(chan struct{}, positiveOr(opts.MaxConcurrency, DefaultMaxConcurrentInvocations)),
		defaults:    opts.DefaultLimits.withDefaults(),
		executionOn: true,
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
	return route.bundle(limits, files), nil
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
	if req.EventType == WebSocketEventMessage && int64(len(req.Message)) > limits.MaxRequestBytes {
		return RouteMetadata{}, ResourceLimits{}, ErrRequestTooLarge
	}
	if int64(len(req.Event.Payload)) > limits.MaxRequestBytes {
		return RouteMetadata{}, ResourceLimits{}, ErrRequestTooLarge
	}
	return route, limits, nil
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
			Entrypoint:        r.BundleObjectKey,
			Methods:           append([]string(nil), r.Methods...),
			FilesystemEnabled: r.FilesystemEnabled,
			FilesystemRoot:    r.FilesystemRoot,
		}},
		Files:  bundleFiles(files),
		Limits: limits,
	}
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
func (DisabledService) PumpWebSockets(context.Context) error { return ErrDisabled }
