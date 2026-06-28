
Yes, that is the model I’d head toward.

But I would be careful with the word **queue**, because there are really three related concepts:

```text
event source  = where events originate
stream/topic  = fan-out event log that many consumers can observe
queue         = work queue where each event is claimed by one worker
pipeline      = named flow from source -> transforms -> output stream/queue
```

For your serial terminal case, you usually want a **stream/topic**, not a work queue. If three browser clients are watching the same terminal, all three should see the same serial output. A classic queue would deliver each event to only one consumer, which is wrong for terminal output.

So I’d probably name the primitive something like **streams** or **pipelines**, and reserve **queue** for one-consumer work processing.

## The better generalized model

Something like:

```yaml
streams:
  serial.raw:
    retain: 1000
    overflow: drop_oldest

  serial.terminal:
    retain: 500
    overflow: drop_oldest

events:
  - selector: "hardware.serial.concretedevicename.*"
    handler: api/serial.star:on_hardware_serial

  - selector: "stream.serial.raw"
    handler: api/serial.star:on_raw_serial

  - selector: "stream.serial.terminal"
    handler: api/ws.star:on_terminal_event
```

Then code can publish derived events:

```python
def on_hardware_serial(ctx, event):
    return streams.publish("serial.raw", event)

def on_raw_serial(ctx, event):
    if event.kind == "read":
        line = {
            "kind": "read",
            "text": event.payload.get("text", ""),
            "seq": event.seq,
        }
        return streams.publish("serial.terminal", line)
```

And a websocket can subscribe to the derived stream:

```python
def on_connect(ctx):
    return [
        ws.subscribe(ctx.conn_id, "stream.serial.terminal"),
        ws.send(ctx.conn_id, snapshot()),
    ]
```

That is basically what your current app is doing manually with `TOPIC = "serial-terminal:session"`, `events.publish(TOPIC, ...)`, `ws.subscribe(...)`, and an `on_event` that forwards event payloads to the websocket. The main improvement is making that pattern first-class instead of hand-rolled in every app. 

## Selectors should be over one unified namespace

Your examples are good:

```text
hardware.serial.concretedevicename
queue.name
queue.*
```

I’d make the namespace slightly more explicit:

```text
hardware.serial.rpi.read
hardware.serial.rpi.error

stream.serial.raw
stream.serial.terminal
stream.alerts

queue.jobs.thumbnail
queue.jobs.notify

app.serial-terminal.command
system.timer.cleanup
```

Then selectors can be:

```yaml
events:
  - selector: "hardware.serial.rpi.*"
    handler: api/serial.star:on_serial

  - selector: "stream.serial.*"
    handler: api/debug.star:on_stream_event

  - selector: "queue.jobs.*"
    handler: api/jobs.star:on_job
```

The dotted namespace gives you one routing surface for hardware events, app-created streams, internal queues, timers, websocket messages, plugin events, and future stuff.

## I’d distinguish streams from queues

This is the distinction I would bake into the API:

### Streams/topics

Use when every subscriber should see the event.

Good for:

```text
serial output
debug logs
terminal transcript
camera status
GPIO changes
websocket fanout
audit-ish event trails
```

Shape:

```yaml
streams:
  serial.terminal:
    retain: 500
    delivery: fanout
```

### Queues

Use when one worker should claim the event.

Good for:

```text
send this email
process this file
take one camera capture
retry this command
run this background job
```

Shape:

```yaml
queues:
  command.work:
    retain: 1000
    delivery: competing_consumers
    retry:
      attempts: 3
```

Terminal output should not go into a work queue. Serial command jobs maybe could.

## The most useful primitive might be `route`

Instead of forcing every simple forwarding path to be a handler, allow declarative routing:

```yaml
routes:
  - from: "hardware.serial.rpi.*"
    to: "stream.serial.raw"

  - from: "stream.serial.terminal"
    to: "ws.serial-terminal"
```

