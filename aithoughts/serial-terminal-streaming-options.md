# Serial Terminal Streaming Options

The `demos/serial-terminal` demo started as a simple HTTP command/response UI:

```text
browser POST /api/command
  -> Starlark serial.request(alias, command, until="\n")
  -> browser prints response.text
```

That works for single-line request/response devices, but it is not a great
terminal model. Real serial devices often emit:

- boot banners before the user types anything
- multi-line command responses
- partial chunks split across reads
- unsolicited logs
- delayed output after the first delimiter
- binary or mixed text/binary frames

The Raspberry Pi Pico test exposed this nicely. `HELP\n` returned:

```text
OK HELP mode=NORMAL
COMMAND HELP
COMMAND CONNECT
...
END HELP
```

With `until="\n"`, `serial.request()` correctly returned the first line and
stopped. Follow-up attempts to scrape `serial.status().recent` after the request
were racey because status is a ring-buffer snapshot, not a stream cursor.

## Option 1: Keep HTTP and require better delimiters

For command/response devices with known response terminators, the demo can keep
using `serial.request()` and let the user set a command-specific delimiter:

```text
Read until: END HELP\r\n
```

For the Pico `HELP` command, that is the cleanest single-request model. The
server writes `HELP\n`, reads until `END HELP\r\n`, and returns the whole help
response.

Pros:

- Smallest change.
- Uses the serial API exactly as it exists today.
- Good for devices with clear command terminators.
- Easy to explain and debug.

Cons:

- Poor fit for interactive terminal behavior.
- The delimiter can differ per command.
- Unsolicited output is invisible unless the UI polls status.
- Binary protocols and partial frames remain awkward.
- Users need to know protocol details before the terminal is useful.

This is a good fallback mode, not the best primary terminal model.

## Option 2: HTTP command plus post-command drain

The current demo can send a command with `serial.request()`, then briefly poll
`serial.status()` and append new read events until the stream is quiet:

```text
POST /api/command
  -> serial.request(... until="\n")
  -> browser prints first response
  -> browser GET /api/status/<alias> every 75ms
  -> browser appends new read events until no new reads for ~225ms
```

Pros:

- Still a small demo-layer change.
- Makes multi-line bursts look better.
- Does not require new Go runtime APIs.
- Gives a decent "web terminal" illusion for line-oriented test devices.

Cons:

- Silence is not a protocol boundary.
- Slow devices can be cut off early.
- Chatty devices can keep the UI draining unrelated output.
- Multiple browser connections can race over the same recent ring buffer.
- The browser has to infer event ordering from timestamps.

This is an acceptable demo patch, but it is still a heuristic.

## Option 3: WebSocket demo that polls `serial.status()`

Move the terminal UI to a WebSocket route, but keep the existing serial Starlark
API underneath.

Shape:

```text
browser opens /ws
  <- {type: "devices", devices: serial.list()}

browser sends {type: "open", device: "rpi"}
  -> serial.open("rpi")
  <- {type: "status", ...}

browser sends {type: "write", text: "HELP\n"}
  -> serial.write("rpi", "HELP\n")

server timer every 50-100ms while connected
  -> serial.status("rpi")
  <- {type: "serial_event", event: ...} for new events
```

In this model, commands are just writes. Terminal output comes from read events.
The debug pane can display every event: open, write, read, close, error.

Pros:

- Much closer to terminal semantics.
- Handles boot banners and unsolicited output.
- Multi-line output is natural.
- The browser receives events continuously instead of guessing after each HTTP
  request.
- The UI can be simpler: input writes bytes, terminal appends reads.

Cons:

- Still uses polling internally.
- Starlark websocket handlers are stateless, so connection state needs to live
  in memory or be encoded in timer events.
- Timer frequency affects latency and server load.
- `serial.status().recent` is a ring buffer; if polling is too slow, events can
  be missed.
- Multiple connections to one serial device need clear semantics.

This is probably the best next step for the demo because it improves UX without
requiring a new runtime primitive.

## Option 4: Add serial event subscriptions to the runtime

Add a first-class stream/event capability so Starlark WebSocket code does not
need to poll `serial.status()`.

Possible Starlark effect shape:

```python
def on_connect(ctx):
    return [
        serial.open("rpi"),
        serial.subscribe(ctx.conn_id, "rpi"),
    ]

def on_message(ctx, msg):
    if msg["type"] == "write":
        return serial.write("rpi", msg["data"])

def on_serial(ctx, event):
    return ws.send(ctx.conn_id, {
        "type": "serial_event",
        "event": event,
    })
```

Or keep serial operations as builtins and add generic events:

```python
events.subscribe(ctx.conn_id, "serial:rpi")
```

Then the Go runtime or hardware service publishes read/write/error events to
that topic.

Pros:

- Correct streaming model.
- Low latency without polling.
- Better backpressure and event ordering are possible.
- Works for terminals, dashboards, log viewers, binary protocol inspectors, and
  device monitors.

Cons:

- Requires runtime design work.
- Needs policy and site isolation rules.
- Needs a durable subscription model matching the existing WebSocket effect
  architecture.
- Needs buffering/backpressure decisions for slow clients.
- Needs a story for reconnect and missed events.

This is the right long-term architecture if serial terminals become a core
Quack feature rather than just a demo.

## Option 5: Add stream cursors to `serial.status()`

Keep polling, but make it less racey by adding cursor-based reads:

```python
events = serial.events("rpi", after = cursor, limit = 100)
```

Each serial event would have a monotonic sequence number in addition to a
timestamp:

```json
{
  "seq": 42,
  "at": "2026-06-27T00:50:33.719225724Z",
  "type": "read",
  "text": "OK HELP mode=NORMAL\r\n",
  "base64": "..."
}
```

The browser or Starlark route stores the last seen `seq` and asks for new events
after that point.

Pros:

- Much safer than timestamp comparisons.
- Useful for both HTTP polling and WebSocket polling.
- Smaller runtime change than full subscriptions.
- Easier to test.

Cons:

- Still polling.
- Still limited by ring-buffer retention.
- Requires changes to the hardware serial actor and Starlark module.
- Cursor state still has to live somewhere.

This is a strong middle path. It makes the current architecture more reliable
without jumping straight to push subscriptions.

## Recommendation

For the demo:

1. Convert `demos/serial-terminal` to WebSocket.
2. Treat terminal input as `serial.write()`, not `serial.request()`.
3. Poll `serial.status()` on a short timer while connected.
4. Track the last event timestamp client-side for now.
5. Display all `read` events in the terminal.
6. Display all events in the debug pane.
7. Keep an optional HTTP/request mode only for testing simple protocols with a
   known delimiter.

For the runtime:

1. Add monotonic sequence numbers to serial events.
2. Expose a cursor API such as `serial.events(alias, after=?, limit=?)`.
3. Later, add first-class serial event subscriptions that integrate with the
   existing WebSocket effects model.

That sequence keeps the demo useful immediately while moving the platform toward
the correct abstraction: serial is a byte stream, and terminal UIs should render
events from that stream instead of pretending every command has one neat HTTP
response.

