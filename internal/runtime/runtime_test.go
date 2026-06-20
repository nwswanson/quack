package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"quack/internal/domain"
	appsettings "quack/internal/settings"
)

func TestDisabledServiceDoesNotInvokeRuntime(t *testing.T) {
	service := NewDisabledService()

	_, err := service.InvokeHTTP(context.Background(), InvocationRequest{
		Site: "foo", Version: 1, Route: "/api", Method: "GET",
	})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("InvokeHTTP error = %v, want ErrDisabled", err)
	}
}

func TestStarlarkExecutorHandlesRequestTupleAndResponseTuple(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{"app.star": `
def handle(req):
    method, path, query, headers, body = req
    return (
        201,
        {"content-type": "text/plain", "x-seen": [method, path, query, headers["x-test"][0]]},
        "body=%s" % body,
    )
`})

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "foo", Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
	}, InvocationRequest{
		Method: http.MethodPost,
		Route:  "/api/echo",
		Query:  "a=1",
		Headers: map[string][]string{
			"X-Test": {"visible"},
		},
		Body: []byte("hello"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated || string(resp.Body) != `body=b"hello"` {
		t.Fatalf("response = %+v body=%q, want created byte body", resp, string(resp.Body))
	}
	if got := strings.Join(resp.Headers["X-Seen"], "|"); got != "POST|/echo|a=1|visible" {
		t.Fatalf("x-seen = %q, want tuple values from request", got)
	}
}

func TestStarlarkExecutorDoesNotExposeLoadFilesystemAccess(t *testing.T) {
	executor := newTestStarlarkExecutor(t, map[string]string{"app.star": `
def handle(req):
    load("secret.star", "secret")
    return (200, {}, secret)
`})

	_, err := executor.Invoke(context.Background(), Bundle{
		Site: "foo", Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "app.star"}},
	}, InvocationRequest{Method: http.MethodGet, Route: "/api"})
	if !errors.Is(err, ErrInvocationFailure) {
		t.Fatalf("Invoke error = %v, want invocation failure from unavailable load", err)
	}
}

