package settings

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"quack/internal/domain"
)

const (
	DefaultMaxUploadBytes                 int64 = 512 << 20
	DefaultMaxUploadFiles                 int64 = 10000
	DefaultRuntimeMaxDurationMillis       int64 = 500
	DefaultMaxWebSocketConnections        int64 = 1024
	DefaultMaxWebSocketConnectionsPerSite int64 = 128
	DefaultHTTPCacheMaxAgeSeconds         int64 = 3600
)

type SettingType string

const (
	SettingTypeBool   SettingType = "bool"
	SettingTypeInt64  SettingType = "int64"
	SettingTypeString SettingType = "string"
	SettingTypeEnum   SettingType = "enum"
)

type PolicyKind string

const (
	PolicyKindCapability PolicyKind = "capability"
	PolicyKindNumericCap PolicyKind = "numeric_cap"
	PolicyKindEnum       PolicyKind = "enum"
)

type SettingDefinition struct {
	Key           string
	Type          SettingType
	DefaultValue  string
	AllowedScopes []domain.ScopeType
	UserEditable  bool
	SiteEditable  bool
	AdminEditable bool
	PolicyKind    PolicyKind
}

const (
	SettingMaxUploadBytes                        = "max_upload_bytes"
	SettingMaxUploadFiles                        = "max_upload_files"
	SettingMaxRetainedVersions                   = "max_retained_versions"
	SettingDefaultSite                           = "default_site"
	SettingAllowedHosts                          = "allowed_hosts"
	SettingLogLevel                              = "log_level"
	SettingHTTPCacheMode                         = "http.cache.mode"
	SettingHTTPCacheMaxAgeSeconds                = "http.cache.max_age_seconds"
	SettingDatabaseFeature                       = "features.database.enabled"
	SettingDatabaseFeatureRequired               = "features.database.required"
	SettingRuntimeHTTPFeature                    = "features.runtime.http.enabled"
	SettingRuntimeWebSocketFeature               = "features.runtime.websocket.enabled"
	SettingRuntimeMaxDurationMillis              = "runtime.max_duration_ms"
	SettingRuntimeMemoryMaxBytes                 = "runtime.memory.max_bytes"
	SettingRuntimeMemoryWipe                     = "runtime.memory.wipe"
	SettingRuntimeMemoryPersistenceMode          = "runtime.memory.persistence_mode"
	SettingRuntimeMemorySnapshotSave             = "runtime.memory.snapshot_save"
	SettingRuntimeMemorySnapshotMinIntervalMS    = "runtime.memory.snapshot_min_interval_ms"
	SettingRuntimeMemorySnapshotMaxConcurrency   = "runtime.memory.snapshot_max_concurrency"
	SettingRuntimeMemoryShutdownFlushTimeoutMS   = "runtime.memory.shutdown_flush_timeout_ms"
	SettingRuntimeWebSocketMaxConnections        = "runtime.websocket.max_connections"
	SettingRuntimeWebSocketMaxConnectionsPerSite = "runtime.websocket.max_connections_per_site"
	// Deprecated: static.root is kept only for current releases uploaded before
	// static route roots existed. New manifests must use routes[].root.
	SettingStaticRoot = "static.root"
	SettingRoutes     = "routes"
)

