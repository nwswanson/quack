# Hardware plugin implementation log

## 2026-06-25T15:07:16Z

Decision: model this as one hardware plugin boundary with device-class APIs inside it, not as a USB-only plugin or one process per device mapping.

Why:

- The security and operational boundary we need is "host runtime cannot touch hardware unless the server was booted with hardware enabled." A single hardware plugin client is the cleanest place to enforce that hard boot gate.
- UVC cameras, HID devices, and later GPIO are different device classes, but they share inventory, policy, process isolation, lifecycle, logging, and OS sandbox concerns. Splitting those concerns across many plugin families would duplicate the dangerous parts.
- Per-device mapping should be configuration data, not a plugin instance model. Device aliases like `front_door` can map to stable Linux paths later without changing the Starlark API or spawning a plugin per device.
- Keeping one plugin boundary still allows device-class implementation packages behind it. UVC can be ready now, HID can be added next, and GPIO can join later without renaming a "usb" abstraction.

Implication: expose a Starlark `camera` module for the high-level user API, backed by an internal hardware service. The plugin itself remains `hardware` because cameras are only the first class.

Hard gate: if `quack-server` is not started with a hardware plugin path, no hardware-backed Starlark modules are installed. This is intentionally stronger than policy: the code path is unreachable from scripts unless the boot flag creates the hardware service.

Linux approach: bind to the kernel ABI through V4L2 using cgo-free Go and `golang.org/x/sys/unix`. On non-Linux hosts the plugin binary builds, but hardware operations return a clear unsupported-platform error so macOS development can use tests and mocks.

## 2026-06-25T15:15:27Z

Implemented shape:

- `cmd/quack-server` has a hard boot flag: `-hardware-plugin`. If it is omitted, no `camera` module is installed into Starlark at all.
- The hardware plugin is a hashicorp/go-plugin net/rpc process boundary named `hardware`.
- The first device-class API is camera/UVC, exposed to Starlark as `camera.list()` and `camera.capture(...)`.
- Site manifests can request `features.camera`, which becomes the `hardware.camera` policy capability. This is separate from the boot flag: both the boot flag and policy must line up before camera calls can work.
- The Linux UVC implementation uses V4L2 ioctls, mmap, poll, and `/dev/video*` enumeration through `golang.org/x/sys/unix`; no cgo, no FFmpeg, and no capture shell-outs.
- Non-Linux builds keep the plugin binary buildable but return `hardware device access is not supported on this platform` from the provider.

Validation:

- `go test ./...` passes on the macOS host.
- `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test ./internal/hardware ./cmd/quack-hardware-plugin` passes, so the Linux plugin path type-checks without cgo.

Known next Linux-server work:

- Run against real UVC devices and tighten format enumeration beyond the initial MJPEG/default resolution path.
- Add host/admin device alias mapping so Starlark can use names like `front_door` without exposing `/dev/videoN`.
- Add HID device-class interfaces behind the same hardware plugin boundary.
