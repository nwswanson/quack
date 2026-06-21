package cache

import (
	"context"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	"quack/internal/domain"
	appruntime "quack/internal/runtime"
)

type MemoryHotDataReaderOptions struct {
	TTL         time.Duration
	NegativeTTL time.Duration
	Jitter      time.Duration
	Now         func() time.Time
}

type memoryHotDataReader struct {
	source      HotDataReader
	ttl         time.Duration
	negativeTTL time.Duration
	jitter      time.Duration
	now         func() time.Time

	mu      sync.Mutex
	entries map[string]memoryEntry
	calls   map[string]*memoryCall
}

type memoryEntry struct {
	value  any
	expiry time.Time
}

type memoryCall struct {
	done  chan struct{}
	value any
	err   error
}

type cachedCurrentSiteFile struct {
	file       domain.UploadFileRecord
	ok         bool
	siteExists bool
}

type cachedCurrentSiteFiles struct {
	files      []domain.UploadFileRecord
	siteExists bool
}

func NewMemoryHotDataReader(source HotDataReader, opts MemoryHotDataReaderOptions) MutableHotDataReader {
	if opts.TTL <= 0 {
		opts.TTL = 5 * time.Second
	}
	if opts.NegativeTTL <= 0 {
		opts.NegativeTTL = time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &memoryHotDataReader{
		source:      source,
		ttl:         opts.TTL,
		negativeTTL: opts.NegativeTTL,
		jitter:      opts.Jitter,
		now:         opts.Now,
		entries:     map[string]memoryEntry{},
		calls:       map[string]*memoryCall{},
	}
}

func (r *memoryHotDataReader) GetServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	value, err := r.load(ctx, "server_settings", r.ttl, func(ctx context.Context) (any, error) {
		return r.source.GetServerSettings(ctx)
	})
	if err != nil {
		return domain.ServerSettings{}, err
	}
	settings := value.(domain.ServerSettings)
	settings.Locked = cloneBoolMap(settings.Locked)
	return settings, nil
}

func (r *memoryHotDataReader) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	key := "policies:" + policyScopesKey(scopes)
	value, err := r.load(ctx, key, r.ttl, func(ctx context.Context) (any, error) {
		return r.source.LoadPolicies(ctx, scopes)
	})
	if err != nil {
		return nil, err
	}
	return append([]domain.PolicyRecord(nil), value.([]domain.PolicyRecord)...), nil
}

func (r *memoryHotDataReader) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	key := "upload_settings:" + siteSHA + ":" + strconv.FormatInt(version, 10)
	value, err := r.load(ctx, key, r.ttl, func(ctx context.Context) (any, error) {
		return r.source.LoadUploadSettings(ctx, siteSHA, version)
	})
	if err != nil {
		return nil, err
	}
	return cloneStringMap(value.(map[string]string)), nil
}

func (r *memoryHotDataReader) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	value, err := r.load(ctx, "current_site_manifests", r.ttl, func(ctx context.Context) (any, error) {
		return r.source.ListCurrentSiteManifests(ctx)
	})
	if err != nil {
		return nil, err
	}
	return cloneCurrentSiteManifests(value.([]domain.CurrentSiteManifest)), nil
}

func (r *memoryHotDataReader) ListCurrentRuntimeRoutes(ctx context.Context) ([]appruntime.RouteMetadata, error) {
	value, err := r.load(ctx, "current_runtime_routes", r.ttl, func(ctx context.Context) (any, error) {
		return r.source.ListCurrentRuntimeRoutes(ctx)
	})
	if err != nil {
		return nil, err
	}
	return cloneRuntimeRoutes(value.([]appruntime.RouteMetadata)), nil
}

func (r *memoryHotDataReader) ListRuntimeRoutes(ctx context.Context, siteSHA string, version int64) ([]appruntime.RouteMetadata, error) {
	key := "runtime_routes:" + siteSHA + ":" + strconv.FormatInt(version, 10)
	value, err := r.load(ctx, key, r.ttl, func(ctx context.Context) (any, error) {
		return r.source.ListRuntimeRoutes(ctx, siteSHA, version)
	})
	if err != nil {
		return nil, err
	}
	return cloneRuntimeRoutes(value.([]appruntime.RouteMetadata)), nil
}

func (r *memoryHotDataReader) ListRuntimeBundleFiles(ctx context.Context, siteSHA string, version int64) ([]domain.UploadFileRecord, bool, error) {
	key := "runtime_bundle_files:" + siteSHA + ":" + strconv.FormatInt(version, 10)
	value, err := r.load(ctx, key, r.ttl, func(ctx context.Context) (any, error) {
		files, uploadExists, err := r.source.ListRuntimeBundleFiles(ctx, siteSHA, version)
		if err != nil {
			return nil, err
		}
		return cachedCurrentSiteFiles{files: append([]domain.UploadFileRecord(nil), files...), siteExists: uploadExists}, nil
	})
	if err != nil {
		return nil, false, err
	}
	cached := value.(cachedCurrentSiteFiles)
	return append([]domain.UploadFileRecord(nil), cached.files...), cached.siteExists, nil
}