var registry = map[string]SettingDefinition{
	SettingMaxUploadBytes: {
		Key: SettingMaxUploadBytes, Type: SettingTypeInt64, DefaultValue: "536870912",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingMaxUploadFiles: {
		Key: SettingMaxUploadFiles, Type: SettingTypeInt64, DefaultValue: "10000",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingMaxRetainedVersions: {
		Key: SettingMaxRetainedVersions, Type: SettingTypeInt64, DefaultValue: "0",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingDefaultSite: {
		Key: SettingDefaultSite, Type: SettingTypeString, DefaultValue: "",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true,
	},
	SettingAllowedHosts: {
		Key: SettingAllowedHosts, Type: SettingTypeString, DefaultValue: "",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true,
	},
	SettingLogLevel: {
		Key: SettingLogLevel, Type: SettingTypeEnum, DefaultValue: "warn",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindEnum,
	},
	SettingHTTPCacheMode: {
		Key: SettingHTTPCacheMode, Type: SettingTypeEnum, DefaultValue: "revalidate",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindEnum,
	},
	SettingHTTPCacheMaxAgeSeconds: {
		Key: SettingHTTPCacheMaxAgeSeconds, Type: SettingTypeInt64, DefaultValue: "3600",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingDatabaseFeature: {
		Key: SettingDatabaseFeature, Type: SettingTypeBool, DefaultValue: "false",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem, domain.ScopeUser, domain.ScopeSite, domain.ScopeUpload}, SiteEditable: true, AdminEditable: true, PolicyKind: PolicyKindCapability,
	},
	SettingDatabaseFeatureRequired: {
		Key: SettingDatabaseFeatureRequired, Type: SettingTypeBool, DefaultValue: "false",
		AllowedScopes: []domain.ScopeType{domain.ScopeUpload}, SiteEditable: true, PolicyKind: PolicyKindCapability,
	},
	SettingRuntimeHTTPFeature: {
		Key: SettingRuntimeHTTPFeature, Type: SettingTypeBool, DefaultValue: "false",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem, domain.ScopeUser, domain.ScopeSite, domain.ScopeUpload}, SiteEditable: true, AdminEditable: true, PolicyKind: PolicyKindCapability,
	},
	SettingRuntimeWebSocketFeature: {
		Key: SettingRuntimeWebSocketFeature, Type: SettingTypeBool, DefaultValue: "false",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem, domain.ScopeUser, domain.ScopeSite, domain.ScopeUpload}, SiteEditable: true, AdminEditable: true, PolicyKind: PolicyKindCapability,
	},
	SettingRuntimeMaxDurationMillis: {
		Key: SettingRuntimeMaxDurationMillis, Type: SettingTypeInt64, DefaultValue: "500",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingRuntimeMemoryMaxBytes: {
		Key: SettingRuntimeMemoryMaxBytes, Type: SettingTypeInt64, DefaultValue: "33554432",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem, domain.ScopeUser, domain.ScopeSite}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingRuntimeMemoryWipe: {
		Key: SettingRuntimeMemoryWipe, Type: SettingTypeBool, DefaultValue: "false",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem, domain.ScopeUser, domain.ScopeSite}, AdminEditable: true,
	},
	SettingRuntimeMemoryPersistenceMode: {
		Key: SettingRuntimeMemoryPersistenceMode, Type: SettingTypeEnum, DefaultValue: "off",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindEnum,
	},
	SettingRuntimeMemorySnapshotSave: {
		Key: SettingRuntimeMemorySnapshotSave, Type: SettingTypeString, DefaultValue: "60s 1\n15s 100\n10s 1000",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true,
	},
	SettingRuntimeMemorySnapshotMinIntervalMS: {
		Key: SettingRuntimeMemorySnapshotMinIntervalMS, Type: SettingTypeInt64, DefaultValue: "10000",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingRuntimeMemorySnapshotMaxConcurrency: {
		Key: SettingRuntimeMemorySnapshotMaxConcurrency, Type: SettingTypeInt64, DefaultValue: "1",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingRuntimeMemoryShutdownFlushTimeoutMS: {
		Key: SettingRuntimeMemoryShutdownFlushTimeoutMS, Type: SettingTypeInt64, DefaultValue: "5000",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingRuntimeWebSocketMaxConnections: {
		Key: SettingRuntimeWebSocketMaxConnections, Type: SettingTypeInt64, DefaultValue: "1024",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingRuntimeWebSocketMaxConnectionsPerSite: {
		Key: SettingRuntimeWebSocketMaxConnectionsPerSite, Type: SettingTypeInt64, DefaultValue: "128",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	// Deprecated: legacy upload setting retained for old release compatibility.
	// Remove this once stored static.root releases no longer need to serve.
	SettingStaticRoot: {
		Key: SettingStaticRoot, Type: SettingTypeString, DefaultValue: "",
		AllowedScopes: []domain.ScopeType{domain.ScopeUpload}, SiteEditable: true,
	},
	SettingRoutes: {
		Key: SettingRoutes, Type: SettingTypeString, DefaultValue: "",
		AllowedScopes: []domain.ScopeType{domain.ScopeUpload}, SiteEditable: true,
	},
}

func Definitions() []SettingDefinition {
	out := make([]SettingDefinition, 0, len(registry))
	for _, def := range registry {
		out = append(out, def)
	}
	return out
}

func Default(key string) string {
	if def, ok := registry[key]; ok {
		return def.DefaultValue
	}
	return ""
}

func Validate(key, value string) error {
	def, ok := registry[key]
	if !ok {
		return fmt.Errorf("unsupported setting key: %s", key)
	}
	switch def.Type {
	case SettingTypeInt64:
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return fmt.Errorf("%s must be a number", key)
		}
		if n < 0 {
			return fmt.Errorf("%s must be >= 0", key)
		}
	case SettingTypeBool:
		if _, err := strconv.ParseBool(strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("%s must be true or false", key)
		}
	case SettingTypeEnum:
		if key == SettingLogLevel && ParseLogLevel(value) == "" {
			return fmt.Errorf("log level must be debug, info, warn, or error")
		}
		if key == SettingHTTPCacheMode && ParseHTTPCacheMode(value) == "" {
			return fmt.Errorf("%s must be revalidate, anti_cache, or max_age", key)
		}
		if key == SettingRuntimeMemoryPersistenceMode && ParseMemoryPersistenceMode(value) == "" {
			return fmt.Errorf("%s must be off or snapshot", key)
		}
	case SettingTypeString:
		if key == SettingAllowedHosts {
			if _, err := ParseAllowedHosts(value); err != nil {
				return err
			}
		}
		if key == SettingRuntimeMemorySnapshotSave {
			if _, err := ParseMemorySnapshotSaveRules(value); err != nil {
				return err
			}
		}
	}
	return nil
}

func ParseLogLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return "debug"
	case "info":
		return "info"
	case "warn", "warning":
		return "warn"
	case "error":
		return "error"
	default:
		return ""
	}
}

func ParseBool(value string) bool {
	b, _ := strconv.ParseBool(strings.TrimSpace(value))
	return b
}

func ParseHTTPCacheMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "revalidate", "no-cache", "no_cache":
		return "revalidate"
	case "anti_cache", "anti-cache", "no-store", "no_store":
		return "anti_cache"
	case "max_age", "max-age":
		return "max_age"
	default:
		return ""
	}
}

func ParseMemoryPersistenceMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off", "disabled", "none":
		return "off"
	case "snapshot", "rdb":
		return "snapshot"
	default:
		return ""
	}
}

