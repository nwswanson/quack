# Pipes

This document describes Quack's internal pipe facility as implemented today.
It is written for operators and maintainers who need to reason about
concurrency, fanout, attachment, backpressure, and failure isolation rather
than only the Starlark authoring model.

The short version is:

```text
producer -> host pipe store -> manifest event routes -> Starlark handlers
         -> websocket subscriptions -> websocket handler invocations -> frames
```

Pipes are an in-process, site-scoped event transport. They are not a durable
message broker, not a distributed log, and not a replacement for persistent
application state. They are designed to give Quack a host-owned path for
runtime events, hardware events, and websocket fanout while preserving the core
runtime rule: Starlark handles one bounded event at a time and returns
declarative effects.

## Design Model

A pipe is a named stream of events inside the Go host. Events are addressed by a
topic. In the current implementation, the topic also selects the pipe
configuration: publishing to `app.chat.room.7` uses the configured pipe with the
same name when one exists, otherwise the host creates an implicit pipe with
default retention.

Each pipe is scoped by site. Two sites can publish the same topic string without
sharing events, subscribers, sequence numbers, or retained history.

Pipes serve three distinct purposes:

1. Bound recent history for a topic in the host process.
2. Trigger manifest-declared event handlers.
3. Trigger websocket `on_event` handlers for currently subscribed connections.

Those are related but not identical. The pipe store records accepted events.
Manifest event routes run Starlark functions for matching topics. Websocket
subscriptions attach live connections to topics and re-invoke their websocket
entrypoint when matching events are published.

## Manifest Declaration

Pipes and event routes are declared in `site.yml`:

```yaml
pipes:
  - name: serial-terminal-pipes.session
    retain: 64
    overflow: drop_oldest

  - selector: "hardware.serial.*"
    retain: 128
    overflow: drop_oldest
    key_by: selector

events:
  - selector: "hardware.serial.*"
    on_event: api/terminal.star:on_hardware_event
```

Pipe names, pipe selectors, and event selectors are dotted names. Each name
segment may contain letters, digits, `_`, or `-`. Empty segments are rejected.
Wildcards are supported only as the final segment, so `hardware.serial.*` is
valid and `hardware.*.read`, `*.read`, `hardware*`, and `*` are not.

`pipe.name` is the legacy exact-topic form. `pipe.selector` may be exact or a
final-segment prefix selector. A selector is a policy matcher for published
topics; it does not expand into possible topic names.

`on_event` must be formatted as:

```text
file.star:function
```

The file must be a `.star` module in the uploaded site bundle, and the function
name must be a valid Starlark identifier.

At upload time, Quack validates these declarations and persists them into site
settings as JSON under:

```text
runtime.pipes
runtime.events
```

At dispatch time, the runtime HTTP handler reads the current site's persisted
manifest settings. This means event routing follows the current deployed
manifest for the site, not the manifest of an older websocket connection except
where the connection snapshot explicitly retains its route and version for its
own websocket handler invocation.

## Event Shape

The internal pipe event type contains:

```text
id
site
pipe
topic
source_kind
source_name
payload
headers
created_at
seq
```

`site` scopes the event. `pipe` is the resolved pipe name. `topic` is the topic
that handlers and websocket subscribers match. `source_kind` and `source_name`
identify ingress, for example `runtime/events.publish` or
`hardware/<device-id>`. `payload` is an opaque byte slice at the pipe layer.
When Starlark receives the event, the websocket/event runtime decodes the bytes
using the same rule as websocket messages: valid JSON becomes Starlark values,
non-JSON bytes become a string, and an empty payload becomes `None`.

`seq` is assigned per site and pipe. It is monotonic within that in-memory pipe
instance and starts from zero when the process starts or when the pipe is first
created. It is not a global ordering primitive and is not durable across
process restart.

`id` defaults to:

```text
<site>:<pipe>:<seq>
```

if the publisher did not provide one.

The store defensively copies payload bytes and headers on publish and on recent
event retrieval. That prevents a caller from mutating retained event contents
after publication.

## Publishing

Starlark publishes through the `events` module:

```python
return events.publish("app.room.7", {
    "type": "message",
    "room": "7",
    "text": "hello",
})
```

`events.publish` returns a declarative effect. Starlark does not synchronously
write to subscribers and does not directly mutate the pipe store. After the
handler returns, the Go host applies the effect by dispatching the event.

