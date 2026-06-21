# Routing

This document explains how Quack chooses what to do for a public request. It is
written for future engineers who need to change routing behavior without having
to rediscover the full request path.

The short version:

1. The public HTTP server derives a site name from the request host.
2. The release route service looks at the current release for that site.
3. Route declarations from `site.yml` and executable runtime metadata are merged.
4. The longest matching route prefix wins.
5. Static routes go through blob-backed static serving.
6. HTTP and WebSocket runtime routes go through runtime dispatch, with policy
   checks before and during invocation.

Most routing decisions are metadata decisions. Blob contents are opened only
after a request has already been routed to static serving or runtime execution.

## Main Packages

The main packages involved in public routing are:

- `internal/server`: composition root. Wires storage, database, cache, services,
  static handler, runtime handler, and public router.
- `internal/publichttp`: public request router. Converts HTTP requests into
  route decisions and dispatches to static or runtime handlers.
- `internal/releases`: current-release route lookup. Merges route declarations
  and runtime metadata, then chooses the best route.
- `internal/sites`: host/path helpers and static file resolution.
- `internal/statichttp`: static HTTP response handling around blob storage.
- `internal/runtimehttp`: HTTP adapter for runtime invocation.
- `internal/runtime`: runtime route lookup, policy checks, limits, and executor
  invocation.
- `internal/uploads`: upload-time parsing of `site.yml`, persisted upload
  settings, and runtime route metadata.
- `internal/cache`: hot data reader wrappers used by the public path.
- `internal/sqlitedb`: durable source for current releases, settings, files,
  runtime routes, and policies.

## Production Wiring

`internal/server.New` is the composition root.

The current wiring is:

```text
SQLite database
  -> cache.NewPassthroughHotDataReader
  -> cache.NewOtterHotDataReader
  -> sites.NewSiteReadService
  -> statichttp.New

SQLite database + hot reader
  -> releases.NewService
  -> publichttp.ReleaseRouteReader

blob storage + starlark executor
  -> runtime.NewService
  -> runtimehttp.New

static handler + release route reader + runtime handler
  -> publichttp.New
  -> public mux "/"
```

The default production hot reader is Otter-backed. The old memory-backed reader
still exists and follows the same interface, but is not wired by default.

## Host To Site

The public mux sends every public path to `publichttp.Handler.handlePublicRequest`.
The first routing step is `sites.NameFromHost(r.Host)`.

`NameFromHost` does this:

1. Trim whitespace and lowercase.
2. Remove a port if one is present.
3. Trim trailing or leading dots.
4. Drop a leading `www.`.
5. Keep only the first DNS label before the next dot.
6. Validate that label with `CanonicalName`.

Examples:

```text
foo.example.com       -> foo
www.foo.example.com   -> foo
foo:8080              -> foo
foo.                  -> foo
```

Invalid or reserved names return no site and the public handler returns `404`.
`CanonicalName` currently rejects empty names, names longer than 63 characters,
names with dots, leading or trailing hyphens, non-lowercase DNS-label
characters, and the reserved names `v1` and `serve`.

Important consequence: routing is host-centric. The path does not normally
contain the site name. `/serve/...` is not a public escape hatch for picking a
site; on the public surface it is just another path for whatever site the host
resolved to.

## Upload-Time Manifest Parsing

`site.yml` or `site.yaml` is parsed during upload by `internal/uploads`.

The archive reader treats either `site.yml` or `site.yaml` at archive root as
the site manifest. It is not stored as a regular served file. All other regular
files are sanitized and stored as upload files.

The manifest schema currently relevant to routing is:

```yaml
routes:
  - path: /
    kind: static
    root: public

  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    methods: [GET, POST]
    filesystem:
      root: data
```

`manifest.Parse` uses `yaml.Decoder.KnownFields(true)`, so unknown fields fail
the upload. Keep this in mind when adding new YAML fields: they must be added to
`internal/manifest.Manifest` before users can deploy them.

### Static route targets

`routes[].root` is optional on static routes. The empty value means uploaded
files are served from the upload root, which is the historical behavior.

When set, `root` is a relative archive path. It is normalized by
`manifest.SanitizeStaticRoot`:

- backslashes become slashes
- leading and trailing slashes are trimmed after absolute paths are rejected
- empty and `.` become the empty root
- absolute paths are rejected
- any `..` component is rejected
- path components are sanitized like uploaded serving paths

Static routes can also use `file` for exact file aliases:

