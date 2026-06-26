# UVC Hardware Support

## Overview

Quack exposes USB Video Class cameras to site code through a narrow, site-scoped `camera` Starlark module.

The important design rule is:

```text
Starlark never receives raw USB, V4L2, file descriptors, or /dev paths.
```

Instead, an administrator registers a physical camera on the admin side, optionally binds it to a site, and gives that site a stable alias. Site code can then call:

```python
camera.list()
camera.capture(id = "front_desk")
```

The server resolves that alias to an administrator-managed device path and delegates the actual hardware access to a separate hardware plugin process.

This keeps native device access out of the public request/runtime process and gives the host a single place to enforce site isolation, permissions, and capture limits.

## Architecture

### Components

```text
quack-server
  admin listener
    /hardware admin UI
    SQLite hardware device registry

  public listener
    static routes
    Starlark HTTP routes
    Starlark WebSocket routes

  runtime executor
    injects camera module when hardware is configured

  hardware bound service
    maps site + alias -> physical device descriptor
    enforces capture permission
    clamps width/height
    enforces max capture byte limit

quack-hardware-plugin
  separate go-plugin subprocess
  owns native Linux UVC / V4L2 access
  exposes ListDevices and Capture over net/rpc

Linux host
  /dev/video*
  /dev/v4l/by-id/*
  /dev/v4l/by-path/*
```

### Request flow

For a Starlark camera capture:

```text
browser
  -> public HTTP route
  -> Starlark handle(req)
  -> camera.capture(id = "front_desk", width = 640, height = 480)
  -> runtime camera module
  -> bound hardware service
  -> lookup site binding: current site + "front_desk"
  -> validate capture permission
  -> clamp requested dimensions to configured limits
  -> plugin RPC Capture
  -> UVC provider opens /dev/videoN
  -> V4L2 captures one MJPEG frame
  -> bytes returned to Starlark
  -> route returns image/jpeg or JSON/base64
```

For device listing:

```text
Starlark camera.list()
  -> bound hardware service
  -> returns only devices assigned to the current site
  -> returns aliases, labels, permissions, limits, and configured formats
```

A site sees aliases, not host paths. The physical path is deliberately hidden from site code.

## Implementation

### Server boot

Hardware is optional. The server only enables it when started with a hardware plugin path:

```bash
quack-server \
  -root /var/lib/quack/blob \
  -database /var/lib/quack/quack.db \
  -hardware-plugin /usr/local/bin/quack-hardware-plugin
```

If `-hardware-plugin` is empty, hardware is disabled and the Starlark `camera` module is not injected.

At boot, `quack-server`:

1. Starts the hardware plugin client.
2. Wraps the plugin in a repository-bound hardware service.
3. Passes that bound service into the server options.
4. Installs it into the Starlark executor.
5. Injects the `camera` module only when the hardware service is present.

### Plugin process

`quack-hardware-plugin` is a separate executable. It constructs:

```go
hardware.NewLocalService(hardware.NewUVCProvider())
```

and serves it through HashiCorp `go-plugin`.

The handshake uses:

```text
Plugin name: hardware
Protocol: net/rpc
Magic cookie key: QUACK_HARDWARE_PLUGIN
Magic cookie value: hardware-v1
Protocol version: 1
```

The plugin boundary is important. The public server process does not need to link platform-specific camera code directly into the request path. Native device access lives in the plugin subprocess.

### Service interfaces

The hardware service exposes two operations:

```go
ListDevices(ctx, ListDevicesRequest) (ListDevicesResponse, error)
Capture(ctx, CaptureRequest) (CaptureResponse, error)
```

The core request/response shapes are:

```go
type ListDevicesRequest struct {
    Kind string
    Site string
}

type CaptureRequest struct {
    CameraID string
    Site     string
    Width    int
    Height   int
    Format   string
}
```

The service also defines a stable hardware configuration model:

```go
type Config struct {
    Devices            []DeviceDescriptor
    SiteDeviceBindings []SiteDeviceBinding
}
```

A `DeviceDescriptor` represents administrator-owned physical hardware:

```go
type DeviceDescriptor struct {
    ID     string
    Kind   string
    Plugin string
    Path   string
    Label  string
    Limits DeviceLimits
}
```

A `SiteDeviceBinding` grants one site access to one device under one alias:

```go
type SiteDeviceBinding struct {
    Site        string
    Alias       string
    DeviceID    string
    Permissions DevicePermissions
    Limits      DeviceLimits
}
```

### Device kind naming

Internally, the UVC camera kind is:

```text
camera.uvc
```

The admin UI currently uses the friendlier kind value:

```text
uvc-camera
```

The admin model converts `uvc-camera` to `camera.uvc` when building the runtime hardware config.

