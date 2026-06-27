Add a serial actor per configured device inside the hardware plugin process. Use the existing hardware module, follow the stubs, but move into its own file. we want to build for linux and mac.

That actor owns:

/dev/ttyUSB0 or /dev/ttyACM0
open/close lifecycle
read goroutine
write queue
request/response matching
line or binary framing
timeouts
ring buffer of recent events
current status/error state
reconnect/reset logic

**Serial is one of the better hardware interfaces to support in cgo-free Go cross-platform**, as long as you mean “a device that exposes a serial/COM/TTY port,” not arbitrary USB/HID.

The realistic answer:

**Portable API: yes. Portable implementation: no, not internally.**

You can expose one Go interface like:

```go
type Port interface {
    io.ReadWriteCloser
    SetReadTimeout(time.Duration) error
    SetMode(Mode) error
}
```

But under the hood it needs OS-specific implementations:

| OS      | Port names                                   | Underlying model             |
| ------- | -------------------------------------------- | ---------------------------- |
| Linux   | `/dev/ttyUSB0`, `/dev/ttyACM0`, `/dev/ttyS0` | tty + termios/ioctl          |
| macOS   | `/dev/cu.usbserial-*`, `/dev/cu.usbmodem-*`  | BSD tty + termios            |
| Windows | `COM3`, `COM4`, `\\.\COM10`                  | Win32 file handle / COM APIs |

Go is good at this because you can use build-tagged files like `serial_linux.go`, `serial_darwin.go`, and `serial_windows.go`; Go’s build system explicitly supports OS-specific file selection such as `source_windows.go` only compiling on Windows. ([Go Packages][1])

The most practical choice is probably **`go.bug.st/serial`**. It is a cross-platform Go serial library, exposes `GetPortsList`, `Open`, `Mode`, `Read`, `Write`, `Close`, timeouts, DTR/RTS, buffer reset, and platform-independent error codes like port busy, not found, permission denied, invalid speed, etc. ([Go Packages][2]) It also tries to avoid cgo for the main serial functionality, but there is one important caveat: **detailed USB enumeration on macOS requires cgo** because it uses IOKit to get VID/PID/serial metadata. ([Go Packages][2])

So I’d split your design like this:

```go
// Core cgo-free serial API.
serial.open("weather_station")
serial.write("weather_station", b"...")
serial.close("weather_station")

// Optional, best-effort discovery.
serial.list_ports()
```

And make the config explicit:

```yaml
hardware:
  serial:
    - name: weather_station
      path: /dev/ttyUSB0
      baud: 9600
      data_bits: 8
      parity: none
      stop_bits: 1
      read_timeout_ms: 500
```

For Windows:

```yaml
hardware:
  serial:
    - name: weather_station
      path: COM3
      baud: 9600
```

For macOS:

```yaml
hardware:
  serial:
    - name: weather_station
      path: /dev/cu.usbserial-0001
      baud: 9600
```

I would **not** make VID/PID enumeration required for your first version if you want strict cgo-free cross-platform support. Let the admin bind a concrete path. Later you can add richer discovery behind build tags or optional platform-specific helpers.

The right runtime shape is the actor-ish one you were already circling around:

```go
type SerialDevice struct {
    Name   string
    Port   serial.Port
    Writes chan []byte
    Events chan SerialEvent
    Done   chan struct{}
}
```

One goroutine owns the port. It opens the serial device once, continuously reads from it, emits events, and serializes writes. Request handlers and websocket handlers should not directly block on raw reads from the device.

Rough shape:

```go
func runSerialDevice(ctx context.Context, dev SerialDeviceConfig, emit func(Event)) error {
    mode := &serial.Mode{
        BaudRate: dev.Baud,
        DataBits: dev.DataBits,
        Parity:   serial.NoParity,
        StopBits: serial.OneStopBit,
    }

    port, err := serial.Open(dev.Path, mode)
    if err != nil {
        return err
    }
    defer port.Close()

    _ = port.SetReadTimeout(500 * time.Millisecond)

    writes := make(chan []byte, 64)
    buf := make([]byte, 4096)

    go func() {
        for {
            select {
            case <-ctx.Done():
                return
            case b := <-writes:
                _, _ = port.Write(b)
            }
        }
    }()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
            n, err := port.Read(buf)
            if err != nil {
                return err
            }
            if n > 0 {
                emit(Event{
                    Device: dev.Name,
                    Bytes:  append([]byte(nil), buf[:n]...),
                })
            }
        }
    }
}
```

The key design point: **serial is byte-stream I/O, not request/response I/O**. Some devices are command/response, some stream forever, some emit partial lines, some emit binary frames, and some require write pacing. So your framework should not assume `serial.write()` returns the matching response.

I’d expose two layers:

```python
# Fire-and-forget / stream-oriented
serial.write("weather_station", b"READ\n")

# Optional convenience only for simple devices
serial.request("meter", b"MEASURE?\n", until="\n", timeout_ms=1000)
```

Internally `serial.request` should still go through the single device owner so writes and reads do not race.

For Starlark, I’d probably expose:

```python
def on_serial(ctx, event):
    if event.device == "weather_station":
        text = event.text
        memory.set("last_weather_line", text)
        realtime.publish("weather_station", {"line": text})
```

And for websocket/client-originated commands:

```python
def on_ws(ctx, event):
    if event.topic == "weather_station.commands":
        serial.write("weather_station", event.data["command"] + "\n")
```

What you need beyond the basic port API:

1. **Single-owner device lifecycle**
   Open each serial device once. Do not let route handlers open/close it per request.

2. **Reconnect loop**
   USB serial devices disappear and reappear. Your device manager should retry open with backoff.

3. **Framing**
   Raw serial gives chunks, not messages. Add helpers for line framing, delimiter framing, fixed-size frames, and maybe binary packet framing.

4. **Write queue**
   All writes should go through one channel per device. This prevents two web requests from interleaving bytes.

5. **Backpressure/drop policy**
   A fast serial stream can overwhelm slow websocket subscribers. Topics need buffer limits.

6. **Permissions/policy**
   Starlark should not open arbitrary `/dev/tty*` or `COM*`. It should only reference admin-declared names.

7. **Timeout semantics**
   Reads can block. Use read timeouts or context-aware loops. The `go.bug.st/serial` port interface includes `SetReadTimeout`. ([Go Packages][2])

8. **DTR/RTS behavior**
   Some boards reset when DTR toggles, especially Arduino-like devices. `go.bug.st/serial` notes that on Linux/macOS, initial status bits cannot always be set before opening, so DTR/RTS may briefly pulse. ([Go Packages][2])

My recommendation:

Use **`go.bug.st/serial`** unless you have a strong reason to write the low-level implementations yourself. Keep your own abstraction above it:

```go
type Driver interface {
    ListPorts(ctx context.Context) ([]PortInfo, error)
    Open(ctx context.Context, cfg Config) (Port, error)
}
```

Then make `PortInfo` intentionally weak:

```go
type PortInfo struct {
    Name         string
    Description  string // optional
    VID          string // optional
    PID          string // optional
    SerialNumber string // optional
}
```

That lets Linux/Windows/macOS return richer metadata when available, but your platform contract remains: **the stable thing is the configured port name, not discovery metadata**.

Bottom line: **yes, cgo-free cross-platform serial is very doable**, especially for read/write/open/configure. The only area that gets messy is rich USB device discovery, particularly VID/PID/serial-number enumeration on macOS. For your framework, make serial devices admin-configured, long-running, event-emitting resources with a single owner goroutine per port. That fits your websocket/topic model very cleanly.

[1]: https://pkg.go.dev/go/build?utm_source=chatgpt.com "build package"
[2]: https://pkg.go.dev/go.bug.st/serial "serial package - go.bug.st/serial - Go Packages"



This needs to integrated into our settings as well. 
The admin when they set up a serial interface should be able to set (and edit) for a hardware device stuff like:
mode: line
delimiter: "\n"
encoding: utf8
max_frame_bytes: 4096
id, kind=serial.port, path=/dev/ttyUSB0, baud, data bits, parity, stop bits, label, site, alias
some good pyserial settigns to think about too:

ser = serial.Serial()
#ser.port = "/dev/ttyUSB0"
ser.port = "/dev/ttyUSB7"
#ser.port = "/dev/ttyS2"
ser.baudrate = 9600
ser.bytesize = serial.EIGHTBITS #number of bits per bytes
ser.parity = serial.PARITY_NONE #set parity check: no parity
ser.stopbits = serial.STOPBITS_ONE #number of stop bits
#ser.timeout = None          #block read
ser.timeout = 1            #non-block read
#ser.timeout = 2              #timeout block read
ser.xonxoff = False     #disable software flow control
ser.rtscts = False     #disable hardware (RTS/CTS) flow control
ser.dsrdtr = False       #disable hardware (DSR/DTR) flow control
ser.writeTimeout = 2     #timeout for write


let's start small with starlark. Here's a basic shape:

serial.list(kind="serial")

serial.status("weather_station")

serial.open("weather_station")

serial.write(
    "weather_station",
    b"RESET\n",
)

serial.request(
    "weather_station",
    b"READ\n",
    timeout_ms=3000,
    response="line",
)

serial.read(
    "weather_station",
    max=20,
)

serial.close("weather_station")

Right now we will only use it in a sync starlark handler. Later, we will create a separate way to attach to the hardware definition later for a background actor.
