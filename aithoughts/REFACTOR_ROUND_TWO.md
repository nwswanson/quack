# Quack Architecture Refactor Implementation Plan

## Goal

Prepare the codebase for long-term maintainability before adding user-uploaded scripts, dynamic API responses, socket handlers, and other runtime features.

The main architectural goal is to separate these product concepts:

* Publishing creates releases.
* Releases describe what is live.
* Policy decides what a release is allowed to do.
* Public HTTP routes live traffic.
* Static HTTP serves blob-backed files.
* Runtime executes user code.
* Control API manages deployment and administration.

This plan should preserve current behavior while making the future runtime work fit naturally into the codebase.

---

# Phase 0: Safety Net and Baseline

## Objective

Lock down current behavior before changing package boundaries.

## Tasks

* [ ] Run the full test suite and capture current passing state.
* [ ] Add or confirm integration coverage for:

  * [ ] CLI deploy flow.
  * [ ] Upload archive flow.
  * [ ] Public static file serving.
  * [ ] Default site routing.
  * [ ] Publish, unpublish, rollback, and delete flows.
  * [ ] Admin settings updates.
  * [ ] Policy violation reconciliation.
* [ ] Add tests proving public routes do not expose control API routes.
* [ ] Add tests proving admin/control API routes do not depend on public host routing.
* [ ] Add a package dependency snapshot using `go list` or a simple architecture test.
* [ ] Document the intended top-level layers:

  * [ ] Transport.
  * [ ] Application services.
  * [ ] Domain concepts.
  * [ ] Infrastructure.
  * [ ] Composition root.

## Acceptance Criteria

* [ ] All existing tests pass.
* [ ] Refactor can proceed without relying on manual behavior checks.
* [ ] There is clear test coverage around the current user-visible flows.
* [ ] The team agrees that public data-plane behavior and control-plane behavior are separate concerns.

---

# Phase 1: Rename Control Plane API

## Objective

Avoid future ambiguity between Quack’s management API and user-defined dynamic API endpoints.

Current package:

```text
internal/serverapi
```

Target package:

```text
internal/controlapi
```

Alternative acceptable names:

```text
internal/managementapi
internal/deployapi
```

Recommended name:

```text
internal/controlapi
```

## Tasks

* [ ] Rename `internal/serverapi` to `internal/controlapi`.
* [ ] Update package declarations.
* [ ] Update imports.
* [ ] Update tests.
* [ ] Update any comments or docs referring to this package as the general server API.
* [ ] Keep route paths unchanged.
* [ ] Keep response payloads unchanged.
* [ ] Verify CLI behavior is unchanged.
* [ ] Verify integration tests are unchanged except package/import names.

## Acceptance Criteria

* [ ] No behavior changes.
* [ ] All existing `/v1` management routes still work.
* [ ] It is clear that this package owns deployment and management APIs, not future user-generated APIs.

---

# Phase 2: Rename Current “Runtime” Status Concepts

## Objective

Reserve the word `runtime` for actual script/code execution.

Today’s runtime terminology appears to describe whether a static site is active, degraded, suspended, or affected by policy. That is a serving or policy status, not a code runtime.

## Recommended Naming

Replace concepts like:

```text
SiteRuntimeStatus
SiteRuntimeDecision
CurrentSiteRuntime
RuntimeStatus
```

With names like:

```text
SiteServingStatus
SiteAvailability
SitePolicyStatus
SiteServingDecision
CurrentSiteServingStatus
```

Recommended choice:

```text
SiteServingStatus
SiteServingDecision
CurrentSiteServingStatus
```

## Tasks

* [ ] Rename domain types related to static-site runtime status.
* [ ] Rename read service methods that return current runtime decisions.
* [ ] Rename admin UI fields that display runtime status.
* [ ] Rename API or CLI fields only if they are not externally committed.
* [ ] If API fields are externally committed, keep wire field names stable and only rename internal structs.
* [ ] Update tests.
* [ ] Add comments explaining that this status is about serving eligibility, not script execution.

## Acceptance Criteria

