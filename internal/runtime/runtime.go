package runtime

import (
	"context"
	"errors"
)

var ErrDisabled = errors.New("runtime execution is disabled")

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
	RuntimeDisabled RuntimeKind = "disabled"
)

type ResourceLimits struct {
	MaxRequestBytes   int64 `json:"max_request_bytes,omitempty"`
	MaxResponseBytes  int64 `json:"max_response_bytes,omitempty"`
	MaxDurationMillis int64 `json:"max_duration_millis,omitempty"`
	MaxMemoryBytes    int64 `json:"max_memory_bytes,omitempty"`
	MaxConcurrency    int64 `json:"max_concurrency,omitempty"`
}

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

type InvocationRequest struct {
	Site    string
	Version int64
	Route   string
	Method  string
	Headers map[string][]string
	Body    []byte
}

type InvocationResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

type Executor interface {
	Invoke(ctx context.Context, bundle Bundle, req InvocationRequest) (InvocationResponse, error)
}

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
