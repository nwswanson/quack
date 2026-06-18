package server

import "context"

type HotDataCache interface {
	GetServerSettings(ctx context.Context) (ServerSettings, error)
	LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error)
	LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error)
	ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error)
	ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error)
	FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error)
}

type passthroughHotDataCache struct {
	db Database
}

func NewPassthroughHotDataCache(db Database) HotDataCache {
	return passthroughHotDataCache{db: db}
}

func (c passthroughHotDataCache) GetServerSettings(ctx context.Context) (ServerSettings, error) {
	settings, err := c.db.GetServerSettings(ctx)
	if err != nil {
		return ServerSettings{}, err
	}
	settings.Locked = cloneBoolMap(settings.Locked)
	return settings, nil
}

func (c passthroughHotDataCache) LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error) {
	policies, err := c.db.LoadPolicies(ctx, scopes)
	if err != nil {
		return nil, err
	}
	return append([]PolicyRecord(nil), policies...), nil
}

func (c passthroughHotDataCache) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	settings, err := c.db.LoadUploadSettings(ctx, siteSHA, version)
	if err != nil {
		return nil, err
	}
	return cloneStringMap(settings), nil
}

func (c passthroughHotDataCache) ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error) {
	manifests, err := c.db.ListCurrentSiteManifests(ctx)
	if err != nil {
		return nil, err
	}
	out := append([]CurrentSiteManifest(nil), manifests...)
	for i := range out {
		out[i].Settings = cloneStringMap(out[i].Settings)
	}
	return out, nil
}

func (c passthroughHotDataCache) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error) {
	violations, err := c.db.ListPolicyViolations(ctx, siteSHA, version)
	if err != nil {
		return nil, err
	}
	return append([]PolicyViolation(nil), violations...), nil
}

func (c passthroughHotDataCache) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error) {
	return c.db.FindCurrentSiteFile(ctx, site, relativePath)
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
