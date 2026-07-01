# Quack

<img src="quack.jpg" width="400" height="400">

Quack is a small self-hosted app server for publishing weird little web things instantly.

Point Quack at a directory, give it a site name, and it serves the current version by hostname. That is the main trick: easy static file hosting with versioning, rollback, and a tiny control plane.

Quack also includes a compact event runtime for turning rich frontend sites into live systems. HTTP routes, WebSocket sessions, hardware events, timers, and background handlers all communicate through durable named pipes. Pipes can retain recent messages, route by selector, fan events out to subscribers, serialize work by topic, and give handlers a shared coordination layer without requiring a separate broker. You can even bring along your logic--the main handler is starlark, but a WASM module can be called to invoke fast compiled code. 

Think less “formal production platform with RBAC and policies” (It has RBAC and policies if you want to run it at your company for internal demo hosting) and more “a 🤡🚗 full of 🦆s with 🐝🔫s.” Quack is meant to carry a surprising amount of stuff for its size, especially for strange, fun, useful, half-serious apps that should exist before you talk yourself out of deploying them.

## What Quack is for

Quack is designed for:

* Static sites
* Tiny tools
* Vibe-coded apps
* Hardware interface projects
* AI-generated web artifacts
* Personal dashboards
* Internal tools
* Corporate intranet toys
* Indie-web projects
* Realtime demos
* Small multiplayer or collaborative apps
* Homelab services
* Little pages that are too real for localhost but too small for Kubernetes

If your app builds to a directory of files, Quack can probably host it.

If your app needs just a little backend behavior, Quack can probably provide enough: a Starlark handler, a bit of memory, and a WebSocket route.

## Current status

Quack is experimental.

It works, but it is still young. Names may change. Some features are intentionally small. Some operational knobs are more conservative than convenient. That is deliberate: Quack is open source and self-hosted, not an internal tool hiding inside someone else’s trust boundary.

Uploads need limits. Runtime code needs policies. Admin surfaces need protection. Boring boundaries matter.

## Core features

Quack gives you:

* Directory uploads from a CLI
* Static file hosting
* Hostname-based site routing
* `site.yml` route declarations
* Versioned deployments
* Webcam support on linux hosts
* Rollback
* Publish, unpublish, and delete controls
* A browser admin panel
* User and token management
* Secure at-rest encrypted secret stores usable in Starlark
* Server-wide settings
* Policy gates for dynamic features
* Starlark HTTP routes
* Site-scoped memory
* WebSocket routes
* Optional memory snapshot persistence
* HTTP cache controls
* Host allowlisting

The default path is intentionally simple:

```text
local folder -> quack-cli deploy -> quack-server -> public hostname
```

## Quick start

### Dev server

For local iteration, serve a build directory directly without uploading or creating stored revisions:

```bash
quack dev-server ./dist mysite
```

The dev server reads `site.yml` or `site.yaml`, serves files from the directory with normal route lookup, and runs Starlark HTTP/WebSocket routes from the same files using `dev:` blob paths. By default it binds to `127.0.0.1` on an available port and prints the local URL.

Useful flags:

```bash
quack dev-server ./dist mysite --port 8080
quack dev-server ./dist mysite --watch off
quack dev-server ./dist mysite --host-match site
```

Dev status is available at:

```text
/__quack/dev
```

### How to start client + server

Start a server:

```bash
mkdir -p ./quack-data/blobs ./quack-data/memory

PUBLIC_ADDR=:8080 ADMIN_ADDR=:8081 quack-server \
  -root ./quack-data/blobs \
  -database ./quack-data/quack.sqlite \
  -memory-dir ./quack-data/memory
```

Quack starts two listeners:

```text
:8080  public site traffic
:8081  admin panel and control API
```

On first boot, Quack creates an admin user and prints the generated username, password, and token to the server logs. Save those credentials.

Then log in from the CLI:

```bash
quack-cli login \
  --serverURL http://localhost:8081 \
  --token <admin-or-user-token>
```

Deploy a folder:

```bash
quack-cli deploy ./dist mysite
```

Or deploy without saved login config:

```bash
quack-cli deploy ./dist mysite \
  --serverURL http://localhost:8081 \
  --token <admin-or-user-token>
```

The site is then served from the public listener using the request hostname. For example:

```text
mysite.example.com -> site name "mysite"
```

Configure `allowed_hosts` before exposing the public listener. Public host routing fails closed by default, and only configured hosts such as `*.example.com` or exact hostnames are eligible to map to a site.

The important deployment detail is that the CLI talks to the admin/control port, while browsers normally talk to the public port.

## CLI

The CLI binary is `quack-cli`.

### Log in

```bash
quack-cli login
```

Or pass values directly:

```bash
quack-cli login \
  --serverURL http://localhost:8081 \
  --token <token>
```

Login stores config in:

```text
~/.config/quack.json
```

Set `QUACK_CONFIG` to use a different config path.

### Deploy

```bash
quack-cli deploy <directory> [site name]
```

Examples:

```bash
quack-cli deploy ./dist mysite
quack-cli deploy ./mysite
```

When the directory is a simple relative name, Quack can infer the site name from it. For paths like `.` or `./dist`, pass the site name explicitly.

The deploy command streams a tar archive directly to the server. It does not write a temporary archive to disk.

### List sites

```bash
quack-cli sites
```

List every site visible to an admin:

```bash
quack-cli sites --all
```

List sites for a specific user:

```bash
quack-cli sites <username>
```

### Revisions and rollback

Show revisions:

```bash
quack-cli revisions mysite
```

Roll back to the previous version:

```bash
quack-cli rollback mysite
```

### Publish and unpublish

Unpublish a site without deleting its stored revisions:

```bash
quack-cli unpublish mysite
```

Publish it again:

```bash
quack-cli publish mysite
```

### Default site

Set a fallback static site:

```bash
quack-cli default-site homepage
```

Clear the default site:

```bash
quack-cli default-site --clear
```

Default-site fallback only applies when the requested site does not exist. It does not replace missing files on an existing site, and it does not currently apply to runtime routes.

### Delete

```bash
quack-cli delete mysite
```

Delete removes the site and its stored data. Use unpublish when you only want to take a site offline temporarily.

### CLI admin of secrets

Secrets are managed through the admin/control API. Use an admin or site-capable token the same way you do for deploys:

```bash
quack-cli login \
  --serverURL http://localhost:8081 \
  --token <admin-or-user-token>
```

Create or replace a site-scoped secret:

```bash
quack-cli secrets set mysite STRIPE_API_KEY sk_live_...
```

The default scope is `site`, so this is equivalent:

```bash
quack-cli secrets set mysite STRIPE_API_KEY sk_live_... --scope site
```

List secrets visible to the current token:

```bash
quack-cli secrets list
quack-cli secrets list mysite
```

The list output shows metadata only:

```text
SCOPE  SITE    NAME            CREATED              UPDATED
site   mysite  STRIPE_API_KEY  2026-06-25 13:00:00  2026-06-25 13:00:00
```

It does not print decrypted values.

Delete a secret:

```bash
quack-cli secrets delete mysite STRIPE_API_KEY
quack-cli secrets delete mysite STRIPE_API_KEY --scope site
```

The site must already exist. Non-admin users can only manage secrets for sites they are allowed to access. The secrets store must also be unlocked; when it is locked, set/delete/read operations fail instead of silently returning decrypted values.

There is also a `user` scope in the storage/API model:

```bash
quack-cli secrets set mysite PERSONAL_TOKEN value --scope user
```

That scope is for user-owned secret records. Current Starlark runtime access is site-scoped, so application code should use `site` secrets unless a future runtime supplies authenticated user context.

## Project layout

A minimal Quack site can be just this:

```text
dist/
  index.html
  app.css
  app.js
```

Deploy it:

```bash
quack-cli deploy ./dist mysite
```

For routing, add a `site.yml` or `site.yaml` at the root of the uploaded directory:

```text
my-app/
  site.yml
  public/
    index.html
    app.css
    app.js
  api/
    app.star
    socket.star
```

Example `site.yml`:

```yaml
routes:
  - path: /
    kind: static
    root: public

  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    methods: [GET, POST]

  - path: /ws
    kind: websocket
    runtime: starlark
    entrypoint: api/socket.star
```

The manifest file is consumed during upload and is not served as a public file.

## Routing

Quack routes public requests by hostname first.

For a request like this:

```text
https://foo.example.com/
```

Quack derives the site name:

```text
foo
```

Examples:

```text
foo.example.com       -> foo
www.foo.example.com   -> foo
foo:8080              -> foo
foo.                  -> foo
```

The path does not normally select the site. Quack is host-centric.

After Quack knows the site name, it looks at the current release for that site and chooses the best route. Route matching uses longest prefix wins.

Example:

```yaml
routes:
  - path: /
    kind: static
    root: public

  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star

  - path: /api/admin
    kind: http
    runtime: starlark
    entrypoint: api/admin.star
```

Matching behavior:

```text
/                 -> / static route
/about            -> / static route
/api              -> /api HTTP route
/api/users        -> /api HTTP route
/api/admin        -> /api/admin HTTP route
/api/admin/users  -> /api/admin HTTP route
```

### Static routes

A static route exposes uploaded files.

```yaml
routes:
  - path: /
    kind: static
    root: public
```

With `root: public`, this request:

```text
/
```

serves:

```text
public/index.html
```

And this request:

```text
/app.css
```

serves:

```text
public/app.css
```

The static root is not a URL prefix. It is the archive subtree that becomes the public root. So this request:

```text
/public/app.css
```

looks for:

```text
public/public/app.css
```

Static routes can also expose one exact file as a public alias:

```yaml
routes:
  - path: /favicon.ico
    kind: static
    file: media/favicon.ico
```

That serves `media/favicon.ico` at `/favicon.ico`.

### Directory indexes

Quack follows ordinary directory-index behavior:

```text
/              -> index.html
/index.html    -> index.html
/docs/         -> docs/index.html
```

If `/docs` has no exact file but `docs/index.html` exists, Quack redirects to:

```text
/docs/
```

### HTTP routes

HTTP routes run Starlark:

```yaml
routes:
  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    methods: [GET, POST]
```

The route path is a prefix. A request to `/api/todos` is handled by the `/api` route, and Starlark receives the route-relative path.

### WebSocket routes

WebSocket routes also use longest-prefix matching:

```yaml
routes:
  - path: /ws
    kind: websocket
    runtime: starlark
    entrypoint: api/socket.star
```

A request to `/ws/room/123` matches the `/ws` route, and Starlark sees:

```text
/room/123
```

## Starlark HTTP routes

Starlark routes are small server-side scripts. They are good for JSON endpoints, form handlers, counters, tiny APIs, and small app backends.

A Starlark HTTP route defines `handle(req)`:

```python
def handle(req):
    method, path, query, headers, body = req

    count = memory.incr("counter:hits")

    return (
        200,
        {"content-type": "application/json"},
        json.encode({
            "ok": True,
            "path": path,
            "hits": count,
        }),
    )
```

The request is a tuple:

```python
(method, path, query, headers, body)
```

Where:

```text
method   HTTP method
path     route-relative path
query    raw query string
headers  sanitized request headers
body     request body bytes
```

A handler returns:

```python
(status, headers, body)
```

Where:

```text
status   HTTP status code
headers  response headers
body     string, bytes, or None
```

Quack filters sensitive and hop-by-hop headers at the runtime boundary. Public requests do not hand raw cookies, authorization headers, forwarding headers, or transport headers directly to Starlark.

### Available Starlark modules

HTTP and WebSocket routes receive host-provided modules, including:

```python
json
request
uuid
memory
log
http
secret
```

If the server is started with the hardware plugin enabled, HTTP and WebSocket routes also receive:

```python
camera
```

HTTP routes can also opt into a read-only uploaded-file view with `filesystem`:

```yaml
routes:
  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    filesystem:
      root: data
```

Then the route can use `fs`:

```python
def handle(req):
    files = fs.listdir("/")
    text = fs.read("message.txt")

    return (
        200,
        {"content-type": "application/json"},
        json.encode({
            "files": files,
            "message": text,
        }),
    )
```

The filesystem module is read-only and scoped to the configured uploaded subtree.

## Camera hardware module in Starlark

The `camera` module is only installed when `quack-server` is started with `-hardware-plugin`. If that boot flag is omitted, Starlark code cannot access the module at all.

The current hardware plugin supports Linux UVC cameras through the kernel Video4Linux2 API. It does not use cgo, FFmpeg, libuvc, or shell commands. On macOS and other non-Linux systems the plugin binary can build, but camera operations return an unsupported-platform error.

A site may request logical camera capabilities in `site.yml`, but `site.yml` never grants access to physical hardware and must not contain host paths such as `/dev/video0`.

```yaml
capabilities:
  camera:
    front_door:
      required: false
      permissions:
        capture:
          roles: ["admin", "staff"]
      limits:
        max_width: 640
        max_height: 480
```

Policy still applies after the boot flag is enabled. The server must allow the `hardware.camera` capability for the site, and the admin UI must bind the device to a site. Unbound devices are stored as host inventory but are not enumerated for any site.

```yaml
devices:
  - id: cam_01
    kind: uvc-camera
    path: /dev/video2
    label: "Front desk Logitech C270"
    site: acme
```

Example:

```python
def handle(req):
    frame = camera.capture("cam_01", width=640, height=480, format="MJPG")
    return (
        200,
        {"content-type": frame["mime_type"]},
        frame["data"],
    )
```

Available functions:

```python
camera.list()
camera.capture(alias, width=640, height=480, format="MJPG")
```

`camera.list()` returns only the current site's logical assignments with fields such as `id`, `alias`, `kind`, `label`, `permissions`, `limits`, and `formats`. It does not expose host paths, USB descriptors, bus topology, or plugin process details.

`camera.capture(...)` accepts the site alias, such as `cam_01`, and returns a dictionary containing `id`, `mime_type`, raw byte `data`, base64 text, width, height, format, and device kind. The host resolves `site + alias` through the admin binding and only then calls the hardware plugin with the physical device path.

## Secrets module in Starlark

Every Starlark HTTP and WebSocket route receives a `secret` module.

A site does not declare secrets in `site.yml`. Secrets are operational state managed through the admin/control plane, not deployment state. They are not uploaded with the site archive, not served as static files, and not versioned with releases.

Use the module like this:

```python
def handle(req):
    if not secret.unlocked():
        return (
            503,
            {"content-type": "application/json"},
            json.encode({"error": "secrets are locked"}),
        )

    api_key = secret.get("site", "STRIPE_API_KEY")

    return (
        200,
        {"content-type": "application/json"},
        json.encode({
            "configured": True,
        }),
    )
```

Available functions:

```python
secret.unlocked()
secret.exists(scope, name)
secret.get(scope, name)
```

Behavior:

```text
secret.unlocked()          -> True when the store is configured and unlocked.
secret.exists("site", n)   -> True when the named site secret exists and the store is unlocked.
secret.get("site", n)      -> decrypted string value, or an error.
```

`secret.get` returns a string. It is not automatically redacted. Do not log it, return it to the browser, store it in `memory`, or include it in WebSocket payloads unless that is explicitly the behavior you want.

Secrets are scoped to the current site. A route running for `foo` reads `foo` secrets; it cannot ask for `bar` secrets. Different routes in the same site share the same site-scoped secrets, and different versions of the same site also see the same current secret values.

Failure behavior is intentionally explicit:

```text
No secret store configured       secret.unlocked() is False; secret.exists(...) is False; secret.get(...) errors.
Store locked                     secret.unlocked() is False; secret.exists(...) is False; secret.get(...) errors.
Secret missing                   secret.exists(...) is False; secret.get(...) errors.
Invalid scope or blank name      secret.exists(...) and secret.get(...) error.
```

For runtime code today, use the `site` scope:

```python
token = secret.get("site", "GITHUB_TOKEN")
```

`user`-scoped secrets exist in the admin/API model, but Starlark runtime routes do not currently receive authenticated user context, so `secret.get("user", "...")` fails rather than guessing which user's secret should be used.

## Memory module

Every Starlark HTTP and WebSocket route gets a `memory` module.

