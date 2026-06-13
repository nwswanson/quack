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

## Docker

Build the server image:

```bash
docker build -t quack-server:dev .
```

Run it locally with ephemeral in-container SQLite and blob storage:

```bash
docker run --rm -p 8080:8080 \
  -e UPLOAD_TOKEN=dev-token \
  quack-server:dev
```

The image starts `quack-server` with:

```text
-root /var/lib/quack -database /var/lib/quack/quack.sqlite
```

That directory is writable inside the container, but it is not persistent unless you mount a volume. For example:

```bash
docker run --rm -p 8080:8080 \
  -e UPLOAD_TOKEN=dev-token \
  -v quack-data:/var/lib/quack \
  quack-server:dev
```

## Cloudflare Tunnel

`quack-server` works behind Cloudflare Tunnel when Cloudflare routes a public hostname to the Kubernetes Service for the server.

The request flow is:

```text
Browser
  -> Cloudflare edge
  -> Cloudflare Tunnel
  -> cloudflared pod in Kubernetes
  -> quack-server Kubernetes Service
  -> quack-server pod
```

For a Kubernetes Service named `quack-server` in the `default` namespace, an explicit Cloudflare Tunnel route can look like:

```yaml
ingress:
  - hostname: foo.example.com
    service: http://quack-server.default.svc.cluster.local:8080

  - service: http_status:404
```

Then upload a site named `foo`:

```bash
quack deploy ./site foo \
  --token dev-token \
  --serverURL https://foo.example.com
```

Public requests to `https://foo.example.com/` will reach `quack-server`, which reads the request `Host` header and maps the left-most label to the site name:

```text
foo.example.com -> foo
```

The important requirement is that `quack-server` receives the original public `Host` header. If the tunnel rewrites `Host` to the internal Kubernetes service name, set the host header explicitly:

```yaml
ingress:
  - hostname: foo.example.com
    service: http://quack-server.default.svc.cluster.local:8080
    originRequest:
      httpHostHeader: foo.example.com

  - service: http_status:404
```

For multiple sites, a wildcard hostname can route all site subdomains to the same service:

```yaml
ingress:
  - hostname: "*.example.com"
    service: http://quack-server.default.svc.cluster.local:8080

  - service: http_status:404
```

With that setup:

```text
foo.example.com -> site foo
bar.example.com -> site bar
```

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
