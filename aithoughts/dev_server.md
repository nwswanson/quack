The key design choice is : **dev-server should be a repository/storage adapter over the build directory, not a fake deploy/upload path**.

## Recommended shape

Add:

```text
quack dev-server <build-dir> [site]
```

Default behavior:

```text
build directory
→ dev catalog / dev repository
→ normal route lookup
→ normal static/runtime/websocket handlers
→ dev storage opens files from build directory
```

Do **not** create upload records through upload APIs, do **not** copy into blob storage, and do **not** create version churn.

The repo already has good seams for this. Static serving resolves a site file, receives an `UploadFileRecord`, then calls `store.OpenBlob(file.BlobPath)` before `http.ServeContent`; that makes virtual `dev:<path>` blob paths a good fit.  The hot-data/source interfaces already expose the repository views dev-server needs: current manifests, current files, runtime routes, bundle files, policies, and server settings.  Public routing already composes static, runtime, route reader, and host resolver behind `publichttp.New(...)`. 

## New packages / files

I’d keep this out of the production DB/storage packages except for small shared helpers.

```text
cmd/quack/main.go
internal/devserver/
  command.go          # CLI parsing + run loop
  catalog.go          # filesystem scan + site.yaml translation
  repository.go       # hot-data/repository implementation
  storage.go          # dev: blob opener
  watcher.go          # fsnotify/poll watcher abstraction
  host.go             # dev host resolver
  tls.go              # optional local cert generation
  server.go           # composition root for public-only dev server
```

Potentially:

```text
internal/uploads/manifest_adapter.go
```

Move or duplicate the manifest-to-settings/runtime-route conversion helpers so dev-server can reuse them without pretending to upload. Today upload processing converts `site.yaml` into `ManifestSettings` and `RuntimeRoutesFromManifest`, including `BundleObjectKey` based on the uploaded file’s `BlobPath`. 

## Core data model

Use your proposed structure, but make it immutable per refresh:

```go
type DevSiteSource struct {
    Site       string
    SiteSHA    string
    Generation int64
    RootDir    string
    Manifest   manifest.Manifest
    Settings   map[string]string
    Files      map[string]domain.UploadFileRecord
    Routes     []appruntime.RouteMetadata
    LoadedAt   time.Time
}
```

Then wrap it in an atomic snapshot:

```go
type DevRepository struct {
    current atomic.Pointer[DevSiteSource]
    settings domain.ServerSettings
    policies []domain.PolicyRecord
}
```

Important: treat `Generation` as `Version`. Start at `1`, increment on every successful refresh. This keeps runtime route lookup and runtime bundle lookup consistent because the route metadata and bundle files both point at the same generation.

## Filesystem-backed storage

Add:

```go
type DevStorage struct {
    RootDir string
}

func (s DevStorage) OpenBlob(ctx context.Context, blobPath string) (*os.File, error)
```

Behavior:

```text
dev:index.html        → <build-dir>/index.html
dev:assets/app.js     → <build-dir>/assets/app.js
dev:server.star       → <build-dir>/server.star
```

Rules:

1. Only accept `dev:` paths.
2. Normalize slash paths.
3. Reject empty, absolute, `..`, symlink escapes if we decide to guard against them.
4. Open the real file from `RootDir`.
5. `AcceptFile`, `DeleteSite`, and `DeleteSiteVersion` should be no-ops or return “unsupported in dev mode.” Prefer unsupported unless some composed server interface requires them.

Every `UploadFileRecord` gets:

```go
RelativePath: "assets/app.js"
BlobPath:     "dev:assets/app.js"
FileSHA:      maybe hash, maybe weak dev hash
Bytes:        stat.Size()
```

For static files, `FileSHA` can be a fast dev ETag source. Options:

1. Accurate SHA-256 on refresh. Simple and closest to prod, but reads every file.
2. Dev weak hash from `mtime + size + path`. Faster.
3. Empty `FileSHA`. Static handler already tolerates no ETag.

I’d default to **mtime+size weak dev hash** and add `--hash-files` for prod-like testing.

## Dev catalog refresh

Refresh flow:

```text
watch change
→ debounce
→ read site.yaml
→ parse manifest
→ scan files
→ build UploadFileRecord map with dev: blob paths
→ build CurrentSiteManifest settings
→ build runtime RouteMetadata
→ atomically swap snapshot
→ log generation
```

This should happen on startup too. Startup should fail if the build dir is missing or `site.yaml` is invalid. After startup, refresh errors should keep the last good generation serving and log the error loudly.

### site.yaml translation