type MemorySnapshotSaveRule struct {
	After   time.Duration
	Changes int64
}

func ParseMemorySnapshotSaveRules(value string) ([]MemorySnapshotSaveRule, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("%s must contain at least one rule", SettingRuntimeMemorySnapshotSave)
	}
	lines := strings.Split(value, "\n")
	rules := make([]MemorySnapshotSaveRule, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s rule %q must be '<duration> <changes>'", SettingRuntimeMemorySnapshotSave, line)
		}
		after, err := time.ParseDuration(fields[0])
		if err != nil {
			seconds, parseErr := strconv.ParseInt(fields[0], 10, 64)
			if parseErr != nil {
				return nil, fmt.Errorf("%s rule %q has invalid duration", SettingRuntimeMemorySnapshotSave, line)
			}
			after = time.Duration(seconds) * time.Second
		}
		if after < 0 {
			return nil, fmt.Errorf("%s rule %q duration must be >= 0", SettingRuntimeMemorySnapshotSave, line)
		}
		changes, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || changes <= 0 {
			return nil, fmt.Errorf("%s rule %q changes must be > 0", SettingRuntimeMemorySnapshotSave, line)
		}
		rules = append(rules, MemorySnapshotSaveRule{After: after, Changes: changes})
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("%s must contain at least one rule", SettingRuntimeMemorySnapshotSave)
	}
	return rules, nil
}

func FormatMemorySnapshotSaveRules(rules []MemorySnapshotSaveRule) string {
	lines := make([]string, 0, len(rules))
	for _, rule := range rules {
		lines = append(lines, rule.After.String()+" "+strconv.FormatInt(rule.Changes, 10))
	}
	return strings.Join(lines, "\n")
}

func ParseAllowedHosts(value string) ([]string, error) {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	seen := map[string]bool{}
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		host := strings.Trim(strings.ToLower(strings.TrimSpace(field)), ".")
		if host == "" || seen[host] {
			continue
		}
		if err := validateAllowedHostPattern(host); err != nil {
			return nil, err
		}
		seen[host] = true
		out = append(out, host)
	}
	return out, nil
}

func FormatAllowedHosts(hosts []string) string {
	return strings.Join(hosts, "\n")
}

func validateAllowedHostPattern(host string) error {
	if strings.Contains(host, "://") || strings.Contains(host, "/") || strings.Contains(host, ":") {
		return fmt.Errorf("allowed hosts must be hostnames, optionally prefixed with *.")
	}
	if strings.Contains(host, "*") {
		if !strings.HasPrefix(host, "*.") || strings.Count(host, "*") != 1 {
			return fmt.Errorf("allowed host wildcards must use the form *.example.com")
		}
		host = strings.TrimPrefix(host, "*.")
	}
	if host == "" || strings.Contains(host, "..") {
		return fmt.Errorf("allowed hosts must be valid hostnames")
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("allowed hosts must be valid hostnames")
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("allowed hosts must be valid hostnames")
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z':
			case r >= '0' && r <= '9':
			case r == '-':
			default:
				return fmt.Errorf("allowed hosts must contain only letters, numbers, hyphens, dots, and leftmost wildcards")
			}
		}
	}
	return nil
}