Memory is a small site-scoped store. It is useful for counters, UI state, presence, leaderboards, recent events, small documents, and demo data.

It is not Redis. It is intentionally smaller than Redis. It is also much easier to carry around in a tiny app server.

Memory is scoped by site name:

```text
site "foo" memory != site "bar" memory
```

Different routes in the same site share memory. Different deployed versions of the same site also share memory.

### Memory quota

The default per-site memory quota is 32 MiB.

```python
memory.usage()
memory.quota()
```

Writes that would exceed the quota fail by returning `False` and leave the old value untouched.

### Key-value storage

```python
memory.get("name", "anonymous")
memory.set("name", "Quack")
memory.delete("name")
```

Inspect memory:

```python
memory.type("name")
memory.keys()
memory.items()
memory.clear()
```

### Counters

```python
memory.incr("hits")
memory.incr("hits", 5)
memory.decr("hits")
```

Counters are signed 64-bit integers.

### Lists

```python
memory.list_push("events", {"type": "join"})
memory.list_push("events", {"type": "leave"}, side = "left")

memory.list_pop("events")
memory.list_len("events")
memory.list_range("events", -10, -1)
```

List ranges are inclusive and support negative indexes.

### Sets

```python
memory.set_add("online", "alice")
memory.set_remove("online", "alice")
memory.set_contains("online", "alice")
memory.set_members("online")
```

### Sorted sets

```python
memory.zadd("leaderboard", 42.0, "alice")
memory.zscore("leaderboard", "alice")
memory.zrange("leaderboard", 0, 9, with_scores = True)
memory.zremove("leaderboard", "alice")
```

Sorted sets are useful for leaderboards, rankings, priority queues, and “top N” lists.

### Example

```python
def handle(req):
    count = memory.incr("counter:hits")
    memory.list_push("events", {
        "type": "hit",
        "count": count,
    })
    memory.zadd("leaderboard", float(count), "latest")

    return (
        200,
        {"content-type": "application/json"},
        json.encode({
            "count": count,
            "recent": memory.list_range("events", -5, -1),
            "leaders": memory.zrange("leaderboard", 0, 9, with_scores = True),
            "usage": memory.usage(),
            "quota": memory.quota(),
        }),
    )
```

### Persistence

Memory is process-local runtime state. By default, it is not persisted.

Quack can optionally save memory snapshots. Enable this from the admin settings by setting memory persistence mode to `snapshot`.

Snapshot persistence is useful for small durable state, but it is not a replicated database. It does not make WebSocket events cross-node, and it does not turn Quack memory into a clustered service.

For small homelab and single-server deployments, that is often exactly the right tradeoff.

## WebSockets

Quack has a Starlark-based WebSocket runtime for small realtime apps.

The design rule is:

```text
Go owns sockets, connection state, subscriptions, event routing, queues, and timers.

Starlark handles one event at a time and returns declarative effects.
```

Starlark does not receive a raw socket. It does not install long-running callbacks. It does not keep a live listener around.

Instead, Starlark returns effects like:

```python
ws.send(...)
ws.subscribe(...)
events.publish(...)
```

The Go host validates and applies those effects.

### Declare a WebSocket route

```yaml
routes:
  - path: /ws
    kind: websocket
    runtime: starlark
    entrypoint: api/socket.star
```

Dynamic WebSocket routes are policy-gated. An administrator must allow dynamic WebSocket routes before sites can deploy or execute them.

### Handler shape

A WebSocket Starlark file can define any of these functions:

```python
def on_connect(ctx):
    return []

def on_message(ctx, msg):
    return []

def on_event(ctx, event):
    return []

def on_disconnect(ctx):
    return []
```

All handlers are optional. Missing handlers are treated as no-ops.

Each handler returns one of:

```text
None
a single effect
a list of effects
a tuple of effects
```

### Context

Handlers receive a `ctx` object:

```python
ctx.site
ctx.version
ctx.route
ctx.path
ctx.query
ctx.headers
ctx.conn_id
ctx.params
ctx.user
```

### Messages

When a client sends a message:

* JSON messages become Starlark values.
* Non-JSON messages become strings.
* Empty messages become `None`.

For example, this client message:

```json
{"type":"edit","doc_id":"123","content":"hello"}
```

can be read in Starlark as:

```python
msg["type"]
msg["doc_id"]
msg["content"]
```

### Effects

Send to one connection:

```python
ws.send(ctx.conn_id, {"type": "ready"})
```

Subscribe a connection to a topic:

```python
ws.subscribe(ctx.conn_id, "doc:123")
```

Broadcast to a topic:

```python
ws.broadcast("doc:123", {"type": "changed"})
```

Publish an event:

```python
events.publish("doc:123", {
    "type": "changed",
    "doc_id": "123",
})
```

Close a connection:

```python
ws.close(ctx.conn_id, code = 1000, reason = "bye")
```

Unsubscribe:

```python
ws.unsubscribe(ctx.conn_id, "doc:123")
ws.unsubscribe_all(ctx.conn_id)
```

### Example: collaborative document updates

```python
def on_connect(ctx):
    doc_id = ctx.path.strip("/") or "default"
    topic = "doc:" + doc_id

    return [
        ws.subscribe(ctx.conn_id, topic),
        ws.send(ctx.conn_id, {
            "type": "ready",
            "doc_id": doc_id,
        }),
    ]

def on_message(ctx, msg):
    if msg["type"] == "edit":
        doc_id = msg["doc_id"]

        memory.set("doc:" + doc_id, msg["content"])

        return [
            events.publish("doc:" + doc_id, {
                "type": "document_updated",
                "doc_id": doc_id,
                "content": msg["content"],
            }),
        ]

    return []

def on_event(ctx, event):
    if event.payload["type"] == "document_updated":
        return [
            ws.send(ctx.conn_id, event.payload),
        ]

    return []

def on_disconnect(ctx):
    return [
        ws.unsubscribe_all(ctx.conn_id),
    ]
```

