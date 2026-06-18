# How to write a hot data cache layer

This document explains how to add a concrete cache implementation for the server hot path.

The current architecture intentionally keeps caching outside SQLite and outside HTTP handlers:

```text
handler
  -> SiteReadService
      -> HotDataReader
          -> Database

handler
  -> SiteWriteService
      -> Database
      -> HotDataInvalidator
```

The important idea is that handlers ask for application concepts, not cache details. A handler should ask for server settings, current runtime status, or a current file. It should not know whether the answer came from SQLite, memory, Redis, a snapshot, or a read-through cache.

## Key interfaces

The read side is `HotDataReader`:

```go
type HotDataReader interface {
	GetServerSettings(ctx context.Context) (ServerSettings, error)
	LoadPolicies(ctx context.Context, scopes []PolicyScope) ([]PolicyRecord, error)
	LoadUploadSettings(ctx context.Context, siteSHA string, version int64) (map[string]string, error)
	ListCurrentSiteManifests(ctx context.Context) ([]CurrentSiteManifest, error)
	ListPolicyViolations(ctx context.Context, siteSHA string, version int64) ([]PolicyViolation, error)
	FindCurrentSiteFile(ctx context.Context, site string, relativePath string) (UploadFileRecord, bool, bool, error)
}
```

The invalidation side is `HotDataInvalidator`:

```go
type HotDataInvalidator interface {
	InvalidateServerSettings(ctx context.Context) error
	InvalidateSite(ctx context.Context, site string) error
	InvalidateSiteVersion(ctx context.Context, siteSHA string, version int64) error
	InvalidatePolicies(ctx context.Context) error
}
```

A real cache normally implements both:

```go
type MutableHotDataReader interface {
	HotDataReader
	HotDataInvalidator
}
```

`SiteReadService` only needs a `HotDataReader`. `SiteWriteService` needs a durable database plus a `HotDataInvalidator`.

## Why the cache is not inside `Database`

`Database` is the durable storage boundary. It should know how to read and write SQLite-backed records.

Cache policy is a different concern:

- TTLs
- negative cache behavior
- singleflight/stampede protection
- invalidation after writes
- copying mutable cached values
- observability around hits and misses

Putting those concerns into `internal/sqlitedb` would mix persistence with serving optimization. Keeping the cache as a separate `HotDataReader` makes it replaceable and keeps SQLite code boring.

## Existing implementations

There are two implementations today.

### Pass-through reader

`NewPassthroughHotDataReader(db)` delegates every read to the database and copies mutable results before returning them.

Use it when you want no caching:

```go
hot := NewPassthroughHotDataReader(db)
read := NewSiteReadService(hot)
write := NewSiteWriteService(db, hot, NewNoopHotDataInvalidator())
```

### Memory reader

`NewMemoryHotDataReader(source, opts)` wraps another `HotDataReader`.

It provides:

- TTL caching
- shorter TTLs for negative current-file lookups
- TTL jitter
- singleflight for concurrent misses
- explicit invalidation
- copies of mutable maps/slices on return

Normal server wiring is:

```go
source := NewPassthroughHotDataReader(db)
hot := NewMemoryHotDataReader(source, MemoryHotDataReaderOptions{})
read := NewSiteReadService(hot)
write := NewSiteWriteService(db, hot, hot)
```

The `source` is still the database-backed reader. The `hot` reader is the cache wrapper.

## Implementing a new cache

Create a type that wraps a source `HotDataReader`:

```go
type myHotDataReader struct {
	source HotDataReader
}

func NewMyHotDataReader(source HotDataReader) MutableHotDataReader {
	return &myHotDataReader{source: source}
}
```

The source reader should be treated as the authoritative fallback. On a cache miss, call `source`. On a cache hit, return the cached value.

## Read method pattern

Each read method should follow this shape:

```go
func (r *myHotDataReader) GetServerSettings(ctx context.Context) (ServerSettings, error) {
	if value, ok := r.getSettingsFromCache(); ok {
		value.Locked = cloneBoolMap(value.Locked)
		return value, nil
	}

	value, err := r.source.GetServerSettings(ctx)
	if err != nil {
		return ServerSettings{}, err
	}

	r.storeSettings(value)
	value.Locked = cloneBoolMap(value.Locked)
	return value, nil
}
```

The important rules:

- Do not cache errors.
- Cache successful responses.
- Copy mutable data before returning it.
- Use the same return contract as the source.
- Respect `ctx` when doing fallback work.

## Mutable return values

Some hot data contains maps or slices. Maps and slices are mutable references in Go. If callers mutate a cached map, they can corrupt the cache for future requests.

Always copy these before returning:

- `ServerSettings.Locked`
- `map[string]string` from `LoadUploadSettings`
- `[]PolicyRecord`
- `[]CurrentSiteManifest`
- `CurrentSiteManifest.Settings`
- `[]PolicyViolation`

Use the helpers in `hot_data_reader.go` where possible:

```go
settings.Locked = cloneBoolMap(settings.Locked)
settingsMap := cloneStringMap(cachedSettings)
manifests := cloneCurrentSiteManifests(cachedManifests)
policies := append([]PolicyRecord(nil), cachedPolicies...)
```

Structs without reference fields can be returned directly.

## Current file lookup contract