* [ ] The codebase no longer uses `runtime` to describe static-site availability.
* [ ] Future `internal/runtime` package can exist without awkward naming collisions.
* [ ] External API compatibility is preserved unless intentionally versioned.

---

# Phase 3: Split Public Serving From Static File Serving

## Objective

Prepare the public request path to route between static files, future dynamic HTTP handlers, and future socket handlers.

Current conceptual flow:

```text
sitehttp -> resolve site -> serve static file
```

Target conceptual flow:

```text
publichttp -> resolve site/release/route
           -> statichttp for static files
           -> runtimehttp for dynamic handlers
           -> runtimehttp/socket for websocket routes
```

## Proposed Packages

```text
internal/publichttp
internal/statichttp
```

## Tasks

* [ ] Create `internal/publichttp`.
* [ ] Move public host/site routing responsibility into `publichttp`.
* [ ] Create `internal/statichttp`.
* [ ] Move blob-backed file response behavior into `statichttp`.
* [ ] Keep existing HTTP behavior unchanged:

  * [ ] Static file serving.
  * [ ] Directory/index handling.
  * [ ] Missing file handling.
  * [ ] Suspended/degraded site handling.
  * [ ] Default site behavior.
* [ ] Introduce a route decision type, even if it initially only supports static files.
* [ ] Avoid adding runtime behavior in this phase.
* [ ] Update `server.New` composition to register public routes through `publichttp`.
* [ ] Update integration tests.

## Initial Route Decision Shape

```go
type RouteKind string

const (
    RouteStatic RouteKind = "static"
)

type PublicRouteDecision struct {
    Site    string
    Version int64
    Kind    RouteKind
    Path    string
}
```

This can evolve later into:

```go
const (
    RouteStatic    RouteKind = "static"
    RouteHTTP      RouteKind = "http"
    RouteWebSocket RouteKind = "websocket"
)
```

## Acceptance Criteria

* [ ] Static serving behavior is unchanged.
* [ ] Public request routing is no longer synonymous with static file serving.
* [ ] There is an obvious place to add dynamic HTTP and socket routing later.
* [ ] `statichttp` does not know about future runtime execution.
* [ ] `publichttp` does not directly know about blob storage internals beyond calling the static handler/service.

---

# Phase 4: Extract Publishing and Release Concepts

## Objective

Separate deployment/publishing lifecycle from generic site operations.

Today, upload, manifest validation, current version changes, publish/unpublish, rollback, pruning, and policy reconciliation are closely tied through `sites` and `uploads`.

Target concepts:

```text
publishing creates releases
releases stores and exposes release metadata
sites identifies logical sites
```

## Proposed Packages

```text
internal/publishing
internal/releases
internal/sites
```

## Responsibility Split

### `sites`

Owns:

* [ ] Site name validation.
* [ ] Site identity.
* [ ] Host/default-site mapping helpers, if applicable.

Does not own:

* [ ] Upload processing.
* [ ] Archive ingestion.
* [ ] Runtime execution.
* [ ] Policy evaluation.
* [ ] Static file serving decisions.

### `publishing`

Owns:

* [ ] Deploy archive use case.
* [ ] Begin upload.
* [ ] Add uploaded files.
* [ ] Parse manifest.
* [ ] Validate capability requests.
* [ ] Finalize release.
* [ ] Prune old releases.
* [ ] Invalidate relevant caches.

### `releases`

Owns:

* [ ] Current release lookup.
* [ ] Release metadata.
* [ ] Revision history.
* [ ] Publish.
* [ ] Unpublish.
* [ ] Rollback.
* [ ] Delete release/site records as appropriate.

## Tasks

* [ ] Create `internal/releases`.
* [ ] Move revision/current-version read models into `releases`.
* [ ] Create `internal/publishing`.
* [ ] Move archive upload orchestration from `uploads` into `publishing`, or make `uploads` a lower-level helper used by `publishing`.
* [ ] Reduce `internal/sites` to site identity/path/name concerns.
* [ ] Update control API handlers to depend on `publishing.Service` and `releases.Service`.
* [ ] Update admin UI to depend on read models from `releases` or a query service.
* [ ] Keep database persistence in `sqlitedb`.
* [ ] Keep blob persistence in `storage`.
* [ ] Update tests package by package.