Then handlers are only needed when you transform, filter, or cause side effects:

```yaml
events:
  - selector: "stream.serial.raw"
    handler: api/serial.star:frame_serial

  - selector: "stream.serial.terminal"
    handler: api/terminal.star:remember_terminal_line
```

That gives you a nice layered model:

```text
producer -> route -> stream -> handler -> derived stream -> websocket
```

## Example for your terminal

Config:

```yaml
streams:
  serial.rpi.raw:
    retain: 2000
    overflow: drop_oldest

  serial.rpi.terminal:
    retain: 500
    overflow: drop_oldest

routes:
  - from: "hardware.serial.rpi.*"
    to: "stream.serial.rpi.raw"

events:
  - selector: "stream.serial.rpi.raw"
    handler: api/serial_terminal.star:on_raw_serial

websockets:
  - path: /ws
    handler: api/serial_terminal.star
```

Starlark:

```python
TERMINAL = "stream.serial.rpi.terminal"

def on_connect(ctx):
    return [
        ws.subscribe(ctx.conn_id, TERMINAL),
        ws.send(ctx.conn_id, snapshot()),
    ]

def on_message(ctx, msg):
    if msg["type"] == "write":
        serial.write("rpi", msg["text"] + "\n")
        return []

def on_raw_serial(ctx, event):
    if event.kind == "read":
        line = {
            "kind": "read",
            "text": event.payload.get("text", ""),
            "seq": event.seq,
            "at": event.at,
        }
        memory.ring("terminal:rpi", 500).append(line)
        return streams.publish("serial.rpi.terminal", {
            "type": "terminal",
            "line": line,
        })

    if event.kind == "error":
        return streams.publish("serial.rpi.terminal", {
            "type": "error",
            "message": event.payload.get("error", "serial error"),
        })
```

Now the browser never polls. The serial runtime emits hardware events. The app transforms raw serial events into terminal events. The websocket subscribes to terminal events.

## What `queue.*` selection enables

Allowing selectors like this is powerful:

```yaml
events:
  - selector: "queue.*"
    handler: api/debug.star:on_any_queue_event
```

or:

```yaml
events:
  - selector: "stream.*"
    handler: api/observer.star:on_any_stream_event
```

That gives users a lot of flexibility for internal app architecture:

```text
hardware.serial.rpi.read
  -> stream.serial.rpi.raw
  -> stream.serial.rpi.lines
  -> stream.serial.rpi.commands
  -> queue.serial.rpi.command_jobs
  -> stream.serial.rpi.terminal
```

You can build small pipelines without Quack knowing what the app is.

## But you need loop protection

Once streams can select streams, it is easy to accidentally create this:

```yaml
events:
  - selector: "stream.*"
    handler: api/echo.star:on_event
```

```python
def on_event(ctx, event):
    return streams.publish("debug", event)
```

If `stream.debug` also matches `stream.*`, you created an infinite loop.

So the event envelope needs:

```python
{
    "id": "evt_123",
    "source": "stream.serial.raw",
    "topic": "stream.serial.raw",
    "kind": "read",
    "depth": 2,
    "caused_by": "evt_122",
    "route": ["hardware.serial.rpi.read", "stream.serial.raw"],
    "payload": {...}
}
```

And the runtime should have guardrails:

```text
max event depth
max events emitted per handler
detect immediate self-publish loops
optional allow_reentrant: true
warnings for selector: "*"
```

## My preferred shape

I’d design the core around this:

```yaml
streams:
  name:
    retain: N
    overflow: drop_oldest | reject_new
    replay: true | false

queues:
  name:
    retain: N
    retry: ...
    concurrency: ...

routes:
  - from: selector
    to: stream_or_queue

events:
  - selector: selector
    handler: file.star:function
```

Selectors can match:

```text
hardware.*
stream.*
queue.*
app.*
system.*
```

Then serial is just one producer:

