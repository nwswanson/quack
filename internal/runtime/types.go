package runtime

import (
	"context"
	"errors"
	"io"
	"quack/internal/domain"
	"quack/internal/policy"
	"time"
)

var (
	ErrDisabled          = errors.New("runtime execution is disabled")
	ErrRouteNotFound     = errors.New("runtime route was not found")
	ErrCapabilityDenied  = errors.New("runtime capability denied")
	ErrMethodNotAllowed  = errors.New("runtime method is not allowed")
	ErrRequestTooLarge   = errors.New("runtime request body is too large")
	ErrResponseTooLarge  = errors.New("runtime response body is too large")
	ErrTimeout           = errors.New("runtime execution timed out")
	ErrConcurrencyLimit  = errors.New("runtime concurrency limit reached")
	ErrConnectionLimit   = errors.New("runtime websocket connection limit reached")
	ErrBackpressure      = errors.New("runtime websocket back pressure limit reached")
	ErrInvalidRuntime    = errors.New("invalid runtime configuration")
	ErrInvocationFailure = errors.New("runtime invocation failed")
)

type RuntimeKind string

const (
	RuntimeDisabled RuntimeKind = "disabled"
	RuntimeStarlark RuntimeKind = "starlark"
)

type RouteKind string

const (
	RouteHTTP      RouteKind = "http"
	RouteWebSocket RouteKind = "websocket"
)

type Bundle struct {
	Site    string
	Version int64
	Routes  []Route
	Files   []BundleFile
	Limits  ResourceLimits
}
type BundleFile struct {
	Path     string
	BlobPath string
	FileSHA  string
	Bytes    int64
}
type Route struct {
	Path              string
	Kind              RouteKind
	Entrypoint        string
	Methods           []string
	FilesystemEnabled bool
	FilesystemRoot    string
}
type ResourceLimits struct {
	MaxRequestBytes                int64  `json:"max_request_bytes,omitempty"`
	MaxResponseBytes               int64  `json:"max_response_bytes,omitempty"`
	MaxDurationMillis              int64  `json:"max_duration_millis,omitempty"`
	MaxMemoryBytes                 int64  `json:"max_memory_bytes,omitempty"`
	MaxConcurrency                 int64  `json:"max_concurrency,omitempty"`
	MaxExecutionSteps              uint64 `json:"max_execution_steps,omitempty"`
	MaxScriptBytes                 int64  `json:"max_script_bytes,omitempty"`
	MaxWebSocketConnections        int64  `json:"max_websocket_connections,omitempty"`
	MaxWebSocketConnectionsPerSite int64  `json:"max_websocket_connections_per_site,omitempty"`
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
	FilesystemEnabled    bool
	FilesystemRoot       string
	RequiredCapabilities []string
	ResourceLimits       ResourceLimits
	CreatedAt            string
}
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
type InvocationResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}
type Executor interface {
	Invoke(ctx context.Context, bundle Bundle, req InvocationRequest) (InvocationResponse, error)
}
type WebSocketExecutor interface {
	InvokeWebSocket(ctx context.Context, bundle Bundle, event WebSocketEvent) ([]WebSocketEffect, error)
}
type Repository interface {
	ListRuntimeRoutes(ctx context.Context, siteSHA string, version int64) ([]RouteMetadata, error)
	ListCurrentRuntimeRoutes(ctx context.Context) ([]RouteMetadata, error)
	ListRuntimeBundleFiles(ctx context.Context, siteSHA string, version int64) ([]domain.UploadFileRecord, bool, error)
}
type SettingsReader interface {
	GetServerSettings(ctx context.Context) (domain.ServerSettings, error)
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
	ErrorKind   string
}
type NoopMetrics struct{}

func (NoopMetrics) RecordInvocation(context.Context, InvocationEvent) {}

type ServiceOptions struct {
	Repository        Repository
	Policies          policy.Loader
	Executor          Executor
	WebSocketExecutor WebSocketExecutor
	MaxConcurrency    int64
	DefaultLimits     ResourceLimits
	Settings          SettingsReader
	Metrics           Metrics
	EnableExecution   bool
}
type Service interface {
	InvokeHTTP(ctx context.Context, req InvocationRequest) (InvocationResponse, error)
	InvokeWebSocket(ctx context.Context, req WebSocketInvocationRequest) ([]WebSocketEffect, error)
	PumpWebSockets(ctx context.Context) error
}

type WebSocketEventType string

const (
	WebSocketEventConnect    WebSocketEventType = "connect"
	WebSocketEventMessage    WebSocketEventType = "message"
	WebSocketEventDisconnect WebSocketEventType = "disconnect"
	WebSocketEventEvent      WebSocketEventType = "event"
	WebSocketEventTimer      WebSocketEventType = "timer"
)

type WebSocketInvocationRequest struct {
	Site      string
	Version   int64
	Route     string
	Query     string
	Headers   map[string][]string
	ConnID    string
	EventType WebSocketEventType
	Message   []byte
	Event     WebSocketServerEvent
	Limits    ResourceLimits
}

type WebSocketServerEvent struct {
	Topic   string
	Payload []byte
}

type WebSocketEvent struct {
	Site      string
	Version   int64
	Route     string
	Query     string
	Headers   map[string][]string
	ConnID    string
	EventType WebSocketEventType
	Message   []byte
	Event     WebSocketServerEvent
}

type WebSocketEffectType string

const (
	WebSocketEffectAccept         WebSocketEffectType = "ws.accept"
	WebSocketEffectClose          WebSocketEffectType = "ws.close"
	WebSocketEffectSend           WebSocketEffectType = "ws.send"
	WebSocketEffectBroadcast      WebSocketEffectType = "ws.broadcast"
	WebSocketEffectSubscribe      WebSocketEffectType = "ws.subscribe"
	WebSocketEffectUnsubscribe    WebSocketEffectType = "ws.unsubscribe"
	WebSocketEffectUnsubscribeAll WebSocketEffectType = "ws.unsubscribe_all"
	WebSocketEffectPublish        WebSocketEffectType = "events.publish"
	WebSocketEffectSetTimer       WebSocketEffectType = "timers.set"
)

type WebSocketEffect struct {
	Type    WebSocketEffectType
	ConnID  string
	Topic   string
	Payload []byte
	Code    int
	Reason  string
	Key     string
	After   string
}