`events.publish` is a local live event. It is not durable, not replayed, and not cross-node. It reaches currently connected local subscribers.

### Back pressure

Quack protects the host from slow clients.

Each connection has a bounded outbound queue. If a client cannot keep up, Quack can close and unregister that client instead of letting one slow socket block a broadcast or Starlark invocation.

Broadcast delivery is best-effort.

## Admin panel

The admin panel runs on the admin listener, separate from public site traffic.

By default:

```text
public:  :8080
admin:   :8081
```

Open the admin panel:

```text
http://localhost:8081/
```

The admin panel lets you:

* View sites
* See current version, file count, byte count, memory usage, and WebSocket count
* Publish and unpublish sites
* Roll back versions
* Delete sites
* Create users
* Save generated user tokens
* Change server settings
* Configure policies for dynamic features

## Admin UI secret management

The Admin UI has a **Secrets** page on the admin listener, available only to signed-in administrators.

The page manages the lifecycle of the encrypted secret store:

* `not created` means there is no root key yet.
* `locked` means the encrypted root key exists in the database, but the server process has not unlocked it.
* `unlocked` means the server process has the decrypted root key in memory and can encrypt/decrypt individual secret values.

On first setup, an admin creates the unlock password. Quack generates a random root key, encrypts that root key with the password-derived key, stores the encrypted root key in SQLite, and keeps the decrypted root key in process memory.

After a server restart, the encrypted root key is still stored, but the process starts locked again. An admin unlocks it from the Admin UI by entering the password. While locked, Starlark can detect that secrets are unavailable with `secret.unlocked()`, and direct secret reads fail.

When the store is unlocked, the Admin UI can reset the unlock password. Resetting the password re-wraps the same root key with a new password; it does not rewrite every stored secret.

The current Admin UI does not edit individual site secret values. Use the CLI for `set`, `list`, and `delete`. The UI is for root-key creation, unlock, status, and password reset.

Keep the admin listener private. Secret management is intentionally on the admin/control plane, not the public site listener.

### Why the admin panel is on a different port

The public port serves user sites.

The admin port can create users, accept uploads, delete sites, change settings, and enable dynamic code execution.

Those are very different security surfaces.

By keeping them on separate listeners, you can expose public sites to the internet while keeping admin behind something stricter:

```text
public port -> internet
admin port  -> localhost, VPN, private network, tunnel auth, or firewall
```

For production, do not casually expose the admin port to the public internet.

## Server configuration

Start `quack-server` with:

```bash
quack-server \
  -root /var/lib/quack/blobs \
  -database /var/lib/quack/quack.sqlite \
  -memory-dir /var/lib/quack/memory
```

Required flags:

```text
-root       blob storage directory
-database   SQLite database path
```

Optional flags:

```text
-memory-dir              memory snapshot directory
-allow-unauthenticated   allow unauthenticated /v1 API access; development only
-hardware-plugin         path to hardware plugin executable; disabled when empty
```

Environment variables:

```text
PUBLIC_ADDR   public listener, default :8080
ADMIN_ADDR    admin listener, default :8081
```

If `-memory-dir` is omitted, Quack stores memory snapshots beside the database under a `memory` directory.

To enable hardware access, build or install the plugin executable and pass it at server boot:

```bash
quack-server \
  -root /var/lib/quack/blobs \
  -database /var/lib/quack/quack.sqlite \
  -hardware-plugin /usr/local/bin/quack-hardware-plugin
```

This is a hard boot gate. Without `-hardware-plugin`, hardware-backed Starlark modules are not registered, regardless of site manifest, admin UI device bindings, or policy settings.

## Important server settings

Most operational settings can be managed from the admin panel.

| Setting                            | What it does                                                        |
| ---------------------------------- | ------------------------------------------------------------------- |
| Max upload bytes                   | Maximum size of an uploaded archive.                                |
| Max upload files                   | Maximum number of regular files in an upload.                       |
| Max retained versions              | How many old versions to keep. `0` means no pruning.                |
| Default site                       | Static fallback site when a requested site does not exist.          |
| Allowed hosts                      | Hostname allowlist for public requests. Empty means allow any host. |
| Log level                          | Server log verbosity.                                               |
| HTTP cache mode                    | Static file cache behavior.                                         |
| HTTP cache max age seconds         | Max-age value when using max-age cache mode.                        |
| Max runtime duration ms            | Runtime execution time limit.                                       |
| Max WebSocket connections          | Total live WebSocket connection limit.                              |
| Max WebSocket connections per site | Per-site WebSocket connection limit.                                |
| Memory persistence mode            | `off` or `snapshot`.                                                |
| Memory snapshot save rules         | Rules for when dirty memory should be snapshotted.                  |
| Memory snapshot min interval ms    | Minimum time between memory snapshots.                              |
| Memory snapshot max concurrency    | Maximum concurrent memory snapshot writes.                          |
| Memory shutdown flush timeout ms   | Flush timeout during shutdown.                                      |

### Allowed hosts

In production, set an allowed-hosts list.

Example:

```text
example.com
*.example.com
```

This prevents Quack from responding to arbitrary hostnames pointed at the same server.