```yaml
routes:
  - path: /favicon.ico
    kind: static
    file: media/favicon.ico
```

`file` is a relative archive file path sanitized like uploaded serving paths.
It matches only the exact route path. `GET /favicon.ico` serves
`media/favicon.ico`; `GET /favicon.ico/details` does not match that file route.

`root` and `file` are only valid on static routes, and a route cannot set both.
HTTP and WebSocket routes cannot set either static target. Top-level
`static.root` is no longer part of the manifest schema; uploads that declare
`static:` fail unknown-field validation.

Use route-level static roots instead:

```yaml
routes:
  - path: /
    kind: static
    root: public
```

At upload time, the sanitized value is persisted in the `routes` JSON upload
setting with the route declaration.

At serve time, static resolution validates stored route roots and legacy
`static.root` values from older releases again before using them. This makes
malformed stored rows fail closed instead of accidentally serving from the wrong
subtree.

### `routes`

Each route has:

- `path`: required
- `kind`: optional, defaults to `static` when route settings are interpreted
- `root`: optional static-route archive subtree to expose at this route path
- `file`: optional static-route archive file to serve for an exact route path
- `runtime`: currently only `starlark`, and only on `http` routes
- `entrypoint`: required when `runtime` is set
- `methods`: optional list; empty means all methods are allowed at the routing
  layer

Upload validation allows `static`, `http`, and `websocket` route kinds. Only
`http` routes can currently name a runtime. WebSocket metadata can be persisted,
but the runtime execution path currently invokes HTTP-style runtime handling.

## Upload-Time Persistence

Upload uses two persistence paths for route-related data.

First, `uploads.ManifestSettings` stores manifest-derived settings in
`upload_settings`:

- `features.database.enabled`
- `features.database.required`
- `routes` as JSON, when the manifest declared routes

Second, `uploads.RuntimeRoutesFromManifest` stores executable dynamic route
metadata in `runtime_routes` when a route kind is `http` or `websocket`.

For a Starlark HTTP route:

1. The `entrypoint` path is sanitized.
2. The entrypoint file must exist in the uploaded files.
3. The route stores the entrypoint blob path as `BundleObjectKey`.
4. Runtime limits are initialized from runtime defaults.
5. Required capabilities are stored with the route metadata.

Static route declarations do not create `runtime_routes` rows. They live only in
the JSON `routes` upload setting.

## Current Release Read Models

Public routing is based on current release metadata.

`sqlitedb.ListCurrentSiteManifests` reads sites with `current_version > 0` and
then loads upload settings for each current version. This query does not filter
on `live_state`; later file and current-runtime-route queries enforce
`live_state = 'live'`. It returns
`domain.CurrentSiteManifest`:

```go
type CurrentSiteManifest struct {
    Site     string
    SiteSHA  string
    Version  int64
    Settings map[string]string
}
```

The settings map is where public routing sees `routes` and the legacy
`static.root` fallback.

`sqlitedb.ListCurrentRuntimeRoutes` reads `runtime_routes` joined to the current
live site version. It only returns runtime rows for finished uploads and live
current sites. `sqlitedb.ListCurrentSiteFiles` and `FindCurrentSiteFile` also
require `live_state = 'live'`, so unpublished sites do not serve static blobs.

## Public Route Lookup

After the host becomes a site name, `publichttp.Handler.decide` asks its route
reader:

```go
decision, ok, err := h.routes.LookupRoute(r, site, r.URL.Path)
```

In production, the route reader is `publichttp.ReleaseRouteReader`, backed by
`releases.Service`.

If no route reader is configured, the public handler falls back to:

```text
site=<host-derived-site>, kind=static, path=<request path>
```

With the production route reader, `releases.Service.LookupRoute` does this:

1. Load current site manifests.
2. Find the manifest whose `Site` equals the host-derived site.
3. Decode the JSON `routes` setting into declared routes.
4. Load current runtime routes.
5. Filter runtime metadata to the current site SHA and version.
6. Append executable runtime route decisions to the declared routes.
7. Choose the longest matching route prefix.
8. If no route matches, return a static route for the request path.
9. If no current site manifest exists for that site, also return a static route.

The "return a static route" behavior is intentional: static serving is still the
component that decides whether the site exists, whether default-site fallback
applies, and whether a file exists.

One subtlety: declared routes from the `routes` upload setting are appended
before executable runtime metadata. If both sources define the same path, the
longest-prefix chooser keeps the first matching route because ties are not
replaced. In practice, an uploaded Starlark route can produce both:

```text
settings route: kind=http path=/api
runtime route:  kind=http path=/api methods=[GET] limits=...
```

Public routing may choose the settings route and therefore have no public-layer
method list or resource limits. Runtime invocation still looks up the current
runtime metadata by site/version/path and enforces runtime kind, methods,
capabilities, and limits before executing. If you want method checks and limits
to be visible at the public route decision layer, change
`releases.chooseRoute` or `routesFromSettings` deliberately and add tests for
duplicate declared/runtime route paths.

## Route Matching Rules

Route paths are normalized by `releases.cleanRoutePath`:

- prepend `/` if needed
- `path.Clean`
- `.` becomes `/`

A route matches when:

- route path is `/`, which matches everything
- request path equals the route path
- request path starts with `routePath + "/"`, after trimming trailing slash from
  the route path

Static routes with `file` are the exception: they match only when the request
path equals the route path.

When several routes match, the longest route path wins.

Examples:

```yaml
routes:
  - path: /
    kind: static
  - path: /api
    kind: http
  - path: /api/admin
    kind: http
```

```text
/                 -> / static route
/about            -> / static route
/api              -> /api http route
/api/users        -> /api http route
/api/admin        -> /api/admin http route
/api/admin/users  -> /api/admin http route
```

The chosen route decision keeps `Path` as the original request path, not the
matched route prefix. Runtime lookup later repeats the route-prefix selection
against runtime metadata using the request path and current version.

## Static Versus Runtime Dispatch

`publichttp.handlePublicRequest` dispatches based on route kind:

- `static`: only `GET` and `HEAD` are allowed. It calls
  `statichttp.Handler.ServeSiteFile`.
- `http`: checks the request method against the route decision's method list,
  then calls `runtimehttp.Handler.ServeHTTPRoute`. Some route decisions have an
  empty method list because they came from the manifest `routes` setting rather
  than runtime metadata; runtime invocation performs the authoritative method
  check later.
- `websocket`: currently calls `runtimehttp.Handler.ServeHTTPRoute` with the
  same invocation request shape. The lower runtime layer only matches HTTP route
  metadata today, so this is not a complete WebSocket execution path yet.
- unknown: returns `404`.

Dynamic HTTP routes are denied before dispatch unless policy allows
`features.runtime.http.enabled` for system/site scope. This is a route-level
gate in `publichttp.ReleaseRouteReader`.

The runtime service repeats capability checks at invocation time. Do not remove
that second gate: it protects against stale cached route decisions and direct
runtime-service use.

## Static File Resolution

Static dispatch calls:

```go
sites.SiteReadService.ServeSiteFile(ctx, site, urlPath)
```

That loads server settings first so it can pass the configured default site to
`ResolveSiteFile`.

`ResolveSiteFile` does this:

1. Load current site manifest and serving status.
2. If the site is suspended by policy, return a suspended decision.
3. Load all current site files.
4. If the site does not exist and a different default site is configured, rerun
   resolution for the default site with the same URL path.
5. Build an in-memory map from uploaded file relative path to upload file record.
6. Convert the request path into a route-relative URL-facing path.
7. Validate and apply the selected static route root, or legacy `static.root`
   when no route root was selected.
8. Look for the exact file under the static root.
9. If not found and directory-index rules apply, look for
   `<static-root>/<relativePath>/index.html`.
10. Return one of the static serving decisions.

### URL Path To Relative Path

`sites.RequestedRelativePath` converts the URL path to a sanitized relative
serving path.

Examples:

```text
/                 -> index.html, wantsIndex=true
/index.html       -> index.html, wantsIndex=true
/docs/            -> docs/index.html
/docs             -> docs
/assets/app.js    -> assets/app.js
```

The returned relative path is URL-facing. It does not include the static route
root or legacy `static.root`.

### Static Root Application

Static root is applied after URL path normalization:

```go
staticPath := path.Join(staticRoot, relativePath)
```

If the selected static route has `root: public`, then:

```text
request /              looks for public/index.html
request /index.html    looks for public/index.html
request /docs/         looks for public/docs/index.html
request /app.css       looks for public/app.css
request /public/app.css looks for public/public/app.css
```

That last example is important. The static root is not a public URL prefix; it
is the archive subtree that becomes the public URL root. Files above it are not
reachable through an alternate URL.

### Static File Application

Static `file` routes do not apply directory-index behavior and do not strip a
path prefix. The route path is a public alias for one stored archive file:

```yaml
routes:
  - path: /favicon.ico
    kind: static
    file: media/favicon.ico
```

```text
request /favicon.ico         looks for media/favicon.ico
request /favicon.ico/details does not match this file route
```

### Directory Indexes And Redirects

The static resolver follows Nginx-style directory behavior:

- `/docs/` looks directly for `docs/index.html`.
- `/docs` first looks for an exact file named `docs`.
- If no exact file exists, and `docs/index.html` exists, the resolver returns a
  directory redirect decision.
- `statichttp` turns that decision into `301 /docs/`, preserving the query
  string.

When a static route root is set, the lookup path includes the root, but the
redirect does not. For `root: public`, `/docs` redirects to `/docs/`, not
`/public/docs/`.

### Empty Index

If the request wanted an index (`/`, `/index.html`, or another URL that resolves
to an index) and no file exists, the resolver returns `ServeSiteFileEmptyIndex`.
`statichttp` responds with `200 text/html` and an empty body. Non-index missing
paths return `404`.

This is existing behavior and should be considered before changing how missing
root pages behave.

### Serving The Blob

When a file is found, `statichttp` opens the file's `BlobPath` through storage
and calls:

```go
http.ServeContent(w, r, relativePath, time.Time{}, blob)
```

The `relativePath` passed to `ServeContent` is URL-facing and excludes
the static route root or legacy `static.root`. That keeps content-type behavior
aligned with the public URL path rather than the archive-internal storage path.

Blob bytes and file descriptors are not cached by the hot data cache.

## Default Site Fallback

Default-site fallback is owned by static file resolution, not public route
lookup.

`SiteReadService.ServeSiteFile` loads `server_settings.default_site` and passes
it to `ResolveSiteFile`. If the requested site has no current files and the
default site is configured, static resolution retries with the default site.

Important constraints:

- Fallback happens only when the requested site does not exist.
- Fallback does not happen for a missing file on an existing site.
- The same URL path is used against the default site.
- The default site's own static route root applies, with legacy `static.root`
  as fallback.
- Dynamic route lookup does not currently fallback to the default site. If an
  unknown host requests `/api`, release lookup will return a static route for
  the unknown site; static serving may then fallback to default-site static
  content.

If you want runtime routes to participate in default-site fallback, that is a
behavior change in `releases.LookupRoute` and `publichttp.ReleaseRouteReader`,
not in static serving.

## Serving Status And Policy Suspension

Static serving checks current serving status before file lookup.

`currentSiteManifestAndServingStatus` loads current manifests, finds the site,
then loads unresolved policy violations for that site SHA and version.

Currently, serving status only reacts to database feature policy violations:

- no relevant violation: active
- degraded database violation: active static serving with degraded status in the
  decision
- suspended database violation: static serving returns `403`

Runtime HTTP has a separate policy gate:

- Upload validation rejects HTTP runtime route declarations unless
  `features.runtime.http.enabled` is allowed.
- Public route lookup denies HTTP runtime dispatch if current policy denies it.
- Runtime invocation checks required route capabilities again.

## Runtime Invocation

For an HTTP runtime route, public dispatch builds:

```go
runtime.InvocationRequest{
    Site:    decision.Site,
    Version: decision.Version,
    Route:   decision.Path,
    Method:  r.Method,
    Query:   r.URL.RawQuery,
    Limits:  decision.ResourceLimits,
}
```

`runtimehttp` then:

1. Applies a request body limit.
2. Reads the request body into memory.
3. Filters request headers. Sensitive hop-by-hop and identity headers such as
   `Authorization`, `Cookie`, `X-Forwarded-*`, and `X-Real-Ip` are stripped.
4. Calls `runtime.Service.InvokeHTTP`.
5. Filters response headers.
6. Writes the status and body.

`runtime.Service.InvokeHTTP` then:

1. Looks up the best current runtime route for the request site, version, and
   path.
2. Validates runtime kind and HTTP method.
3. Checks required capabilities.
4. Applies route limits with service defaults as fallback. This is the
   authoritative runtime limit application; public route decisions may carry
   zero-value limits when the winning route came from the manifest settings
   record.
5. Enforces global runtime concurrency.
6. Executes with a timeout.
7. Validates response size and status code.
8. Records invocation metrics.

Runtime route lookup in `internal/runtime/routing.go` repeats longest-prefix
matching over `ListCurrentRuntimeRoutes`. It only matches `RouteHTTP` metadata.

