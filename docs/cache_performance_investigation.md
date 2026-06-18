# Hot data cache performance investigation

This note captures the first performance investigation after adding an
Otter-backed hot data reader. The important result is that a low-level metadata
cache can be correct and still fail to improve request latency if the real serve
path is dominated by other work or if the cache is not shaped like the request.

## Observed external benchmark

The motivating benchmark was an ApacheBench run against the public k3s ingress:

```text
ab -n 3000 -c 20 -k -r -s 30 https://nathanielswanson.quack.k3s.nathanielswanson.com/
```

The response body was about 40 KB:

```text
Document Length:        40643 bytes
Concurrency Level:      20
Time taken for tests:   6.741 seconds
Requests per second:    445.02 [#/sec] (mean)
Time per request:       44.942 [ms] (mean)
Time per request:       2.247 [ms] (mean, across all concurrent requests)
Transfer rate:          17734.57 [Kbytes/sec] received
```

Connection timing:

```text
              min  mean[+/-sd] median   max
Connect:        0    0   3.5      0      59
Processing:    10   44  23.1     41     356
Waiting:        5   36  11.0     35     214
Total:         10   45  24.6     41     400
```

The noteworthy number is `Waiting`, which ApacheBench reports as time to first
byte. A mean first-byte time around 36 ms means a significant part of the delay
is before or near the first write. That cannot be explained by a single in-process
cache lookup alone.

The user also observed that a raw 404 from Traefik ingress is around 7 ms. That
is a useful baseline: any quack request path must pay ingress/network overhead on
top of application work, blob access, and response streaming.

## Current serve path

For a normal site request, the handler is:

```text
handleServeFile
  -> serveSiteFile
      -> siteReadService.ServerSettings
      -> serveSiteFileWithFallback
          -> siteReadService.CurrentSiteRuntime
          -> requestedRelativePath
          -> siteReadService.CurrentSiteFile
          -> serveBlob
              -> store.OpenBlob
              -> http.ServeContent
```

The current cache sits under `SiteReadService` as a `HotDataReader`. It caches
individual application reads:

- `GetServerSettings`
- `LoadPolicies`
- `LoadUploadSettings`
- `ListCurrentSiteManifests`
- `ListPolicyViolations`
- `FindCurrentSiteFile`

That means one request still performs several cache lookups, plus path handling,
plus filesystem work. The cache removes some SQLite reads, but it does not turn
the serve path into a single resolved decision.

## What the Otter cache actually changes

The Otter-backed reader replaces the backing database call on a hit for each
cached method. For example:

```text
CurrentSiteFile
  -> HotDataReader.FindCurrentSiteFile
      -> otter.Get("current_site_file:<site>:<relativePath>")
```

On a hit it avoids:

- SQLite query for current site version.
- SQLite query for the current file row.

It still pays:

- cache key construction
- hashing/table lookup inside Otter
- interface value/type assertion
- mutable result copying for methods returning maps/slices
- per-method calls through `SiteReadService`
- blob path cleaning/opening/statting/reading
- response headers/body writes

For current file lookups, the cached value is a small struct:

```go
type cachedCurrentSiteFile struct {
	file       UploadFileRecord
	ok         bool
	siteExists bool
}
```

This is useful, but it is still only one piece of the request.

## Local benchmarks

A benchmark file was added during investigation:

```text
internal/server/hot_data_reader_benchmark_test.go
```

The benchmarks were run with:

```text
env GOCACHE=/private/tmp/quack-go-cache go test ./internal/server \
  -run '^$' \
  -bench 'BenchmarkHotDataReader|BenchmarkServeFileWithBlob' \
  -benchmem \
  -benchtime=2s \
  -count=2
```

The local machine was:

```text
goos: darwin
goarch: arm64
cpu: Apple M1 Pro
```

### Single current-file metadata lookup

This benchmark compares `FindCurrentSiteFile` when the backing source is an
in-memory stub.

```text
BenchmarkHotDataReaderFindCurrentSiteFile/passthrough-8   ~2.9 ns/op     0 B/op   0 allocs/op
BenchmarkHotDataReaderFindCurrentSiteFile/memory-8        ~176 ns/op    48 B/op   1 alloc/op
BenchmarkHotDataReaderFindCurrentSiteFile/otter-8         ~24 ns/op     48 B/op   1 alloc/op
```

Interpretation:

- Otter is much faster than the hand-rolled memory cache for a simple cache hit.
- Otter is still slower than a trivial in-memory passthrough because any cache
  lookup has overhead.
- This benchmark does not mean passthrough is faster than SQLite in production.
  It only shows that if the source read is already extremely cheap, adding a cache
  can lose.

### Serve metadata path

This benchmark performs the metadata reads that a normal serve request performs
before opening the blob:

```text
ServerSettings
CurrentSiteRuntime
CurrentSiteFile
```

Results:

```text
BenchmarkHotDataReaderServeMetadata/passthrough-8   ~158 ns/op   384 B/op   3 allocs/op
BenchmarkHotDataReaderServeMetadata/memory-8        ~819 ns/op   464 B/op   5 allocs/op
BenchmarkHotDataReaderServeMetadata/otter-8         ~211 ns/op   464 B/op   5 allocs/op
```

Interpretation:

- Otter is again much faster than the old memory cache.
- Otter still adds about two allocations over the trivial in-memory source.
- The metadata cache shape still requires multiple calls and repeated application
  logic per request.

### Full handler path with a 40 KB blob

This benchmark runs `handleServeFile` against a 40 KB file on disk using a
discarding response writer.

```text
BenchmarkServeFileWithBlob/passthrough-8   ~16.3 us/op   33895 B/op   16 allocs/op
BenchmarkServeFileWithBlob/otter-8         ~16.9 us/op   33943 B/op   17 allocs/op
```

Interpretation:

- The cache adds roughly 0.6 to 0.7 microseconds locally on this synthetic path.
- That is real overhead, but it is far too small to explain a 36 ms mean
  time-to-first-byte by itself.
- The file-serving path, HTTP machinery, filesystem access, ingress, and cluster
  environment are more likely contributors to the observed end-to-end latency.

## Why Otter can appear slower than no cache

There are several reasons this can happen.

### The cache can be protecting reads that are not the bottleneck

If the SQLite metadata queries are already fast enough relative to:

- opening the blob file
- `http.ServeContent`
- network transfer
- ingress/proxy latency
- TLS
- logging
- container and filesystem overhead

then replacing SQLite reads with cache hits may not improve end-to-end latency.
It may even make a local microbenchmark look slower if the non-cached source is
an in-memory fake or if SQLite pages are hot in the OS cache.

### The cache shape is too low-level

The current cache stores individual data access results. The serve path needs a
higher-level decision:

```text
For this host and URL path, should we serve a blob, redirect, return empty index,
fall back to default site, return 403, or return 404?
```

The current implementation asks several smaller questions on every request:

```text
What are server settings?
What is this site's runtime status?
What is the file for this path?
If no file exists, is there an index file?
```

Caching those pieces independently helps database pressure, but it does not
minimize request work.

### `CurrentSiteRuntime` scans current manifests

`CurrentSiteRuntime` currently calls `ListCurrentSiteManifests` and scans for the
requested site. Even if the list is cached, that means each request has work
proportional to the number of current sites:

```go
manifests, err := s.hot.ListCurrentSiteManifests(ctx)
for _, manifest := range manifests {
	if manifest.Site != site {
		continue
	}
	...
}
```

The cached manifest slice also has to be copied before returning because it
contains mutable map fields. That is correct for safety, but it costs allocations
and CPU.

A request-shaped cache would avoid this scan by caching per-site runtime data.

### The cache does not cache file bytes or file descriptors

Even after a metadata cache hit, `serveBlob` still does:

```text
store.OpenBlob
  -> filepath.Clean
  -> path safety checks
  -> os.Open
http.ServeContent
  -> stat/seek/read/write behavior
```

This is the right baseline for correctness and memory usage, but it means the
metadata cache cannot remove filesystem cost.

### No HTTP cache headers

Uploaded files already have stable content identity through `FileSHA` and upload
version. The current serving path does not appear to use that to emit strong
HTTP caching semantics such as:

- `ETag`
- `Last-Modified`
- `Cache-Control`
- conditional request handling based on `If-None-Match` / `If-Modified-Since`

Without these headers, browsers, Traefik, and intermediate caches cannot cheaply
avoid repeated full 40 KB responses.

For immutable uploaded blobs, HTTP-level caching may deliver a larger win than an
in-process metadata cache.

### No cache metrics

The Otter builder was created without `CollectStats()`, and quack does not expose
hit/miss counters for the hot reader. Without those numbers, it is impossible to
tell whether production traffic is:

- hitting the cache
- missing due to TTL expiry
- missing due to invalidation churn
- missing due to key mismatch
- dominated by only one uncached part of the path

Performance tuning without hit-rate and timing data is mostly guessing.

### Possible cache invalidation churn

The current invalidation is intentionally coarse. For example, site updates evict:

```text
current_site_file:<site>:*
current_site_manifests
upload_settings:<siteSHA>:*
policy_violations:<siteSHA>:*
```

That is correct and simple, but if the site is being updated frequently, hot cache
entries can be evicted often. In that case the system may pay both the cache
overhead and the underlying source read cost.

### `ab` is measuring the whole ingress path

The benchmark goes through:

```text
client -> TLS -> Traefik ingress -> k3s service/network -> quack -> filesystem -> response
```

The raw Traefik 404 baseline around 7 ms is useful, but quack's benchmark includes
more work and a 40 KB response. The observed 36 ms mean `Waiting` value needs
request-stage timing from inside quack before attributing the cost to the hot
data cache.

## What is missing

### 1. Request-stage timing

Add low-cardinality timing around the serve path:

```text
server_settings_lookup_ms
runtime_lookup_ms
current_file_lookup_ms
blob_open_ms
serve_content_ms
total_handler_ms
```