Allowed hosts are hostnames only. Do not include schemes, ports, or paths.

Use:

```text
*.example.com
```

not:

```text
https://*.example.com:443/
```

### Cache modes

Quack supports three static HTTP cache modes.

`revalidate` asks browsers and CDNs to revalidate before reuse. This is a safe default for frequently updated small sites.

`anti_cache` aggressively disables caching.

`max_age` sets a public max-age using `HTTP cache max age seconds`.

For hashed frontend assets, `max_age` can be useful. For experimental apps that change constantly, `anti_cache` is usually easier.

### Runtime policies

Dynamic HTTP routes and dynamic WebSocket routes are policy-gated.

That means an uploaded `site.yml` can declare runtime routes, but Quack will reject or deny them unless policy allows the required capability.

The major dynamic policies are:

```text
Dynamic HTTP routes
Dynamic WebSocket routes
Database feature
Camera hardware
```

Each policy can be set to:

```text
allow
deny
```

with an optional reason.

By default, keep dynamic features denied until you explicitly need them.

## Deploying Quack

A typical production shape looks like this:

```text
internet
  -> reverse proxy / tunnel / load balancer
  -> quack-server public port

private admin access
  -> quack-server admin port
```

For example:

```text
*.example.com        -> quack public listener
quack-admin.internal -> quack admin listener
```

The public proxy must preserve the original `Host` header, because Quack uses it to derive the site name.

```text
foo.example.com -> foo
bar.example.com -> bar
```

### Files to persist

Persist these together:

```text
SQLite database
blob storage root
memory snapshot directory, if enabled
```

For example:

```text
/var/lib/quack/quack.sqlite
/var/lib/quack/blobs/
/var/lib/quack/memory/
```

Back them up as one unit. The database knows which blobs and versions exist.

### Example systemd service

```ini
[Unit]
Description=Quack app server
After=network-online.target
Wants=network-online.target

[Service]
User=quack
Group=quack
Environment=PUBLIC_ADDR=:8080
Environment=ADMIN_ADDR=127.0.0.1:8081
ExecStart=/usr/local/bin/quack-server \
  -root /var/lib/quack/blobs \
  -database /var/lib/quack/quack.sqlite \
  -memory-dir /var/lib/quack/memory
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
```

This example binds the admin listener to localhost and the public listener to all interfaces.

### Example container run

For a container image that includes `quack-server`:

```bash
docker run --rm \
  -p 8080:8080 \
  -p 127.0.0.1:8081:8081 \
  -e PUBLIC_ADDR=:8080 \
  -e ADMIN_ADDR=:8081 \
  -v quack-data:/var/lib/quack \
  quack-server:dev \
  -root /var/lib/quack/blobs \
  -database /var/lib/quack/quack.sqlite \
  -memory-dir /var/lib/quack/memory
```

This exposes the public port normally and binds the admin port only on localhost.

## Maintaining a Quack server

### Keep the admin plane private

The admin listener is where uploads, deletes, users, settings, and policies live. Treat it like infrastructure, not like a public website.

Good options:

```text
localhost only
VPN only
private network only
identity-aware proxy
SSH tunnel
Cloudflare Access or similar
```

### Back up state

Back up:

```text
database
blob root
memory snapshots
```

Do not back up only the blobs. Do not back up only the database.

### Prune old versions when needed

Use `Max retained versions` to stop old deployments from growing forever.

Use rollback-friendly retention for important sites. Use smaller retention for throwaway demos.

### Be intentional with dynamic code

Static hosting is the safest Quack mode.

Starlark HTTP and WebSocket routes are more powerful. Enable them when you want dynamic behavior, and use policy settings to keep that power scoped.

### Watch memory usage

Memory is meant for small app state, not large datasets.

Use:

```python
memory.usage()
memory.quota()
```

inside routes when building dashboards or debugging state.

If a site needs a real database, give it a real database. (Although this will be in a future release.)  Quack memory is for tiny state that benefits from being close to the app--you can do some really cool stuff with it. (Check out demos/pixeldraw.)

### Remember that realtime is local

WebSocket subscriptions and event delivery are local to the running server process.

`events.publish` is not a durable queue. It is not replayed. It is not cross-node.

That is fine for a single-server homelab app. It is not the same as a distributed realtime backend.

### Preserve the public/admin split

When changing Quack or deploying it in a new environment, keep this boundary:

```text
public traffic -> sites and runtime
admin traffic  -> control, users, settings, uploads
```

That split is one of Quack’s most important safety features.

## `site.yml`

`site.yml` is Quack’s upload manifest. Put it at the root of the directory you deploy:

```text
my-app/
  site.yml
  public/
    index.html
    app.css
  api/
    app.star
    socket.star
```

Quack also accepts `site.yaml`. The manifest is read during upload and is not served as a public file.

A minimal static site does not need a manifest at all. Without `site.yml`, Quack serves uploaded files from the upload root. Add `site.yml` when you want to choose a public static root, define runtime routes, expose a read-only filesystem to Starlark, or declare feature requirements.

A typical manifest looks like this:

```yaml
routes:
  - path: /
    kind: static
    root: public

  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    methods: [GET, POST]
    filesystem:
      root: data

  - path: /ws
    kind: websocket
    runtime: starlark
    entrypoint: api/socket.star

exclude:
  - node_modules/
  - .git/
  - "*.log"
```

Quack validates `site.yml` strictly. Unknown fields fail the upload. This is intentional: typos should break loudly instead of silently deploying something different from what you meant.

