# Quack
![Duck](duck.png)
Quack is a tiny publishing layer for AI-made tools, demos, dashboards, games, and static sites.

The idea is simple: point Quack at a folder, give the site a name, and get something shareable back. No framework ceremony, no deployment pipeline, no project-shaped infrastructure detour. Just upload the files and serve the current version by hostname.

This first version is intentionally small. Quack is a Go CLI and HTTP server: the CLI streams a directory as a tar archive, and the server validates the upload, stores files as hashed blobs, tracks versions in SQLite, and serves the active version for each site name.

The long-term shape is "Geocities plus Firebase for the AI era": a low-friction place where generated or hand-built web artifacts can become real, hosted things, with a small set of backend primitives added only when they earn their keep.

## What Quack Does

- Uploads a local folder directly to a Quack server.
- Stores uploaded files as SHA-256-addressed blobs.
- Tracks site versions and metadata in SQLite.
- Serves the latest version of a site based on the request hostname.
- Supports per-user bearer tokens for upload and delete operations.
- Keeps the core model boring on purpose: files in, URL out.

## Run the Server

Start the server:

```bash
go run ./cmd/quack-server -root ./data -database ./quack.sqlite
```

On first startup, the server bootstraps an admin user and logs generated credentials. Use the admin UI to create users; each user gets a bearer token for CLI uploads and deletes.

For compatibility with older clients or scripts, `UPLOAD_TOKEN` can still be set as an additional shared bearer token:

```bash
UPLOAD_TOKEN=dev-token go run ./cmd/quack-server -root ./data -database ./quack.sqlite
```

For local throwaway development only, you can explicitly disable upload/delete authentication:

```bash
go run ./cmd/quack-server -root ./data -database ./quack.sqlite --allow-unauthenticated
```

The admin UI and `/v1` API listen on `:8080` by default. Public site traffic listens on `:8081` by default. Set `ADMIN_ADDR` and `PUBLIC_ADDR` to override them. The legacy `ADDR` environment variable still overrides the admin/API listener when `ADMIN_ADDR` is unset.

The server applies DB-backed upload limits by default:

- `max_upload_bytes`, default `536870912` bytes, or 512 MiB.
- `max_upload_files`, default `10000` regular files.
- `max_retained_versions`, default `0`, meaning retain all published versions.

Use `0` for either upload limit to disable that limit. Set `max_retained_versions` to a positive number to prune older published versions after each successful upload. These settings, along with `log_level`, are initialized from code defaults only when missing and can be edited in the admin UI.

## Run the CLI

Upload a folder to a running server:

```bash
go run ./cmd/quack deploy ./some-folder example \
  --token dev-token \
  --serverURL http://localhost:8080
```

The uploader streams a tar archive directly into the HTTP request. It does not write a temporary archive to disk.

If the directory is a simple folder name, Quack can infer the site name:

```bash
go run ./cmd/quack deploy my-site \
  --token dev-token \
  --serverURL http://localhost:8080
```

Path-like directories such as `.`, `./my-site`, or `../my-site` still require an explicit site name.

List sites available to the authenticated user:

```bash
go run ./cmd/quack sites \
  --token dev-token \
  --serverURL http://localhost:8080
```

Admins can list one user's sites with `quack sites <username>` or every site with `quack sites --all`.

Delete a site and its stored blobs:

```bash
go run ./cmd/quack delete example \
  --token dev-token \
  --serverURL http://localhost:8080
```

List retained revisions for a site:

```bash
go run ./cmd/quack revisions example \
  --token dev-token \
  --serverURL http://localhost:8080
```

Roll back a site to its previous retained revision:

```bash
go run ./cmd/quack rollback example \
  --token dev-token \
  --serverURL http://localhost:8080
```

Unpublish a site without deleting its retained versions or blobs:

```bash
go run ./cmd/quack unpublish example \
  --token dev-token \
  --serverURL http://localhost:8080
```

Publish an unpublished site again:

```bash
go run ./cmd/quack publish example \
  --token dev-token \
  --serverURL http://localhost:8080
```

Set the default site used when a requested site name does not exist:

```bash
go run ./cmd/quack default-site home \
  --token dev-token \
  --serverURL http://localhost:8080
```

Clear it with `quack default-site --clear`.

## Serve Uploaded Files

Quack serves the current version of uploaded files from the public listener based on the request host's left-most label.

For example, a site named `foo` matches:

```text
foo.bar.domain.com
```

A site named `domain` matches:

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

## Docker

Build the server image:

```bash
docker build -t quack-server:dev .
```

Run it locally with ephemeral in-container SQLite and blob storage:

```bash
docker run --rm -p 8080:8080 \
  -p 8081:8081 \
  quack-server:dev
```

The image starts `quack-server` with:

```text
-root /var/lib/quack -database /var/lib/quack/quack.sqlite
```

That directory is writable inside the container, but it is not persistent unless you mount a volume:

```bash
docker run --rm -p 8080:8080 \
  -p 8081:8081 \
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
  - hostname: quack.example.com
    service: http://quack-server.default.svc.cluster.local:8080

  - hostname: foo.example.com
    service: http://quack-server.default.svc.cluster.local:8081

  - service: http_status:404
```

Then upload a site named `foo`:

```bash
quack deploy ./site foo \
  --token dev-token \
  --serverURL https://quack.example.com
```

Public requests to `https://foo.example.com/` reach `quack-server`, which reads the request `Host` header and maps the left-most label to the site name:

```text
foo.example.com -> foo
```

The important requirement is that `quack-server` receives the original public `Host` header. If the tunnel rewrites `Host` to the internal Kubernetes service name, set the host header explicitly:

```yaml
ingress:
  - hostname: foo.example.com
    service: http://quack-server.default.svc.cluster.local:8081
    originRequest:
      httpHostHeader: foo.example.com

  - service: http_status:404
```

For multiple sites, a wildcard hostname can route all site subdomains to the same service:

```yaml
ingress:
  - hostname: "*.example.com"
    service: http://quack-server.default.svc.cluster.local:8081

  - service: http_status:404
```

With that setup:

```text
foo.example.com -> site foo
bar.example.com -> site bar
```

## Current Limitations

- Files are stored as SHA-256-addressed blobs under `blobs/site:<site-sha>/<version>/file:<file-sha>`.
- Upload metadata is saved through a database adapter. The current concrete implementation uses SQLite via `modernc.org/sqlite`.
- Symlinks and unusual filesystem entries are skipped by the client.
- Symlinks and unsupported tar entries are rejected by the server.
- There is no compression, chunking, resumable upload, deduplication, TLS setup, custom backend, scheduled jobs, or user account system.

## Roadmap

Quack should stay small, but a few pieces are natural next steps:

- Persistent storage behind the existing storage interface.
- Resumable and chunked uploads.
- Optional compression.
- Content-addressed storage and deduplication.
- File serving and metadata APIs.
- Configuration files and production deployment options.