Hardware events also publish into pipes. The server runs a hardware event stream
loop, receives mapped `hardware.HardwareEvent` values, and calls
`DispatchHardwareEvent`. For serial devices, bound hardware events become topics
of the form:

```text
hardware.serial.<site-device-alias>.<event-kind>
```

Examples:

```text
hardware.serial.meter.opened
hardware.serial.meter.read
hardware.serial.meter.read_error
hardware.serial.meter.closed
hardware.serial.meter.overflow
```

Only devices bound to the site with read permission are mapped into that site's
runtime topic namespace. The mapping strips physical device paths from the site
view and exposes the configured site alias.

## Attachment

There is no long-lived Starlark listener object. Attachment is represented as
host-owned data.

Websocket code attaches a connection to a topic with:

```python
def on_connect(ctx):
    return [
        ws.subscribe(ctx.conn_id, "app.room.7"),
        ws.send(ctx.conn_id, {"type": "ready"}),
    ]
```

The Go socket manager records the subscription in two indexes:

```text
scoped topic -> connection ids
connection id -> scoped topics
```

The scoped topic is the site plus the topic. This prevents accidental cross-site
fanout even when topic names match.

On disconnect, close, explicit `ws.unsubscribe`, or `ws.unsubscribe_all`, the
manager removes the connection from the subscription indexes. When the host
unregisters a connection, it also closes the underlying network connection and
its writer loop.

Manifest event routes are a second attachment mechanism. They attach a Starlark
function to a selector, not a websocket connection to a topic. A matching event
causes the host to invoke the declared event handler:

```python
def on_hardware_event(ctx, event):
    # event.topic and event.payload are available here.
    return events.publish("app.serial.session", {
        "type": "terminal",
        "line": event.payload,
    })
```

The handler can return the same effect types as a websocket handler, including
`events.publish`, `ws.send`, `ws.broadcast`, and subscription changes. In
practice, event handlers are often used to transform low-level producer topics
into application-level topics.

## Retrieval And Replay

The host pipe store keeps recent events in memory and exposes internal recent
event retrieval to Go code. Today there is no public Starlark API for reading a
pipe's retained events, seeking by sequence number, acknowledging delivery, or
replaying missed events to a websocket subscriber.

This matters operationally:

- A websocket subscription receives future events after the subscription effect
  has been applied.
- A new websocket connection should send its initial view from durable or
  application-owned state, such as the `memory` module, database-backed state,
  or a domain-specific snapshot.
- The retained pipe buffer is a host-side operational primitive, not a user
  visible event log with consumer offsets.

The `serial-terminal-pipes` demo follows this pattern. The pipe carries live
session updates, while terminal history and debug history are stored in
`memory` and sent as a snapshot on connect:

```python
def on_connect(ctx):
    return [
        ws.subscribe(ctx.conn_id, TOPIC),
        ws.send(ctx.conn_id, {"type": "ready", "conn_id": ctx.conn_id}),
        ws.send(ctx.conn_id, _snapshot()),
    ]
```

This distinction is deliberate. It keeps websocket attachment cheap and
best-effort while leaving application semantics for replay, compaction, and
state repair in an explicit state store.

## Retention And Overflow

Each pipe selector has a retention policy:

```yaml
pipes:
  - name: app.telemetry
    retain: 256
    overflow: drop_oldest

  - selector: "room.*"
    retain: 64
    overflow: drop_oldest
    key_by: topic
    max_topics: 256
    topic_overflow: evict_lru
```

Supported overflow modes are:

```text
drop_oldest
drop_new
```

If `overflow` is omitted, the store uses `drop_oldest`. If `retain` is omitted
or is zero with `drop_oldest`, the store uses a default retention of 64 events.
For `drop_new`, set an explicit positive `retain`; a zero-sized `drop_new`
pipe is already full and rejects all publications. Negative retention is
rejected at manifest validation time.

With `drop_oldest`, the store accepts the new event and trims retained history
to the newest `retain` events. This is appropriate for UI notification streams,
status updates, and terminal-like views where current liveness is more
important than preserving every event in the host buffer.

With `drop_new`, the store rejects publication when the retained buffer is full.
The dispatch path treats a rejected publish as a no-op: event handlers and
websocket subscribers are not invoked for that event. This mode is useful when a
pipe represents a bounded admission queue and dropping older context would be
more misleading than dropping new work.

