
Implement this as a **hardware event stream** from plugin to server, not as polling, and not as direct plugin access to runtime pipes.

The model should be:

```text
serial fd / pyserial
    ↓
hardware serial actor read loop
    ↓
plugin-local hardware event queue
    ↓
server-owned WatchHardwareEvents stream
    ↓
server validates/maps event
    ↓
events.publish("hardware.serial.<alias>.read", ...)
    ↓
selectors / Starlark / WebSocket subscribers
```

The hardware plugin becomes the equivalent of pySerial’s `ReaderThread`: it owns the blocking read loop and turns incoming bytes into events.

## 1. Add a generic plugin-to-server event stream

Do not make this serial-only unless you are absolutely sure serial is the only hardware producer you will ever have. The better primitive is:

```text
WatchHardwareEvents
```

instead of:

```text
WatchSerialEvents
```

The existing RPC shape remains:

```text
OpenSerial
CloseSerial
WriteSerial
RequestSerial
SerialStatus
```

Then add:

```text
WatchHardwareEvents
```

The server calls it and holds the stream open:

```text
quack-server ── WatchHardwareEvents() ──▶ quack-hardware-plugin
quack-server ◀──── HardwareEvent stream ── quack-hardware-plugin
```

Prefer **server subscribes to plugin stream** over **plugin calls back to server**.

That gives you simpler lifecycle and security:

```text
server owns connection
server authenticates plugin
server reconnects on failure
plugin does not need callback URLs
plugin does not know runtime routing
```

## 2. The serial actor should emit raw read events

Inside the hardware/plugin process, each opened serial actor should have a persistent read loop:

```text
open serial port
start read goroutine/thread
block on read/select
when bytes arrive:
    create serial-read HardwareEvent
    push to plugin event queue
```

Conceptually:

```go
func (a *SerialActor) readLoop(ctx context.Context) {
    buf := make([]byte, 4096)

    for {
        n, err := a.port.Read(buf)

        if n > 0 {
            chunk := append([]byte(nil), buf[:n]...)

            a.emit(HardwareEvent{
                Type:       "serial.read",
                DeviceID:   a.DeviceID,
                Generation: a.Generation,
                Seq:        a.nextSeq(),
                Time:       time.Now(),
                Payload: HardwareEventPayload{
                    SerialRead: &SerialReadPayload{
                        Bytes: chunk,
                    },
                },
            })
        }

        if err != nil {
            a.emit(serialReadErrorEvent(err))
            return
        }
    }
}
```

The important part: this loop is not runtime polling. It is the hardware actor’s real event pump. It blocks efficiently until serial data arrives.

## 3. Use a hardware event envelope

Do not stream just bytes. Stream an envelope.

Something like:

```go
type HardwareEvent struct {
    ID          string
    PluginID    string
    DeviceID    string
    Type        string

    Generation  string
    Seq         uint64
    Time        time.Time

    Payload     HardwarePayload

    Origin      string
    CausationID string
    CorrelationID string
}
```

For serial reads:

```go
type SerialReadPayload struct {
    Bytes []byte
}
```

Required fields I would include from day one:

```text
id
plugin_id
device_id
type
generation
seq
time
payload.bytes
```

`generation` matters because serial devices disconnect and reconnect. You want to distinguish:

```text
device weather_station, generation A, seq 97
device weather_station, generation B, seq 1
```

Those are different physical sessions.

## 4. Server translates hardware identity to runtime topic

The plugin should not publish directly to:

```text
hardware.serial.weather_station.read
```

The plugin should emit physical/device-level facts:

```text
plugin_id = local-plugin-1
device_id = usb-serial-abc123
type = serial.read
bytes = ...
```

Then the server maps that to app/runtime space:

```text
plugin device id
    ↓
registered hardware device
    ↓
site binding
    ↓
site alias
    ↓
runtime topic
```

So the server publishes:

```text
hardware.serial.<alias>.read
```

Example:

```text
plugin event:
    type: serial.read
    device_id: usb-serial-abc123

server mapping:
    usb-serial-abc123 -> site device alias "weather_station"

runtime publish:
    hardware.serial.weather_station.read
```

