Yes. That is exactly the kind of case where go-plugin starts to look attractive.

The shape I’d use is:

host process
  - HTTP server
  - tenant/site routing
  - auth/policy
  - Starlark/goja/WASM user scripts
  - durable app state
  - secrets store
UVC plugin process
  - talks to USB camera / libuvc / V4L2 / platform APIs
  - owns native/cgo/weird device code
  - exposes a narrow RPC/gRPC interface
  - can crash/restart independently

go-plugin runs plugins as separate subprocesses and communicates over RPC or gRPC, rather than loading code into the host address space. The docs explicitly call out that plugins cannot crash the host process via a plugin panic, and that the plugin only has access to the interfaces and arguments the host gives it, not the host process memory.  

So for your example, the host should not let user scripts touch USB directly. Instead, user scripts call a high-level capability:

camera.capture("front-desk", width=640, height=480)

Then the host does:

1. Resolve tenant/site/user.
2. Check policy: is this site allowed to use camera front-desk?
3. Check quota/rate limits.
4. Validate options.
5. Call UVC plugin over RPC.
6. Return sanitized result to script.

The plugin should only see something like:
```
type CameraPlugin interface {
    ListDevices(ctx context.Context) ([]DeviceInfo, error)
    Open(ctx context.Context, req OpenRequest) (SessionID, error)
    CaptureFrame(ctx context.Context, session SessionID, req FrameRequest) (Frame, error)
    Close(ctx context.Context, session SessionID) error
}
```
Not:
```
type CameraPlugin interface {
    EvalUserScript(...)
    ReadSecret(...)
    RawHostDB(...)
    OpenArbitraryDevice(...)
}
```
That distinction matters a lot.

What boundary you get

You get a process memory boundary.

The UVC plugin cannot directly read the host heap, corrupt host Go objects, or panic the host. That is a very real improvement over native in-process Go plugins or cgo inside your main server.

You also get a fault boundary.

USB/device/video code is exactly the kind of thing that can hang, panic, deadlock, leak resources, or wedge itself. Putting it in a plugin process means the host can kill and restart it.

You get a dependency boundary.

Your main server can remain mostly boring, static, cgo-free Go. 

You get a capability boundary.

The plugin only receives the RPC methods and arguments you expose. go-plugin supports Go-interface-like plugins, gRPC, protocol versioning, logging, checksum verification, and TLS configuration between host and plugin.  

What boundary you do not get automatically

You do not automatically get a full sandbox.

A plugin process still runs as some OS user. If you launch it as the same user as the host, with the same filesystem permissions, same network access, same environment variables, and same /dev access, then a malicious plugin can still do plenty of damage.

So the real security model is:

go-plugin gives you the RPC/process split.
The OS gives you the sandbox.

For the UVC plugin, I’d run it with tighter OS permissions than the host:

- separate Unix user
- no access to host secrets/env
- only specific /dev/videoN or USB device nodes
- no write access to app database
- no broad filesystem access
- optional seccomp/AppArmor/systemd sandboxing/container
- memory/CPU/process limits
- restart policy

On Linux, this maps nicely to systemd hardening or a small container where only /dev/video0 is mounted. The host talks to the plugin over a local socket/RPC channel.

The important inversion

For security, the plugin should not be “trusted helper that user scripts can freely call.”

It should be:
```
untrusted user script
  -> host policy/capability layer
    -> trusted narrow RPC interface
      -> UVC plugin
```
The host remains the reference monitor. It decides who can do what. The plugin just performs hardware operations after the host has authorized them.

Bad:

script directly talks to plugin
plugin decides auth
plugin has broad camera/device powers

Good:

script asks host for camera capability
host checks tenant/site/user policy
host calls plugin with constrained request
plugin returns frame/result

Why this is better than in-process modules

In-process module:

host + scripts + UVC code + native deps all share one address space

A memory bug, cgo issue, panic, blocking syscall, or bad device interaction can take down the whole server.

go-plugin style:

host + scripts       UVC plugin
same process? no     separate process
same memory?  no     RPC/gRPC boundary
same deps?    no     plugin binary owns deps
same crash?   no     host can restart plugin

That is a much better match for hardware access.

A clean design for Quack-style modules

I’d probably define internal capability layers like this:

Go host modules:
  - route/runtime
  - policy
  - tenant/site isolation
  - secrets
  - memory/db
  - plugin manager
Starlark-visible modules:
  - camera.capture(...)
  - camera.list(...)
  - camera.stream_token(...)
External plugins:
  - uvc-camera-plugin
  - maybe ffmpeg-plugin
  - maybe browser-render-plugin
  - maybe image-processing-plugin

The Starlark API should be boring and safe:
```
frame = camera.capture(
    name = "front_door",
    width = 640,
    height = 480,
    format = "jpeg",
)
```
The actual plugin RPC can be lower-level, but still not raw/unbounded:
```
service CameraService {
  rpc ListDevices(ListDevicesRequest) returns (ListDevicesResponse);
  rpc CaptureFrame(CaptureFrameRequest) returns (CaptureFrameResponse);
  rpc StartStream(StartStreamRequest) returns (stream FrameChunk);
  rpc StopStream(StopStreamRequest) returns (StopStreamResponse);
}
```
My read

