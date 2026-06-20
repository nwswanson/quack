package runtime

import (
	"context"
	"errors"
)

var ErrDisabled = errors.New("runtime execution is disabled")

// Bundle is the runtime-facing view of a published release.
//
// Phase 12 TODO: extend this with the exact bundle location and integrity data
// the chosen executor needs. For a process sandbox that may be an extracted
// directory plus a digest; for WASM it may be a module blob key; for an external
// worker it may be an immutable object key the worker can fetch.
type Bundle struct {
	Site    string
	Version int64
	Routes  []Route
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
	// RuntimeDisabled is intentionally the only runtime kind with behavior today.
	//
	// Phase 12 TODO: add explicit runtime kinds only after choosing the execution
	// strategy. Avoid generic names like "script" unless the executor contract can
	// actually support more than one language or isolation mode.
	RuntimeDisabled RuntimeKind = "disabled"
)

// ResourceLimits records limits declared for a runtime route.
//
// Phase 12 TODO: define where each limit is enforced. Request/response byte
// caps likely belong in runtimehttp, duration belongs around Executor.Invoke,
// memory and concurrency depend on the executor strategy. Do not treat stored
// limits as security controls until the executor actively enforces them.
type ResourceLimits struct {
	MaxRequestBytes   int64 `json:"max_request_bytes,omitempty"`
	MaxResponseBytes  int64 `json:"max_response_bytes,omitempty"`
	MaxDurationMillis int64 `json:"max_duration_millis,omitempty"`
	MaxMemoryBytes    int64 `json:"max_memory_bytes,omitempty"`
	MaxConcurrency    int64 `json:"max_concurrency,omitempty"`
}

// RouteMetadata is persisted release metadata, not an executable runtime.
//
// Phase 12 TODO: when execution is added, load this metadata into a Bundle and
// verify the route's RequiredCapabilities against policy immediately before
// invocation. Upload-time validation is not enough because policy can change
// after a release is published.
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
// Phase 12 TODO: decide which HTTP details are intentionally exposed. Query
// string, remote address, host, cookies, and trailers are omitted for now; add
// them deliberately and test that sensitive control-plane headers cannot leak
// into user code.
type InvocationRequest struct {
	Site    string
	Version int64
	Route   string
	Method  string
	Headers map[string][]string
	Body    []byte
}

// InvocationResponse is the transport-neutral response from runtime code.
//
// Phase 12 TODO: validate status codes and headers before runtimehttp writes
// them. In particular, decide whether user code may set hop-by-hop headers,
// Set-Cookie, Cache-Control, and response content length.
type InvocationResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

// Executor is the narrow boundary where untrusted code execution will live.
//
// Phase 12 TODO: keep sandbox setup, filesystem isolation, network policy,
// timeout handling, and resource accounting behind this interface. The runtime
// package should orchestrate those concerns but must not import HTTP adapters,
// SQLite, or concrete storage implementations.
type Executor interface {
	Invoke(ctx context.Context, bundle Bundle, req InvocationRequest) (InvocationResponse, error)
}

// Service owns runtime invocation policy and orchestration.
//
// Phase 12 TODO: replace DisabledService in composition with a service that
// looks up route metadata, evaluates capabilities, enforces limits, calls the
// executor, and emits logs/metrics. Keep the disabled implementation available
// for tests and for deployments that do not opt into runtime execution.
type Service interface {
	InvokeHTTP(ctx context.Context, req InvocationRequest) (InvocationResponse, error)
}

type DisabledService struct{}

func NewDisabledService() DisabledService {
	return DisabledService{}
}

func (DisabledService) InvokeHTTP(ctx context.Context, req InvocationRequest) (InvocationResponse, error) {
	return InvocationResponse{}, ErrDisabled
}