```text
hardware.serial.rpi.read
```

and your app can derive whatever it needs:

```text
stream.serial.rpi.raw
stream.serial.rpi.lines
stream.serial.rpi.terminal
queue.serial.rpi.commands
```

That is the sweet spot: Quack provides the event plumbing, retention, replay, selector matching, delivery semantics, and loop protection. The user decides what the pipeline means.



Yes. I think a **generic internal event pipeline with selector-bound handlers** is the better core design.

The serial-specific version:

```yaml
hardware:
  serial:
    - device: rpi
      on_event: api/serial.star
```

is understandable, but it bakes the routing model into the serial subsystem. The more scalable Quack model is:

```yaml
events:
  - selector: "hardware.serial.*"
    on_event: api/serial.star:on_serial
```

or more explicitly:

```yaml
events:
  - selector: "hardware.serial.rpi.read"
    on_event: api/serial.star:on_serial_read

  - selector: "hardware.serial.rpi.error"
    on_event: api/serial.star:on_serial_error
```

That is much closer to what Quack probably wants long-term.

Your current websocket handler is already halfway there: it has a `TOPIC`, publishes state/terminal/debug messages, subscribes websocket connections to that topic, and has a generic `on_event(ctx, event)` that forwards event payloads to the websocket. The awkward part is that serial events are still pulled through `serial.status(...).recent` inside `_drain_status()` instead of entering that same event system directly. 

## The model I’d use

The runtime has one internal event pipeline:

```text
hardware/plugin producers
        ↓
internal event bus / event log
        ↓
selector matching
        ↓
Starlark event handlers
        ↓
memory / websocket / app events / commands
```

For serial:

```text
serial reader loop
        ↓
hardware.serial.rpi.read event
        ↓
api/serial.star:on_serial
        ↓
append transcript
        ↓
publish app.serial-terminal.rpi.line
        ↓
websocket clients receive it
```

So yes, something like this:

```yaml
events:
  - selector: "hardware.serial.*"
    on_event: api/serial.star:on_serial
```

makes more sense than serial having its own special `on_event` field.

## Why generic events are better

Because serial is not special in the way that matters. A lot of hardware and runtime things have the same shape:

```text
serial byte arrived
GPIO pin changed
HID report arrived
camera frame captured
process wrote stdout
timer fired
file changed
webhook arrived
job completed
```

All of these are:

```text
producer -> event -> handler -> side effects
```

So Quack should probably have one general event pipeline, and serial should just be one producer.

Then users can build apps like:

```yaml
events:
  - selector: "hardware.serial.rpi.read"
    on_event: api/rpi.star:on_read

  - selector: "hardware.gpio.button1.change"
    on_event: api/buttons.star:on_change

  - selector: "hardware.hid.scanner.input"
    on_event: api/scanner.star:on_scan

  - selector: "app.alerts.*"
    on_event: api/alerts.star:on_alert
```

That is cleaner than each subsystem inventing:

```yaml
serial.on_event
gpio.on_change
hid.on_report
camera.on_frame
process.on_stdout
```

Those can exist as convenience shorthands later, but the underlying mechanism should be generic.

## Event names should be structured

I would use a stable dot-path namespace:

```text
hardware.serial.rpi.open
hardware.serial.rpi.close
hardware.serial.rpi.read
hardware.serial.rpi.write
hardware.serial.rpi.error
hardware.serial.rpi.status
```

Maybe also:

```text
hardware.serial.*.read
hardware.serial.rpi.*
hardware.*.*.error
app.serial-terminal.rpi.line
ws.serial-terminal.command
system.timer.cleanup
```

But internally, do not treat the string as the only source of truth. The event should also have structured fields:

```python
{
    "id": "evt_...",
    "seq": 1842,
    "topic": "hardware.serial.rpi.read",
    "source": "hardware.serial.rpi",
    "kind": "read",
    "device": "rpi",
    "device_kind": "serial",
    "at": "...",
    "payload": {
        "bytes": "...base64...",
        "text": "OK\r\n",
        "encoding": "utf-8",
    }
}
```