When multiple pipe selectors match a publish topic, exact selectors win first,
then the longest prefix selector wins. For example, `room.audit.*` is selected
over `room.*` for `room.audit.created`.

Selector pipes also choose how retained history is keyed:

```yaml
pipes:
  - selector: "room.*"
    retain: 64
    key_by: topic
    max_topics: 256
    topic_overflow: evict_lru

  - selector: "notifications.*"
    retain: 512
    key_by: selector
```

`key_by: topic` retains each concrete topic under its own pipe name, so
`room.1.message` and `room.2.message` have separate recent-event buffers. A
wildcard selector using `key_by: topic` must set `max_topics`; otherwise random
topic names could create unbounded retained pipe objects. `topic_overflow` may
be `evict_lru` or `drop_new`, and defaults to `evict_lru`.

`key_by: selector` retains all matching events under the selector name, so
`notifications.email` and `notifications.sms` share the `notifications.*`
buffer. This is the safer choice for broad internal streams.

Pipes may also be configured as `unlimited`. An unlimited pipe is not trimmed by
the pipe store. This should be treated as an exceptional mode because it
transfers memory growth risk to the process. Use it only where event volume is
externally bounded or where a future persistence layer is expected to replace
the in-memory store.

Retention is per process and per site. It is not persisted to SQLite and is not
replicated between Quack processes.

## Concurrency

The pipe store is protected by a single mutex. Publication and recent retrieval
for all pipes serialize through that mutex. The critical section is intentionally
small: normalize config, find or create the pipe, assign sequence metadata,
copy payload and headers, append, and trim.

### Performance Implication

The single mutex makes the in-memory pipe store simple and gives each accepted
event a clear host-local sequence assignment, but it also creates one shared
publication bottleneck for the process. A hot pipe can briefly delay publication
or recent-history reads for unrelated pipes because every pipe must pass through
the same lock. In normal websocket demo and hardware-event use, the lock is held
only around small memory operations, so Starlark execution, event fanout, and
socket writes dominate latency long before the pipe store lock does.

The place to be careful is high-rate, many-producer traffic with large payloads
or frequent `Recent` calls. Payload and header copying happens while the mutex
is held, so larger events increase lock hold time. Operators should treat pipes
as lightweight coordination and observability streams, not as a high-throughput
broker. If a workload needs sustained broker-like fan-in across many independent
topics, it should be measured carefully before depending on the current
in-memory store shape.

The store does not hold its mutex while invoking Starlark handlers or writing
websocket frames. Dispatch happens after publication returns. This prevents a
slow handler or slow client from blocking unrelated pipe publication while
holding the pipe store lock.

Runtime invocations are independently bounded by the runtime service semaphore.
HTTP handlers, websocket handlers, and event handlers all acquire from the same
concurrency pool before executing Starlark. If the pool is full, the invocation
fails with `ErrConcurrencyLimit`. For event dispatch, a failed manifest handler
is skipped; for websocket subscriber dispatch, a failed subscriber invocation
causes that websocket to be closed and dispatch continues to other subscribers.

The current dispatch implementation is synchronous within a single publish:

1. Publish into the pipe store.
2. Invoke matching manifest event handlers in sequence.
3. For each matching websocket subscriber snapshot, invoke that connection's
   websocket `on_event` handler in sequence.
4. Apply each handler's returned effects before moving on.

This gives straightforward causality within one process: effects from an event
handler are applied before later subscriber snapshots in the same dispatch
continue. It also means a high-fanout topic can spend substantial wall-clock
time walking subscribers, even though each individual Starlark invocation is
bounded. Operators should treat fanout size and handler duration as capacity
inputs.

Because nested `events.publish` effects are applied during effect processing,
event pipelines can be chained. The system does not currently provide durable
cycle detection, maximum pipeline depth, or topological scheduling. Application
authors should avoid unbounded publish loops such as handler A publishing to
handler B while handler B publishes back to A.

## Ordering

Within a single pipe, accepted events receive increasing sequence numbers under
the store mutex. That provides a host-local order for events accepted by that
pipe.

Delivery order has narrower guarantees:

- A single dispatch walks manifest routes and websocket subscriber snapshots
  synchronously.
- Subscriber snapshots are built from Go maps, so iteration order is not a
  stable API.
