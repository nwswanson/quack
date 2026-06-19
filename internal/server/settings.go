package server

import (
	"quack/internal/domain"
	appsettings "quack/internal/settings"
)

type SettingType = appsettings.SettingType

const (
	SettingTypeBool   = appsettings.SettingTypeBool
	SettingTypeInt64  = appsettings.SettingTypeInt64
	SettingTypeString = appsettings.SettingTypeString
	SettingTypeEnum   = appsettings.SettingTypeEnum
)

type ScopeType = domain.ScopeType

const (
	ScopeSystem = appsettings.ScopeSystem
	ScopeUser   = appsettings.ScopeUser
	ScopeSite   = appsettings.ScopeSite
	ScopeUpload = appsettings.ScopeUpload
)

type PolicyKind = appsettings.PolicyKind

const (
	PolicyKindCapability = appsettings.PolicyKindCapability
	PolicyKindNumericCap = appsettings.PolicyKindNumericCap
	PolicyKindEnum       = appsettings.PolicyKindEnum
)

type SettingDefinition = appsettings.SettingDefinition

const (
	SettingMaxUploadBytes          = appsettings.SettingMaxUploadBytes
	SettingMaxUploadFiles          = appsettings.SettingMaxUploadFiles
	SettingMaxRetainedVersions     = appsettings.SettingMaxRetainedVersions
	SettingDefaultSite             = appsettings.SettingDefaultSite
	SettingLogLevel                = appsettings.SettingLogLevel
	SettingDatabaseFeature         = appsettings.SettingDatabaseFeature
	SettingDatabaseFeatureRequired = appsettings.SettingDatabaseFeatureRequired
)

func SettingDefinitions() []SettingDefinition {
	return appsettings.Definitions()
}

func settingDefault(key string) string {
	return appsettings.Default(key)
}

func ValidateSettingValue(key, value string) error {
	return appsettings.Validate(key, value)
}

func parseLogLevelName(value string) string {
	return appsettings.ParseLogLevel(value)
}

func parseBoolSetting(value string) bool {
	return appsettings.ParseBool(value)
}