This keeps policy centralized. The plugin should not know about sites, Starlark, selectors, or websocket topics.

## 5. Publish lifecycle events too

Serial does not only produce bytes. It also produces state changes. Make these first-class events:

```text
hardware.serial.<alias>.opened
hardware.serial.<alias>.closed
hardware.serial.<alias>.read
hardware.serial.<alias>.read_error
hardware.serial.<alias>.write_error
hardware.serial.<alias>.disconnected
hardware.serial.<alias>.reconnected
hardware.serial.<alias>.overflow
```

Then selectors can do useful things:

```yaml
events:
  - selector: "hardware.serial.*.read"
    on_event: api/serial.star:on_serial

  - selector: "hardware.serial.*.disconnected"
    on_event: api/serial.star:on_serial_down
```

## 6. Treat byte chunks as chunks, not messages

Serial is a byte stream. One read event is not necessarily one logical message.

This:

```text
event 1 bytes: "HE"
event 2 bytes: "LLO\n"
```

is equivalent to:

```text
event 1 bytes: "HELLO\n"
```

So your base event should be raw bytes:

```text
hardware.serial.<alias>.read
```

Then optionally add runtime-level framing:

```text
hardware.serial.<alias>.line
hardware.serial.<alias>.frame
```

I would keep protocol framing out of the hardware plugin unless the admin explicitly configures a generic line/framing layer. The plugin should be dumb and reliable:

```text
read bytes
timestamp
sequence
emit
```

The runtime can provide nicer helpers later.

## 7. Move `serial.request()` above the event stream

Long term, `serial.request()` should not depend on plugin-local recent events.

It should become sugar over:

```text
runtime calls WriteSerial
runtime waits for matching read events
runtime accumulates bytes until condition
runtime returns result or timeout
```

So:

```python
serial.request("weather_station", b"READ\n", until=b"\n", timeout_ms=1000)
```

becomes internally:

```text
1. register waiter on hardware.serial.weather_station.read
2. call WriteSerial(weather_station, b"READ\n")
3. accumulate incoming read chunks
4. stop when until/timeout/max_bytes condition is met
5. return response
```

This is better because now all consumers share one truth:

```text
web terminal
Starlark event handler
serial.request waiter
admin diagnostics
```

all see the same event stream.

The plugin can still keep a small ring buffer for debug/status, but it should no longer be the primary runtime delivery mechanism.

## 8. Add bounded queues and explicit overflow

This is the main implementation trap.

Once serial becomes push-based, a slow consumer can create pressure:

```text
serial reader
    ↓
plugin queue
    ↓
WatchHardwareEvents stream
    ↓
server pipe
    ↓
Starlark handler / websocket
```

You need bounded queues at the plugin and server boundary.

I would define per-device settings like:

```yaml
serial:
  max_event_queue_bytes: 1048576
  max_event_queue_items: 1024
  overflow_policy: drop_oldest
```

Possible overflow policies:

```text
drop_oldest
drop_newest
disconnect_device
disconnect_subscriber
block_reader
```

For raw serial, I would avoid `block_reader` as the default. Blocking the reader can cause kernel/driver buffers to fill and make behavior harder to reason about.

Recommended defaults:

```text
plugin device queue: bounded, drop_oldest, emit overflow event
server websocket queues: bounded, disconnect slow subscriber or drop_oldest
request waiters: fail if overflow touches their generation/window
```

Do not silently drop. Emit:

```text
hardware.serial.<alias>.overflow
```

or annotate the next read event with:

```go
DroppedEvents int64
DroppedBytes  int64
```

## 9. Keep writes as commands, not events, for now

Do not immediately make this symmetrical:

```text
events.publish("hardware.serial.weather_station.write", ...)
```

That is tempting, but it makes the event bus both the event plane and the command plane.

For now, keep writes explicit:

```python
serial.write("weather_station", b"READ\n")
```

and internally:

```text
runtime serial.write
    ↓
server checks permission/site binding
    ↓
server calls plugin WriteSerial
    ↓
plugin writes to fd
```

Reads become events. Writes remain commands.

That avoids a whole class of loops like:

```text
handler sees serial.read
handler publishes serial.write
peer receives serial.write
peer republishes serial.write
...
```