### Bound service behavior

The bound service is the main security boundary between Starlark and hardware.

It requires every operation to include a site. It resolves:

```text
site + alias -> device descriptor
```

For `ListDevices`, it returns only bindings for the current site.

For `Capture`, it:

1. Requires a non-empty site.
2. Requires a non-empty camera alias.
3. Looks up the binding for `site + alias`.
4. Rejects capture if the camera is not assigned to that site.
5. Rejects capture if `permissions.capture` is false.
6. Resolves the alias to the administrator-managed device path.
7. Applies the effective width/height limits.
8. Passes the effective `max_capture_bytes` limit to the upstream plugin.
9. Rejects any returned frame that still exceeds `max_capture_bytes`.
10. Rewrites the response camera ID back to the site alias.

This means Starlark code cannot escape its assigned cameras by guessing `/dev/video2` or another site’s alias.

### Limit merging

Limits can exist on both the administrator device descriptor and the site binding.

The effective limit is the stricter positive value:

```text
effective max = min(device max, binding max)
```

If either side is unset or `<= 0`, the other side wins. If both are unset, there is no limit for that field.

Supported limits:

```text
max_width
max_height
max_fps
max_capture_bytes
```

The current capture implementation clamps width and height and enforces max capture bytes. `max_fps` is present in the model for future streaming/frame-rate use, but the current snapshot capture path captures one still frame.

### Linux UVC implementation

On Linux, `UVCProvider` uses V4L2 directly through `golang.org/x/sys/unix`. It is cgo-free.

Device listing:

1. Globs `/dev/video*`.
2. Opens each candidate with `O_RDWR | O_NONBLOCK | O_CLOEXEC`.
3. Calls `VIDIOC_QUERYCAP`.
4. Requires video capture capability.
5. Requires streaming capability.
6. Returns metadata such as driver, card, bus info, path, stable path, and basic MJPG format information.

Stable paths are discovered by checking:

```text
/dev/v4l/by-id/*
/dev/v4l/by-path/*
```

Capture:

1. Resolves the requested camera ID.
2. Acquires a per-device capture lock so only one capture can touch a physical camera at a time.
3. Defaults width to `640`.
4. Defaults height to `480`.
5. Defaults format to `MJPG`.
6. Rejects formats other than `MJPG`/`MJPEG`.
7. Sets V4L2 format to MJPEG.
8. Rejects the capture before requesting buffers if the driver-reported frame buffer size exceeds `max_capture_bytes`.
9. Requests mmap buffers.
10. Rejects any mmap buffer larger than `max_capture_bytes` before mapping it.
11. Queues buffers.
12. Starts streaming.
13. Polls until a frame is ready or the context is canceled.
14. Dequeues one buffer.
15. Rejects any dequeued frame larger than `max_capture_bytes` before copying it into Go memory.
16. Copies the MJPEG bytes.
17. Requeues the buffer.
18. Stops streaming.
19. Releases the per-device capture lock.
20. Returns the frame as `image/jpeg`.

On non-Linux platforms, the stub provider returns “unsupported platform.”

## Database and admin model

Hardware administration is stored in SQLite.

Current tables:

```sql
hardware_devices (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  path TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)

hardware_site_bindings (
  device_id TEXT PRIMARY KEY,
  site TEXT NOT NULL DEFAULT '',
  alias TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY(device_id) REFERENCES hardware_devices(id) ON DELETE CASCADE
)
```

A device currently has at most one site binding because `hardware_site_bindings.device_id` is the primary key. That means one physical camera can be assigned to one site at a time in the current implementation.

The database provides:

```go
ListHardwareDevices(ctx)
SaveHardwareDevice(ctx, device)
DeleteHardwareDevice(ctx, id)
HardwareConfig(ctx)
```

`HardwareConfig(ctx)` converts admin rows into the runtime `hardware.Config`.

## Administration

### Enabling hardware

Build or deploy both binaries:

```bash
quack-server
quack-hardware-plugin
```

Start the server with:

```bash
quack-server \
  -root /var/lib/quack/blob \
  -database /var/lib/quack/quack.db \
  -hardware-plugin /usr/local/bin/quack-hardware-plugin
```

The plugin process must have permission to open the camera device path. The server only needs permission to execute and communicate with the plugin.

For a Linux host, this usually means one of:

```text
run the plugin as a user in the video group
grant the plugin process access to /dev/videoN
mount the specific /dev/videoN into the container or pod
mount /dev/v4l/by-id/... if stable device names are desired
```

Prefer stable paths when possible:

```text
/dev/v4l/by-id/usb-Logitech_Webcam_C270-video-index0
/dev/v4l/by-path/pci-0000:00:14.0-usb-0:3:1.0-video-index0
```

