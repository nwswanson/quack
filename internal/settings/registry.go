package settings

import (
	"fmt"
	"strconv"
	"strings"

	"quack/internal/domain"
)

const (
	DefaultMaxUploadBytes int64 = 512 << 20
	DefaultMaxUploadFiles int64 = 10000
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
	SettingMaxUploadBytes          = "max_upload_bytes"
	SettingMaxUploadFiles          = "max_upload_files"
	SettingMaxRetainedVersions     = "max_retained_versions"
	SettingDefaultSite             = "default_site"
	SettingLogLevel                = "log_level"
	SettingDatabaseFeature         = "features.database.enabled"
	SettingDatabaseFeatureRequired = "features.database.required"
	SettingRuntimeHTTPFeature      = "features.runtime.http.enabled"
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
	SettingLogLevel: {
		Key: SettingLogLevel, Type: SettingTypeEnum, DefaultValue: "warn",
		AllowedScopes: []domain.ScopeType{domain.ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindEnum,
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