func (r *memoryHotDataReader) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error) {
	key := "policy_violations:" + siteSHA + ":" + strconv.FormatInt(version, 10)
	value, err := r.load(ctx, key, r.ttl, func(ctx context.Context) (any, error) {
		return r.source.ListPolicyViolations(ctx, siteSHA, version)
	})
	if err != nil {
		return nil, err
	}
	return append([]domain.PolicyViolation(nil), value.([]domain.PolicyViolation)...), nil
}

func (r *memoryHotDataReader) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
	key := "current_site_file:" + site + ":" + relativePath
	value, err := r.load(ctx, key, r.ttl, func(ctx context.Context) (any, error) {
		file, ok, siteExists, err := r.source.FindCurrentSiteFile(ctx, site, relativePath)
		if err != nil {
			return nil, err
		}
		return cachedCurrentSiteFile{file: file, ok: ok, siteExists: siteExists}, nil
	})
	if err != nil {
		return domain.UploadFileRecord{}, false, false, err
	}
	cached := value.(cachedCurrentSiteFile)
	return cached.file, cached.ok, cached.siteExists, nil
}

func (r *memoryHotDataReader) ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error) {
	key := "current_site_files:" + site
	value, err := r.load(ctx, key, r.ttl, func(ctx context.Context) (any, error) {
		files, siteExists, err := r.source.ListCurrentSiteFiles(ctx, site)
		if err != nil {
			return nil, err
		}
		return cachedCurrentSiteFiles{files: append([]domain.UploadFileRecord(nil), files...), siteExists: siteExists}, nil
	})
	if err != nil {
		return nil, false, err
	}
	cached := value.(cachedCurrentSiteFiles)
	return append([]domain.UploadFileRecord(nil), cached.files...), cached.siteExists, nil
}

func (r *memoryHotDataReader) InvalidateServerSettings(ctx context.Context) error {
	r.deletePrefix("server_settings")
	return nil
}

func (r *memoryHotDataReader) InvalidateSite(ctx context.Context, site string) error {
	r.deletePrefix("current_site_file:" + site + ":")
	r.deletePrefix("current_site_files:" + site)
	r.deletePrefix("current_site_manifests")
	r.deletePrefix("current_runtime_routes")
	return nil
}

func (r *memoryHotDataReader) InvalidateSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	r.deletePrefix("upload_settings:" + siteSHA + ":")
	r.deletePrefix("runtime_routes:" + siteSHA + ":")
	r.deletePrefix("runtime_bundle_files:" + siteSHA + ":")
	r.deletePrefix("policy_violations:" + siteSHA + ":")
	r.deletePrefix("current_site_manifests")
	r.deletePrefix("current_runtime_routes")
	return nil
}

func (r *memoryHotDataReader) InvalidatePolicies(ctx context.Context) error {
	r.deletePrefix("policies:")
	r.deletePrefix("policy_violations:")
	return nil
}

func (r *memoryHotDataReader) load(ctx context.Context, key string, ttl time.Duration, load func(context.Context) (any, error)) (any, error) {
	now := r.now()
	r.mu.Lock()
	if entry, ok := r.entries[key]; ok && now.Before(entry.expiry) {
		value := entry.value
		r.mu.Unlock()
		return value, nil
	}
	if call := r.calls[key]; call != nil {
		done := call.done
		r.mu.Unlock()
		select {
		case <-done:
			return call.value, call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &memoryCall{done: make(chan struct{})}
	r.calls[key] = call
	r.mu.Unlock()

	value, err := load(ctx)

	r.mu.Lock()
	call.value = value
	call.err = err
	if err == nil {
		if file, ok := value.(cachedCurrentSiteFile); ok && !file.ok {
			ttl = r.negativeTTL
		}
		if files, ok := value.(cachedCurrentSiteFiles); ok && !files.siteExists {
			ttl = r.negativeTTL
		}
		r.entries[key] = memoryEntry{value: value, expiry: now.Add(r.ttlWithJitter(key, ttl))}
	}
	delete(r.calls, key)
	close(call.done)
	r.mu.Unlock()
	return value, err
}

func (r *memoryHotDataReader) ttlWithJitter(key string, ttl time.Duration) time.Duration {
	if r.jitter <= 0 {
		return ttl
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	offset := time.Duration(int64(h.Sum32()%uint32(2*r.jitter+1))) - r.jitter
	if ttl+offset <= 0 {
		return ttl
	}
	return ttl + offset
}

func (r *memoryHotDataReader) deletePrefix(prefix string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key := range r.entries {
		if key == prefix || strings.HasPrefix(key, prefix) {
			delete(r.entries, key)
		}
	}
}

func policyScopesKey(scopes []domain.PolicyScope) string {
	var b strings.Builder
	for i, scope := range scopes {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(string(scope.Type))
		b.WriteByte(':')
		b.WriteString(scope.ID)
	}
	return b.String()
}