Avoid relying on `/dev/video2` when multiple cameras may be added or removed.

### Admin UI

The admin page includes a Hardware tab for admin users.

The page lists:

```text
ID
Kind
Path
Label
Site
Alias
Delete action
```

The create form currently includes:

```text
ID
Kind
Path
Label
Site
```

The kind dropdown currently offers:

```text
uvc-camera
```

The path field expects a Linux device path, for example:

```text
/dev/video2
```

or a stable V4L symlink:

```text
/dev/v4l/by-id/...
```

The site dropdown can bind the camera to a published site or leave it unbound.

Current behavior:

```text
ID: physical/admin ID, for example cam_01
Kind: uvc-camera
Path: host/plugin-visible device path
Label: human-readable admin label
Site: optional site binding
Alias: currently defaults to the device ID when a site is selected
```

The current admin form does not expose a separate alias field. If a site is selected and no alias is set, the alias becomes the device ID.

### Example admin setup

Create a device:

```text
ID: front_desk
Kind: uvc-camera
Path: /dev/v4l/by-id/usb-Logitech_Webcam_C270-video-index0
Label: Front desk Logitech C270
Site: lobby
```

The site `lobby` can now use:

```python
camera.capture(id = "front_desk")
```

A different site cannot use that alias unless the admin reassigns the binding.

### Current administrative limitations

The current snapshot has a solid foundation, but the admin surface is intentionally minimal.

Not currently exposed in the admin UI:

```text
custom alias input
capture permission toggle
stream permission toggle
max width
max height
max fps
max capture bytes
multi-site bindings for one physical device
device probing/test capture button
CLI hardware administration
control API hardware administration
```

The structs already support permissions and limits, but the current admin form does not expose them.

## Starlark use

### Module availability

When hardware is configured, HTTP and WebSocket Starlark routes receive:

```python
camera
```

When hardware is not configured, the module is not injected.

A site does not declare physical devices in `site.yml`. Hardware binding is operational state managed by the administrator. The site only declares normal runtime routes.

Example `site.yml`:

```yaml
routes:
  - path: /camera
    kind: http
    runtime: starlark
    entrypoint: api/camera.star
    methods: [GET]
```

### `camera.list`

```python
camera.list(kind = "camera.uvc")
```

`kind` is optional. If omitted, it defaults to:

```text
camera.uvc
```

Returns a list of dictionaries:

```python
[
    {
        "id": "front_desk",
        "alias": "front_desk",
        "kind": "camera.uvc",
        "label": "Front desk Logitech C270",
        "permissions": {
            "capture": True,
            "stream": False,
        },
        "limits": {
            "max_width": 0,
            "max_height": 0,
            "max_fps": 0,
            "max_capture_bytes": 0,
        },
        "formats": [
            {
                "pixel_format": "MJPG",
                "width": 640,
                "height": 480,
                "fps": [],
            },
        ],
    },
]
```

Only cameras assigned to the current site are returned.

### `camera.capture`

```python
camera.capture(
    id = "front_desk",
    width = 640,
    height = 480,
    format = "MJPG",
)
```

Arguments:

```text
id       required site-visible camera alias
width    optional requested width
height   optional requested height
format   optional, currently MJPG/MJPEG only
```

Returns a dictionary:

```python
{
    "id": "front_desk",
    "mime_type": "image/jpeg",
    "data": b"...jpeg bytes...",
    "base64": "...base64 encoded jpeg...",
    "width": 640,
    "height": 480,
    "format": "MJPG",
    "device_kind": "camera.uvc",
}
```

Use `data` when returning the JPEG directly. Use `base64` when embedding in JSON.

### Return a JPEG directly

```python
def handle(req):
    frame = camera.capture(
        id = "front_desk",
        width = 640,
        height = 480,
    )

    return (
        200,
        {
            "content-type": frame["mime_type"],
            "cache-control": "no-store",
        },
        frame["data"],
    )
```

### Return a base64 JSON payload

```python
def handle(req):
    frame = camera.capture(
        id = "front_desk",
        width = 640,
        height = 480,
    )

    return (
        200,
        {"content-type": "application/json"},
        json.encode({
            "id": frame["id"],
            "mime_type": frame["mime_type"],
            "width": frame["width"],
            "height": frame["height"],
            "format": frame["format"],
            "base64": frame["base64"],
        }),
    )
```

### List cameras for a site

```python
def handle(req):
    devices = camera.list()

    return (
        200,
        {"content-type": "application/json"},
        json.encode({
            "devices": devices,
        }),
    )
```

### Handle missing or unassigned cameras

A failed capture raises a Starlark invocation error. For public routes, the normal runtime error behavior applies.

