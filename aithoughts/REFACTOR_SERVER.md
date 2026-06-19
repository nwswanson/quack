Background:

A solid refactor here should be staged around import-cycle avoidance. The current biggest issue is that `server` owns almost every concept: HTTP handlers, auth/session types, upload/archive workflow, storage, settings/policy rules, cache readers, and persistence-facing record types. `sqlitedb` also imports `server`, so splitting handlers first would probably create messy cycles.

My recommended end state:

```text
internal/
  domain/              # shared records and core names
    users.go
    sites.go
    uploads.go
    settings.go
    policy.go
    runtime.go

  settings/            # setting registry + validation/parsing
    registry.go

  sites/               # site naming/path/routing decisions
    names.go
    paths.go
    read_service.go
    write_service.go
    runtime.go

  uploads/             # tar upload workflow, manifest validation, pruning
    service.go
    archive.go
    errors.go

  hotdata/             # cache/read-through layer
    reader.go
    memory.go
    otter.go

  storage/             # blob storage implementation
    storage.go
    blob.go

  server/              # HTTP composition only
    server.go
    router.go
    middleware.go
    handler.go

  serverapi/ or httpapi/
    auth.go
    upload_routes.go
    site_routes.go
    admin_api_routes.go

  adminui/
    routes.go
    templates.go
    templates/admin.html

  sqlitedb/
    sqlite.go
```

I would not do this all at once. I’d use this sequence.

1. Extract `domain` first

Move the pure data types out of `server/database.go`, `server/storage.go`, and `server/runtime.go`:

* `AdminUser`, `CreatedUser`
* `PublishedSite`
* `ServerSettings`
* `PolicyScope`, `PolicyRecord`, `PolicyViolation`
* `CurrentSiteManifest`
* `RevisionRecord`, `RollbackRecord`, `UnpublishRecord`, `PublishRecord`
* `UploadRecord`, `UploadState`, `UploadFileRecord`
* `SiteRuntimeStatus`, `SiteRuntimeDecision`
* `EffectiveValue`, `UploadPolicy`
* `ErrSiteOwnership`

Then update `sqlitedb` to import `internal/domain` instead of `internal/server`.

This is the unlock. After this, persistence no longer depends on the HTTP server package.

2. Shrink the giant `Database` interface

Right now `server.Database` is a god-interface. Split it by consumer:

```go
type UploadRepository interface { ... }
type SiteRepository interface { ... }
type UserRepository interface { ... }
type SessionRepository interface { ... }
type SettingsRepository interface { ... }
type PolicyRepository interface { ... }
```

Keep `sqlitedb.Database` implementing all of them naturally. Don’t force one central interface unless `server.New` still wants a convenience aggregate.

The key Go rule: define interfaces near the package that consumes them, not in `sqlitedb`.

3. Extract `settings`

Move from `server/settings.go`:

* setting constants
* `SettingDefinition`
* `SettingDefinitions`
* `ValidateSettingValue`
* `parseLogLevelName`
* `parseBoolSetting`

I’d make exported helpers where needed:

```go
settings.Default(key)
settings.Validate(key, value)
settings.ParseBool(value)
settings.ParseLogLevel(value)
```

This package will be used by `sqlitedb`, read/write services, admin UI, and logging.

4. Extract `storage`

Move `Storage`, `StoredFile`, `StoredFileResult`, and `BlobStorage` into `internal/storage`.

The storage package should probably not know about `UploadRecord`; only blob/file input/output. Upload metadata belongs in `domain`.

5. Extract `sites`

Move these out of `shared_handlers.go` and `site_read_service.go`:

* `siteFromHost`
* `canonicalSiteName`
* `requestedRelativePath`
* `shouldTryDirectoryIndex`
* `directoryRedirectPath`
* `siteAndPathFromServePath`
* `siteFromDeletePath`
* `siteFromSuffixedSitePath`
* `sha256Hex`, maybe as `sites.HashName`
* `SiteReadService`
* `SiteWriteService`
* runtime decision logic
* database feature policy resolution

