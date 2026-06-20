package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"quack/internal/policy"
	"time"
)

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
	metrics := opts.Metrics
	if metrics == nil {
		metrics = NoopMetrics{}
	}
	return &service{
		repo:        opts.Repository,
		policies:    opts.Policies,
		executor:    opts.Executor,
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
	resp, err = s.executor.Invoke(invokeCtx, route.bundle(limits), req)
	if err != nil {
		return InvocationResponse{}, invocationError(invokeCtx, err)
	}
	return validateResponse(resp, limits)
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
func eventForRoute(route RouteMetadata) InvocationEvent {
	return InvocationEvent{Site: route.Site, Version: route.Version, Route: route.RoutePath, RuntimeKind: route.RuntimeKind}
}
func (r RouteMetadata) bundle(limits ResourceLimits) Bundle {
	return Bundle{
		Site:    r.Site,
		Version: r.Version,
		Routes:  []Route{{Path: r.RoutePath, Kind: r.RouteKind, Entrypoint: r.BundleObjectKey, Methods: append([]string(nil), r.Methods...)}},
		Limits:  limits,
	}
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