Typical failure cases:

```text
hardware disabled
camera alias is empty
camera is not assigned to the current site
capture permission is false
plugin process cannot open the device
device path is wrong
camera does not support MJPEG at the requested size
capture exceeds max_capture_bytes
runtime context times out
```

Use `camera.list()` when the route should degrade gracefully:

```python
def handle(req):
    devices = camera.list()
    if not devices:
        return (
            404,
            {"content-type": "application/json"},
            json.encode({"error": "no camera assigned"}),
        )

    frame = camera.capture(id = devices[0]["id"])

    return (
        200,
        {"content-type": frame["mime_type"]},
        frame["data"],
    )
```

## Security model

### What Starlark can see

Site code can see:

```text
assigned alias
kind
label
permissions
limits
formats
captured bytes
```

Site code cannot see:

```text
/dev/video path
stable host path
driver string
bus info
other sites' cameras
unassigned hardware
raw file descriptors
USB APIs
V4L2 ioctls
plugin process memory
server process memory
```

### Isolation rules

The site boundary is enforced by the bound hardware service, not by convention in Starlark.

A capture request must resolve through:

```text
current site + camera alias
```

A site cannot capture a camera unless an administrator has created a binding for that site.

### Plugin boundary

The plugin is a separate process. This is useful because UVC/V4L2 access is platform-specific and more failure-prone than ordinary request handling.

A plugin crash should be treated as a hardware subsystem failure, not as a public runtime crash. The server should report capture/list errors and keep the rest of the site serving path alive.

### Recommended operational policy

For production:

```text
mount only the specific camera devices the plugin needs
prefer /dev/v4l/by-id paths
run the plugin with least privilege
avoid giving the main server broad /dev access
set max_capture_bytes for every assigned camera
set max_width and max_height for public routes
do not expose camera routes without normal site authentication when privacy matters
do not log captured image data or base64 strings
```

## Deployment notes

### Bare metal or VM

Example:

```bash
sudo usermod -aG video quack
sudo systemctl restart quack
```

Start Quack with:

```bash
/usr/local/bin/quack-server \
  -root /var/lib/quack/blob \
  -database /var/lib/quack/quack.db \
  -hardware-plugin /usr/local/bin/quack-hardware-plugin
```

Register the camera in the admin UI with a path such as:

```text
/dev/v4l/by-id/usb-Logitech_Webcam_C270-video-index0
```

### Kubernetes / k3s

The plugin process must run in a container that can see the video device.

A minimal deployment shape is:

```yaml
volumeMounts:
  - name: video0
    mountPath: /dev/video0

volumes:
  - name: video0
    hostPath:
      path: /dev/video0
      type: CharDevice
```

The hardware plugin path passed to `quack-server` must point to an executable inside the server container. If the server and plugin are packaged together, this can be:

```text
-hardware-plugin /usr/local/bin/quack-hardware-plugin
```

If the hardware plugin needs device access but the main server should not have it, the next architecture step would be to run the hardware plugin as a separate sidecar/service and replace the local go-plugin subprocess with an RPC transport that can cross process/container boundaries intentionally.

## Suggested next improvements

### Admin UI

Add fields for:

```text
Alias
Capture permission
Stream permission
Max width
Max height
Max FPS
Max capture bytes
```

This would expose the model that already exists in `DevicePermissions` and `DeviceLimits`.

### Device probing

Add a “Probe” or “Test capture” action to the Hardware page.

It should show:

```text
whether the plugin is reachable
whether the path opens
driver/card/bus info
stable path suggestion
supported formats
sample capture size
```

### Stable path assistance

When the admin enters `/dev/video2`, the plugin can report the matching `/dev/v4l/by-id` or `/dev/v4l/by-path` path and recommend using it instead.

### Multi-site bindings

The current database schema allows one binding per device. If one physical camera should be exposed to multiple sites under different aliases or permissions, change:

```sql
PRIMARY KEY(device_id)
```

to something like:

```sql
PRIMARY KEY(site, alias)
```

and add a uniqueness rule only where needed.

### Route-level capability policy

Currently, any Starlark route for a site with an assigned camera can import/use the injected `camera` module. A future policy layer could require an explicit route capability, for example:

```yaml
routes:
  - path: /camera
    kind: http
    runtime: starlark
    entrypoint: api/camera.star
    capabilities:
      - camera.capture
```

Then deployment/policy validation could block accidental camera use.

### Streaming

The model already has `permissions.stream` and `max_fps`. A future streaming API could be added without changing the capture API:

```python
camera.stream(...)
```

or through WebSocket effects managed by the Go host rather than long-running Starlark loops.

Until then, `camera.capture` should be treated as a single-frame still capture API.
