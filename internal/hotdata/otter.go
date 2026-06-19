package hotdata

import (
	"context"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maypok86/otter"

	"quack/internal/domain"
)

type OtterHotDataReaderOptions struct {
	Capacity    int
	TTL         time.Duration
	NegativeTTL time.Duration
	Jitter      time.Duration
}

type otterHotDataReader struct {
	source      HotDataReader
	cache       otter.CacheWithVariableTTL[string, any]
	ttl         time.Duration
	negativeTTL time.Duration
	jitter      time.Duration

	mu    sync.Mutex
	calls map[string]*otterCall
}

type otterCall struct {
	done  chan struct{}
	value any
	err   error
}

func NewOtterHotDataReader(source HotDataReader, opts OtterHotDataReaderOptions) MutableHotDataReader {
	if opts.Capacity <= 0 {
		opts.Capacity = 10_000
	}
	if opts.TTL <= 0 {
		opts.TTL = 5 * time.Second
	}
	if opts.NegativeTTL <= 0 {
		opts.NegativeTTL = time.Second
	}
	cache, err := otter.MustBuilder[string, any](opts.Capacity).WithVariableTTL().Build()
	if err != nil {
		panic(err)
	}
	return &otterHotDataReader{
		source:      source,
		cache:       cache,
		ttl:         opts.TTL,
		negativeTTL: opts.NegativeTTL,
		jitter:      opts.Jitter,
		calls:       map[string]*otterCall{},
	}
}

func (r *otterHotDataReader) GetServerSettings(ctx context.Context) (domain.ServerSettings, error) {
	value, err := r.load(ctx, "server_settings", r.ttl, func(ctx context.Context) (any, error) {
		settings, err := r.source.GetServerSettings(ctx)
		if err != nil {
			return domain.ServerSettings{}, err
		}
		settings.Locked = cloneBoolMap(settings.Locked)
		return settings, nil
	})
	if err != nil {
		return domain.ServerSettings{}, err
	}
	settings := value.(domain.ServerSettings)
	settings.Locked = cloneBoolMap(settings.Locked)
	return settings, nil
}

func (r *otterHotDataReader) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	key := "policies:" + policyScopesKey(scopes)
	value, err := r.load(ctx, key, r.ttl, func(ctx context.Context) (any, error) {
		policies, err := r.source.LoadPolicies(ctx, scopes)
		if err != nil {
			return nil, err
		}
		return append([]domain.PolicyRecord(nil), policies...), nil
	})
	if err != nil {
		return nil, err
	}
	return append([]domain.PolicyRecord(nil), value.([]domain.PolicyRecord)...), nil
}

func (r *otterHotDataReader) LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error) {
	key := "upload_settings:" + siteSHA + ":" + strconv.FormatInt(version, 10)
	value, err := r.load(ctx, key, r.ttl, func(ctx context.Context) (any, error) {
		settings, err := r.source.LoadUploadSettings(ctx, siteSHA, version)
		if err != nil {
			return nil, err
		}
		return cloneStringMap(settings), nil
	})
	if err != nil {
		return nil, err
	}
	return cloneStringMap(value.(map[string]string)), nil
}

func (r *otterHotDataReader) ListCurrentSiteManifests(ctx context.Context) ([]domain.CurrentSiteManifest, error) {
	value, err := r.load(ctx, "current_site_manifests", r.ttl, func(ctx context.Context) (any, error) {
		manifests, err := r.source.ListCurrentSiteManifests(ctx)
		if err != nil {
			return nil, err
		}
		return cloneCurrentSiteManifests(manifests), nil
	})
	if err != nil {
		return nil, err
	}
	return cloneCurrentSiteManifests(value.([]domain.CurrentSiteManifest)), nil
}

func (r *otterHotDataReader) ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]domain.PolicyViolation, error) {
	key := "policy_violations:" + siteSHA + ":" + strconv.FormatInt(version, 10)
	value, err := r.load(ctx, key, r.ttl, func(ctx context.Context) (any, error) {
		violations, err := r.source.ListPolicyViolations(ctx, siteSHA, version)
		if err != nil {
			return nil, err
		}
		return append([]domain.PolicyViolation(nil), violations...), nil
	})
	if err != nil {
		return nil, err
	}
	return append([]domain.PolicyViolation(nil), value.([]domain.PolicyViolation)...), nil
}

func (r *otterHotDataReader) FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (domain.UploadFileRecord, bool, bool, error) {
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

func (r *otterHotDataReader) ListCurrentSiteFiles(ctx context.Context, site string) ([]domain.UploadFileRecord, bool, error) {
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

func (r *otterHotDataReader) InvalidateServerSettings(ctx context.Context) error {
	r.deletePrefix("server_settings")
	return nil
}

func (r *otterHotDataReader) InvalidateSite(ctx context.Context, site string) error {
	r.deletePrefix("current_site_file:" + site + ":")
	r.deletePrefix("current_site_files:" + site)
	r.deletePrefix("current_site_manifests")
	return nil
}

func (r *otterHotDataReader) InvalidateSiteVersion(ctx context.Context, siteSHA string, version int64) error {
	r.deletePrefix("upload_settings:" + siteSHA + ":")
	r.deletePrefix("policy_violations:" + siteSHA + ":")
	r.deletePrefix("current_site_manifests")
	return nil
}

func (r *otterHotDataReader) InvalidatePolicies(ctx context.Context) error {
	r.deletePrefix("policies:")
	r.deletePrefix("policy_violations:")
	return nil
}

func (r *otterHotDataReader) load(ctx context.Context, key string, ttl time.Duration, load func(context.Context) (any, error)) (any, error) {
	if value, ok := r.cache.Get(key); ok {
		return value, nil
	}

	r.mu.Lock()
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
	call := &otterCall{done: make(chan struct{})}
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
		r.cache.Set(key, value, r.ttlWithJitter(key, ttl))
	}
	delete(r.calls, key)
	close(call.done)
	r.mu.Unlock()
	return value, err
}

func (r *otterHotDataReader) ttlWithJitter(key string, ttl time.Duration) time.Duration {
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

func (r *otterHotDataReader) deletePrefix(prefix string) {
	r.cache.DeleteByFunc(func(key string, value any) bool {
		return key == prefix || strings.HasPrefix(key, prefix)
	})
}