The topic is for routing. The fields are for code.

That lets a handler do:

```python
def on_serial(ctx, event):
    if event.kind == "read":
        text = event.payload.get("text", "")
        ...
```

without parsing `"hardware.serial.rpi.read"` manually.

## Handler config should name the function

Your proposed shape is good:

```yaml
events:
  - selector: "hardware.serial.*"
    on_event: api/serial.star:on_serial
```

I would require the function name. Avoid magic `on_event` unless omitted:

```yaml
events:
  - selector: "hardware.serial.*"
    handler: api/serial.star:on_serial
```

Then maybe allow shorthand:

```yaml
events:
  - selector: "hardware.serial.*"
    handler: api/serial.star
```

where `api/serial.star:on_event` is implied.

But explicit is better once apps get large.

## Selector matching should support more than glob strings eventually

The simple version:

```yaml
selector: "hardware.serial.*"
```

is fine.

But you may eventually want structured selectors:

```yaml
events:
  - match:
      source: hardware.serial
      device: rpi
      kind: read
    handler: api/serial.star:on_read
```

or:

```yaml
events:
  - selector: "hardware.serial.*.read"
    where:
      payload.text_contains: "ALARM"
    handler: api/alarm.star:on_alarm
```

I would not build the fancy version first. Start with topic globs. But design the event envelope so structured matching is possible later.

## The serial loop belongs below this

With the generic pipeline, serial works like this:

```text
serial plugin opens /dev/ttyACM0
serial plugin runs the blocking read loop
serial plugin emits hardware.serial.rpi.read events
Quack event pipeline routes those events
Starlark handlers run per event
```

The user does **not** write:

```python
while True:
    serial.status(...)
```

Instead, user code is per event:

```python
def on_serial(ctx, event):
    if event.kind == "read":
        events.publish("app.serial-terminal.rpi.line", {
            "text": event.payload.get("text", ""),
        })
```

This keeps Starlark simple and bounded. The runtime owns the blocking loop, cancellation, reconnects, buffering, and backpressure.

## You still want app-level topics

There should be a distinction between **hardware events** and **app events**.

Hardware event:

```text
hardware.serial.rpi.read
```

App event:

```text
app.serial-terminal.session.terminal
```

Websocket topic:

```text
ws.serial-terminal.session
```

In your current app, `TOPIC = "serial-terminal:session"` is basically the websocket fanout topic. That is not quite the same as the hardware event stream. 

I would keep these separate:

```text
hardware.serial.rpi.read
  raw-ish device event

app.serial-terminal.rpi.transcript
  interpreted UI transcript event

app.serial-terminal.rpi.state
  UI state event
```

That separation matters because multiple apps might consume the same hardware stream differently.

Example:

```yaml
events:
  - selector: "hardware.serial.rpi.read"
    handler: api/terminal.star:on_serial

  - selector: "hardware.serial.rpi.read"
    handler: api/alarm.star:on_serial

  - selector: "hardware.serial.rpi.error"
    handler: api/ops.star:on_hardware_error
```

One serial stream, multiple consumers.

## Delivery semantics need to be explicit

This is where generic event pipelines can get messy if underspecified.

I would define simple semantics:

```text
Events have stable IDs.
Events have per-source sequence numbers.
Ordering is guaranteed per source.
Handlers may receive an event more than once.
Handlers should be idempotent.
The runtime keeps a bounded replay buffer.
Slow consumers can fall behind and receive a dropped-events marker.
```

Do not promise exactly-once. It is not worth it.

For serial terminal use, at-least-once plus sequence IDs is enough. If a terminal sees duplicate event `seq=1842`, it can ignore it.

## Backpressure matters

The serial reader cannot block forever because a Starlark handler is slow.