You can add command topics later if you want, but they should be a deliberate feature with permissioning and loop prevention.

## 10. The core server ingester

The server should have a hardware manager that owns plugin streams:

```go
func (m *HardwareManager) runPluginEventStream(ctx context.Context, plugin PluginClient) {
    for {
        stream, err := plugin.WatchHardwareEvents(ctx, WatchHardwareEventsRequest{})
        if err != nil {
            m.markPluginDisconnected(plugin.ID())
            backoff()
            continue
        }

        for {
            ev, err := stream.Recv()
            if err != nil {
                m.markPluginDisconnected(plugin.ID())
                break
            }

            m.handleHardwareEvent(ctx, plugin.ID(), ev)
        }
    }
}
```

Then:

```go
func (m *HardwareManager) handleHardwareEvent(ctx context.Context, pluginID string, ev HardwareEvent) {
    if !m.validateEvent(pluginID, ev) {
        return
    }

    runtimeEvent, ok := m.translateHardwareEvent(ev)
    if !ok {
        return
    }

    m.events.Publish(ctx, runtimeEvent.Topic, runtimeEvent)
}
```

Translation does:

```text
validate plugin owns device
resolve device registration
resolve site aliases/bindings
construct topic
attach metadata
publish into pipe
```

## 11. Runtime event shape

The Starlark-facing event should probably not expose raw plugin internals by default.

Maybe:

```python
def on_serial(ctx, event):
    event.topic       # "hardware.serial.weather_station.read"
    event.type        # "hardware.serial.read"
    event.device      # "weather_station"
    event.bytes       # b"..."
    event.text        # optional decoded text, maybe lossy/explicit
    event.seq
    event.generation
    event.time
```

For safety, I would make text decoding explicit or configured. Raw bytes should be canonical.

Good default:

```python
event.bytes
```

Optional convenience:

```python
event.text(encoding="utf-8", errors="replace")
```

Avoid automatically treating arbitrary serial bytes as valid UTF-8.

## 12. Minimal viable implementation

I would implement in this order.

### Phase 1: plugin event stream

Add:

```text
WatchHardwareEvents
```

with only serial read events.

No Starlark selectors yet. Just prove server receives events.

```text
serial actor reads bytes
plugin emits HardwareEvent
server logs HardwareEvent
```

### Phase 2: server publish bridge

Add translation:

```text
HardwareEvent(serial.read, device_id)
    -> RuntimeEvent("hardware.serial.<alias>.read")
    -> events.publish(...)
```

Now websocket subscribers can watch serial output without polling.

### Phase 3: selector handlers

Enable:

```yaml
events:
  - selector: "hardware.serial.*.read"
    on_event: api/serial.star:on_serial
```

Add guardrails:

```text
max handler runtime
max event size
max queued events
permission check for site binding
```

### Phase 4: make `serial.request()` consume runtime events

Rebuild request/response on top of:

```text
WriteSerial + wait on runtime serial-read events
```

Deprecate plugin-local recent-events as the source of truth.

### Phase 5: lifecycle, overflow, reconnect

Add:

```text
opened
closed
disconnected
reconnected
overflow
read_error
write_error
```

This makes the admin UI and websocket behavior much easier to reason about.

## The final architecture

The clean final model is:

```text
quack-hardware-plugin
  serial actors
    own fd
    perform blocking reads
    emit raw hardware events
    keep diagnostic ring buffer only

  WatchHardwareEvents
    streams hardware events to server


quack-server
  hardware manager
    owns plugin connections
    consumes WatchHardwareEvents
    validates events
    maps device IDs to site aliases
    publishes runtime events

  runtime event bus / pipes
    stores/distributes runtime events
    wakes selector handlers
    feeds websocket subscribers
    feeds serial.request waiters
```

## The key design rule

Use this as the rule of thumb:

```text
The plugin owns physical I/O.
The server owns routing, policy, and application semantics.
The event bus owns distribution.
```

So the missing on-ramp is specifically:

```text
plugin serial actor
    -> HardwareEvent
    -> WatchHardwareEvents stream
    -> server hardware ingester
    -> events.publish(...)
```

That is the correct implementation direction.