For Starlark routes, the executor receives a bundle whose route entrypoint is
the stored blob object key, not the original source path. The script loader
opens that blob through storage.

### Starlark Bundle Filesystem

Starlark receives a read-only `fs` module only when its route opts in with a
`filesystem` block:

```yaml
routes:
  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    filesystem:
      root: data
```

`filesystem.root` is relative to the tarball root. Empty, `.`, and `/` enable
the whole uploaded tarball as the Starlark filesystem root. A non-empty value
such as `data` exposes only that subtree, rebased so Starlark reads
`fs.read("profile.txt")` for the uploaded file `data/profile.txt`.

Starlark paths may start with `/`; leading slashes are normalized away. `..`
traversal is rejected. The module never exposes host filesystem paths.

```python
fs.exists(path)      # bool; true for files or virtual directories
fs.read(path)        # string file contents
fs.read_bytes(path)  # bytes file contents
fs.listdir(path)     # sorted immediate child names
fs.stat(path)        # dict: path, type, size, and sha256 for files
```

Missing files, reading directories, listing files, and oversized reads fail the
Starlark invocation. File reads are currently bounded by the route
`max_script_bytes` limit.

## Caching

The public path uses `cache.HotDataReader` instead of calling SQLite directly.

The interface is application-shaped:

```go
GetServerSettings
LoadPolicies
LoadUploadSettings
ListCurrentSiteManifests
ListCurrentRuntimeRoutes
ListRuntimeRoutes
ListRuntimeBundleFiles
ListPolicyViolations
FindCurrentSiteFile
ListCurrentSiteFiles
```

The default concrete cache is `otterHotDataReader`.

### Cache Keys

Current key shapes:

```text
server_settings
policies:<scope-key>
upload_settings:<siteSHA>:<version>
current_site_manifests
current_runtime_routes
runtime_routes:<siteSHA>:<version>
policy_violations:<siteSHA>:<version>
current_site_file:<site>:<relativePath>
current_site_files:<site>
```

Static root serving currently uses `ListCurrentSiteFiles(site)`, because it
needs to map URL-facing paths through the selected static route root or legacy
`static.root` before choosing a blob. That means a static request can cache the
file list for the current site rather than one file lookup per URL path.

### TTL And Negative Caching

Both Otter and memory caches default to:

- positive TTL: 5 seconds
- negative TTL: 1 second

Negative TTL is used for missing file lookups and missing site file-list lookups.
Errors are not cached.

Both caches use singleflight-style call coalescing so concurrent misses for the
same key share one source load.

### Mutation Safety

Cached maps and slices are cloned before storing and before returning. This is
important because settings maps, current manifest slices, policy slices, and
runtime route slices are mutable Go values.

If you add a cached method returning a map or slice, add clone helpers and tests.

### Invalidation

The hot reader also implements invalidation:

```text
InvalidateServerSettings:
  server_settings

InvalidateSite(site):
  current_site_file:<site>:
  current_site_files:<site>
  current_site_manifests
  current_runtime_routes

InvalidateSiteVersion(siteSHA, version):
  upload_settings:<siteSHA>:
  runtime_routes:<siteSHA>:
  policy_violations:<siteSHA>:
  current_site_manifests
  current_runtime_routes

InvalidatePolicies:
  policies:
  policy_violations:
```

Writers call invalidation through `sites.SiteWriteService` and release service
operations. Upload finalization invalidates the site and version. Upload setting
writes invalidate the version. Publish, rollback, unpublish, and delete
invalidate site-level views.

Policy invalidation does not currently clear `current_runtime_routes`, because
runtime route metadata itself does not encode current policy decisions. The
policy gate calls `LoadPolicies`, so clearing policy keys is enough for current
policy results.

## End-To-End Examples

### Static Site With Public Root

Archive:

```text
site.yml
public/index.html
public/docs/index.html
scripts/build.sh
data/source.json
```

Manifest:

```yaml
routes:
  - path: /
    kind: static
    root: public
```

Requests for `foo.example.com`:

```text
/              -> static route -> public/index.html
/index.html    -> static route -> public/index.html
/docs          -> static route -> redirect /docs/ if public/docs/index.html exists
/docs/         -> static route -> public/docs/index.html
/scripts/build.sh -> static route -> public/scripts/build.sh, usually 404
/public/index.html -> static route -> public/public/index.html, usually 404
```

`scripts/build.sh` and `data/source.json` above `public` are not served.

