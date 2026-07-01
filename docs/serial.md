# Serial Hardware Support

## Overview

Quack exposes configured serial ports to site code through a narrow, site-scoped
`serial` Starlark module.

The important design rule is:

```text
Starlark never receives raw /dev paths, COM names, file descriptors, or direct
serial library handles.
```

Instead, an administrator registers a physical serial device, binds it to a
site, and gives that site a stable alias. Site code can then call:

```python
serial.open("weather_station")
serial.write("weather_station", b"READ\n")
```

The server resolves the alias to an administrator-managed device path and
delegates actual port access to the separate hardware plugin process.

Serial ports are not opened by listing, actor creation, status checks, writes,
or requests. A site must explicitly call:

```python
serial.open("weather_station")
```

before it can write or request data from that device. This makes hardware access
intentional in site code and keeps the lifecycle visible.

## Architecture

### Components

```text
quack-server
  admin listener
    /hardware admin UI
    SQLite hardware device registry

  public listener
    Starlark HTTP routes
    Starlark WebSocket routes

  runtime executor
    injects serial module when hardware is configured

  hardware bound service
    maps site + alias -> physical serial descriptor
    enforces serial_read and serial_write permissions
    hides physical port paths from Starlark

quack-hardware-plugin
  separate go-plugin subprocess
  owns serial actors and physical serial.Port handles
  exposes serial lifecycle and I/O over net/rpc

host OS
  Linux: /dev/ttyUSB0, /dev/ttyACM0, /dev/ttyS0
  macOS: /dev/cu.usbserial-*, /dev/cu.usbmodem-*
```

The implementation uses `go.bug.st/serial` for cgo-free serial port access on
Linux and macOS. Quack's public contract is the configured device alias; port
discovery metadata is intentionally weak and best-effort.

### Request flow

For opening a serial device:

```text
Starlark serial.open("weather_station")
  -> runtime serial module
  -> bound hardware service
  -> lookup current site + "weather_station"
  -> validate alias is assigned to the site
  -> validate the device is kind "serial"
  -> require serial_read or serial_write permission
  -> plugin RPC OpenSerial
  -> serial provider gets/creates the per-port actor
  -> actor opens the configured OS path
  -> actor starts its read goroutine
```

For writing:

```text
Starlark serial.write("weather_station", b"READ\n")
  -> runtime serial module
  -> bound hardware service
  -> validate serial_write permission
  -> plugin RPC WriteSerial
  -> serial actor serializes the write through its command queue
  -> bytes are written to the already-open port
```

If `serial.write` is called before `serial.open`, the actor returns an error and
does not attempt to open the port.

For simple request/response devices:

```text
Starlark serial.request("meter", b"MEASURE?\n", until="\n", timeout_ms=1000)
  -> validate serial_write and serial_read permissions
  -> actor writes the request bytes
  -> actor collects bytes from the read stream
  -> response completes at delimiter, max_bytes, timeout, or port error
```

Serial is a byte stream. `serial.request` is only a convenience for devices with
simple command/response protocols. Streaming devices, chatty devices, and custom
binary protocols should use explicit writes plus status/recent-event inspection
or a future event-driven serial API.

## Configuration

### Device descriptors

Serial devices are administrator-owned physical devices. The stable thing Quack
uses at runtime is the configured path, not USB VID/PID discovery metadata.

Example hardware config:

```yaml
devices:
  - id: weather_station_01
    kind: serial
    path: /dev/ttyUSB0
    label: Weather station
    serial:
      baud: 9600
      data_bits: 8
      parity: none
      stop_bits: "1"
      read_timeout_ms: 500
      request_timeout_ms: 1000
      write_queue_size: 64
      recent_events: 64
      reconnect_ms: 1000

site_device_bindings:
  - site: acme
    alias: weather_station
    device_id: weather_station_01
    permissions:
      serial_read: true
      serial_write: true
```

macOS example path:

```yaml
path: /dev/cu.usbserial-0001
```

Linux examples:

```yaml
path: /dev/ttyUSB0
path: /dev/ttyACM0
```

### Serial options

Supported serial options:

```text
baud
data_bits
parity
stop_bits
read_timeout_ms
request_timeout_ms
write_chunk_bytes
write_delay_ms
disable_write_drain
write_queue_size
recent_events
reconnect_ms
```

Defaults:

```text
baud = 9600
data_bits = 8
parity = none
stop_bits = 1
read_timeout_ms = 500
request_timeout_ms = 1000
write_chunk_bytes = 256
write_delay_ms = 2
disable_write_drain = false
write_queue_size = 64
recent_events = 64
reconnect_ms = 1000
```

Supported parity values:

```text
none
odd
even
mark
space
```

Supported stop bit values:

```text
1
1.5
2
```

### Permissions

Serial access uses permissions separate from camera permissions:

```yaml
permissions:
  serial_read: true
  serial_write: true
```