Use the existing manifest parser. It already models features, exclude, route kinds, static roots/files, HTTP routes, websocket routes, and filesystem roots.  The translation should produce:

```go
domain.CurrentSiteManifest{
    Site: site,
    SiteSHA: sites.HashName(site),
    Version: generation,
    Settings: uploads.ManifestSettings(manifest),
}
```

And runtime metadata roughly like `RuntimeRoutesFromManifest`, but with dev file records. The important difference is that an entrypoint maps to:

```go
BundleObjectKey: "dev:" + entrypointPath
```

not a blob storage key.

### Runtime bundle files

For `ListRuntimeBundleFiles(siteSHA, version)`, return the dev snapshot’s file list when `siteSHA` and `version` match. The runtime filesystem module reads files through blob keys, so dev mode can expose filesystem-backed app files without special runtime changes. The runtime repository interface already asks for both current runtime routes and runtime bundle files. 

## Repository methods to implement

Implement these directly on `DevRepository`:

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

Behavior:

| Method                     | Dev behavior                                                                  |
| -------------------------- | ----------------------------------------------------------------------------- |
| `GetServerSettings`        | return static dev settings                                                    |
| `LoadPolicies`             | return allow-all dev policies for runtime/http/ws/db, unless config overrides |
| `LoadUploadSettings`       | return current manifest settings if version matches                           |
| `ListCurrentSiteManifests` | one manifest for the dev site                                                 |
| `ListCurrentRuntimeRoutes` | current generation runtime routes                                             |
| `ListRuntimeRoutes`        | routes for matching generation                                                |
| `ListRuntimeBundleFiles`   | current files for matching generation                                         |
| `ListPolicyViolations`     | empty in dev by default                                                       |
| `FindCurrentSiteFile`      | map lookup by relative path                                                   |
| `ListCurrentSiteFiles`     | all current file records                                                      |

This gives `sites.NewSiteReadService`, `releases.NewService`, `runtime.NewService`, `runtimehttp.New`, and `statichttp.New` the same views they already expect. `server.New` currently wires DB, cache, uploads, releases, static, runtime, admin, control API, and public server together. For dev-server, I would not call `server.New` directly because it brings upload/admin/control composition that dev mode does not need; instead create a smaller dev composition root that mirrors only the public path. 

## Server composition

Build a public-only dev server:

```go
repo := devserver.NewRepository(...)
store := devserver.NewStorage(buildDir)

read := sites.NewSiteReadService(repo)
releaseService := releases.NewService(repo, repo)

staticHandler := statichttp.New(store, read)

executor, _ := runtime.NewStarlarkExecutor(
    runtime.ScriptLoaderFunc(func(ctx context.Context, key string) (io.ReadCloser, error) {
        return store.OpenBlob(ctx, key)
    }),
    runtime.ResourceLimits{},
)

runtimeService := runtime.NewService(runtime.ServiceOptions{
    Repository: repo,
    Policies: repo,
    Executor: executor,
    EnableExecution: true,
})

runtimeHandler := runtimehttp.New(runtimeService, runtimehttp.WithSettings(repo))

publichttp.New(
    staticHandler,
    publichttp.WithHostResolver(devHostResolver),
    publichttp.WithRoutes(publichttp.ReleaseRouteReader{
        Releases: releaseService,
        Policies: repo,
    }),
    publichttp.WithRuntime(runtimeHandler),
).Register(mux)
```

This keeps normal public routing, normal static serving, normal runtime HTTP, and normal websocket handling. Public routing already dispatches static, HTTP runtime, and websocket runtime route decisions through the same handler. 

## Host matching

I’d make dev host behavior intentionally looser than production.

Default:

```text
any Host header → configured dev site
```

That means all of these work:

```text
http://localhost:8080/
http://127.0.0.1:8080/
http://foo.test:8080/
```

Add stricter options:

```text
--site my-site
--host-match any       # default: any host maps to my-site
--host-match site      # use existing sites.NameFromHost behavior
--allowed-host host    # repeatable; blocks anything else
```

Why default to `any`: the current production host resolver derives the site from the first DNS label and can also block based on allowed hosts. That’s correct in prod, but inconvenient locally.  Route matching should still happen normally **after** the dev resolver chooses the site.

## Watching

Support three modes:

```text
--watch=fs       # default when fsnotify is available
--watch=poll     # portable fallback
--watch=off
```

Also:

```text
--watch-interval 500ms
--watch-debounce 100ms
--ignore node_modules
--ignore .git
--ignore .DS_Store
--ignore-from-site-yaml=true
```

### What to watch

