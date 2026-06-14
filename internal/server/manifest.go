package server

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

const MaxSiteManifestBytes int64 = 64 << 10

type SiteManifest struct {
	Features SiteManifestFeatures `yaml:"features"`
}

type SiteManifestFeatures struct {
	Database SiteManifestDatabase `yaml:"database"`
}

type SiteManifestDatabase struct {
	Enabled  bool `yaml:"enabled"`
	Required bool `yaml:"required"`
}

func DefaultSiteManifest() SiteManifest {
	return SiteManifest{}
}

func ParseSiteManifest(r io.Reader, size int64) (SiteManifest, error) {
	if size > MaxSiteManifestBytes {
		return SiteManifest{}, badArchiveError{err: fmt.Errorf("site.yaml exceeds %d bytes", MaxSiteManifestBytes)}
	}
	limited := io.LimitReader(r, MaxSiteManifestBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return SiteManifest{}, badArchiveError{err: fmt.Errorf("read site.yaml: %w", err)}
	}
	if int64(len(data)) > MaxSiteManifestBytes {
		return SiteManifest{}, badArchiveError{err: fmt.Errorf("site.yaml exceeds %d bytes", MaxSiteManifestBytes)}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return DefaultSiteManifest(), nil
	}

	var manifest SiteManifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&manifest); err != nil {
		return SiteManifest{}, badArchiveError{err: fmt.Errorf("invalid site.yaml: %w", err)}
	}
	if manifest.Features.Database.Required && !manifest.Features.Database.Enabled {
		return SiteManifest{}, badArchiveError{err: fmt.Errorf("database.required cannot be true when database.enabled is false")}
	}
	return manifest, nil
}

func ManifestSettings(manifest SiteManifest) map[string]string {
	return map[string]string{
		SettingDatabaseFeature:         boolSetting(manifest.Features.Database.Enabled),
		SettingDatabaseFeatureRequired: boolSetting(manifest.Features.Database.Required),
	}
}

func boolSetting(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