This is the real domain/service layer for serving sites.

I’d rename a few things while moving:

```go
canonicalSiteName      -> sites.CanonicalName
siteFromHost           -> sites.NameFromHost
requestedRelativePath  -> sites.RequestedFilePath
resolveSiteFile        -> sites.ResolveFile
```

6. Extract `hotdata`

Move:

* `HotDataReader`
* `HotDataInvalidator`
* passthrough reader
* memory reader
* otter reader
* clone helpers

This package should depend on `domain` and maybe `sites` only if it caches `ServeSiteFileDecision`. If that creates an awkward cycle, keep file-serving decision caching in `sites` and make `hotdata` cache only raw repository reads.

7. Extract `uploads`

Move the upload workflow out of `shared_handlers.go`:

* `uploadArchive`
* `acceptArchive`
* `acceptArchiveEntry`
* `acceptRegularFile`
* `pruneRetainedVersions`
* `badArchiveError`
* `uploadLimitError`
* manifest settings conversion

The HTTP handler should become thin:

```go
func (h *handler) handleUploadArchive(w http.ResponseWriter, r *http.Request) {
    req := uploads.Request{
        Site: site,
        User: user,
        Body: r.Body,
        Policy: policy,
    }
    resp, err := h.uploads.UploadArchive(r.Context(), req)
    ...
}
```

This is probably the highest-value functional extraction, because upload currently mixes HTTP, tar parsing, storage, DB mutation, policy, pruning, logging, and response shaping.

8. Split HTTP by surface

Once the services are out, `server` can become small:

* `server.go`: `New`, dependency wiring
* `router.go`: admin-host routing
* `middleware.go`: request logging
* `handler.go`: shared handler struct
* `auth.go`: bearer/admin session lookup

Then move route groups:

* `adminui`: HTML login/settings/policy page routes
* `httpapi`: `/v1/...` JSON API routes
* `sitehttp`: public site serving routes

If you don’t want three HTTP packages yet, at least split files inside `server` after the domain/services are extracted. Package boundaries should come after the code stops sharing private helpers everywhere.

9. Keep `server.New` as composition root

`server.New` should still wire:

```go
source := hotdata.NewPassthrough(db)
hot := hotdata.NewMemory(source, ...)
read := sites.NewReadService(hot)
write := sites.NewWriteService(db, hot, hot)
uploads := uploads.NewService(db, store, read, write)
```

Then handlers receive interfaces/services, not raw `db` for everything.

The goal is that `server` becomes “transport and wiring,” not “business logic.”

Suggested migration order:

1. `domain`
2. repository interface split
3. `settings`
4. `storage`
5. `sites` path/name helpers
6. `SiteReadService` / `SiteWriteService`
7. `hotdata`
8. `uploads`
9. admin/API/site HTTP route packages
10. clean up tests and fake DBs

I’d expect steps 1–4 to be mostly mechanical. Steps 5–8 are where behavior can subtly change, so run the full test suite after each one.

The main thing I’d avoid is creating packages like `utils`, `common`, or `models`. This code has real boundaries already: domain records, settings/policy, site serving, uploads, hot-data caching, storage, HTTP surfaces, and SQLite. Lean into those.

------------

# `internal/server` Refactor Plan

Goal: turn `internal/server` from a dumping ground into a small HTTP/composition package, with domain types, services, storage, caching, settings, uploads, and persistence boundaries separated cleanly.

## Target shape

```text
internal/
  domain/
  settings/
  sites/
  uploads/
  hotdata/
  storage/
  server/
  serverapi/        # optional later split
  adminui/          # optional later split
  sqlitedb/
```

## Phase 1: Extract shared domain types

Purpose: break the dependency where `sqlitedb` imports `server` for core record types.

* [x] Create `internal/domain`.
* [x] Move user/admin types:

  * [x] `AdminUser`
  * [x] `CreatedUser`
* [x] Move site summary/revision types:

  * [x] `PublishedSite`
  * [x] `RevisionRecord`
  * [x] `RollbackRecord`
  * [x] `UnpublishRecord`
  * [x] `PublishRecord`
