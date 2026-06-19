package hotdata

import (
	"context"
	"strings"

	"quack/internal/domain"
	"quack/internal/sites"
)

type HotDataReader interface {
	GetServerSettings(ctx context.Context) (domain.ServerSettings, error)
	LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error)
	LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error)
	ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error)
	ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error)
	FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error)
	ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error)
	ServeSiteFile(ctx context.Context, site string, urlPath string) (sites.ServeSiteFileDecision, error)
}

type Source interface {
	GetServerSettings(ctx context.Context) (domain.ServerSettings, error)
	LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error)
	LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error)
	ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error)
	ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error)
	FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error)
	ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error)
}

type MutableHotDataReader interface {
	HotDataReader
	HotDataInvalidator
}

type HotDataInvalidator interface {
	InvalidateServerSettings(ctx context.Context) error
	InvalidateSite(ctx context.Context, site string) error
	InvalidateSiteVersion(ctx context.Context, siteSHA string, version int64) error
	InvalidatePolicies(ctx context.Context) error
}

type passthroughHotDataReader struct {
	db Source
}

func NewPassthroughHotDataReader(db Source) HotDataReader {
	return passthroughHotDataReader{db: db}
}

func (r passthroughHotDataReader) GetServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	settings, err := r.db.GetServerSettings(ctx)
	if err != nil {
		return domain.ServerSettings{}, err
	}
	settings.Locked = cloneBoolMap(settings.Locked)
	return settings, nil
}

func (r passthroughHotDataReader) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	policies, err := r.db.LoadPolicies(ctx, scopes)
	if err != nil {
		return nil, err
	}
	return append([]domain.PolicyRecord(nil), policies...), nil
}

func (r passthroughHotDataReader) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	settings, err := r.db.LoadUploadSettings(ctx, siteSHA, version)
	if err != nil {
		return nil, err
	}
	return cloneStringMap(settings), nil
}

func (r passthroughHotDataReader) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	manifests, err := r.db.ListCurrentSiteManifests(ctx)
	if err != nil {
		return nil, err
	}
	return cloneCurrentSiteManifests(manifests), nil
}

func (r passthroughHotDataReader) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error) {
	violations, err := r.db.ListPolicyViolations(ctx, siteSHA, version)
	if err != nil {
		return nil, err
	}
	return append([]domain.PolicyViolation(nil), violations...), nil
}

func (r passthroughHotDataReader) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	return r.db.FindCurrentSiteFile(ctx, site, relativePath)
}

func (r passthroughHotDataReader) ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error) {
	files, siteExists, err := r.db.ListCurrentSiteFiles(ctx, site)
	if err != nil {
		return nil, false, err
	}
	return append([]domain.UploadFileRecord(nil), files...), siteExists, nil
}

func (r passthroughHotDataReader) ServeSiteFile(ctx context.Context, site string, urlPath string) (sites.ServeSiteFileDecision, error) {
	settings, err := r.GetServerSettings(ctx)
	if err != nil {
		return sites.ServeSiteFileDecision{}, err
	}
	return sites.ResolveSiteFile(ctx, r, site, urlPath, strings.TrimSpace(settings.DefaultSite), false)
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	if in == nil {
		return nil
	}
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneCurrentSiteManifests(in []domain.CurrentSiteManifest) []domain.CurrentSiteManifest {
	out := append([]domain.CurrentSiteManifest(nil), in...)
	for i := range out {
		out[i].Settings = cloneStringMap(out[i].Settings)
	}
	return out
}