## Acceptance Criteria

* [ ] Deploying a static site still works.
* [ ] Revisions still list correctly.
* [ ] Publish, unpublish, rollback, and delete still work.
* [ ] `sites` is no longer a catch-all application service.
* [ ] There is a clear release abstraction that future runtime bundles can attach to.

---

# Phase 5: Extract Manifest Parsing

## Objective

Make the site manifest the declaration point for static assets, future dynamic routes, and requested capabilities.

Current manifest behavior should remain supported, but the package should not live inside a broad protocol bucket long term.

## Proposed Package

```text
internal/manifest
```

## Initial Responsibilities

* [ ] Parse manifest files.
* [ ] Strictly validate manifest fields.
* [ ] Normalize feature declarations.
* [ ] Convert manifest declarations into capability requests.
* [ ] Convert manifest declarations into route declarations.

## Initial Manifest Domain Shape

```go
type Manifest struct {
    Features Features
    Routes   []Route
}

type Features struct {
    Database FeatureFlag
}

type FeatureFlag struct {
    Enabled  bool
    Required bool
}

type Route struct {
    Path       string
    Kind       RouteKind
    Entrypoint string
}
```

Runtime routes can be added later without changing the control API boundary.

## Tasks

* [ ] Create `internal/manifest`.
* [ ] Move manifest parsing out of `protocol`.
* [ ] Keep existing manifest syntax backward compatible.
* [ ] Add tests for:

  * [ ] Empty manifest.
  * [ ] Database feature enabled.
  * [ ] Database feature required.
  * [ ] Unknown fields.
  * [ ] Invalid route declarations, if route syntax is introduced now.
* [ ] Update publishing flow to use `manifest.Parse`.
* [ ] Keep wire-level protocol request/response types in `protocol`.

## Acceptance Criteria

* [ ] `protocol` no longer owns product manifest semantics.
* [ ] Manifest parsing is independently testable.
* [ ] Future route declarations have a natural home.
* [ ] Existing uploads remain compatible.

---

# Phase 6: Extract Policy and Capability Evaluation

## Objective

Move from one-off database feature policy to a generic capability model that can support runtime permissions.

Future runtime features will need policy decisions for:

* Dynamic HTTP handlers.
* WebSockets.
* Database access.
* Network access.
* Environment variables.
* Secrets.
* File writes.
* CPU and memory limits.
* Request duration.
* Concurrent connections.

## Proposed Package

```text
internal/policy
```

## Core Types

```go
type CapabilityRequest struct {
    Key      string
    Required bool
    Value    string
}

type Evaluation struct {
    Allowed    bool
    Violations []Violation
}

type Violation struct {
    Key      string
    Reason   string
    Required bool
}
```

## Tasks

* [ ] Create `internal/policy`.
* [ ] Move policy resolution out of `sites`.
* [ ] Convert current database feature policy into a capability:

  * [ ] `database`.
  * [ ] or `capability.database`.
* [ ] Preserve existing admin UI behavior for database feature policy.
* [ ] Preserve existing upload validation behavior.
* [ ] Preserve existing policy violation reconciliation behavior.
* [ ] Add capability evaluation tests.
* [ ] Add tests for required versus optional capabilities.
* [ ] Add tests for system-level policy inheritance.
* [ ] Add tests for future site-level or release-level overrides, even if not implemented yet.
* [ ] Keep persistent policy records in `domain` or move them into `policy` if they are not broadly shared.

## Acceptance Criteria

* [ ] Policy evaluation is not tied to static sites.
* [ ] Database access is represented as one capability among many.
* [ ] Publishing can ask policy whether a release is allowed.
* [ ] Public serving can ask policy whether a release is currently servable.
* [ ] Runtime can later ask policy whether an invocation is allowed.

---