* [x] Move upload metadata types:

  * [x] `UploadRecord`
  * [x] `UploadState`
  * [x] `UploadFileRecord`
* [x] Move settings/policy records:

  * [x] `ServerSettings`
  * [x] `PolicyScope`
  * [x] `PolicyRecord`
  * [x] `PolicyViolation`
  * [x] `CurrentSiteManifest`
* [x] Move runtime/policy result types:

  * [x] `EffectiveValue[T]`
  * [x] `UploadPolicy`
  * [x] `SiteRuntimeStatus`
  * [x] `SiteRuntimeDecision`
* [x] Move shared errors:

  * [x] `ErrSiteOwnership`
* [x] Update `internal/sqlitedb` to import `internal/domain` instead of `internal/server`.
* [x] Update `internal/server` to import `internal/domain`.
* [x] Run tests.
* [x] Commit as a mechanical move.

## Phase 2: Split the giant `Database` interface

Purpose: replace one god-interface with narrower consumer-owned interfaces.

* [x] Identify current `Database` consumers:

  * [x] upload flow
  * [x] site read service
  * [x] site write service
  * [x] auth/session handling
  * [x] admin user management
  * [x] settings/policy management
  * [x] revision/publish/delete actions
* [x] Create smaller interfaces near their consumers:

  * [x] `UploadRepository`
  * [x] `SiteReadRepository`
  * [x] `SiteWriteRepository`
  * [x] `UserRepository`
  * [x] `SessionRepository`
  * [x] `SettingsRepository`
  * [x] `PolicyRepository`
* [x] Keep `sqlitedb.Database` implementing these naturally.
* [x] Avoid adding adapter layers unless needed.
* [x] Remove or shrink the central `server.Database` interface.
* [ ] Update fake test databases to implement only needed interfaces.
* [x] Run tests.

## Phase 3: Extract settings and policy definitions

Purpose: make setting validation reusable by SQLite, services, and admin UI without importing `server`.

* [x] Create `internal/settings`.
* [ ] Move setting-related types:

  * [x] `SettingType`
  * [x] `ScopeType`
  * [x] `PolicyKind`
  * [x] `SettingDefinition`
* [x] Move setting constants:

  * [x] `SettingMaxUploadBytes`
  * [x] `SettingMaxUploadFiles`
  * [x] `SettingMaxRetainedVersions`
  * [x] `SettingDefaultSite`
  * [x] `SettingLogLevel`
  * [x] `SettingDatabaseFeature`
  * [x] `SettingDatabaseFeatureRequired`
* [x] Move registry:

  * [x] `settingRegistry`
  * [x] `SettingDefinitions`
* [x] Export needed helpers:

  * [x] `Default(key string) string`
  * [x] `Validate(key, value string) error`
  * [x] `ParseBool(value string) bool`
  * [x] `ParseLogLevel(value string) string`
* [x] Update `sqlitedb` to use `settings.Validate`.
* [x] Update services/admin code to use `settings` package.
* [x] Run tests.

## Phase 4: Extract blob storage

Purpose: separate filesystem blob storage from HTTP/server logic.

* [x] Create `internal/storage`.
* [x] Move storage interface/types:

  * [x] `Storage`
  * [x] `StoredFile`
  * [x] `StoredFileResult`
* [x] Move implementation:

  * [x] `BlobStorage`
  * [x] `NewBlobStorage`
  * [x] `AcceptFile`
  * [x] `OpenBlob`
  * [x] `DeleteSite`
  * [x] `DeleteSiteVersion`
* [x] Keep upload metadata types in `domain`, not `storage`.
* [x] Update `server.New` and upload code to depend on `storage.Storage`.
* [x] Move `storage_test.go` into `internal/storage`.
* [x] Run tests.

## Phase 5: Extract site naming/path helpers

Purpose: isolate pure site-routing rules before moving services.