### Top-level fields

The supported top-level fields are:

```yaml
routes: []
exclude: []
features:
  database:
    enabled: false
    required: false
```

### `routes`

`routes` tells Quack how public requests should map to static files, Starlark HTTP handlers, and Starlark WebSocket handlers.

Each route has this shape:

```yaml
routes:
  - path: /
    kind: static
    root: public
```

Supported route fields:

| Field        | Used by             | Meaning                                                              |
| ------------ | ------------------- | -------------------------------------------------------------------- |
| `path`       | all routes          | Required public route prefix.                                        |
| `kind`       | all routes          | `static`, `http`, or `websocket`. Defaults to `static` when omitted. |
| `root`       | static only         | Uploaded directory to expose at this route.                          |
| `file`       | static only         | Uploaded file to expose at exactly this route path.                  |
| `runtime`    | HTTP/WebSocket only | Currently only `starlark`.                                           |
| `entrypoint` | runtime routes      | Starlark file to execute. Required when `runtime` is set.            |
| `methods`    | HTTP routes         | Optional list of allowed HTTP methods. Empty means all methods.      |
| `expose_errors` | runtime routes   | Optional. Return Starlark errors to clients when true. Defaults false. |
| `filesystem` | Starlark HTTP only  | Optional read-only uploaded-file access for the route.               |

Route paths are normalized. `api` and `/api` both behave like `/api`. Matching uses longest prefix wins.

```yaml
routes:
  - path: /
    kind: static
    root: public

  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star

  - path: /api/admin
    kind: http
    runtime: starlark
    entrypoint: api/admin.star
```

Request behavior:

```text
/                 -> / static route
/about            -> / static route
/api              -> /api HTTP route
/api/users        -> /api HTTP route
/api/admin        -> /api/admin HTTP route
/api/admin/users  -> /api/admin HTTP route
/apiary           -> / static route
```

`/apiary` does not match `/api`; a route matches the exact path or paths underneath it.

### Static routes

Static routes serve uploaded files.

```yaml
routes:
  - path: /
    kind: static
    root: public
```

With this archive:

```text
site.yml
public/index.html
public/app.css
public/docs/index.html
data/private.json
```

Requests behave like this:

```text
/              -> public/index.html
/app.css       -> public/app.css
/docs/         -> public/docs/index.html
/data/private.json -> usually 404
/public/app.css    -> usually 404
```

`root` is the uploaded directory that becomes public. It is not part of the URL.

So this:

```yaml
routes:
  - path: /
    kind: static
    root: public
```

means:

```text
URL /app.css -> uploaded file public/app.css
```

not:

```text
URL /public/app.css -> uploaded file public/app.css
```

`root` must be a relative archive path. Absolute paths and `..` are rejected.

A static route can also expose one exact file:

```yaml
routes:
  - path: /favicon.ico
    kind: static
    file: media/favicon.ico
```

That serves:

```text
/favicon.ico -> media/favicon.ico
```

It does not match paths underneath it:

```text
/favicon.ico/details -> not this file route
```

A static route may use `root` or `file`, but not both.

### Static routes under a prefix

Static routes do not have to be mounted at `/`.

```yaml
routes:
  - path: /
    kind: static
    root: public

  - path: /assets
    kind: static
    root: build/assets
```

Then:

```text
/                 -> public/index.html
/about            -> public/about or public/about/index.html
/assets/app.js    -> build/assets/app.js
/assets/icons/x.svg -> build/assets/icons/x.svg
```

### HTTP Starlark routes

HTTP routes run a Starlark file for matching requests.

```yaml
routes:
  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    methods: [GET, POST]
```

The entrypoint is relative to the upload root and must exist in the deployed directory.
By default, Starlark failures return a generic 500 response to clients and log the detailed error on the server. Set `expose_errors: true` on a runtime route only when you want clients to see the Starlark error text.

A route at `/api` matches:

```text
/api
/api/todos
/api/todos/123
```

Inside Starlark, the handler sees the path under the route. For example, a request to `/api/todos/123` under the `/api` route is exposed as `/todos/123`.

`methods` is optional:

```yaml
routes:
  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    methods: [GET, POST]
```

If `methods` is omitted or empty, all methods are allowed by the route declaration.

Dynamic HTTP routes are policy-gated. The server administrator must allow dynamic HTTP runtime routes before sites can deploy or execute them.

### Starlark HTTP filesystem access

By default, a Starlark route executes its entrypoint but does not get open access to all uploaded files. To give a route read-only file access, add a `filesystem` block:

```yaml
routes:
  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    filesystem:
      root: data
```

With this upload:

```text
site.yml
api/app.star
data/profile.json
data/messages/welcome.txt
```

the Starlark route can read files through the `fs` module:

```python
def handle(req):
    profile = fs.read("profile.json")
    welcome = fs.read("messages/welcome.txt")

    return (200, {"content-type": "application/json"}, profile)
```

`filesystem.root` is relative to the upload root. A root of `data` exposes the uploaded `data/` directory as `/` inside the Starlark filesystem.

These are equivalent ways to expose the whole uploaded archive:

```yaml
filesystem:
  root: .
```

```yaml
filesystem:
  root: /
```

```yaml
filesystem:
  root: ""
```

Paths are still sandboxed. Leading slashes are normalized away, `..` traversal is rejected, and host filesystem paths are never exposed.

The `filesystem` block is currently only supported for Starlark HTTP routes, not static routes or WebSocket routes.

### WebSocket Starlark routes