### Static Plus Runtime API

Manifest:

```yaml
routes:
  - path: /
    kind: static
    root: public
  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    methods: [GET, POST]
```

Request behavior:

```text
/              -> route "/" static -> public/index.html
/assets/app.js -> route "/" static -> public/assets/app.js
/api           -> route "/api" http -> Starlark runtime
/api/users     -> route "/api" http -> Starlark runtime
/apiary        -> route "/" static, because "/api" does not match "/apiary"
```

The runtime entrypoint `api/app.star` can live outside the static route root. It
is not served as a static file unless the static root includes it and a URL maps
to it.

## Where To Change Behavior

Use these entry points when changing routing behavior:

- Host parsing or reserved public host labels: `internal/sites/paths.go`.
- YAML schema and validation: `internal/manifest/manifest.go`.
- How manifest fields become persisted settings: `internal/uploads/service.go`.
- How runtime route metadata is derived from YAML: `uploads.RuntimeRoutesFromManifest`.
- Static-versus-runtime route selection: `internal/releases/routes.go`.
- Public method gates and dispatch behavior: `internal/publichttp/routes.go`.
- Static root, directory index, default-site fallback, empty index behavior:
  `internal/sites/read_service.go` and `internal/sites/paths.go`.
- HTTP response behavior for static files: `internal/statichttp/routes.go`.
- Runtime request/response HTTP adaptation: `internal/runtimehttp/handler.go`.
- Runtime route lookup, capability checks, limits, and executor invocation:
  `internal/runtime`.
- Cache TTLs, keys, cloning, and invalidation: `internal/cache`.
- Current release, upload settings, current files, and runtime route queries:
  `internal/sqlitedb`.

## Test Areas To Update

Routing behavior is covered across several packages. When changing routing,
prefer focused unit tests plus one integration test that exercises the public
handler.

Useful test files:

- `internal/sites/paths_test.go`: host and path helper behavior.
- `internal/sites/read_service_test.go`: static file resolution, static root,
  default-site fallback, serving status.
- `internal/releases/service_test.go`: route declaration and longest-prefix
  route lookup.
- `internal/publichttp/routes_test.go`: public dispatch decisions and policy
  gates.
- `internal/statichttp/routes_test.go`: static HTTP responses.
- `internal/runtimehttp/handler_test.go`: runtime HTTP adapter behavior.
- `internal/runtime/runtime_test.go`: runtime lookup, limits, capabilities, and
  executor behavior.
- `internal/server/public_site_integration_test.go`: end-to-end public routing.
- `internal/cache/*_test.go`: cache cloning, invalidation, and negative caching.

For behavior that crosses package boundaries, add or update an integration test
in `internal/server/public_site_integration_test.go`. For example, static-root
behavior should prove that `/` maps to `public/index.html` and that
`/public/index.html` is not an alternate way to access the same file.

## Current Limitations And Sharp Edges

- Route lookup scans the full current manifest list and current runtime route
  list. The cache makes these reads cheap, but the cache shape is still
  metadata-method-shaped rather than request-shaped.
- Static serving builds a per-request map of all current site files after
  loading the cached file list.
- Blob bytes and open file handles are not cached.
- `routes` JSON from upload settings is decoded with errors ignored in
  `routesFromSettings`. Malformed stored settings fail soft by producing fewer
  declared routes.
- Declared route decisions can tie with runtime metadata for the same path. The
  settings route currently wins the public-layer tie, so runtime metadata is the
  authoritative source for runtime methods and limits.
- Runtime route metadata errors from SQLite scan fail the route lookup.
- WebSocket route execution is not complete; public dispatch has a WebSocket
  route kind, but runtime lookup currently matches HTTP route metadata.
- Dynamic default-site fallback is not implemented.
- Static serving returns an empty `200 text/html` for a missing requested index.
  This is historical behavior; change it deliberately and with tests if the
  product expectation changes.

## Mental Model

Think of routing as three layers:

```text
Host layer:
  Host header -> site name

Release layer:
  current release metadata -> static/http/websocket decision

Serving layer:
  static decision -> blob-backed file lookup under static route root
  runtime decision -> executable metadata lookup + policy + executor
```

Keeping those layers separate is the main design constraint. A host change
should not need to understand blobs. A route matching change should not need to
open files. A runtime execution change should not make static serving import the
runtime package. When adding new behavior, preserve those boundaries unless you
are intentionally redesigning the routing architecture.
