# Quack

Quack is a small Go folder upload system. It contains a CLI uploader that streams a directory as a tar archive and an HTTP server that accepts and validates the archive.

The first version is intentionally minimal: uploads are parsed and counted, but files are not persisted or served.

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

## Run the Uploader

Upload a folder to a running server:

```bash
go run ./cmd/quack \
  -server http://localhost:8080 \
  -token dev-token \
  -site example.com \
  -directory ./some-folder
```

The uploader streams a tar archive directly into the HTTP request. It does not write a temporary archive to disk.

## Current Limitations

- Files are stored as SHA-256-addressed blobs under `blobs/site:<site-sha>/<version>/file:<file-sha>`.
- Upload metadata is saved through a database adapter. The current concrete implementation uses SQLite via `modernc.org/sqlite`.
- Symlinks and unusual filesystem entries are skipped by the client.
- Symlinks and unsupported tar entries are rejected by the server.
- There is no compression, chunking, resumable upload, deduplication, TLS setup, file serving, or user account system.

## Roadmap

- Persistent storage behind the existing storage interface.
- Resumable and chunked uploads.
- Optional compression.
- Content-addressed storage and deduplication.
- File serving and metadata APIs.
- Configuration files and production deployment options.
