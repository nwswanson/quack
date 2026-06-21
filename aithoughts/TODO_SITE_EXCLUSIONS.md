# site.yml Upload Exclusions

Goal: allow a site to declare local files and directories that should not be uploaded. This should cover examples like editor swap files and dependency folders:

```yaml
exclude:
  - "*.swp"
  - "node_modules"
```

The uploader should apply this before tar streaming so excluded files are never sent to the server. That keeps uploads smaller, avoids counting ignored files against upload limits, and matches the user's expectation that ignored content is not transmitted.

## Config Shape

Add a top-level `exclude` list to `site.yml` / `site.yaml`:

```yaml
exclude:
  - "*.swp"
  - "node_modules/**"

routes:
  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
```

Support `node_modules/**` as the explicit directory-tree form. As a convenience, normalize bare path patterns that identify a directory during the walk, such as `node_modules`, so they exclude that directory and all descendants.

## Matching Semantics

- Match against slash-separated paths relative to the upload root.
- Basename-only patterns like `*.swp` should match file or directory basenames at any depth.
- Path patterns containing `/` should match the full relative path.
- Directory matches should skip the whole subtree with `filepath.SkipDir`.
- `site.yml` and `site.yaml` should still be read as upload metadata even if an exclusion pattern would otherwise match them.
- Symlinks and non-regular files should continue to be skipped as they are today.

## Implementation Plan

1. Extend `internal/manifest.Manifest`:

   ```go
   Exclude []string `json:"exclude" yaml:"exclude"`
   ```

2. Validate exclusions in `internal/manifest`:
   - reject empty patterns
   - normalize `\` to `/`
   - reject absolute paths
   - reject `..` path traversal
   - check glob syntax with `path.Match`
   - normalize trailing `/` patterns to directory-tree patterns

3. Add an exclusion matcher, likely near manifest or protocol archive code:
   - input: parsed exclude patterns
   - output: whether a relative path should be skipped
   - include whether the path is a directory so directory rules can skip efficiently

4. Change the client upload path:
   - `internal/client.UploadDirectory` reads `site.yml` or `site.yaml` from the upload root before opening the tar stream
   - parse the manifest locally using the existing parser
   - pass `manifest.Exclude` into tar creation

5. Change tar creation conservatively:
   - add `protocol.WriteTarWithOptions(ctx, root, w, options)` and keep `WriteTar` as the no-options wrapper
   - `WriteTarOptions` can start with `Exclude []string`
   - apply exclusions inside `filepath.WalkDir` after computing the relative slash path and before writing headers

6. Preserve server behavior:
   - the server should continue parsing `site.yml` / `site.yaml` from the tar stream when present
   - the manifest should not be stored as a served upload file, matching current behavior
   - server-side archive validation remains the authority for malicious or malformed clients

## Tests

- `internal/manifest` parses `exclude`.
- Unknown fields are still rejected.
- Invalid exclude patterns are rejected.
- `*.swp` excludes nested swap files.
- `node_modules` or `node_modules/**` skips the entire directory tree.
- Excluded directories are not walked into.
- `site.yml` / `site.yaml` is still emitted for server-side parsing even if a broad pattern would match it.
- Upload response file count and bytes exclude skipped files.

## README Example

Add a short section near the CLI upload docs:

```yaml
exclude:
  - "*.swp"
  - "node_modules"
```

Explain that exclusions are evaluated by the CLI before upload, use paths relative to the site root, and prevent matching files from being sent.
