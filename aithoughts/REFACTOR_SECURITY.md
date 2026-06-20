I would **not** implement this as scattered CORS checks in `/api`, DB, websockets, and static serving. Make it a single **capability + origin permission system** that every surface asks before doing anything. The existing code already points in that direction: admin/public servers are separated, public serving is isolated in `sitehttp`, settings already have scoped definitions, policies already have system/user/site scopes, uploads already persist `site.yaml` feature settings, and runtime violations already degrade or suspend sites.    

## 1. Core security model

Use three separate concepts:

### A. Surface

A surface is the thing being exposed:

| Surface        | Examples                                 | Notes                                                                        |
| -------------- | ---------------------------------------- | ---------------------------------------------------------------------------- |
| `static`       | normal site files                        | mostly public, but hotlink/CORS controls can affect browser-readable use     |
| `api`          | `/api`, `/api/*` Starlark handler        | browser-callable scriptable app surface                                      |
| `database`     | injected Starlark `db.*` funcs           | must be checked inside the injected function, not only before running `/api` |
| `database.raw` | raw entity lookup/linking API            | more dangerous than normal app DB access; separate permission                |
| `websocket`    | `/ws`, `/api/ws`                         | Origin checked at upgrade and per-message capabilities retained              |
| `hotlink`      | cross-origin static embeds / asset reads | should be treated mainly as bandwidth/CORS policy, not strong data security  |

### B. Operation

Each surface has operations. Do not collapse read and write.

```text
api.read
api.write
database.read
database.write
database.raw_read
database.raw_write       # probably deny by default
websocket.connect
websocket.write
hotlink.read
static.read
```

This is how you get “API can be world, hotlink can inherit server default, database can be site-only.”

### C. Origin exposure mode

Use a small ordered set:

```text
deny
same_origin
same_site_domain
quack_parent_domain
authorized_origins
world
```

I would define them this way:

| Mode                  | Meaning                                                                                                                                                                                                                             |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `deny`                | No browser origin may use it. Server-side/admin calls still require explicit auth.                                                                                                                                                  |
| `same_origin`         | Exact scheme + host + port only. Safest default for write operations.                                                                                                                                                               |
| `same_site_domain`    | Same registrable domain, after verified domain ownership. For example `app.example.com` can call `api.example.com` only if the domain is verified for the site/account.                                                             |
| `quack_parent_domain` | For hosted Quack subdomains, if the site is served as `foo.bar.com`, allow `https://bar.com` and optionally configured sibling origins under `bar.com`. Do this only for configured Quack base domains, not arbitrary custom hosts. |
| `authorized_origins`  | Exact whitelist such as `https://app.customer.com`. Wildcards only as explicit `https://*.example.com`, never implicit.                                                                                                             |
| `world`               | Any origin. For credentialless public access only; never combine with cookies or ambient admin/session auth.                                                                                                                        |

The important rule: **CORS is not authorization.** The permission resolver should decide whether the request is allowed, then CORS headers merely expose that decision to browsers.

## 2. Effective policy resolution

Generalize the current database-only feature policy into a reusable resolver. Today, the registry has `features.database.enabled` and `features.database.required`, the manifest parser only understands the database feature, and the database schema already has `policies`, `upload_settings`, and `site_policy_violations`.   

Add definitions like:

```go
features.api.enabled
features.api.required
features.database.enabled
features.database.required
features.websocket.enabled
features.websocket.required

permissions.api.read
permissions.api.write
permissions.database.read
permissions.database.write
permissions.database.raw_read
permissions.database.raw_write
permissions.websocket.connect
permissions.websocket.write
permissions.hotlink.read

origins.authorized
origins.parent_domain.enabled
```

Policy resolution should be:

```text
server default
→ system policy ceiling
→ user policy ceiling
→ site policy ceiling
→ current upload/site.yaml request
→ request-time origin check
→ per-surface/per-operation capability
```

Use a “maximum exposure” lattice:

```text
deny < same_origin < same_site_domain < quack_parent_domain < authorized_origins < world
```

Admin policy sets the ceiling. A site can request something less restrictive only if the ceiling allows it. Example: if system says `database.write <= same_origin`, then a site requesting `database.write = world` becomes a runtime violation or deployment rejection.

For required features, keep the existing degrade/suspend pattern:

| Requested feature | Policy result | Required? | Runtime result             |
| ----------------- | ------------- | --------: | -------------------------- |
| allowed           | allowed       |    either | active                     |
| denied            | denied        |     false | degraded; feature disabled |
| denied            | denied        |      true | suspended                  |

That matches the existing database behavior where policy violations can degrade or suspend the current site. 

## 3. Request-time decision object

Every public request should produce a `RequestContext` like:

```go
type RequestContext struct {
    Site              string
    SiteSHA           string
    Host              string
    Origin            Origin
    OriginPresent     bool
    Surface           Surface
    Operation         Operation
    Actor             Actor        // anonymous, admin token, site runtime, etc.
    Capabilities      CapabilitySet
}
```

Then all dangerous operations ask the same question:

```go
decision := permissions.Check(ctx, PermissionRequest{
    Site: site,
    Surface: "database",
    Operation: "write",
    Origin: parsedOrigin,
    Host: r.Host,
    Actor: actor,
})
```

The Starlark runtime should not receive a raw Mongo client. It should receive guarded functions:

```python
db.get(...)
db.query(...)
db.insert(...)
db.update(...)
db.delete(...)
entity.lookup(...)
```

Each function checks the current request capabilities. That prevents the confused-deputy case where `/api` is world-callable but `database.write` is site-only. In that case, the world caller can hit the API handler, but `db.insert` fails unless the caller’s origin also satisfies `database.write`.

## 4. CORS behavior

Implement one CORS middleware for public dynamic surfaces:

```text
/api
/api/*
/ws preflight, if any
raw entity lookup routes
maybe hotlink/static asset CORS
```

Rules:

1. Normalize `Origin` strictly: scheme, host, port; lowercase host; strip trailing dot; reject `null` unless a future explicit sandbox mode needs it.
2. Add `Vary: Origin, Access-Control-Request-Method, Access-Control-Request-Headers`.
3. For `world`, use `Access-Control-Allow-Origin: *` only when the response is credentialless.
4. For anything else, reflect the exact allowed origin.
5. Do not set `Access-Control-Allow-Credentials` for public world APIs.
6. Only allow requested headers that the surface needs: probably `Content-Type`, maybe `Authorization` for token-authenticated API calls.
7. Treat OPTIONS preflight as a permission check for the intended method and headers, not as a blanket pass.
8. Keep admin UI separate. It already has same-origin POST protection using `Origin`/`Referer` and `SameSite=Lax` admin cookies, and that should not be merged with public CORS. 

For non-browser requests with no `Origin`, I would allow only:

```text
read operations if otherwise public
server-to-server operations with explicit bearer/admin/site token
same-origin-style browser writes only when Origin/Host proves it
```

Do not let “missing Origin” mean “same origin” for sensitive writes.

## 5. Host, site, and domain model

Current public routing derives the site from the first DNS label of the host, so `foo.example.com` maps to site `foo`. There is also path parsing support for `/serve/{site}/...`, which is useful for hotlink-style routes.  

For the new model, add a real host resolver:

```go
type SiteHostResolver interface {
    ResolveHost(ctx context.Context, host string) (ResolvedSiteHost, bool, error)
}
```

Back it with:

```sql
site_domains (
  host TEXT PRIMARY KEY,
  site_sha TEXT NOT NULL,
  site TEXT NOT NULL,
  kind TEXT NOT NULL,          -- quack_subdomain, custom_domain, alias
  verified_at TEXT,
  created_by_user_id INTEGER,
  parent_domain TEXT,
  allow_parent_origin INTEGER NOT NULL DEFAULT 0
)
```

Behavior:

| Host type              | Example                                                      | Allowed parent logic                                                     |
| ---------------------- | ------------------------------------------------------------ | ------------------------------------------------------------------------ |
| Quack base domain      | `foo.bar.com` where `bar.com` is configured as platform base | `quack_parent_domain` may allow `https://bar.com`                        |
| Verified custom domain | `api.customer.com`                                           | parent/sibling access only if customer verified ownership and enabled it |
| Unknown arbitrary host | anything else                                                | no parent-domain trust                                                   |

Use `golang.org/x/net/publicsuffix` or equivalent logic for registrable domain handling. Do not derive parent trust by simply chopping off the first label for every hostname.

## 6. Site manifest shape

Extend `site.yaml` from database-only features to all surfaces.

Example for the case you described:

```yaml
features:
  api:
    enabled: true
    required: true
  database:
    enabled: true
    required: true
  websockets:
    enabled: true
    required: false

permissions:
  api:
    read: world
    write: world
  database:
    read: same_origin
    write: same_origin
    raw_read: deny
    raw_write: deny
  websocket:
    connect: authorized_origins
    write: authorized_origins
  hotlink:
    read: inherit

origins:
  authorized:
    - https://app.example.com
    - https://admin.example.com
```

This should compile into upload settings, just like the current upload service converts manifest database flags into upload settings. 

Important: `site.yaml` is a request, not absolute authority. The effective permission is capped by admin/system policy.

## 7. Code implementation plan

### Phase 1: General permission package

Add `internal/permissions`.

Core types:

```go
type Surface string
type Operation string
type ExposureMode string

const (
    ModeDeny              ExposureMode = "deny"
    ModeSameOrigin        ExposureMode = "same_origin"
    ModeSameSiteDomain    ExposureMode = "same_site_domain"
    ModeQuackParentDomain ExposureMode = "quack_parent_domain"
    ModeAuthorizedOrigins ExposureMode = "authorized_origins"
    ModeWorld             ExposureMode = "world"
)
```

Add:

```go
ParseOrigin(header string) (Origin, bool, error)
NormalizeOrigin(...)
Check(...)
CheckCORSPreflight(...)
ApplyCORSHeaders(...)
```

Unit-test the lattice and origin matching heavily.

### Phase 2: Generalize settings and policies

Extend `internal/settings/registry.go` with enum/list types. The current registry already has `AllowedScopes`, `SiteEditable`, `AdminEditable`, and `PolicyKind`; expand that rather than inventing separate config code. 

Add:

```go
SettingTypeOriginList
SettingTypeExposureMode
PolicyKindExposureCeiling
```

Keep policy storage in the existing `policies` table initially. If origin lists get too awkward in `value`, add:

```sql
origin_allowlists (
  scope_type TEXT NOT NULL,
  scope_id TEXT NOT NULL,
  surface TEXT NOT NULL,
  origin TEXT NOT NULL,
  created_by_user_id INTEGER,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY(scope_type, scope_id, surface, origin)
)
```

### Phase 3: Extend domain/read/write services

Add effective permission methods beside the existing site runtime and policy methods:

```go
EffectiveSitePermissions(ctx, actor, site) (domain.EffectivePermissions, error)
CheckSitePermission(ctx, req domain.PermissionRequest) (domain.PermissionDecision, error)
SystemPolicyMatrix(ctx) (...)
SitePolicyMatrix(ctx, site) (...)
```

Refactor the current database-only `DatabaseAllowed` path into this generic resolver. The old function can become a compatibility wrapper around:

```go
CheckSitePermission(surface=database, operation=read/write)
```

### Phase 4: Public route registration

Right now public only registers static site serving. Add dynamic routes before the catch-all static handler:

```go
publicMux.HandleFunc("/api", appHandler.handleAPI)
publicMux.HandleFunc("/api/", appHandler.handleAPI)
publicMux.HandleFunc("/ws", wsHandler.handleWS)
publicMux.HandleFunc("/serve/", hotlinkHandler.handleServePath)
sitehttp.New(store, read).Register(publicMux)
```

The static handler should remain the fallback. The current public/admin split is good; keep `/v1` management APIs on admin, and put site runtime `/api` on public. 

### Phase 5: Starlark runtime

Add `internal/siteruntime` or `internal/starlarkapp`.

Recommended layout:

```text
internal/siteruntime/
  handler.go
  starlark.go
  cache.go
  request.go
  response.go
  db.go
  limits.go
```

Load one of:

```text
/api.star
/app.star
quack.star
```

or make it explicit in `site.yaml`:

```yaml
runtime:
  handler: api.star
```

Runtime rules:

* Cache compiled Starlark by `site_sha + version + file_sha`.
* Execute with context deadline.
* No filesystem, network, environment, or process access.
* Limit request body size separately from static upload size.
* Limit response size.
* Limit CPU/steps if the Starlark library supports interruption; otherwise run with strict timeouts and avoid unbounded builtins.
* Inject only safe objects: `request`, `response`, `json`, `db`, `ws`, maybe `log`.
* Treat all script errors as `500` with sanitized output to users and detailed server logs.

### Phase 6: Database injection

Use Mongo behind a narrow interface:

```go
type SiteDatabase interface {
    Get(ctx, collection, id string) (Value, error)
    Query(ctx, collection string, q Query, opts QueryOptions) ([]Value, error)
    Insert(ctx, collection string, doc Value) (ID, error)
    Update(ctx, collection, id string, patch Patch) error
    Delete(ctx, collection, id string) error
}
```

Do not expose raw Mongo filters at first. For raw entity lookup, create a separate API with its own permission key:

```text
database.raw_read
database.raw_write
```

Default both to `deny`.

Every DB call must know:

```go
site
request origin
surface that initiated it
operation
current script identity
```

Then enforce:

```text
api.write allows the request to run a mutating API route
database.write allows this request to mutate DB
database.raw_* allows raw lookup/linking
```

That gives you the requested composition:

```text
api.write = world
hotlink.read = inherit/server
database.write = same_origin
```

A world-origin caller can hit `/api`, but cannot cause `db.insert` unless database permission also allows that origin.

### Phase 7: WebSockets

For websockets, check permissions before upgrade:

```text
surface = websocket
operation = connect
```

After upgrade:

* store the permission decision on the connection,
* cap message size,
* cap connections per site/IP/origin,
* idle timeout + ping/pong,
* rate limit messages,
* check `websocket.write` and DB capabilities on each message handler,
* close with policy-specific close codes when denied.

Do not rely on browser CORS for websockets; use the `Origin` header in the upgrade request.

### Phase 8: Hotlinking

Separate two things:

1. **CORS-readable static assets**: whether another origin’s JS can read the asset.
2. **Embeddable assets**: whether another site can load image/script/font URLs.

Browsers can hotlink images without CORS, and `Referer` can be absent, so hotlink policy is not a strong secret-protection mechanism. Use it for bandwidth/embed control, not data security.

Recommended keys:

```text
permissions.hotlink.read
permissions.static.cors_read
permissions.static.embed
permissions.static.frame
```

Keep `frame` separate so “hotlink assets” does not accidentally mean “allow this site to be framed anywhere.”

## 8. Admin UI plan

Replace the current single database policy section with a policy matrix. The admin UI already lists sites, runtime status, and policy reason, and has server settings and policy editing hooks. 

Add:

### Server policy page

Rows:

```text
api.read
api.write
database.read
database.write
database.raw_read
database.raw_write
websocket.connect
hotlink.read
```

Columns:

```text
default
maximum allowed exposure
forced value?
site editable?
reason
```

### Site detail page

For each site:

```text
current upload requested value
effective value
admin ceiling
violation status
required?
reason
```

Add buttons:

```text
Allow world API reads
Allow world API writes
Restrict database to same origin
Add authorized origin
Verify custom domain
Enable parent-domain trust
Suspend feature
Suspend site
```

### Origin/domain management

Add UI for:

```text
Authorized origins
Verified domains
Quack parent-domain behavior
Custom domain verification status
```

Display normalized origins exactly as stored.

### Audit log

Add a `policy_audit_log` table or append-only records:

```text
who changed what
old value
new value
scope
site
time
reason
```

Policy changes are security-sensitive enough to audit.

## 9. CLI plan

Existing CLI has login, deploy, sites, revisions, rollback, publish/unpublish, default-site, delete. Extend it rather than making a separate admin tool. 

Commands:

```bash
quack policy get [--site foo]
quack policy set --site foo api.write world
quack policy set --site foo database.write same_origin
quack policy set --system hotlink.read world
quack policy unset --site foo api.write

quack origins list foo
quack origins add foo https://app.example.com
quack origins remove foo https://app.example.com

quack domains list foo
quack domains add foo api.example.com
quack domains verify foo api.example.com

quack features get foo
quack features set foo api.enabled true
```

For your example:

```bash
quack policy set --site foo api.read world
quack policy set --site foo api.write world
quack policy set --site foo database.read same_origin
quack policy set --site foo database.write same_origin
quack policy set --site foo hotlink.read inherit
```

Add equivalent `/v1` management endpoints on the admin server, using the existing bearer-token/admin-user model. The current server API already authenticates bearer tokens and supports admin/user access checks, so extend that pattern. 

## 10. Defaults I would ship

Safe defaults:

```text
api.read = same_origin
api.write = same_origin
database.read = same_origin
database.write = same_origin
database.raw_read = deny
database.raw_write = deny
websocket.connect = same_origin
websocket.write = same_origin
hotlink.read = server_default
static.cors_read = same_origin
```

Server default for hotlink can be:

```text
hotlink.read = world
```

because static public assets are already public in practice, but I would still keep CORS-readable static access separate from embedding.

## 11. Concrete example: API world, hotlink server-level, database site-only

Effective configuration:

```yaml
features:
  api:
    enabled: true
    required: true
  database:
    enabled: true
    required: true

permissions:
  api:
    read: world
    write: world
  database:
    read: same_origin
    write: same_origin
    raw_read: deny
    raw_write: deny
  hotlink:
    read: inherit
```

Result:

| Request                                                                                      |                    API allowed? |                           DB write allowed? |
| -------------------------------------------------------------------------------------------- | ------------------------------: | ------------------------------------------: |
| `https://foo.bar.com` calls `https://foo.bar.com/api`                                        |                             yes |                                         yes |
| `https://bar.com` calls `https://foo.bar.com/api`, with `quack_parent_domain` enabled for DB |                             yes |                                         yes |
| `https://bar.com` calls it, but DB is `same_origin`                                          |                             yes |                                          no |
| `https://evil.com` calls `/api`                                                              |                             yes |                                          no |
| server-side bearer/admin call                                                                |         depends on token policy | depends on explicit server/admin capability |
| image tag on another site loads static asset                                                 | depends on hotlink/embed policy |                                         n/a |

The big invariant: **permission must be checked at the point of effect**. Running `/api` is one effect. Writing database state is another. Upgrading a websocket is another. Serving a CORS-readable static asset is another. Each one gets its own decision.