- Independent producers can publish concurrently; the store serializes sequence
  assignment, but downstream handler execution interleaves according to goroutine
  scheduling and runtime semaphore availability.
- Websocket writes are enqueued per connection and written by one writer
  goroutine per connection, so frame order for a single connection follows
  enqueue order.

Do not use pipe order as a substitute for domain-level versioning when the
application needs conflict resolution, deduplication, or exactly-once state
transitions. Include application sequence numbers or idempotency keys in the
payload where those properties matter.

## Backpressure

There are three relevant backpressure boundaries.

First, producer-to-pipe admission is governed by the pipe retention policy.
`drop_oldest` preserves producer progress and bounds retained history.
`drop_new` rejects new events when the buffer is full. Rejection stops both
manifest handler invocation and websocket fanout for that event.

Second, Starlark execution is bounded by runtime limits: request payload size,
response payload size, execution steps, maximum duration, memory, and global
runtime concurrency. A slow event handler consumes one runtime slot until it
completes or times out. The host does not let a Starlark handler remain attached
as a live callback.

Third, websocket egress is bounded per connection. Each websocket connection
has an outbound queue of 64 frames and a writer goroutine. Sending and
broadcasting enqueue frames; they do not write directly from the Starlark
invocation goroutine. If the queue is full, the host treats the client as slow,
unregisters the connection, removes subscriptions, and returns
`ErrBackpressure` to the effect application path. The writer also applies a
five-second write deadline and unregisters the connection on write failure.

Broadcast is best-effort. A slow or broken subscriber can be closed without
preventing healthy subscribers from continuing to receive frames. For direct
`ws.send` effects, backpressure can fail the current effect application and
cause the connection to close.

Hardware ingress has its own queue before events become pipe events. The
hardware event queue defaults to 1024 items and drops the oldest hardware event
when full, annotating the new event with dropped-event metadata when possible.
Serial actors also keep their own recent-event ring, defaulting to 64 events,
for `serial.status()`. These hardware buffers are separate from runtime pipe
retention and should be sized with the producer's data rate in mind.

## Serial As A Concrete Application

Serial is the clearest example of why pipes exist. A serial device is a
continuous producer. A websocket terminal is a set of intermittent consumers.
Starlark should not own the blocking serial read loop or a long-lived socket
listener. The host owns both sides.

The data path is:

```text
serial actor
  -> hardware event queue
  -> bound site alias mapping
  -> hardware.serial.<alias>.<kind> pipe topic
  -> manifest event handler
  -> application session topic
  -> websocket subscribers
```

Opening and writing are command-style operations initiated from Starlark:

```python
serial.open(alias)
serial.write(alias, data)
```

Reads and device state changes flow back through hardware events. In
`demos/serial-terminal-pipes`, the manifest subscribes an event handler to all
serial hardware topics:

```yaml
events:
  - selector: "hardware.serial.*"
    on_event: api/terminal.star:on_hardware_event
```

The handler transforms hardware events into application session messages:

```python
def on_hardware_event(ctx, event):
    return _apply_serial_event(event.topic, event.payload)
```

For a `hardware.serial.meter.read` event, the demo app appends a terminal line
to memory and publishes a derived event on:

```text
serial-terminal-pipes.session
```

Websocket connections subscribe to that session topic on connect. Their
`on_event` handler forwards the event payload to the browser:

```python
def on_event(ctx, event):
    return ws.send(ctx.conn_id, event.payload)
```

This architecture decouples device ingestion from browser availability. Serial
input can be read and converted into pipe events without a browser polling
`serial.status().recent`. Browser clients receive live updates while connected,
and initial screen state is reconstructed from application memory when they
connect.

The operational tradeoff is explicit: pipe fanout is live and best-effort,
while terminal history is application state. If serial output must be audited or
replayed after process restart, it should be written to a durable store in the
event handler rather than relying on pipe retention.

## Websockets As A Concrete Application

Websockets are the primary consumer attachment mechanism for pipes today. The
browser opens one route, for example:

```yaml
routes:
  - path: /ws
    kind: websocket
    runtime: starlark
    entrypoint: api/terminal.star
```

On connect, the host reserves a connection id, invokes `on_connect`, upgrades
the HTTP connection, attaches the network connection to the reserved socket
record, and applies returned effects. A subscription effect attaches the
connection to a topic in the host registry.