`FindCurrentSiteFile` has a three-value result:

```go
func FindCurrentSiteFile(
	ctx context.Context,
	site string,
	relativePath string,
) (file UploadFileRecord, ok bool, siteExists bool, err error)
```

Preserve this exactly:

- `ok=true, siteExists=true`: file exists.
- `ok=false, siteExists=true`: site exists, but this path does not.
- `ok=false, siteExists=false`: site does not exist or is not live.
- `err != nil`: lookup failed and should not be cached.

Negative lookups are worth caching because missing files can be hot. Use a shorter TTL unless invalidation is comprehensive.

Example cache entry:

```go
type cachedCurrentSiteFile struct {
	file       UploadFileRecord
	ok         bool
	siteExists bool
}
```

## TTLs and jitter

Use TTLs so stale cache entries eventually heal even if invalidation misses something.

Use shorter TTLs for negative entries:

```go
positiveTTL := 5 * time.Second
negativeTTL := 1 * time.Second
```

Use jitter so many entries do not expire at the exact same time:

```go
effectiveTTL := ttl + deterministicJitter(cacheKey)
```

Deterministic jitter is often easier to test than random jitter. It also avoids needing global random state.

## Singleflight

Concurrent misses for the same key should share one database load.

Without singleflight, a burst of requests for the same missing key can all hit SQLite at once. That is exactly the kind of hot-path pressure the cache is meant to reduce.

The memory reader uses a small in-process singleflight map:

```go
calls map[string]*memoryCall
```

For another implementation, use the same principle:

- If key is already loading, wait for that load.
- If not, mark it as loading and perform the source read.
- Store the result only if there was no error.
- Wake waiters.

## Invalidation

Invalidation happens after successful durable writes. Do not invalidate before a write succeeds.

The current coarse invalidation methods are:

- `InvalidateServerSettings`
- `InvalidateSite`
- `InvalidateSiteVersion`
- `InvalidatePolicies`

Coarse invalidation is preferred at this stage. It is better to evict a little too much than to serve stale data because a fine-grained invalidation missed a related entry.

Expected write invalidations:

- `SaveServerSettings`: invalidate server settings.
- `SavePolicy`: invalidate policies and runtime-related policy data.
- `SaveUploadSettings`: invalidate upload settings for that site version.
- `FinishUpload`: invalidate site and site-version data.
- `PublishSite`: invalidate site data.
- `RollbackSite`: invalidate site data.
- `UnpublishSite`: invalidate site data.
- `DeleteSite`: invalidate site data.
- `SavePolicyViolation` / `ResolvePolicyViolation`: invalidate site-version runtime data.

This is why invalidation lives in `SiteWriteService`, not in handlers and not in SQLite.

## Wiring a new implementation

Change `server.go` only at the construction point:

```go
source := NewPassthroughHotDataReader(db)
hot := NewMyHotDataReader(source, opts)
read := NewSiteReadService(hot)
write := NewSiteWriteService(db, hot, hot)
```

If your implementation only supports reads and not invalidation:

```go
source := NewPassthroughHotDataReader(db)
hot := NewMyReadOnlyHotDataReader(source, opts)
read := NewSiteReadService(hot)
write := NewSiteWriteService(db, hot, NewNoopHotDataInvalidator())
```

Prefer implementing invalidation if the reader caches anything with a TTL longer than a few seconds.

## Testing checklist

Add tests for each cache implementation.

Read behavior:

- First read loads from source.
- Second read hits cache.
- TTL expiry reloads from source.
- Database/source errors are not cached.
- Returned maps and slices cannot mutate cache internals.

Current file lookup:

- Existing file is cached.
- Missing file on existing site is cached.
- Missing site is cached.
- Source errors are not cached.
- The `ok` and `siteExists` booleans are preserved exactly.

Concurrency:

- Concurrent misses for the same key are singleflighted.
- Concurrent misses for different keys do not block each other more than necessary.

Invalidation:

- Server settings invalidation reloads settings.
- Site invalidation evicts current file lookups and current manifests.
- Site-version invalidation evicts upload settings and policy violations.
- Policy invalidation evicts policy reads and runtime-related policy data.
- Failed writes do not invalidate.

Service integration:

- `SiteReadService` should work with the new reader without handler changes.
- `SiteWriteService` should invalidate the new reader after successful writes.

## Common mistakes

Do not return cached maps or slices directly.

Do not cache errors. A transient SQLite error should not poison the cache.

Do not make handlers call the cache directly. Add an application method to `SiteReadService` if the handler needs a new read concept.

Do not add invalidation to SQLite methods. SQLite should remain durable storage.

Do not make the cache interface generic with `Get`, `Set`, or `Delete`. The interface should describe application data, because that keeps call sites readable and makes invalidation easier to reason about.

Do not use long TTLs until invalidation is well covered by tests.

## When to add a snapshot cache

A future cache may expose a higher-level current-site snapshot internally:

```go
type CurrentSiteSnapshot struct {
	Site    string
	SiteSHA string
	Version int64
	Runtime SiteRuntimeDecision
	Files   map[string]UploadFileRecord
}
```

That can make serving very fast, but add it only after profiling shows repeated current-site lookups are still expensive. Keep blob storage reads outside the snapshot unless there is a clear reason to cache file contents.