func TestDemoStarlarkBundleExecutes(t *testing.T) {
	script, err := os.ReadFile("../../demos/starlark-basic/api/app.star")
	if err != nil {
		t.Fatal(err)
	}
	executor := newTestStarlarkExecutor(t, map[string]string{"demo.star": string(script)})

	resp, err := executor.Invoke(context.Background(), Bundle{
		Site: "demo", Version: 1, Routes: []Route{{Path: "/api", Kind: RouteHTTP, Entrypoint: "demo.star"}},
	}, InvocationRequest{
		Method:  http.MethodPost,
		Route:   "/api/report",
		Query:   "sample=true",
		Headers: map[string][]string{"User-Agent": {"quack-test"}},
		Body:    []byte("hello"),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.Headers["Content-Type"][0] != "application/json; charset=utf-8" {
		t.Fatalf("response = %+v body=%s, want JSON ok response", resp, body)
	}
	for _, want := range []string{`"runtime": "starlark"`, `"path": "/report"`, `"body_size": 5`, `"user_agent": "quack-test"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("demo body = %s, want %s", body, want)
		}
	}
}

func TestServiceInvokesStarlarkBehindPolicyGate(t *testing.T) {
	svc := NewService(ServiceOptions{
		Repository: newRuntimeRepo(RouteMetadata{
			Site: "foo", Version: 3, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark,
			BundleObjectKey: "app.star", Methods: []string{http.MethodPost}, RequiredCapabilities: []string{"runtime.http"},
		}),
		Policies:        runtimePolicyLoader{policies: []domain.PolicyRecord{{ScopeType: domain.ScopeSystem, Key: appsettings.SettingRuntimeHTTPFeature, Mode: "allow"}}},
		Executor:        newTestStarlarkExecutor(t, map[string]string{"app.star": `def handle(req): return (200, {"content-type": "text/plain"}, "ok")`}),
		EnableExecution: true,
	})

	resp, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 3, Route: "/api", Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(resp.Body) != "ok" {
		t.Fatalf("response = %+v body=%q, want ok", resp, string(resp.Body))
	}
}

func TestServiceDeniesRuntimeWithoutPolicy(t *testing.T) {
	svc := NewService(ServiceOptions{
		Repository: newRuntimeRepo(RouteMetadata{
			Site: "foo", Version: 3, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark,
			BundleObjectKey: "app.star", RequiredCapabilities: []string{"runtime.http"},
		}),
		Policies:        runtimePolicyLoader{},
		Executor:        newTestStarlarkExecutor(t, map[string]string{"app.star": `def handle(req): return (200, {}, "ok")`}),
		EnableExecution: true,
	})

	_, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 3, Route: "/api", Method: http.MethodGet})
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("InvokeHTTP error = %v, want capability denial", err)
	}
}

func TestServiceRejectsOversizedRequestBeforeExecutor(t *testing.T) {
	executor := &recordingExecutor{}
	svc := NewService(ServiceOptions{
		Repository: newRuntimeRepo(RouteMetadata{
			Site: "foo", Version: 1, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark,
			BundleObjectKey: "app.star", ResourceLimits: ResourceLimits{MaxRequestBytes: 3},
		}),
		Policies:        allowRuntimeHTTPPolicy(),
		Executor:        executor,
		EnableExecution: true,
	})

	_, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet, Body: []byte("toolarge")})
	if !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("InvokeHTTP error = %v, want request too large", err)
	}
	if executor.called {
		t.Fatal("executor was called for oversized request")
	}
}

func TestServiceRejectsOversizedResponse(t *testing.T) {
	svc := NewService(ServiceOptions{
		Repository: newRuntimeRepo(RouteMetadata{
			Site: "foo", Version: 1, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark,
			BundleObjectKey: "app.star", ResourceLimits: ResourceLimits{MaxResponseBytes: 3},
		}),
		Policies:        allowRuntimeHTTPPolicy(),
		Executor:        &recordingExecutor{resp: InvocationResponse{StatusCode: http.StatusOK, Body: []byte("toolarge")}},
		EnableExecution: true,
	})

	_, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("InvokeHTTP error = %v, want response too large", err)
	}
}

func TestServiceTimesOutRunawayStarlark(t *testing.T) {
	svc := NewService(ServiceOptions{
		Repository: newRuntimeRepo(RouteMetadata{
			Site: "foo", Version: 1, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark,
			BundleObjectKey: "app.star", ResourceLimits: ResourceLimits{MaxDurationMillis: 25, MaxExecutionSteps: 1000},
		}),
		Policies: allowRuntimeHTTPPolicy(),
		Executor: newTestStarlarkExecutor(t, map[string]string{"app.star": `
def handle(req):
    while True:
        pass
    return (200, {}, "never")
`}),
		EnableExecution: true,
	})

	_, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})
	if !errors.Is(err, ErrInvocationFailure) && !errors.Is(err, ErrTimeout) {
		t.Fatalf("InvokeHTTP error = %v, want timeout or step-limit invocation failure", err)
	}
}

func TestServiceLimitsConcurrentInvocations(t *testing.T) {
	executor := &blockingExecutor{started: make(chan struct{}), release: make(chan struct{})}
	svc := NewService(ServiceOptions{
		Repository:      newRuntimeRepo(RouteMetadata{Site: "foo", Version: 1, RoutePath: "/api", RouteKind: RouteHTTP, RuntimeKind: RuntimeStarlark, BundleObjectKey: "app.star"}),
		Policies:        allowRuntimeHTTPPolicy(),
		Executor:        executor,
		MaxConcurrency:  1,
		EnableExecution: true,
	})

	started := make(chan struct{})
	done := make(chan error)
	go func() {
		close(started)
		_, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})
		done <- err
	}()
	<-started
	executor.waitStarted(t)

	_, err := svc.InvokeHTTP(context.Background(), InvocationRequest{Site: "foo", Version: 1, Route: "/api", Method: http.MethodGet})
	if !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("second InvokeHTTP error = %v, want concurrency limit", err)
	}
	close(executor.release)
	if err := <-done; err != nil {
		t.Fatalf("first InvokeHTTP error = %v", err)
	}
}

func newTestStarlarkExecutor(t *testing.T, scripts map[string]string) *StarlarkExecutor {
	t.Helper()
	executor, err := NewStarlarkExecutor(scriptMap(scripts), ResourceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

type scriptMap map[string]string

func (m scriptMap) OpenScript(ctx context.Context, objectKey string) (io.ReadCloser, error) {
	script, ok := m[objectKey]
	if !ok {
		return nil, errors.New("missing script")
	}
	return io.NopCloser(strings.NewReader(script)), nil
}

type runtimeRepo struct {
	routes []RouteMetadata
}

func newRuntimeRepo(routes ...RouteMetadata) runtimeRepo {
	return runtimeRepo{routes: routes}
}

func (r runtimeRepo) ListRuntimeRoutes(ctx context.Context, siteSHA string, version int64) ([]RouteMetadata, error) {
	return append([]RouteMetadata(nil), r.routes...), nil
}

func (r runtimeRepo) ListCurrentRuntimeRoutes(ctx context.Context) ([]RouteMetadata, error) {
	return append([]RouteMetadata(nil), r.routes...), nil
}

type runtimePolicyLoader struct {
	policies []domain.PolicyRecord
}

func allowRuntimeHTTPPolicy() runtimePolicyLoader {
	return runtimePolicyLoader{policies: []domain.PolicyRecord{{ScopeType: domain.ScopeSystem, Key: appsettings.SettingRuntimeHTTPFeature, Mode: "allow"}}}
}

func (l runtimePolicyLoader) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	var out []domain.PolicyRecord
	for _, p := range l.policies {
		for _, scope := range scopes {
			if p.ScopeType == scope.Type && p.ScopeID == scope.ID {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

type recordingExecutor struct {
	called bool
	resp   InvocationResponse
	err    error
}

func (e *recordingExecutor) Invoke(ctx context.Context, bundle Bundle, req InvocationRequest) (InvocationResponse, error) {
	e.called = true
	return e.resp, e.err
}

type blockingExecutor struct {
	startOnce sync.Once
	started   chan struct{}
	release   chan struct{}
}

func (e *blockingExecutor) Invoke(ctx context.Context, bundle Bundle, req InvocationRequest) (InvocationResponse, error) {
	e.startOnce.Do(func() {
		close(e.started)
	})
	select {
	case <-e.release:
		return InvocationResponse{StatusCode: http.StatusOK}, nil
	case <-ctx.Done():
		return InvocationResponse{}, ctx.Err()
	}
}

func (e *blockingExecutor) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-e.started:
	case <-time.After(time.Second):
		t.Fatal("blocking executor did not start")
	}
}
