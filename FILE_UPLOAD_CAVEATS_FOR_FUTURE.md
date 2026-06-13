# File Upload Caveats For Future

This project currently accepts a streamed tar archive, hashes each regular file, writes each file as a blob, and then calls a stub upload metadata saver once after the archive is fully read.

## Current Behavior

The server processes uploads sequentially:

1. Read one tar header.
2. Validate and sanitize the archive path.
3. For regular files, stream the file body through SHA-256 and into a temporary file.
4. Rename the temporary file to `blobs/site:<site-sha>/<version>/file:<file-sha>`.
5. Append one metadata record to the in-memory upload record.
6. After the tar stream reaches EOF, call `SaveUpload` once with the full upload record.

File contents are streamed. The server does not load a whole uploaded archive or whole file into memory.

## What Happens With 10k Files

An upload containing 10,000 regular files will result in:

- 10,000 tar entries processed serially.
- 10,000 temporary files created.
- 10,000 SHA-256 hashes computed.
- 10,000 filesystem renames into the blob store.
- 10,000 metadata records held in memory until the upload completes.
- One final metadata save call after the entire upload succeeds.

This should work for ordinary small files, but it will be filesystem-operation-heavy and slower than a bulk write or batched metadata pipeline.

## Current Practical Limits

The current implementation has no explicit guardrails for:

- Maximum upload size.
- Maximum file count.
- Maximum single-file size.
- Maximum path length.
- Request read timeout.
- Per-site concurrency.
- Global upload concurrency.
- Disk space reservation or quota.
- Cleanup of blobs if an upload fails halfway.

The implementation also currently uses version `1` for every upload. Concurrent uploads to the same site/version can collide.

## Important Edge Cases

Sanitized path collisions are possible. For example, both `a b.txt` and `a_b.txt` can sanitize to the same relative path.

Duplicate file content maps to the same blob path because the file hash is the address. On Unix-like systems, `os.Rename` can replace an existing file. That is probably content-safe when the hash is identical, but the behavior is not explicitly modeled as deduplication yet.

The upload metadata is held in memory as a slice until the archive finishes. This is acceptable for thousands of files, but not for unbounded uploads with hundreds of thousands or millions of files.

The version directory is created for each file even though it is shared by the upload. This is harmless but inefficient.

## Future Guardrails

Before treating uploads as production-safe, add:

- Body size limits, likely with `http.MaxBytesReader`.
- Maximum accepted file count per upload.
- Maximum accepted file size.
- Maximum accepted sanitized path length.
- Server read and write timeouts.
- Per-site upload locking or version allocation.
- Staged upload directories with cleanup on failure.
- Atomic publish of upload metadata only after all blobs are written.
- Collision detection for sanitized relative paths.
- Explicit handling for duplicate blob writes.
- Quotas for site storage and active uploads.
- Metrics for file count, byte count, upload duration, and failures.

## Design Direction

The blob-oriented approach is a good security baseline because archive paths are not used as filesystem write paths. The next hardening step is to make upload acceptance bounded and transactional: allocate a version, stage blob writes, record metadata as a coherent manifest, and publish the version only after the full upload succeeds.
