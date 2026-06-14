# TODO: Settings And Policy Read Performance

## Context

Settings and policy resolution currently performs synchronous SQLite reads on request paths that need effective runtime decisions.

Examples:

- Upload policy resolution reads `server_settings` through `GetServerSettings`.
- Database feature policy checks read `policies` through `LoadPolicies`.
- Static serving checks runtime site status through the resolver before serving content.

This is acceptable for the current project scale because SQLite reads are local and fast, uploads and admin edits are low-frequency, and static serving already performs SQLite reads to resolve current files.

## Current Risk

The static-serving path is the first place to watch.

`ResolveCurrentSiteRuntime` currently asks the database for current site manifests and then finds the matching site in memory. That keeps the resolver API simple, but it is not the right long-term shape for high request volume.

The immediate concern is not correctness. It is avoidable read amplification on every static request.

## Near-Term Fix

Add a targeted database method for runtime status lookups, for example:

```go
GetCurrentSiteManifest(ctx context.Context, site string) (CurrentSiteManifest, bool, error)
```

Then update `ResolveCurrentSiteRuntime` to load only the requested site instead of listing all current site manifests.

Keep upload and admin paths as direct SQLite reads for now.

## Later Optimization

If settings or policy lookups become hot, add an in-memory snapshot cache:

- Load settings and policies into memory at startup.
- Invalidate or refresh the snapshot after admin settings or policy saves.
- Keep the resolver as the only place that computes effective decisions.
- Avoid caching uploaded file metadata unless static serving performance specifically requires it.

## Acceptance Criteria

- Static file serving no longer lists all current site manifests for each request.
- Upload limit resolution remains DB-backed and correct.
- Policy changes still take effect immediately after admin save.
- Runtime site status remains resolved through the resolver.
- `go test ./...` passes.