# Phase 7: Reduce `hotdata` to Cached Reads, Not Final Decisions

## Objective

Prevent cache behavior from encoding too much current static-serving logic.

The cache should make expensive reads cheap. It should not own final HTTP serving decisions.

## Target Cache Responsibilities

Good cache candidates:

* [ ] Current release for site.
* [ ] Release manifest.
* [ ] Release file metadata.
* [ ] Server settings.
* [ ] Policy records.
* [ ] Policy violations.
* [ ] Default site mapping.

Avoid caching as the primary abstraction:

* [ ] Final static file serving decision.
* [ ] Final dynamic route decision.
* [ ] Final runtime invocation decision.
* [ ] Final socket upgrade decision.

## Tasks

* [ ] Review current cached decision keys.
* [ ] Replace final serving-decision cache entries with cached underlying read models where practical.
* [ ] Keep compatibility if this is too large for one phase, but add comments marking final-decision caching as transitional.
* [ ] Add cache invalidation tests for:

  * [ ] New release published.
  * [ ] Rollback.
  * [ ] Unpublish.
  * [ ] Policy update.
  * [ ] Default site update.
  * [ ] Settings update.
* [ ] Make cache invalidation operate around product concepts:

  * [ ] Release changed.
  * [ ] Policy changed.
  * [ ] Settings changed.
  * [ ] Host/default-site mapping changed.

## Acceptance Criteria

* [ ] `hotdata` no longer needs to understand every public HTTP outcome.
* [ ] Adding runtime routes will not require duplicating the static serving cache pattern.
* [ ] Cache invalidation is organized around release/policy/settings changes.
* [ ] Public routing remains correct after publish, rollback, unpublish, and policy updates.

---

# Phase 8: Introduce Runtime Package Skeleton

## Objective

Create the future home for user-uploaded script execution without implementing the full runtime yet.

## Proposed Packages

```text
internal/runtime
internal/runtimehttp
```

## Runtime Package Responsibilities

`internal/runtime` owns:

* [ ] Runtime bundle metadata.
* [ ] Entrypoint metadata.
* [ ] Runtime route metadata.
* [ ] Invocation request and response domain types.
* [ ] Executor interface.
* [ ] Runtime service orchestration.
* [ ] Capability checks needed before invocation.
* [ ] Logs/metrics interfaces, if introduced.

`internal/runtime` does not own:

* [ ] `http.ServeMux`.
* [ ] Admin UI.
* [ ] CLI response DTOs.
* [ ] Static file serving.
* [ ] SQLite implementation.
* [ ] Blob storage implementation details.

## Initial Interfaces

```go
type Service interface {
    InvokeHTTP(ctx context.Context, req InvocationRequest) (InvocationResponse, error)
}

type Executor interface {
    Invoke(ctx context.Context, bundle Bundle, req InvocationRequest) (InvocationResponse, error)
}

type InvocationRequest struct {
    Site      string
    Version   int64
    Route     string
    Method    string
    Headers   map[string][]string
    Body      []byte
}

type InvocationResponse struct {
    StatusCode int
    Headers    map[string][]string
    Body       []byte
}
```

## Tasks

* [ ] Create `internal/runtime`.
* [ ] Create placeholder domain types.
* [ ] Create `Executor` interface.
* [ ] Create no-op or disabled runtime service implementation.
* [ ] Create `internal/runtimehttp`.
* [ ] Add a runtime HTTP adapter that returns a clear disabled/not-implemented response.
* [ ] Do not enable user execution yet.
* [ ] Add tests proving runtime routes are not active unless explicitly configured.

## Acceptance Criteria

* [ ] Runtime terminology now refers only to script/code execution.
* [ ] Future runtime work has a clear package home.
* [ ] Public routing can theoretically route to runtime without changing static file serving.
* [ ] No security-sensitive execution behavior is introduced prematurely.

---

# Phase 9: Add Route Table Support

## Objective

Allow a release to describe both static routes and future dynamic routes.

## Route Kinds

```text
static
http
websocket
```

Initial implementation may only allow `static`.

## Tasks

