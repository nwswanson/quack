package server

import "context"

type HotDataReader interface {
	GetServerSettings(ctx context.Context) (ServerSettings, error)
	LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error)
	LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error)
	ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error)
	ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error)
	FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error)
}

type hotDataSource interface {
	GetServerSettings(ctx context.Context) (ServerSettings, error)
	LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error)
	LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error)
	ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error)
	ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error)
	FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error)
}

type HotDataInvalidator interface {
	InvalidateServerSettings(ctx context.Context) error
	InvalidateSite(ctx context.Context, site string) error
	InvalidateSiteVersion(ctx context.Context, siteSHA string, version int64) error
	InvalidatePolicies(ctx context.Context) error
}

type MutableHotDataReader interface {
	HotDataReader
	HotDataInvalidator
}

type passthroughHotDataReader struct {
	db hotDataSource
}

func NewPassthroughHotDataReader(db hotDataSource) HotDataReader {
	return passthroughHotDataReader{db: db}
}

func NewNoopHotDataInvalidator() HotDataInvalidator {
	return noopHotDataInvalidator{}
}

type noopHotDataInvalidator struct{}

func (noopHotDataInvalidator) InvalidateServerSettings(ctx context.Context) error {
	return nil
}

func (noopHotDataInvalidator) InvalidateSite(ctx context.Context, site string) error {
	return nil
}

func (noopHotDataInvalidator) InvalidateSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	return nil
}

func (noopHotDataInvalidator) InvalidatePolicies(ctx context.Context) error {
	return nil
}

func (r passthroughHotDataReader) GetServerSettings(ctx context.Context) (ServerSettings, error) {
	settings, err := r.db.GetServerSettings(ctx)
	if err != nil {
		return ServerSettings{}, err
	}
	settings.Locked = cloneBoolMap(settings.Locked)
	return settings, nil
}

func (r passthroughHotDataReader) LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error) {
	policies, err := r.db.LoadPolicies(ctx, scopes)
	if err != nil {
		return nil, err
	}
	return append([]PolicyRecord(nil), policies...), nil
}

func (r passthroughHotDataReader) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	settings, err := r.db.LoadUploadSettings(ctx, siteSHA, version)
	if err != nil {
		return nil, err
	}
	return cloneStringMap(settings), nil
}

func (r passthroughHotDataReader) ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error) {
	manifests, err := r.db.ListCurrentSiteManifests(ctx)
	if err != nil {
		return nil, err
	}
	return cloneCurrentSiteManifests(manifests), nil
}

func (r passthroughHotDataReader) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error) {
	violations, err := r.db.ListPolicyViolations(ctx, siteSHA, version)
	if err != nil {
		return nil, err
	}
	return append([]PolicyViolation(nil), violations...), nil
}

func (r passthroughHotDataReader) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error) {
	return r.db.FindCurrentSiteFile(ctx, site, relativePath)
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

func cloneCurrentSiteManifests(in []CurrentSiteManifest) []CurrentSiteManifest {
	out := append([]CurrentSiteManifest(nil), in...)
	for i := range out {
		out[i].Settings = cloneStringMap(out[i].Settings)
	}
	return out
}