Permission behavior:

```text
serial.open    requires serial_read or serial_write
serial.write   requires serial_write
serial.request requires serial_write and serial_read
serial.status  requires the alias to be assigned to the site
serial.close   requires the alias to be assigned to the site
serial.list    returns only aliases assigned to the current site
```

This allows a site to have command-only access, read/write request access, or no
serial access to a device. Physical paths remain hidden from Starlark in all
cases.

## Starlark API

The `serial` module is injected only when the server has a configured hardware
service.

### list

```python
devices = serial.list()
```

Returns only serial aliases assigned to the current site. Each item has the same
logical device shape used by other hardware modules:

```python
{
    "id": "weather_station",
    "alias": "weather_station",
    "kind": "serial",
    "label": "Weather station",
    "permissions": {
        "serial_read": True,
        "serial_write": True,
        "capture": False,
        "stream": False,
    },
    "limits": {...},
    "formats": [],
}
```

The physical `path` is not included.

### open

```python
result = serial.open("weather_station")
```

Returns:

```python
{"id": "weather_station", "open": True}
```

`open` is idempotent while the actor already has an open port. If the port was
closed or has not yet been opened, the actor opens the configured OS path using
the configured serial options.

### write

```python
result = serial.write("weather_station", b"READ\n")
```

`data` may be `bytes` or `string`.

Returns:

```python
{"id": "weather_station", "bytes": 5}
```

`write` requires the device to already be open. It does not open the port
implicitly.

The hardware actor treats writes defensively because serial ports are byte
streams backed by small kernel and device buffers. A single `serial.write` call
is split into bounded low-level writes, retries short writes until all bytes are
accepted, treats zero-byte progress as `io.ErrShortWrite`, drains the port after
each chunk by default, and yields between chunks. This keeps a large paste or
blob from being handed to the serial driver as one unpaced burst. `write` still
reports bytes accepted by the host serial stack, not a protocol-level
acknowledgement from the attached device.

Very fragile line-oriented firmware consoles can be configured to receive one
byte per low-level write:

```yaml
serial:
  baud: 9600
  write_chunk_bytes: 1
  write_delay_ms: 5
```

With that configuration, `serial.write("dish", "azangle 90\r")` is sent as
individual byte writes for `a`, `z`, `a`, and so on through the carriage return.
Use this mode for devices that behave like interactive terminals rather than
robust stream parsers. This is a nod for saveitforparts--I love your channel! :) 

### transfer

```python
transfer = serial.transfer("weather_station", firmware_bytes)
```

`data` may be `bytes` or `string`.

Returns immediately after the host accepts the transfer:

```python
{
    "id": "weather_station",
    "transfer_id": "xfer_...",
    "bytes": 16777216,
    "accepted": True,
}
```

`transfer` requires the device to already be open. The serial actor owns the
transfer after acceptance, writes it in bounded chunks, and marks the device
busy until the transfer completes, fails, or is cancelled by closing the port.
Normal writes, requests, and second transfers are rejected while the device is
busy.

Transfer lifecycle is reported through hardware events:

```text
hardware.serial.weather_station.transfer_started
hardware.serial.weather_station.transfer_progress
hardware.serial.weather_station.transfer_completed
hardware.serial.weather_station.transfer_failed
hardware.serial.weather_station.transfer_cancelled
```

Transfer event payloads are small JSON objects:

```json
{
  "transfer_id": "xfer_...",
  "status": "running",
  "bytes_written": 4096,
  "total_bytes": 16777216
}
```

Raw transfer bytes are not emitted as hardware event payloads. Site pipes should
carry transfer lifecycle and progress events, not firmware bodies.

`serial.transfer(alias, bytes)` still receives the full byte value in the
Starlark handler. For browser uploads, configure route request limits with that
memory use in mind. A 16 MiB firmware image may be reasonable for a dedicated
upload route, but it is intentionally a different operational profile from an
interactive terminal command.

### request

```python
resp = serial.request(
    "weather_station",
    b"READ\n",
    until="\n",
    timeout_ms=1000,
    max_bytes=4096,
)
```

`data` and `until` may be `bytes` or `string`.

Returns:

```python
{
    "id": "weather_station",
    "data": b"72.4\n",
    "text": "72.4\n",
    "base64": "NzIuNAo=",
    "timeout": False,
}
```

Completion rules:

```text
delimiter found in accumulated bytes
max_bytes reached
timeout_ms elapsed
port read/write error
```

Only one pending request is allowed per serial actor. This keeps read matching
deterministic. A second concurrent request returns an error until the active one
finishes.

### status

```python
status = serial.status("weather_station")
```

Returns:

```python
{
    "id": "weather_station",
    "open": True,
    "status": "open",
    "error": "",
    "busy": False,
    "transfer_id": "",
    "transfer_status": "",
    "transfer_bytes": 0,
    "transfer_total": 0,
    "recent": [
        {
            "at": "2026-06-26T15:04:05Z",
            "type": "write",
            "data": b"READ\n",
            "text": "READ\n",
            "base64": "UkVBRAo=",
            "error": "",
        },
    ],
}
```