* [x] Create `internal/sites`.
* [x] Move site name helpers:

  * [x] `canonicalSiteName` → `sites.CanonicalName`
  * [x] `siteFromHost` → `sites.NameFromHost`
  * [x] `sha256Hex` → `sites.HashName`
* [x] Move serving path helpers:

  * [x] `requestedRelativePath` → `sites.RequestedRelativePath`
  * [x] `shouldTryDirectoryIndex`
  * [x] `directoryRedirectPath`
* [x] Move API path parsers:

  * [x] `siteAndPathFromServePath`
  * [x] `siteFromDeletePath`
  * [x] `siteFromSuffixedSitePath`
* [x] Move relevant tests from `handlers_test.go` into `internal/sites`.
* [x] Keep names exported only where actually needed.
* [x] Run tests.

## Phase 6: Extract site read service

Purpose: move serving decisions and runtime policy calculation out of HTTP handlers.

* [x] Move `SiteReadService` into `internal/sites`.
* [x] Move serving decision types:

  * [x] `ServeSiteFileStatus`
  * [x] `ServeSiteFileDecision`
* [x] Move read service implementation:

  * [x] `siteReadService`
  * [x] `NewSiteReadService`
  * [x] `ServerSettings`
  * [x] `UploadPolicy`
  * [x] `ValidateUploadManifest`
  * [x] `CurrentSiteRuntime`
  * [x] `CurrentSiteFile`
  * [x] `ServeSiteFile`
* [x] Move helper logic:

  * [x] `resolveSiteFile`
  * [x] `currentSiteRuntime`
  * [x] `SystemDatabasePolicy`
  * [x] `databaseAllowed`
  * [x] `runtimeDecisionFromViolations`
* [x] Replace direct `server` type references with `domain` and `settings`.
* [x] Move `site_read_service_test.go`.
* [x] Run tests.

## Phase 7: Extract site write service

Purpose: keep mutation/invalidation behavior out of HTTP and separate from SQLite.

* [x] Move `SiteWriteService` into `internal/sites`.
* [x] Move write service implementation:

  * [x] `siteWriteService`
  * [x] `NewSiteWriteService`
  * [x] `SaveServerSettings`
  * [x] `SavePolicy`
  * [x] `SaveUploadSettings`
  * [x] `FinishUpload`
  * [x] `RollbackSite`
  * [x] `UnpublishSite`
  * [x] `PublishSite`
  * [x] `DeleteSite`
  * [x] `ReconcilePolicyViolations`
* [x] Move narrow write repository interface with the service.
* [x] Keep hot-data invalidation abstract behind an interface.
* [x] Move `site_write_service_test.go`.
* [x] Run tests.

## Phase 8: Extract hot data caching

Purpose: make cache/read-through behavior independent of the HTTP server package.

* [x] Create `internal/hotdata`.
* [x] Move interfaces:

  * [x] `HotDataReader`
  * [x] `HotDataInvalidator`
  * [x] `MutableHotDataReader`
* [x] Move passthrough reader:

  * [x] `NewPassthroughHotDataReader`
  * [x] clone helpers
* [x] Move memory reader:

  * [x] `MemoryHotDataReaderOptions`
  * [x] `NewMemoryHotDataReader`
* [x] Move otter reader:

  * [x] `OtterHotDataReaderOptions`
  * [x] `NewOtterHotDataReader`
* [x] Decide whether `ServeSiteFileDecision` cache belongs in:

  * [x] `hotdata`, if no cycle is introduced
  * [ ] `sites`, if keeping serving decisions closer to domain logic is cleaner
* [x] Move hot-data tests and benchmarks.
* [x] Run tests.

## Phase 9: Extract upload service

Purpose: pull the tar/archive upload workflow out of HTTP handlers.

* [x] Create `internal/uploads`.
* [x] Define an upload service:

  * [x] `Service`
  * [x] `NewService`
  * [x] `UploadArchive`
* [ ] Define upload request/result types if protocol response should stay out of the service:

  * [x] `Request`
  * [ ] `UploadResult`
* [x] Move upload flow:

  * [x] `uploadArchive`
  * [x] `acceptArchive`
  * [x] `acceptArchiveEntry`
  * [x] `acceptRegularFile`
  * [x] `pruneRetainedVersions`
