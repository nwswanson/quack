# File Upload Caveats For Future

This project currently accepts a streamed tar archive, hashes each regular file, writes each file as a blob, and then saves upload metadata through a database adapter once after the archive is fully read. The current concrete database implementation is SQLite via `modernc.org/sqlite`.

## Current Behavior

The server processes uploads sequentially:

1. Read one tar header.
2. Validate and sanitize the archive path.
3. For regular files, stream the file body through SHA-256 and into a temporary file.
4. Rename the temporary file to `blobs/site:<site-sha>/<version>/file:<file-sha>`.
5. Append one metadata record to the in-memory upload record.
6. After the tar stream reaches EOF, call the database adapter once with the full upload record.

File contents are streamed. The server does not load a whole uploaded archive or whole file into memory.

## Default Upload Limits

The server enforces these upload limits by default:

- Maximum tar request size: `536870912` bytes, or 512 MiB.
- Maximum accepted regular files per upload: `10000`.

Both limits can be overridden with `quack-server` flags:

```bash
go run ./cmd/quack-server \
  -root ./data \
  -database ./quack.sqlite \
  -max-upload-bytes 536870912 \
  -max-upload-files 10000
```

Use `0` for either flag to disable that limit.

The defaults are intentionally conservative but useful. A 512 MiB tar limit is enough for many static-site and asset-folder uploads while bounding request body size, disk writes, and hashing work. A 10,000-file limit covers moderately large static sites and matches the known 10k-file scenario, while bounding per-upload metadata memory and SQLite insert work.

## Unsafe Path And Entry Handling

There are two relevant cases: archives produced by the `quack` client and arbitrary tar streams posted directly to the server.

For archives produced by `quack`:

- Absolute paths are not produced. The client walks the selected root and writes relative tar names.
- `..` paths are not produced in normal operation because names are derived from paths under the selected root.
- Symlinks are skipped.
- Device files, sockets, FIFOs, and other unusual filesystem entries are skipped.
- Directories are included as directory metadata.
- Regular files are included and streamed.

For arbitrary tar uploads sent directly to the server:

- Absolute archive paths are rejected with `400 bad request`.
- Paths containing `..` as a path segment are rejected with `400 bad request`.
- Empty paths are rejected.
- Symlinks are rejected as unsupported tar entries.
- Hardlinks are rejected as unsupported tar entries.
- Character devices are rejected as unsupported tar entries.
- Block devices are rejected as unsupported tar entries.
- FIFOs are rejected as unsupported tar entries.
- Other unsupported tar entry types are rejected.
- Directories are accepted as metadata only; no blob is written.
- Regular files are accepted, sanitized for serving, hashed, and stored as blobs.

The server does not unpack tar paths to disk. Archive paths are only used as metadata after validation and serving-path sanitization. Blob writes use hash-derived paths under `blobs/site:<site-sha>/<version>/file:<file-sha>`.

Unsupported tar entries currently fail the entire upload instead of being skipped. That is stricter and safer, but it means a tar containing one symlink or hardlink rejects the whole upload.

## What Happens With 10k Files

An upload containing 10,000 regular files will result in:

- 10,000 tar entries processed serially.
- 10,000 temporary files created.
- 10,000 SHA-256 hashes computed.
- 10,000 filesystem renames into the blob store.
- 10,000 metadata records held in memory until the upload completes.
- One final SQLite-backed metadata save call after the entire upload succeeds.

This should work for ordinary small files, but it will be filesystem-operation-heavy and slower than a bulk write or batched metadata pipeline.

## Current Practical Limits

The current implementation still has no explicit guardrails for:

- Maximum single-file size.
- Maximum path length.
- Request read timeout.
- Per-site concurrency.
- Global upload concurrency.
- Disk space reservation or quota.
- Cleanup of blobs if an upload fails halfway.

Versions are allocated from the database. A new site starts at version `1`; later uploads use the stored current version plus one. Concurrent uploads for the same site still need stronger per-site locking or transactional reservation to avoid allocation races.

## Important Edge Cases

Sanitized path collisions are possible. For example, both `a b.txt` and `a_b.txt` can sanitize to the same relative path.

Duplicate file content maps to the same blob path because the file hash is the address. On Unix-like systems, `os.Rename` can replace an existing file. That is probably content-safe when the hash is identical, but the behavior is not explicitly modeled as deduplication yet.

The upload metadata is held in memory as a slice until the archive finishes. This is acceptable for thousands of files, but not for unbounded uploads with hundreds of thousands or millions of files.

The version directory is created for each file even though it is shared by the upload. This is harmless but inefficient.

Deleting a site removes the site metadata from SQLite and removes the site's blob tree under `blobs/site:<site-sha>`. This operation spans SQLite and the filesystem, so it is not fully atomic. If one side succeeds and the other fails, manual repair or a future reconciliation job may be needed.

## Future Guardrails

Before treating uploads as production-safe, add:

- Body size limits, likely with `http.MaxBytesReader`.
- Maximum accepted file count per upload.
- Maximum accepted file size.
- Maximum accepted sanitized path length.
- Server read and write timeouts.
- Per-site upload locking or transactional version reservation.
- Staged upload directories with cleanup on failure.
- Atomic publish of upload metadata only after all blobs are written.
- Collision detection for sanitized relative paths.
- Explicit handling for duplicate blob writes.
- Explicit tests for rejected tar path and entry types.
- Quotas for site storage and active uploads.
- Reconciliation for database/blob mismatches after failed delete or failed upload cleanup.
- Metrics for file count, byte count, upload duration, and failures.

## Design Direction

The blob-oriented approach is a good security baseline because archive paths are not used as filesystem write paths. The next hardening step is to make upload acceptance bounded and transactional: allocate a version, stage blob writes, record metadata as a coherent manifest, and publish the version only after the full upload succeeds.