Possible status values:

```text
closed
open
error
```

The recent ring buffer includes open, close, read, write, and error events. It
is intended for diagnostics and lightweight polling, not as a durable event log.

### close

```python
result = serial.close("weather_station")
```

Returns:

```python
{"id": "weather_station", "closed": True}
```

Closing stops reconnect attempts and closes the physical port if it is open.
Future writes and requests fail until the script calls `serial.open` again.

## Actor Lifecycle

Each configured physical serial path is owned by one actor inside the hardware
plugin process.

The actor owns:

```text
the serial.Port handle
open/close lifecycle
read goroutine
serialized write queue
one active request matcher
request timeout timer
recent event ring buffer
current status/error state
reconnect timer after successful explicit open
```

Route handlers and Starlark code never read directly from a port. They send
commands to the actor and wait for a response.

### Explicit open

Actor creation does not open the serial port. The provider may create an actor
for status, close, write, or request routing, but the actor stays closed until
it receives an `open` command.

This avoids surprising hardware side effects. Some boards reset when DTR/RTS
changes during open, and some devices treat open as a meaningful host action.
Requiring `serial.open` makes that action explicit.

### Reconnect behavior

After a successful explicit open, the actor treats disconnection as recoverable.
If a read or write error closes the port, the actor records the error and tries
to reopen after `reconnect_ms`.

Reconnect stops when:

```text
serial.close(alias) is called
the provider/service is closed
the plugin process exits
```

If the initial `serial.open` fails, the actor reports the error and does not
keep retrying in the background. Site code can call `serial.open` again later.

## Framing And Request Matching

Serial is byte-stream I/O. The OS and device decide how bytes are chunked, so a
single device message may arrive in several reads, and several messages may
arrive in one read.

`serial.request` accumulates read bytes into a request buffer. It completes when
the delimiter appears anywhere in the accumulated buffer, when `max_bytes` is
reached, or when the timeout fires.

Default request behavior:

```text
until = "\n"
timeout_ms = request_timeout_ms config value, or 1000
max_bytes = 4096
```

Binary protocols can pass a bytes delimiter:

```python
resp = serial.request("scale", b"\x02W\x03", until=b"\x03")
```

For protocols needing checksums, escaping, length prefixes, or multiple
interleaved responses, keep the lower-level actor model and add a protocol-aware
helper above it rather than letting several handlers race on raw reads.

## Security Model

The serial module follows the same hardware security model as camera support:

```text
site code names an alias
the bound service resolves site + alias
the bound service checks permissions
the plugin receives the physical path
the response is rewritten back to the alias
```

Starlark cannot:

```text
open arbitrary /dev/tty* paths
guess another site's device alias
see physical serial paths in list or status responses
hold a raw serial handle
install a long-running read callback
perform concurrent unmatched reads
```

The plugin process is the only process that owns the serial port handle. The
public runtime process communicates with it through the hardware service RPC
interface.

## Operational Notes

### Device paths

For production Linux deployments, prefer stable udev paths or symlinks over
numbered `/dev/ttyUSB0` paths when possible. Numbered paths can change when
devices are unplugged or when boot order changes.

The initial design intentionally does not require VID/PID discovery. Explicit
administrator-configured paths are simpler, portable, and avoid macOS cgo
requirements for richer IOKit metadata.

### DTR/RTS side effects

Many Arduino-like boards reset when the serial port opens because modem control
lines change. Quack does not hide this. `serial.open` is explicit so site code
and administrators can reason about when those side effects may happen.

### Backpressure

The actor command channel has a configurable `write_queue_size`. Writes and
requests are serialized, so one slow or disconnected device can delay commands
for that device without interleaving bytes from different handlers.

Fast unsolicited read streams are recorded only in the recent event ring buffer
today. They are not durable and are not automatically published to WebSocket
subscribers.

### Timeouts

There are two timeout layers:

```text
read_timeout_ms controls the serial library read loop timeout behavior
request_timeout_ms is the default high-level serial.request timeout
```

Callers can override the high-level request timeout per call:

```python
serial.request("meter", b"MEASURE?\n", timeout_ms=250)
```

## Example Route

```python
def handle(req):
    serial.open("weather_station")
    resp = serial.request(
        "weather_station",
        b"READ\n",
        until="\n",
        timeout_ms=1000,
        max_bytes=256,
    )
    if resp["timeout"]:
        return (504, {"content-type": "text/plain"}, "serial timeout")
    return (200, {"content-type": "text/plain"}, resp["text"])
```

For command-only devices:

```python
def handle(req):
    serial.open("sign")
    serial.write("sign", "HELLO\n")
    return (204, {}, None)
```
