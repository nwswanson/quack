package runtime

import "time"

const (
	DefaultMaxRequestBytes                int64  = 1 << 20
	DefaultMaxResponseBytes               int64  = 1 << 20
	DefaultMaxDuration                           = 2500 * time.Millisecond
	DefaultMaxMemoryBytes                 int64  = 32 << 20
	DefaultMaxConcurrentInvocations       int64  = 8
	DefaultMaxExecutionSteps              uint64 = 0
	DefaultMaxScriptBytes                 int64  = 256 << 10
	DefaultMaxWebSocketConnections        int64  = 1024
	DefaultMaxWebSocketConnectionsPerSite int64  = 128
)

var defaultResourceLimits = ResourceLimits{
	MaxRequestBytes:                DefaultMaxRequestBytes,
	MaxResponseBytes:               DefaultMaxResponseBytes,
	MaxDurationMillis:              DefaultMaxDuration.Milliseconds(),
	MaxMemoryBytes:                 DefaultMaxMemoryBytes,
	MaxConcurrency:                 DefaultMaxConcurrentInvocations,
	MaxExecutionSteps:              DefaultMaxExecutionSteps,
	MaxScriptBytes:                 DefaultMaxScriptBytes,
	MaxWebSocketConnections:        DefaultMaxWebSocketConnections,
	MaxWebSocketConnectionsPerSite: DefaultMaxWebSocketConnectionsPerSite,
}

func (l ResourceLimits) withDefaults() ResourceLimits {
	return l.withFallback(defaultResourceLimits)
}
func (l ResourceLimits) withFallback(defaults ResourceLimits) ResourceLimits {
	l.MaxRequestBytes = positiveOr(l.MaxRequestBytes, defaults.MaxRequestBytes)
	l.MaxResponseBytes = positiveOr(l.MaxResponseBytes, defaults.MaxResponseBytes)
	l.MaxDurationMillis = positiveOr(l.MaxDurationMillis, defaults.MaxDurationMillis)
	l.MaxMemoryBytes = positiveOr(l.MaxMemoryBytes, defaults.MaxMemoryBytes)
	l.MaxConcurrency = positiveOr(l.MaxConcurrency, defaults.MaxConcurrency)
	l.MaxScriptBytes = positiveOr(l.MaxScriptBytes, defaults.MaxScriptBytes)
	l.MaxWebSocketConnections = positiveOr(l.MaxWebSocketConnections, defaults.MaxWebSocketConnections)
	l.MaxWebSocketConnectionsPerSite = positiveOr(l.MaxWebSocketConnectionsPerSite, defaults.MaxWebSocketConnectionsPerSite)
	if l.MaxExecutionSteps == 0 {
		l.MaxExecutionSteps = defaults.MaxExecutionSteps
	}
	return l
}
func positiveOr(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}