* [ ] Add route declarations to manifest model.
* [ ] Persist route declarations as part of release metadata.
* [ ] Add route lookup to release read service.
* [ ] Update public routing to use route lookup.
* [ ] Preserve current static file fallback behavior.
* [ ] Add tests for:

  * [ ] Static route resolution.
  * [ ] Unknown route.
  * [ ] Index fallback.
  * [ ] Route precedence.
  * [ ] Future dynamic route declared but disabled by policy.
  * [ ] WebSocket route declared but disabled by policy.

## Acceptance Criteria

* [ ] Public routing is route-table driven.
* [ ] Static sites behave exactly as before.
* [ ] Dynamic route declarations can exist without being executable yet.
* [ ] Route precedence is explicit and tested.

---

# Phase 10: Prepare Persistence for Runtime Metadata

## Objective

Allow releases to include runtime bundles and route metadata without coupling runtime to SQLite.

## Data to Model

* [ ] Release ID or site/version pair.
* [ ] Runtime kind.
* [ ] Entrypoint.
* [ ] Bundle object key.
* [ ] Route path.
* [ ] Route method constraints.
* [ ] Required capabilities.
* [ ] Resource limits.
* [ ] Created timestamp.
* [ ] Published status through existing release lifecycle.

## Tasks

* [ ] Add repository interfaces in consumer packages.
* [ ] Implement persistence in `sqlitedb`.
* [ ] Add migrations or schema initialization updates.
* [ ] Add tests for persistence round trips.
* [ ] Keep runtime repository interfaces small and use-case-specific.
* [ ] Avoid adding a massive catch-all database interface in `server`.

## Acceptance Criteria

* [ ] Runtime metadata can be saved with a release.
* [ ] Runtime metadata can be read by public routing.
* [ ] Runtime metadata can be evaluated by policy.
* [ ] Runtime package does not import `sqlitedb`.
* [ ] Public HTTP package does not depend on SQLite directly.

---

# Phase 11: Introduce Disabled Dynamic HTTP Path

## Objective

Wire the public route path for dynamic HTTP handlers while execution remains disabled by default.

## Tasks

* [ ] Add `RouteHTTP` route kind.
* [ ] Allow manifest to declare HTTP routes only behind a feature flag or experimental setting.
* [ ] Add public route resolution for HTTP routes.
* [ ] Route HTTP requests to `runtimehttp`.
* [ ] Have `runtimehttp` return a controlled disabled response.
* [ ] Add policy check for dynamic HTTP capability.
* [ ] Add tests for:

  * [ ] Dynamic route disabled globally.
  * [ ] Dynamic route denied by policy.
  * [ ] Dynamic route declared but no executor configured.
  * [ ] Static routes still work.
  * [ ] Unknown routes still behave correctly.

## Acceptance Criteria

* [ ] The public data plane can distinguish static and dynamic routes.
* [ ] Runtime execution is still not enabled.
* [ ] Failure modes are explicit and safe.
* [ ] Existing static site behavior is unaffected.

---

# Phase 12: Add Real Runtime Execution Behind an Explicit Gate

## Objective

Add actual user-script execution only after routing, policy, persistence, and package boundaries are in place.

## Requirements Before Starting

* [ ] Runtime package exists.
* [ ] Runtime HTTP adapter exists.
* [ ] Route table exists.
* [ ] Capability policy exists.
* [ ] Release metadata supports runtime bundles.
* [ ] Execution is disabled by default.
* [ ] Tests prove static behavior is unaffected.

## Tasks

* [ ] Choose executor strategy:

  * [ ] Process sandbox.
  * [ ] WASM runtime.
  * [ ] Container isolation.
  * [ ] External worker service.
* [ ] Define runtime limits:

  * [ ] Max request body size.
  * [ ] Max response body size.
  * [ ] Max execution duration.
  * [ ] Max memory.
  * [ ] Max concurrent invocations.
* [ ] Define filesystem behavior:

  * [ ] Read-only bundle.
  * [ ] No arbitrary host filesystem access.
  * [ ] Temporary directory policy.
