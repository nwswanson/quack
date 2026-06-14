
Overall: the app is already pointed in a good direction. The package split is sensible for a small Go service: `cmd/quack`, `cmd/quack-server`, `internal/client`, `internal/protocol`, `internal/server`, `internal/sqlitedb`. The biggest architectural issue is not SQLite. It is that `internal/server` has become the place where almost every concern meets: HTTP routing, admin UI, auth, upload orchestration, policy resolution, domain records, persistence interfaces, storage coordination, and response mapping.

My recommendations, in priority order:

1. Add an application service layer

Right now handlers do real workflows, especially upload:

`HTTP request -> auth -> policy -> tar parsing -> blob writes -> DB begin/finish/fail -> prune -> user-site linking -> response`

That should move behind service methods like:

```go
type DeploymentService struct {
    DB      DeploymentStore
    Storage Storage
    Policy  PolicyResolver
}

func (s *DeploymentService) PublishArchive(ctx context.Context, actor Principal, site string, archive io.Reader) (PublishResult, error)
func (s *DeploymentService) DeleteSite(ctx context.Context, actor Principal, site string) (DeleteResult, error)
func (s *DeploymentService) RollbackSite(ctx context.Context, actor Principal, site string) (RollbackResult, error)
```

Then HTTP handlers mostly become request parsing, auth extraction, service call, response encoding. This makes the core app testable without HTTP and gives you one place to enforce invariants.

2. Split the giant `Database` interface

`server.Database` currently covers uploads, users, sessions, settings, policies, manifests, policy violations, revisions, ownership, and lifecycle. That will keep growing.

I would split by use case, not by table:

```go
type AuthStore interface { ... }
type DeploymentStore interface { ... }
type SettingsStore interface { ... }
type PolicyStore interface { ... }
type AdminStore interface { ... }
```

Keep one concrete SQLite implementation if you want. The goal is not database portability. The goal is making dependencies explicit and keeping workflows from accidentally depending on the whole world.

3. Treat SQLite as the coordination engine

Since SQLite is purpose built here, lean into it. The DB should be the source of truth for state transitions, cleanup work, and recovery.

The current upload flow can leave filesystem/DB divergence in a few places:

* blobs can be written before `FinishUpload` succeeds
* failed uploads may leave blob directories behind
* prune metadata can succeed while blob deletion fails
* delete metadata can succeed while filesystem deletion fails

I would add explicit durable cleanup state, for example:

* `uploads.state = uploading|finished|error|abandoned`
* `blob_gc_jobs`
* `pending_site_deletes`
* `pending_version_deletes`

Then a small reconciler deletes physical blobs based on DB truth. This is much better than trying to make SQLite and the filesystem behave like one atomic transaction.

4. Introduce versioned migrations

The current `CREATE TABLE IF NOT EXISTS` plus `ALTER TABLE ... duplicate column name` style is fine early, but it gets brittle.

Add a `schema_migrations` table and embedded ordered migrations. Still simple, still SQLite-native. This matters once you have user data in the wild.

Also add/check indexes for the actual read paths:

* `sites(site)` because serving looks up by site name
* `uploads(site_sha, state, version)`
* `uploads(publisher_user_id)`
* `user_sessions(token_hash, expires_at)`
* maybe `site_policy_violations(site_sha, upload_version, resolved_at)`

5. Make the single-node model explicit

SQLite plus local blob storage strongly implies one active server process per data directory. That is totally reasonable, but encode it as architecture:

* process lock around the database/root directory
* clear startup error if another server owns it
* documented backup/restore story
* periodic WAL checkpoint policy if needed
* admin-visible health info: DB path, storage root, WAL mode, pending cleanup jobs

Do not accidentally drift toward a horizontally scalable design unless you actually want distributed storage and coordination.

6. Separate “admin user” from “API principal”

`AdminUser` is currently doing double duty: web admin sessions, API tokens, ownership, and permissions. I’d introduce a neutral domain type:

```go
type Principal struct {
    ID       int64
    Username string
    Role     Role
    Scopes   []string
}
```

Then admin sessions and API tokens both authenticate into a `Principal`. This will keep permissions cleaner as you add per-site tokens, deploy-only users, read-only users, service accounts, etc.

7. Finish the settings/policy registry idea

The `settingRegistry` is a good architectural seed. But today there is duplication between `ServerSettings`, form parsing, policy handling, validation, defaults, and resolver behavior.

I’d make the registry the source of truth for:

* default values
* allowed scopes
* editable surfaces
* validation
* policy behavior
* UI rendering metadata, eventually

This prevents each new setting from requiring edits across handlers, SQLite parsing, policy resolver, admin template, and tests.

8. Keep protocol separate, but add a real error envelope

`internal/protocol` is a good boundary between CLI and server. I would add a shared error response instead of reusing upload-shaped responses in generic error paths:

```go
type ErrorResponse struct {
    OK    bool   `json:"ok"`
    Error string `json:"error"`
    Code  string `json:"code,omitempty"`
}
```

The CLI can still render friendly messages, but the API contract becomes more consistent.

9. Loosen storage from filesystem details

`Storage.OpenBlob` returns `*os.File`, which ties the interface to local disk. SQLite can remain intentional while storage may still evolve.

Prefer an interface such as:

```go
type BlobReader interface {
    io.Reader
    io.Seeker
    io.Closer
}
```

Then `http.ServeContent` still works, but storage is not forced to expose `os.File`.

10. Suggested eventual package shape

I would evolve toward this gradually:

```text
cmd/quack
cmd/quack-server

internal/protocol
internal/client

internal/httpserver      // routes, handlers, templates, cookies
internal/app             // DeploymentService, AdminService, PolicyService
internal/domain          // UploadRecord, Principal, Policy, Settings, errors
internal/sqlitedb        // SQLite implementation of app stores
internal/blobfs          // filesystem blob storage
```

You do not need to do this all at once. First move upload/delete/rollback workflows into services inside the existing `server` package. Once the boundaries are clear, splitting packages becomes mechanical.

The short version: keep SQLite, keep the simple Go style, but move business workflows out of handlers, make DB state transitions more explicit, and use SQLite as the durable coordinator between metadata and blob storage. That will buy you the most architectural headroom without turning this into enterprise soup.