For your specific phrase — “host ran user scripts, but plugin did USB UVC” — yes, that is a strong design.

But I would describe the boundary precisely:

go-plugin isolates native/hardware/device complexity from the host process.
It does not, by itself, make the plugin safe.
The host must still enforce capability checks before calling the plugin, and the OS should sandbox the plugin process.

That gives you a very good architecture:

Untrusted script execution stays constrained in the host runtime.
Privileged hardware access moves out to a narrow, restartable, sandboxable plugin process.
The host mediates all access between them.

That is much better than putting USB/cgo/device code directly into the same Go process that evaluates user-controlled Starlark.

I would not put the USB path in site.yml.

The USB path is host/admin/platform state, not site state.

Bad:

camera:
  path: /dev/video0

That leaks host topology into the app, is not portable, and lets site config pretend to claim hardware it should not control.

The cleaner split is:

platform/admin config:
  physical device path, plugin choice, serial number, host binding
admin assignment:
  which site gets which logical device
site.yml:
  optional logical capability usage, aliases, route/script permissions, quotas

Recommended model

Think of it as three layers.

1. Host device inventory
   "There is a USB UVC camera at /dev/video2."
2. Admin entitlement
   "Site acme may use that camera as front_door."
3. Site policy
   "Within this site, users with role camera_viewer may capture frames."

site.yml should only contain layer 3, and maybe a logical declaration for layer 2.

Platform/admin config

This is trusted host config or database state, created by the admin UI.

Example:
```
devices:
  - id: cam_01
    kind: camera.uvc
    plugin: uvc-camera
    path: /dev/video2
    match:
      vendor_id: "046d"
      product_id: "0825"
      serial: "ABC123"
    label: "Front desk Logitech C270"
```
Or in DB:
```
device_id: cam_01
kind: camera.uvc
plugin: uvc-camera
host_path: /dev/video2
vendor_id: 046d
product_id: 0825
serial: ABC123
created_by: admin
```
This layer is privileged. Sites should not be able to edit it.

Admin assignment

The admin links the physical device to a site using a logical name:
```
site_device_bindings:
  - site: acme
    alias: front_door
    device_id: cam_01
    permissions:
      capture: true
      stream: false
    limits:
      max_width: 1280
      max_height: 720
      max_fps: 5
      max_capture_bytes: 2000000
```
This could also live entirely in your admin DB.

That gives the site a stable logical handle:

camera://front_door

The site never sees:

/dev/video2
usb bus 001 device 004
vendor/product ids
plugin process path

What goes in site.yml

I’d put only site-local policy and usage declarations.

Example:
```
capabilities:
  camera:
    front_door:
      required: false
      description: "Front door camera"
      permissions:
        capture:
          roles: ["admin", "staff"]
        stream:
          roles: ["admin"]
      limits:
        max_width: 640
        max_height: 480
        max_fps: 2
        max_duration_seconds: 10
```
Then Starlark uses the logical name:
```
frame = camera.capture(
    "front_door",
    width = 640,
    height = 480,
    format = "jpeg",
)
```
The host resolves it:

site.yml alias: front_door
        ↓
admin binding: acme/front_door -> cam_01
        ↓
platform device: cam_01 -> /dev/video2
        ↓
plugin call: uvc-camera.Capture(...)

Minimal version: put nothing in site.yml

This is also valid.

If the admin UI does all of this:

- creates descriptor
- links USB path
- assigns device to site
- assigns users/roles
- sets limits

Then site.yml can contain nothing about cameras.

The host can inject available capabilities at runtime:

camera.capture("front_door")

And if the site does not have front_door, the call fails.

That is the simplest and safest model.

My preferred compromise

I’d use this:

Admin DB:
  physical device descriptor
  site-to-device binding
  hard security limits
site.yml:
  optional logical aliases and app-level role policy

Example site.yml:
```
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
        max_captures_per_minute: 10
```
But the admin DB still has the real binding:

site=acme
alias=front_door
device_id=cam_01

The host should enforce both:

effective limit = min(admin hard limit, site.yml requested limit)
effective permission = admin grants capability AND site policy allows user

So if site.yml asks for:

max_width: 99999

the host clamps it to the admin-approved maximum.

Important rule

site.yml may request or describe capabilities.

It must not grant them.

This is the key security distinction:

site.yml says:
  "this app expects a camera called front_door"
admin/platform says:
  "this site is allowed to use actual hardware cam_01 as front_door"

That avoids a compromised site repo giving itself access to host devices.

Final recommendation

Use this split:

platform.yml / admin DB:
  device path, plugin binary, USB descriptor, serial, host-level constraints
admin DB:
  site assignment: site + alias -> device
site.yml:
  optional capability declaration, role policy, app-level limits

For v1, I would put nothing physical in site.yml. At most:
```
capabilities:
  camera:
    front_door:
      required: false
      permissions:
        capture:
          roles: ["admin", "staff"]
```
That gives you portability, safety, and a clean mental model: sites consume named capabilities; admins bind those names to real hardware.