* [ ] Define network behavior:

  * [ ] Disabled by default.
  * [ ] Policy-gated if enabled.
* [ ] Define secrets behavior:

  * [ ] No secrets by default.
  * [ ] Explicit capability required.
* [ ] Implement executor.
* [ ] Add invocation logging.
* [ ] Add structured errors.
* [ ] Add metrics hooks.
* [ ] Add integration tests for successful invocation.
* [ ] Add integration tests for timeout, panic/error, oversized body, oversized response, and denied capability.
* [ ] Add load/concurrency tests.

## Acceptance Criteria

* [ ] Runtime execution is opt-in.
* [ ] Runtime execution is policy-gated.
* [ ] Runtime execution has resource limits.
* [ ] Runtime execution failures do not affect static serving.
* [ ] Runtime execution does not require control API package changes except deployment metadata.
* [ ] Runtime execution does not require static HTTP package changes.

---

# Phase 13: Add WebSocket Support

## Objective

Add socket handling as a runtime route kind after HTTP invocation is stable.

## Tasks

* [ ] Add `RouteWebSocket` route kind.
* [ ] Add manifest syntax for socket routes.
* [ ] Add policy capability for WebSocket support.
* [ ] Add socket resource limits:

  * [ ] Max connection duration.
  * [ ] Max message size.
  * [ ] Max concurrent sockets per site.
  * [ ] Idle timeout.
* [ ] Add runtime socket interface.
* [ ] Add runtime HTTP adapter support for upgrade requests.
* [ ] Add tests for:

  * [ ] Upgrade allowed.
  * [ ] Upgrade denied by policy.
  * [ ] Non-upgrade request to socket route.
  * [ ] Message size exceeded.
  * [ ] Idle timeout.
  * [ ] Connection cleanup.

## Acceptance Criteria

* [ ] WebSockets are route-table driven.
* [ ] WebSockets are policy-gated.
* [ ] WebSockets have explicit lifecycle limits.
* [ ] HTTP runtime and socket runtime share only appropriate abstractions.
* [ ] Static serving remains independent.

---

# Phase 14: Clean Up Composition Root

## Objective

Prevent `server.Database` and `server.New` from becoming long-term dependency dumping grounds.

## Tasks

* [ ] Review `server.Database`.
* [ ] Split broad embedded repository interfaces if they become too large.
* [ ] Prefer constructing services explicitly in the composition root.
* [ ] Consider replacing one massive DB interface with a dependency struct.
* [ ] Keep `sqlitedb` as the concrete provider of persistence interfaces.
* [ ] Keep transport packages dependent on services, not raw database handles.
* [ ] Ensure `cmd/quack-server` remains readable.

## Preferred Direction

```go
type Dependencies struct {
    ControlAPI http.Handler
    AdminUI    http.Handler
    PublicHTTP http.Handler
}
```

Or:

```go
type Services struct {
    Publishing publishing.Service
    Releases   releases.Service
    Policy     policy.Service
    Runtime    runtime.Service
}
```

Avoid:

```go
type Database interface {
    uploads.Repository
    releases.Repository
    runtime.Repository
    sockets.Repository
    secrets.Repository
    logs.Repository
    everything.Repository
}
```

## Acceptance Criteria

* [ ] New runtime features do not require bloating a single database interface.
* [ ] Transport packages receive narrow dependencies.
* [ ] The composition root remains the only place where concrete infrastructure is assembled.
* [ ] Package dependencies remain acyclic and understandable.

---

# Phase 15: Documentation and Architecture Tests

## Objective

Make the new boundaries durable.

## Tasks

* [ ] Add `docs/architecture.md`.
* [ ] Document package responsibilities.
* [ ] Document control plane versus data plane.
* [ ] Document release lifecycle.
* [ ] Document policy/capability model.
* [ ] Document public request routing.
* [ ] Document runtime execution model.
* [ ] Add architecture tests or dependency checks.
* [ ] Add package comments for major packages.
* [ ] Add examples for static-only manifest.
* [ ] Add examples for future dynamic HTTP manifest.
* [ ] Add examples for future WebSocket manifest.