* [x] Move upload-specific errors:

  * [x] `badArchiveError`
  * [x] `uploadLimitError`
* [x] Move manifest helper:

  * [x] `ManifestSettings`
* [x] Keep HTTP response formatting in HTTP handlers.
* [x] Keep protocol tar/path validation in `internal/protocol`.
* [x] Update upload handler to call `uploads.Service`.
* [ ] Move upload-related handler tests or add dedicated upload service tests.
* [x] Run tests.

## Phase 10: Slim down `internal/server`

Purpose: leave `server` as transport composition and routing, not business logic.

* [x] Keep `server.New` as the composition root.
* [x] Keep/rename files around HTTP concerns only:

  * [x] `server.go`
  * [x] `router.go`
  * [x] `middleware.go`
  * [x] `handler.go`
  * [x] `auth.go`
* [x] Move request logger into `middleware.go`.
* [x] Move admin-host router into `router.go`.
* [x] Make handler dependencies explicit:

  * [x] token/auth config
  * [x] storage service
  * [x] upload service
  * [x] site read service
  * [x] site write service
  * [x] user/session repositories
* [x] Remove fallback constructors from handler methods if possible:

  * [x] `siteReadService()`
  * [x] `siteWriteService()`
* [x] Run tests.

## Phase 11: Optional HTTP package split

Purpose: separate public site serving, JSON API, and admin UI once core services are clean.

* [x] Create `internal/serverapi` or keep as `internal/server/api`.
* [x] Move JSON API routes:

  * [x] login check
  * [x] upload
  * [x] list sites
  * [x] default site settings
  * [x] delete site
  * [x] revisions
  * [x] rollback
  * [x] publish/unpublish
* [x] Create `internal/adminui`.
* [x] Move admin UI:

  * [x] HTML routes
  * [x] admin templates
  * [x] admin page data
  * [x] admin session cookie helpers
  * [x] same-origin checks
* [x] Create `internal/sitehttp` if useful.
* [x] Move public site serving handlers.
* [x] Keep `internal/server` wiring these route groups together.
* [x] Run tests.

## Phase 12: Test cleanup

Purpose: make tests reflect the new architecture instead of preserving old coupling.

* [ ] Split `handlers_test.go` into focused test files:

  * [ ] site name/path tests
  * [ ] public site serving tests
  * [ ] upload route tests
  * [ ] admin UI tests
  * [ ] API route tests
* [ ] Replace giant fake DBs with narrow fakes per package.
* [ ] Keep SQLite integration tests in `internal/sqlitedb`.
* [ ] Add service-level tests for uploads.
* [ ] Add package-level tests for settings validation.
* [ ] Run full test suite.
* [ ] Run race tests if feasible.

## Phase 13: Final cleanup

* [ ] Remove obsolete aliases and compatibility shims.
* [ ] Check for package names like `common`, `utils`, or `models`; avoid them.
* [ ] Check exported names and reduce anything that does not need to be public.
* [ ] Confirm package import direction is clean:

  * [ ] `sqlitedb` imports `domain`/`settings`, not `server`
  * [ ] `server` imports services, not the other way around
  * [ ] `uploads` does not import HTTP handlers
  * [ ] `sites` does not import HTTP handlers
  * [ ] `storage` does not import server/services
* [ ] Run `go test ./...`.
* [ ] Run formatting.
* [ ] Commit final cleanup.

## Desired dependency direction

```text
server
  -> adminui / serverapi / sitehttp
  -> uploads
  -> sites
  -> hotdata
  -> storage
  -> domain
  -> settings

sqlitedb
  -> domain
  -> settings

protocol
  -> no server dependency
```

## Main rule to preserve

`internal/server` should answer: “How do HTTP requests get routed and wired?”

It should not answer:

* how uploads are processed
* how site names are validated
* how runtime policy is resolved
* how cache invalidation works
* how blobs are stored
* how SQLite persists records
* what the core domain records are
