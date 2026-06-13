# Quack

Quack is a small Go folder upload system. It contains a CLI uploader that streams a directory as a tar archive and an HTTP server that accepts and validates the archive.

The first version is intentionally minimal: uploads are stored as hashed blobs, tracked in SQLite, and served back by site name.

## Run the Server

Start the server without upload authentication:

```bash
go run ./cmd/quack-server -root ./data -database ./quack.sqlite
```

Start the server with bearer-token authentication:

```bash
UPLOAD_TOKEN=dev-token go run ./cmd/quack-server -root ./data -database ./quack.sqlite
```

The server listens on `:8080` by default. Set `ADDR` to override it.

The server applies upload limits by default:

- `-max-upload-bytes`, default `536870912` bytes, or 512 MiB.
- `-max-upload-files`, default `10000` regular files.

Use `0` for either flag to disable that limit. These defaults are intended to fit ordinary static-site uploads, including moderately large sites, while preventing unbounded request bodies and unbounded metadata growth.

## Run the Uploader

Upload a folder to a running server:

```bash
go run ./cmd/quack deploy ./some-folder example.com \
  --token dev-token \
  --serverURL http://localhost:8080
```

The uploader streams a tar archive directly into the HTTP request. It does not write a temporary archive to disk.

Delete a site and its stored blobs:

```bash
go run ./cmd/quack delete example.com \
  --token dev-token \
  --serverURL http://localhost:8080
```

## Serve Uploaded Files

The server serves the current version of uploaded files from `/` based on the request host's left-most label.

For example, an upload with `-site foo` matches:

```text
foo.bar.domain.com
```

An upload with `-site domain` matches:

```text
domain.com
```

It does not match:

```text
foo.domain.com
```

Requests for `/` serve `index.html` when present. If the current site has no `index.html`, the server returns a blank `200 OK` page. Other paths, such as `/file.js`, are served directly when present.

You can also bypass host matching by serving through:

```text
/serve/<site>
```

For example, `/serve/foo/file.js` serves the current `file.js` for site `foo`, regardless of the request host. `/serve/foo` and `/serve/foo/` use the same `index.html` default behavior.

## Current Limitations

- Files are stored as SHA-256-addressed blobs under `blobs/site:<site-sha>/<version>/file:<file-sha>`.
- Upload metadata is saved through a database adapter. The current concrete implementation uses SQLite via `modernc.org/sqlite`.
- Symlinks and unusual filesystem entries are skipped by the client.
- Symlinks and unsupported tar entries are rejected by the server.
- There is no compression, chunking, resumable upload, deduplication, TLS setup, or user account system.

## Roadmap

- Persistent storage behind the existing storage interface.
- Resumable and chunked uploads.
- Optional compression.
- Content-addressed storage and deduplication.
- File serving and metadata APIs.
- Configuration files and production deployment options.