## Suggested Architecture Rules

* [ ] `runtime` must not import `net/http`.
* [ ] `runtime` must not import `sqlitedb`.
* [ ] `statichttp` must not import `runtime`.
* [ ] `controlapi` must not perform public route resolution.
* [ ] `publichttp` must not perform deploy/archive ingestion.
* [ ] `policy` must not import transport packages.
* [ ] `manifest` must not import transport packages.
* [ ] `sqlitedb` may implement interfaces but should not own application use cases.
* [ ] `server` and `cmd/quack-server` are allowed to compose concrete dependencies.

## Acceptance Criteria

* [ ] A new engineer can understand where to add static, dynamic HTTP, and WebSocket features.
* [ ] The architecture has executable or reviewable guardrails.
* [ ] Future work is less likely to collapse back into `sites` or `sitehttp`.

---

# Recommended Execution Order

## Safe First PRs

* [ ] Phase 0: Safety net and baseline.
* [ ] Phase 1: Rename `serverapi` to `controlapi`.
* [ ] Phase 2: Rename current runtime status concepts.
* [ ] Phase 5: Extract manifest parsing.

## Medium-Risk Structural PRs

* [ ] Phase 3: Split public serving from static file serving.
* [ ] Phase 4: Extract publishing and release concepts.
* [ ] Phase 6: Extract policy and capability evaluation.
* [ ] Phase 7: Reduce `hotdata` to cached reads.

## Future Feature-Enabling PRs

* [ ] Phase 8: Introduce runtime package skeleton.
* [ ] Phase 9: Add route table support.
* [ ] Phase 10: Prepare persistence for runtime metadata.
* [ ] Phase 11: Introduce disabled dynamic HTTP path.

## Feature PRs

* [ ] Phase 12: Add real runtime execution.
* [ ] Phase 13: Add WebSocket support.
* [ ] Phase 14: Clean up composition root.
* [ ] Phase 15: Documentation and architecture tests.

---

# Non-Goals for the Initial Refactor

* [ ] Do not implement script execution during the package-boundary cleanup.
* [ ] Do not introduce WebSockets before HTTP runtime routing is stable.
* [ ] Do not change external API payloads unless intentionally versioned.
* [ ] Do not rewrite SQLite persistence unless required by the new release/runtime metadata model.
* [ ] Do not turn `domain` into a dumping ground for every shared type.
* [ ] Do not move code purely for smaller files; move code to reflect product concepts.

---

# Final Target Shape

```text
cmd/
  quack/
  quack-server/

internal/
  server/            # composition root
  controlapi/        # deploy/list/revisions/settings management API
  adminui/           # HTML admin UI
  publichttp/        # public data-plane router
  statichttp/        # blob-backed static file serving
  runtimehttp/       # HTTP/WebSocket adapter for runtime execution

  publishing/        # deploy archive -> release
  releases/          # current release, history, publish/unpublish/rollback
  sites/             # site identity, names, paths
  manifest/          # site manifest parsing and validation
  policy/            # capability evaluation and violations
  runtime/           # script execution domain and service

  storage/           # blob storage
  sqlitedb/          # persistence implementation
  hotdata/           # cached read models

  access/            # authorization
  settings/          # typed settings registry
  protocol/          # external control-plane wire contracts
  client/            # CLI HTTP client
  domain/            # small set of truly shared core types
```

## Final Definition of Done

* [ ] Static hosting behavior is preserved.
* [ ] Control plane and data plane are clearly separated.
* [ ] Runtime terminology refers only to code execution.
* [ ] Site availability/policy status has separate terminology.
* [ ] Releases are the core deployable unit.
* [ ] Policy is capability-based.
* [ ] Manifest parsing has its own package.
* [ ] Public routing can choose static, HTTP runtime, or WebSocket runtime routes.
* [ ] Runtime execution can be added without bloating `sites`, `sitehttp`, or `controlapi`.
* [ ] Package boundaries are documented and tested.
