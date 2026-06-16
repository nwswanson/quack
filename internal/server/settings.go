package server

import (
	"fmt"
	"strconv"
	"strings"
)

type SettingType string

const (
	SettingTypeBool   SettingType = "bool"
	SettingTypeInt64  SettingType = "int64"
	SettingTypeString SettingType = "string"
	SettingTypeEnum   SettingType = "enum"
)

type ScopeType string

const (
	ScopeSystem ScopeType = "system"
	ScopeUser   ScopeType = "user"
	ScopeSite   ScopeType = "site"
	ScopeUpload ScopeType = "upload"
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
	AllowedScopes []ScopeType
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
)

var settingRegistry = map[string]SettingDefinition{
	SettingMaxUploadBytes: {
		Key: SettingMaxUploadBytes, Type: SettingTypeInt64, DefaultValue: "536870912",
		AllowedScopes: []ScopeType{ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingMaxUploadFiles: {
		Key: SettingMaxUploadFiles, Type: SettingTypeInt64, DefaultValue: "10000",
		AllowedScopes: []ScopeType{ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingMaxRetainedVersions: {
		Key: SettingMaxRetainedVersions, Type: SettingTypeInt64, DefaultValue: "0",
		AllowedScopes: []ScopeType{ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindNumericCap,
	},
	SettingDefaultSite: {
		Key: SettingDefaultSite, Type: SettingTypeString, DefaultValue: "",
		AllowedScopes: []ScopeType{ScopeSystem}, AdminEditable: true,
	},
	SettingLogLevel: {
		Key: SettingLogLevel, Type: SettingTypeEnum, DefaultValue: "warn",
		AllowedScopes: []ScopeType{ScopeSystem}, AdminEditable: true, PolicyKind: PolicyKindEnum,
	},
	SettingDatabaseFeature: {
		Key: SettingDatabaseFeature, Type: SettingTypeBool, DefaultValue: "false",
		AllowedScopes: []ScopeType{ScopeSystem, ScopeUser, ScopeSite, ScopeUpload}, SiteEditable: true, AdminEditable: true, PolicyKind: PolicyKindCapability,
	},
	SettingDatabaseFeatureRequired: {
		Key: SettingDatabaseFeatureRequired, Type: SettingTypeBool, DefaultValue: "false",
		AllowedScopes: []ScopeType{ScopeUpload}, SiteEditable: true, PolicyKind: PolicyKindCapability,
	},
}

func SettingDefinitions() []SettingDefinition {
	out := make([]SettingDefinition, 0, len(settingRegistry))
	for _, def := range settingRegistry {
		out = append(out, def)
	}
	return out
}

func settingDefault(key string) string {
	if def, ok := settingRegistry[key]; ok {
		return def.DefaultValue
	}
	return ""
}

func ValidateSettingValue(key, value string) error {
	def, ok := settingRegistry[key]
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
		if key == SettingLogLevel && parseLogLevelName(value) == "" {
			return fmt.Errorf("log level must be debug, info, warn, or error")
		}
	}
	return nil
}

func parseLogLevelName(value string) string {
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

func parseBoolSetting(value string) bool {
	b, _ := strconv.ParseBool(strings.TrimSpace(value))
	return b
}
