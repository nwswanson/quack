package server

import appsettings "quack/internal/settings"

import (
	"quack/internal/protocol"
)

const MaxSiteManifestBytes int64 = protocol.MaxSiteManifestBytes

type SiteManifest = protocol.SiteManifest
type SiteManifestFeatures = protocol.SiteManifestFeatures
type SiteManifestDatabase = protocol.SiteManifestDatabase

func ManifestSettings(manifest SiteManifest) map[string]string {
	return map[string]string{
		appsettings.SettingDatabaseFeature:         boolSetting(manifest.Features.Database.Enabled),
		appsettings.SettingDatabaseFeatureRequired: boolSetting(manifest.Features.Database.Required),
	}
}

func boolSetting(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