So the event flow should be:

```text
serial read loop -> append to bounded device event log -> dispatch async to handlers
```

Not:

```text
serial read loop -> synchronously run all handlers -> continue reading
```

Otherwise one slow handler can cause the OS serial buffer to fill and lose data.

You want something like:

```text
device event log limit: 10,000 events or 1 MB
on overflow: drop oldest and emit hardware.serial.rpi.dropped
handler timeout: bounded
handler failures: emit app/system error event
```

For terminal output, dropping oldest may be fine. For protocols where loss is unacceptable, the app should use a stronger storage/queue primitive or a flow-controlled command protocol.

## Avoid event loops

Once handlers can publish events, you need to prevent accidental loops.

Bad:

```yaml
events:
  - selector: "*"
    handler: api/log.star:on_event
```

and then:

```python
def on_event(ctx, event):
    events.publish("app.log.event", event)
```

which matches `*` again.

So you need one or more of:

```text
reserved namespaces
max event depth
ctx.cause / ctx.parent_event_id
do not route events published by same handler unless explicitly allowed
selector validation warnings
```

I would probably have namespaces:

```text
hardware.*  produced by hardware/runtime
system.*    produced by Quack runtime
app.*       produced by user apps
ws.*        websocket/application messages, if exposed
```

And make broad selectors noisy or admin-only.

## Permissions should apply at selector level

A site should not be able to bind to:

```yaml
selector: "hardware.serial.*"
```

unless it has permission to those devices.

Better:

```yaml
events:
  - selector: "hardware.serial.rpi.*"
    handler: api/serial.star:on_serial
```

And Quack validates:

```text
site has access to serial alias rpi?
handler can receive read data?
handler can write back to device?
handler can observe errors/status?
```

For `hardware.serial.*`, Quack should expand that only across devices the site is allowed to see.

## What this does to the websocket terminal

With the generic pipeline, your app becomes cleaner.

Config:

```yaml
events:
  - selector: "hardware.serial.rpi.*"
    handler: api/serial_terminal.star:on_serial
```

Starlark:

```python
TOPIC = "app.serial-terminal.rpi"

def on_connect(ctx):
    ws.subscribe(ctx.conn_id, TOPIC)
    return ws.send(ctx.conn_id, snapshot())

def on_message(ctx, msg):
    if msg["type"] == "write":
        serial.write("rpi", msg["text"] + "\n")
        return []

    if msg["type"] == "open":
        serial.open("rpi")
        return []

    if msg["type"] == "close":
        serial.close("rpi")
        return []

def on_serial(ctx, event):
    if event.kind == "read":
        line = {
            "kind": "read",
            "text": event.payload.get("text", ""),
            "seq": event.seq,
            "at": event.at,
        }
        memory.ring("terminal:rpi", 500).append(line)
        return events.publish(TOPIC, {
            "type": "terminal",
            "line": line,
        })

    if event.kind == "error":
        return events.publish(TOPIC, {
            "type": "error",
            "message": event.payload.get("error", "serial error"),
        })
```

No browser polling. No `serial.status().recent` diffing. No timestamp cursor. No “drain” workaround in the websocket handler.

## My recommendation

Use the generic pipeline as the foundation:

```yaml
events:
  - selector: "hardware.serial.*"
    handler: api/serial.star:on_serial
```

Then optionally support serial-specific sugar:

```yaml
hardware:
  serial:
    - device: rpi
      events:
        read: api/serial.star:on_read
        error: api/serial.star:on_error
```

but compile that internally to the generic event routing table.

The core should be:

```text
producers emit events
events have names and structured envelopes
selectors bind events to handlers
handlers are short-lived and per-event
long-running loops stay in runtime/plugins
apps publish derived app events
websockets subscribe to app topics
```

That gives you the clean “serial reader loop -> event queue -> consumers” architecture, but it also works for every other weird hardware/device/plugin thing you add later.