Watch all files under the build directory, but apply ignores before indexing and before triggering refresh.

Ignore defaults:

```text
.git/
node_modules/
.DS_Store
*.swp
*.tmp
```

Then merge `site.yaml` `exclude` patterns. The manifest parser already normalizes excludes in the current codebase, so use those semantics for both dev indexing and dev watch filtering. 

### fsnotify vs mtime

Use fsnotify for fast feedback, but keep mtime polling as a first-class option because it’s more reliable on network filesystems, Docker bind mounts, WSL, and generated build directories.

Polling snapshot should track:

```go
path → {mtime, size, mode, maybe inode if available}
```

Do not hash contents for change detection by default.

Refresh debounce should coalesce bursts from build tools.

## SSL / websockets

WebSockets do not require TLS locally. The current runtime websocket handler uses normal HTTP upgrade semantics; it does not need special TLS behavior inside the app. So default should be plain HTTP:

```text
http://localhost:<port>
ws://localhost:<port>
```

Add TLS only for browser APIs that require secure contexts or for testing production-like websocket URLs:

```text
--https=off            # default
--https=self-signed    # generate local cert in state dir
--cert cert.pem --key key.pem
```

For `--https=self-signed`, generate a dummy cert for:

```text
localhost
127.0.0.1
::1
<site>.localhost
```

Print the HTTPS and WSS URLs. Do not try to install trust roots automatically.

## State / persistence

Separate three categories:

### 1. Source files

Always live in the build directory. Never copied.

### 2. Dev runtime memory

Use a dev state dir:

```text
.quack-dev/
  memory/
  certs/
  logs/
  config.json
```

Default:

```text
--state-dir <build-dir>/.quack-dev
```

But allow:

```text
--state-dir /tmp/quack-dev/<site>
--memory=off|snapshot
```

I’d default memory persistence to **off** unless the manifest or a flag requests snapshot mode. It avoids confusing “why did my local state survive?” behavior.

### 3. Generated certs / config

Keep these in `state-dir`, not the build artifact namespace, and add `.quack-dev/` to default ignores.

## Admin UI

Default: **no admin UI**.

Reason: dev-server should be automatic “yes” with large maximums, not an admin exercise. The admin UI is useful for production settings, users, publishing, rollback, and policies, but dev mode has no uploads, no versions, no publish/unpublish, and no real user model.

Add optional debug admin later:

```text
--admin
--admin-port 0
```

But for v1 I’d skip it and print a dev status endpoint instead:

```text
GET /__quack/dev
```

Return current generation, site, build dir, manifest parse status, file count, route count, and last refresh error.

## Dev settings / policy

Default server settings:

```go
MaxUploadBytes: very large or irrelevant
MaxUploadFiles: very large or irrelevant
MaxRetainedVersions: 0
MaxWebSocketConnections: high
MaxWebSocketConnectionsPerSite: high
DefaultSite: site
AllowedHosts: nil or configured
LogLevel: debug
MemoryPersistenceMode: off
```

Policies:

```text
runtime.http       allowed
runtime.websocket  allowed
database           allowed if needed
```

But make this configurable for prod-like tests:

```text
--policy=strict
--policy=dev
--config quack.dev.yaml
```

`--policy=dev` is default. `--policy=strict` uses normal policy defaults, which lets tests catch missing feature declarations.

## Logging

Do not “worm” random logging everywhere first. Start with focused dev-server logs at the boundaries:

```text
dev refresh started
dev refresh succeeded generation=N files=N routes=N duration=...
dev refresh failed error=...
dev open blob blobPath=... path=... error=...
dev route decision host=... site=... path=... kind=...
dev websocket connect/disconnect site=... route=...
```

Set normal log level to debug by default in dev mode, but keep most extra logs in `internal/devserver` and route composition wrappers. If deeper runtime logs become necessary, add structured debug logs near existing runtime route lookup and bundle loading later.

## Env vars

Support flags first, env vars second:

```text
QUACK_DEV_HOST
QUACK_DEV_PORT
QUACK_DEV_SITE
QUACK_DEV_WATCH
QUACK_DEV_WATCH_INTERVAL
QUACK_DEV_STATE_DIR
QUACK_DEV_HTTPS
QUACK_DEV_CERT
QUACK_DEV_KEY
QUACK_DEV_LOG_LEVEL
QUACK_DEV_HOST_MATCH
```

Do not reuse `UPLOAD_TOKEN`, `PUBLIC_ADDR`, or `ADMIN_ADDR` for CLI dev-server unless you intentionally want compatibility with `quack-server`. Keep the CLI dev-server namespace clear.