WebSocket routes declare a Starlark entrypoint that handles socket lifecycle events.

```yaml
routes:
  - path: /ws
    kind: websocket
    runtime: starlark
    entrypoint: api/socket.star
```

A route at `/ws` matches:

```text
/ws
/ws/room/123
/ws/room/abc
```

Inside Starlark, the path is route-relative. For example:

```text
/ws/room/123 -> /room/123
```

A WebSocket Starlark file can define:

```python
def on_connect(ctx):
    return []

def on_message(ctx, msg):
    return []

def on_event(ctx, event):
    return []

def on_disconnect(ctx):
    return []
```

All handlers are optional.

Dynamic WebSocket routes are policy-gated separately from HTTP routes. The server administrator must allow dynamic WebSocket runtime routes before sites can deploy or execute them.

### `exclude`

`exclude` removes files from the upload before Quack stores them.

```yaml
exclude:
  - node_modules/
  - .git/
  - dist/**/*.map
  - "*.log"
```

Exclude patterns are relative to the upload root. Absolute patterns are rejected. Patterns cannot contain `..`.

A pattern with a slash matches against the uploaded path:

```yaml
exclude:
  - private/*.json
```

A pattern without a slash matches by basename:

```yaml
exclude:
  - "*.log"
```

Directory-style patterns exclude the tree:

```yaml
exclude:
  - node_modules/
  - .git/
```

The manifest itself is never excluded by `exclude`, because Quack needs it during upload and does not serve it afterward.

### `features`

The currently supported top-level feature declarations are `features.database` and `features.camera`.

```yaml
features:
  database:
    enabled: true
    required: false
  camera:
    enabled: true
    required: true
```

This declares that the site wants the database and legacy camera feature. Policy determines whether those capabilities are allowed.

`required` controls how Quack treats policy denial:

```yaml
features:
  database:
    enabled: true
    required: true
  camera:
    enabled: true
    required: true
```

If `required` is true, the feature must be allowed for the site to serve correctly. `required: true` is invalid unless `enabled: true` is also set.

Most small Quack apps do not need this block. The Starlark `memory` module does not require `features.database`; memory is its own runtime module. New camera-aware apps should prefer `capabilities.camera` declarations with logical aliases; camera access is only useful when the server was also booted with `-hardware-plugin` and an admin has bound a device to the site.

### Complete examples

#### Static app with a public root

```yaml
routes:
  - path: /
    kind: static
    root: public
```

Good for:

```text
public/index.html
public/assets/app.js
public/assets/app.css
```

Deploy with:

```bash
quack-cli deploy ./my-app mysite
```

#### Static frontend plus Starlark API

```yaml
routes:
  - path: /
    kind: static
    root: public

  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    methods: [GET, POST]
```

Good for:

```text
public/index.html
public/app.js
api/app.star
```

Request behavior:

```text
/              -> static
/app.js        -> static
/api           -> Starlark
/api/todos     -> Starlark
```

#### Static frontend plus API plus WebSocket

```yaml
routes:
  - path: /
    kind: static
    root: public

  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    methods: [GET, POST]

  - path: /ws
    kind: websocket
    runtime: starlark
    entrypoint: api/socket.star
```

Good for small realtime apps where the frontend is static, the API is Starlark, and live updates happen through WebSockets.

#### Static app with private data files for Starlark

```yaml
routes:
  - path: /
    kind: static
    root: public

  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    filesystem:
      root: data

exclude:
  - node_modules/
  - .git/
  - "*.log"
```

With this layout:

```text
public/index.html
api/app.star
data/config.json
data/messages/welcome.txt
```

The `public/` directory is served to browsers. The `data/` directory is not public through the static route, but the `/api` Starlark route can read it through `fs`.

### Common mistakes

Do not use the old top-level `static` shape:

```yaml
static:
  root: public
```

Use route-level static roots instead:

```yaml
routes:
  - path: /
    kind: static
    root: public
```

Do not put `root` on an HTTP or WebSocket route:

```yaml
routes:
  - path: /api
    kind: http
    root: public
```

Use `filesystem` for Starlark file access:

```yaml
routes:
  - path: /api
    kind: http
    runtime: starlark
    entrypoint: api/app.star
    filesystem:
      root: data
```

Do not set both `root` and `file` on the same static route:

```yaml
routes:
  - path: /favicon.ico
    kind: static
    root: public
    file: media/favicon.ico
```

Choose one:

```yaml
routes:
  - path: /favicon.ico
    kind: static
    file: media/favicon.ico
```

Do not assume `/apiary` matches `/api`. Route prefix matching is path-segment aware:

```text
/api/users -> matches /api
/apiary    -> does not match /api
```

Do not include absolute paths or `..` in manifest paths:

```yaml
root: /var/www
entrypoint: ../app.star
filesystem:
  root: ../data
```

Manifest paths should be relative to the uploaded directory.

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

Set the server `allowed_hosts` setting to the same trusted tenant suffix, for example `*.example.com`. Requests for hosts outside that list are rejected with `421 Misdirected Request` instead of being interpreted as site names.

## Roadmap

Quack’s roadmap has two moods.

The fun mood:

* More small-app primitives
* More realtime helpers
* Better local-first patterns
* GPIO experiments
* Weird homelab integrations
* Strange indie-web toys

The boring mood:

* RBAC
* Auth improvements
* Better security policies
* Better multi-user deployment controls
* More locked-down admin workflows
* Safer defaults for semi-public servers

Both moods matter.

Quack should stay small, but it should not stay timid.