This can initially be log-only and sampled. The goal is to answer:

- Is metadata lookup slow?
- Is blob open slow?
- Is response writing slow?
- Are slow requests clustered around cache misses?
- Is ingress adding most of the delay before quack sees the request?

### 2. Cache hit/miss counters

Expose at least:

```text
hot_cache_hits_total{method=...}
hot_cache_misses_total{method=...}
hot_cache_load_errors_total{method=...}
hot_cache_invalidations_total{kind=...}
hot_cache_size
```

If using Otter stats directly, create the cache with:

```go
otter.MustBuilder[string, any](capacity).
	CollectStats().
	WithVariableTTL().
	Build()
```

That still needs method-level counters, because one global hit ratio is not
enough. `FindCurrentSiteFile` and `ListCurrentSiteManifests` have very different
performance implications.

### 3. A per-site current snapshot cache

The next useful cache should be shaped around the serve path, not around database
methods.

Possible internal shape:

```go
type CurrentSiteSnapshot struct {
	Site    string
	SiteSHA string
	Version int64
	Runtime SiteRuntimeDecision
	Files   map[string]UploadFileRecord
}
```

With this, serving `GET /path` becomes:

```text
snapshot := cache.Get(site)
if snapshot.Runtime is suspended -> 403
file := snapshot.Files[relativePath]
if found -> serve blob
if not found and directory index applies -> check snapshot.Files[indexPath]
...
```

That collapses multiple cache lookups and a manifest scan into one cache lookup
and one or two map lookups.

Invalidation can remain coarse:

```text
FinishUpload      -> invalidate snapshot(site)
PublishSite       -> invalidate snapshot(site)
RollbackSite      -> invalidate snapshot(site)
UnpublishSite     -> invalidate snapshot(site)
DeleteSite        -> invalidate snapshot(site)
SavePolicy        -> invalidate all snapshots or runtime data
Policy violation  -> invalidate snapshot(site) or runtime data
```

This cache should not include blob bytes at first. Cache metadata and decisions
before caching content.

### 4. A direct per-site runtime lookup

If a full snapshot is too much for the next step, a smaller improvement is to
avoid `ListCurrentSiteManifests` on every served request.

Add a reader method like:

```go
CurrentSiteRuntime(ctx context.Context, site string) (SiteRuntimeDecision, bool, error)
```

or cache a per-site runtime record internally. The important point is avoiding
an all-sites list and scan for every request.

### 5. HTTP caching headers

Static uploaded content should be cacheable. Good candidates:

```text
ETag: "<file sha>"
Cache-Control: public, max-age=..., immutable
```

The right policy depends on whether URLs are immutable. If the same URL path can
change when a site is updated, then long `immutable` caching for bare paths like
`/` or `/app.js` can be wrong unless URLs are content-hashed. Safer initial
options:

- Emit `ETag` using `UploadFileRecord.FileSHA`.
- Support conditional GET returning `304 Not Modified`.
- Consider longer `Cache-Control` only for clearly content-addressed assets.

Even `304` support can significantly reduce bandwidth and response body cost for
repeat browser traffic.

### 6. Local and in-cluster benchmarks

Use separate benchmarks for:

```text
direct localhost -> quack
pod-to-service inside cluster
through Traefik HTTP
through Traefik HTTPS
```

This separates application time from ingress/TLS/network time.

For example:

```text
ab -n 3000 -c 20 -k http://127.0.0.1:<port>/
ab -n 3000 -c 20 -k http://<service>/
ab -n 3000 -c 20 -k https://<ingress>/
```

Pair those with internal request-stage logs so a slow external request can be
mapped to quack's own timing.

## Recommended next steps

1. Add instrumentation before changing cache design again.

   Measure metadata lookup, blob open, response write, total handler time, and
   cache hit/miss counts. This will identify whether the cache is the problem or
   merely not helping.

2. Keep Otter only if SQLite metadata reads are a measurable bottleneck.

   Otter is faster than the old memory cache in local microbenchmarks, but a
   cache hit still has nonzero overhead. If SQLite is not the bottleneck, the
   cache can look like a regression.

3. Replace method-shaped hot reads with a serve-shaped snapshot cache.

   The likely win is one lookup per site/path request, not several smaller
   cached reads.

4. Add `ETag` and conditional GET support.

   This is likely to matter more than metadata caching for repeated requests to
   40 KB static files.

5. Re-run the external `ab` benchmark only after collecting internal timing.

   Otherwise, ingress latency, file serving, and cache behavior remain mixed
   together.

## Bottom line

The current Otter cache is not obviously "slow" in isolation. In local
benchmarks, it is faster than the old hand-rolled memory cache and adds less than
one microsecond to a synthetic 40 KB serve path.

The current design is probably too low-level to deliver the performance win the
system needs. The missing piece is a request-shaped cache, plus observability to
prove whether latency is coming from metadata reads, blob access, response
writing, or the ingress path.