## Host address and port allocation

Defaults:

```text
--addr 127.0.0.1
--port 0
```

Using port `0` avoids collisions and lets the OS pick. Print the actual URL after `Listen` succeeds.

Also support:

```text
--host 0.0.0.0
--port 8080
--port-file .quack-dev/port
```

`--port-file` is useful for integration tests and external tooling.

Use one listener for public traffic. If admin/debug is later added, use a separate listener.

## Looser dev behavior

I’d make these dev-only:

1. All runtime capabilities allowed by default.
2. High request/response/script/websocket limits.
3. Any host maps to the dev site by default.
4. Last good generation keeps serving after a bad edit.
5. File hashes are weak/fast unless `--hash-files` is set.
6. Missing `site.yaml` can be allowed with `--default-manifest`, but default should require it if the product expects every build output to have one.
7. Disable HTTP caching aggressively. Static handler already sets `Cache-Control: no-cache` in the current version. 

## CLI plan

Extend `cmd/quack/main.go`:

```go
case "dev-server":
    resp, err = runDevServer(os.Args[2:])
    textOutput = true
```

But unlike other commands, this is long-running and should not JSON-encode a response. The CLI currently treats most commands as request/response and JSON-encodes non-text outputs, so `dev-server` should be a special case that owns stdout/stderr and blocks until interrupted. 

Suggested usage:

```text
quack dev-server <build-dir> [site]
  --host 127.0.0.1
  --port 0
  --watch fs|poll|off
  --watch-interval 500ms
  --watch-debounce 100ms
  --ignore <pattern>
  --host-match any|site
  --allowed-host <host>
  --https off|self-signed
  --cert <path>
  --key <path>
  --state-dir <path>
  --policy dev|strict
  --log-level debug|info|warn|error
  --hash-files
```

## Implementation phases

### Phase 1: Public static dev server

Implement:

```text
DevStorage
DevRepository
initial catalog scan
CurrentSiteManifest
ListCurrentSiteFiles
FindCurrentSiteFile
public-only server composition
CLI command
```

Acceptance:

```text
quack dev-server ./build
curl http://localhost:<port>/index.html
```

serves directly from `./build/index.html`, and editing the file is reflected after restart.

### Phase 2: site.yaml static routes

Implement manifest parsing and settings translation.

Acceptance:

```yaml
routes:
  - path: /assets
    kind: static
    root: public/assets
```

routes through normal route lookup and static handler.

### Phase 3: runtime HTTP and WebSocket routes

Implement runtime route metadata generation with `BundleObjectKey: dev:<entrypoint>` and `ListRuntimeBundleFiles`.

Acceptance:

```yaml
routes:
  - path: /api
    kind: http
    runtime: starlark
    entrypoint: server.star

  - path: /ws
    kind: websocket
    runtime: starlark
    entrypoint: ws.star
```

uses normal runtime handlers.

### Phase 4: watch and refresh

Add fsnotify and polling modes. Refresh by rebuilding the snapshot, not mutating maps in place.

Acceptance:

```text
edit site.yaml → routes change without restart
edit server.star → next request uses new file
delete file → route/file lookup reflects deletion
bad site.yaml → last good generation continues serving, dev status reports error
```

### Phase 5: dev ergonomics

Add:

```text
host-match options
state dir
memory persistence settings
dev status endpoint
TLS/self-signed cert
port-file
config file
```

### Phase 6: tests

Add tests for:

1. `DevStorage.OpenBlob` rejects escapes and opens `dev:` files.
2. Catalog scan builds virtual blob paths.
3. `site.yaml` static routes become current manifest settings.
4. Runtime HTTP route metadata points at `dev:<entrypoint>`.
5. Runtime bundle files are returned for the active generation.
6. Refresh swaps generations atomically.
7. Host resolver maps localhost/127.0.0.1/custom hosts as configured.
8. Poll watcher notices mtime and size changes.
9. Bad refresh preserves last good snapshot.
10. No upload APIs are called in dev-server tests.

## Main design risks

The biggest risk is accidentally coupling dev-server to the production `server.New` composition and dragging in DB/upload/admin semantics. Avoid that by giving dev-server its own public-only composition root.

The second risk is cache staleness. Either bypass the hot cache in dev mode or make refresh call the invalidator for site/version/runtime routes. Since `DevRepository` can serve atomically cloned snapshots cheaply, I’d skip `cache.NewOtterHotDataReader` in dev-server entirely.

The third risk is host confusion. Defaulting every host to the configured dev site is the least surprising local behavior; stricter production-like host matching can be opt-in.