When a pipe event is published to that topic, the host does not directly send
the pipe payload. Instead, it invokes the websocket route's `on_event` handler
for each subscriber snapshot:

```python
def on_event(ctx, event):
    return ws.send(ctx.conn_id, event.payload)
```

This extra invocation is important. It allows the websocket route to filter,
reshape, authorize, or enrich the event before sending. It also keeps the
transport consistent: all outbound socket writes are effects applied by Go.

The websocket connection registry is local to the process. Subscriptions are
not persisted and are removed when the connection closes. Connection counts are
bounded by server settings:

```text
runtime.websocket.max_connections
runtime.websocket.max_connections_per_site
```

Those limits protect the live socket registry, while the outbound queue and
write deadline protect the process from slow clients.

## Failure Modes

The pipe system is intentionally fail-stop at several boundaries:

- If event settings cannot be read, dispatch returns an error to the caller.
- If a pipe rejects a publish under `drop_new`, the event is silently not
  dispatched.
- If a manifest event handler fails, dispatch skips that handler's effects and
  continues.
- If a websocket subscriber's `on_event` invocation fails, the host closes that
  subscriber and continues dispatching.
- If applying effects for a subscriber fails, the host closes that subscriber
  and continues dispatching.
- If applying effects for a manifest event handler fails, dispatch returns that
  error to the publisher.
- If the hardware event stream fails or closes, the server logs a warning and
  retries after a one-second delay until context cancellation.

These choices favor process liveness and isolation over durable delivery. A bad
subscriber should not block the topic. A bad event handler should not keep
hardware ingestion from recovering. A slow websocket should be removed rather
than allowed to accumulate unbounded memory.

## Security And Isolation

Pipes inherit Quack's site isolation. Pipe storage is keyed by site and pipe
name. Websocket subscriptions are keyed by site and topic. Hardware events are
mapped into site topics only through configured device bindings and permissions.

Starlark cannot subscribe arbitrary background code to a pipe. It can only
return declarative effects from bounded invocations. The host owns the live
registries, queues, network connections, and hardware readers.

Payload authorization is still an application concern. If an application uses a
shared topic such as `app.room.7`, its websocket handler must ensure that the
current connection should receive that room's events before forwarding them.
The current public runtime user is anonymous, so applications should avoid
encoding private data into broadly shared topics unless the deployment boundary
already provides access control.

## Operational Guidance

Prefer `drop_oldest` for UI notification streams, terminal sessions, sensor
dashboards, and other cases where the newest value is most useful.

Prefer `drop_new` only when preserving the existing retained window is more
important than accepting new events. Remember that a rejected event does not
trigger handlers or subscribers.

Keep pipe payloads small. Large payloads count against runtime request and
response limits when delivered to handlers and can increase memory pressure in
retained buffers and websocket queues.

Keep event handlers short and idempotent. The host bounds execution, but it
does not provide durable retry or exactly-once delivery.

Use application state for snapshots. Websocket clients should receive an
initial snapshot from memory or another state store, then subscribe for live
pipe updates.

Include domain sequence numbers when order matters. Pipe `seq` is useful for
host-local observation, but application-level sequencing should live in the
payload.

Avoid publish cycles. The current implementation supports chained publishes but
does not provide cycle detection or a maximum event graph depth.

Treat unlimited pipes as hazardous. They are acceptable only when the event
cardinality is known to be small or when another layer bounds production.

For high-fanout topics, measure handler duration and subscriber count together.
Dispatch is synchronous per publish, and every subscriber can cause a Starlark
invocation.

## Implementation Map

Primary files:

- `internal/eventpipe/pipe.go`: in-memory pipe store, retention, overflow,
  sequence assignment, and defensive copying.
- `internal/manifest/manifest.go`: `pipes` and `events` schema validation.
- `internal/uploads/service.go`: upload-time persistence of pipe and event
  manifest settings.
- `internal/runtime/types.go`: event invocation and websocket effect types.
- `internal/runtime/service.go`: bounded event invocation and runtime limits.
- `internal/runtime/starlark_websocket.go`: Starlark websocket and event handler
  invocation, event payload decoding, and effect parsing.
- `internal/runtimehttp/handler.go`: hardware event ingress into runtime
  dispatch.
- `internal/runtimehttp/websocket.go`: pipe dispatch, event route matching,
  websocket subscriptions, connection registry, outbound queues, and backpressure.
- `internal/hardware/events.go`: hardware event queue and overflow behavior.
- `internal/hardware/types.go`: site binding and serial hardware event topic
  mapping.
- `internal/hardware/serial.go`: serial actor recent-event buffer and hardware
  event emission.

Representative demos:

- `demos/websocket-ping`: minimal websocket request/effect flow.
- `demos/event-pipes-lab`: multi-stage event pipelines and websocket tracing.
- `demos/serial-terminal-pipes`: serial hardware events transformed into a
  websocket terminal session through pipes.

## Current Limitations

The current pipe system is intentionally smaller than a data-intensive message
platform:

- no durable persistence for pipe events
- no cross-process or cross-node replication
- no consumer groups
- no acknowledgements
- no retry queue or dead-letter queue
- no public Starlark API for retained-event replay
- no per-subscriber offsets
- no exactly-once delivery
- no stable ordering across independent subscribers
- no cycle detection for chained publishes
- no configurable websocket queue depth

Those limitations should be treated as part of the operational contract. Pipes
are the host-owned event spine for live Quack applications. Durable workflows
should layer explicit state, idempotency, and persistence on top of them.

## Evolution Path

The next version should improve reliability without turning Quack into a
general-purpose streaming platform. The right target is a small, inspectable,
single-node event substrate that keeps the same programming model:

```text
events in -> bounded Starlark invocation -> declarative effects out
```

The most valuable additions are local durability, controlled replay, cycle
protection, and clearer backpressure controls. Cross-node replication and
Kafka-style consumer groups are intentionally poor fits for the current product
shape. They add distributed systems obligations that would dominate the runtime:
leader election, partition ownership, membership, offset coordination,
compaction semantics, operational repair, and cross-node websocket affinity.
Quack should instead make the single-host case excellent and leave multi-node
event distribution to an external system if an installation truly needs it.

### Durable Local Pipe Store

The first step is to make selected pipes durable in SQLite. This should be
opt-in per pipe:

```yaml
pipes:
  - name: app.jobs
    retain: 1000
    durable: true
    overflow: drop_oldest
```

A minimal schema is enough:

```text
pipe_events(
  site text,
  pipe text,
  seq integer,
  topic text,
  source_kind text,
  source_name text,
  headers_json text,
  payload blob,
  created_at text,
  primary key(site, pipe, seq)
)
```

The current in-memory store can remain the hot path for non-durable pipes. For
durable pipes, publish should append to SQLite inside the same serialized writer
model Quack already uses elsewhere, then update the in-memory tail. Retention
can be enforced by deleting rows below the retained sequence window. This gives
restart survival and operator inspection without introducing a separate broker.

Durability should not imply exactly-once delivery. It should mean that accepted
events can survive process restart and can be replayed by sequence number.
Applications that need exactly-once state changes should still use idempotency
keys or domain versions in their payloads.

### Store Concurrency Improvements

If measurement shows the single pipe-store mutex becoming material, split the
store along the same boundary users already reason about: site plus pipe name.
A straightforward improvement is a short global map lock only for lookup or
creation, with each pipe owning its own mutex for sequence assignment, append,
trim, and recent reads. That lets unrelated pipes publish and read recent
history concurrently while preserving per-pipe sequence ordering.

Another option is striped locking by hashed `(site, pipe)` key. Striping avoids
one mutex per pipe and keeps implementation compact, but unrelated hot pipes can
still collide on the same stripe. Per-pipe locks are easier to reason about when
operator-visible pipe names matter; stripes are attractive only if lock count or
pipe churn becomes a real concern.

Do not split the lock before profiling. The current global lock is simple,
keeps accepted-event metadata assignment easy to audit, and stays out of
Starlark execution and websocket writes. Any future change should preserve
these invariants:

- per-pipe sequence numbers remain monotonic;
- event payloads and headers are still copied before publication returns;
- rejected publishes still skip event dispatch;
- no lock is held while invoking Starlark handlers or writing websocket frames.

### Replay API

Once durable or retained events exist behind a stable interface, expose a small
Starlark read API rather than a full consumer protocol:

```python
events.recent("app.jobs", limit = 100)
events.after("app.jobs", seq = last_seen, limit = 100)
```

The API should return event records with `seq`, `topic`, `payload`, `headers`,
and `created_at`. It should be explicitly pull-based. Websocket connections can
then implement reconnect repair by storing the last seen sequence and asking for
events after that point before subscribing for live updates.

This avoids per-subscriber offsets in the host. The application owns its cursor
when it needs one. That keeps the host small and avoids the ambiguity of when a
websocket frame is actually consumed by browser code.

### Acknowledgements And Job Semantics

Pipe fanout and work queues should remain separate concepts. Most pipe topics
are streams: all matching subscribers may need to observe the same event.
Acknowledgements only make sense for queue-like work where one worker claims a
task.

If Quack needs local job processing, add a separate queue mode rather than
overloading every pipe:

```yaml
pipes:
  - name: app.jobs.thumbnail
    durable: true
    mode: queue
    max_attempts: 5
```

Queue mode can add a small lease table:

```text
pipe_event_attempts(
  site text,
  pipe text,
  seq integer,
  status text,
  attempts integer,
  leased_until text,
  last_error text
)
```

That is enough for at-least-once local jobs: claim with a short lease, run a
bounded handler, ack on success, release or retry on failure. This should not be
presented as a consumer group system. It is a local durable work queue with
leases.

### Retry And Dead Letters

Retries should be explicit and bounded. A manifest event route can eventually
accept retry settings:

```yaml
events:
  - selector: "app.jobs.thumbnail"
    on_event: api/jobs.star:on_thumbnail
    retry:
      max_attempts: 5
      backoff: exponential
      dead_letter: app.jobs.thumbnail.dead
```

The host should record attempt count, last error, and next eligible time. When
attempts are exhausted, it should publish a compact dead-letter event containing
the original event id or sequence, the failing handler, the final error, and
the original payload. Dead-letter topics should be ordinary durable pipes so
operators can inspect and repair them with the same APIs.

For stream-style websocket fanout, automatic retry is usually the wrong
default. A disconnected or slow websocket should repair itself with replay on
reconnect if the application needs that behavior.

### Cycle Detection

Cycle detection is a high-value, low-footprint improvement. The dispatch path
should carry a small trace context through nested publishes:

```text
root_event_id
depth
visited route/topic edges
```

A reasonable default policy is:

- reject dispatch when depth exceeds a small limit such as 32
- reject an immediate repeated edge such as the same handler publishing back to
  the same topic in one trace
- emit a structured runtime error event or log entry with the trace

This does not require graph analysis or durable topology state. It only
protects the process from accidental recursive publish loops in one dispatch
tree.

### Ordering And Offsets

Per-pipe sequence numbers are worth preserving and making durable. They are
simple, cheap, and useful for replay. The host should not try to provide stable
global ordering across independent pipes or subscribers.

The practical contract should be:

- durable order is per site and pipe
- websocket frame order is per connection
- application-level ordering across topics belongs in the payload
- reconnect repair uses `events.after(pipe, seq)` where the application knows
  which pipe it is following

This is enough for terminals, local telemetry, UI sessions, and single-host job
queues without inheriting the complexity of a distributed log.

### Configurable Backpressure

The hard-coded websocket outbound queue depth should become a server setting,
and possibly a route-level limit later:

```text
runtime.websocket.send_queue_depth
runtime.websocket.write_timeout_millis
```

Pipe-level admission should also expose clear metrics:

```text
accepted events
dropped oldest events
dropped new events
current retained events
handler failures
subscriber closes from backpressure
```

Configuration is less important than visibility. Operators need to know whether
loss happened at producer admission, handler execution, or websocket egress.

### What Not To Add

Do not add cross-process or cross-node replication to the built-in pipe store.
It would force Quack to solve cluster membership, event ownership, duplicate
delivery, offset coordination, and websocket routing. Those are the core
responsibilities of dedicated brokers and orchestrators.

Do not add implicit exactly-once delivery. The honest target is at-least-once
for durable queues and best-effort plus replay for streams. Exactly-once effects
require application idempotency because Starlark handlers can call external
systems, mutate memory, publish more events, and send websocket frames.

Do not make every websocket subscription a durable consumer. Websocket
connections are ephemeral transport attachments. If a browser needs recovery,
the application should store a cursor and request replay explicitly.

The north star is small and legible: local durable append, bounded replay,
bounded retry for queue mode, cycle guards, and good metrics. That moves pipes
from "live event spine" to "reliable local event spine" without pretending to be
a distributed data platform.
