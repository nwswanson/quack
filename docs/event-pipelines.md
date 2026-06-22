# Internal Event Pipelines

Status: research/design note.

This document sketches a host-owned event pipeline architecture for Quack. MQTT
is used as the concrete example, but MQTT should not be the core abstraction.
The deeper requirement is a safe way for external and internal producers to
place work onto bounded, inspectable queues that can feed Starlark handlers,
memory updates, WebSocket fanout, timers, and future adapters.

The important design rule is the same as the WebSocket runtime:

```text
Adapters do not own application behavior.
Starlark does not own long-lived listeners, sleeps, sockets, or broker clients.
Go owns admission, queues, durability, retries, back pressure, routing, and
effect application.
```

## Current Context

Quack already has several pieces that point toward this model:

- WebSocket routes are host-owned. Starlark receives one event and returns
  declarative effects.
- `events.publish` is a local, in-process trigger for current WebSocket
  subscribers. It is intentionally not durable, replayable, cross-node, or
  history-bearing.
- WebSocket outbound delivery uses bounded per-connection queues. Slow clients
  are closed rather than letting one connection block the runtime.
- Runtime invocations already have request size, response size, duration,
  execution step, and concurrency limits.
- `memory` is site-scoped and quota-bound. It is useful as application state,
  but it should not become the general queueing substrate.
- Upload-time manifest validation, policy checks, settings, runtime route
  metadata, SQLite persistence, and `internal/server` composition are already
  the right places to wire new capabilities.

The gap is a general event substrate between "something happened" and "run a
handler or deliver to a socket."

## Goals

- Accept events from many producer types: MQTT, timers, WebSocket messages,
  HTTP hooks, admin actions, internal maintenance jobs, memory watches, and
  future adapters.
- Normalize those events into one host-owned envelope.
- Apply admission control before work enters the system.
- Support both transient fanout and durable queue/stream semantics.
- Preserve bounded resource use per site, topic, adapter, and process.
- Make back pressure explicit: reject, pause, drop, coalesce, retry, or
  dead-letter according to policy.
- Keep Starlark as short-lived handler invocations that return effects.
- Keep broker secrets and network clients out of uploaded site code.

## Non-Goals

- Do not make MQTT a first-class internal model. MQTT is one ingress adapter.
- Do not expose raw broker clients, sockets, goroutines, or blocking reads to
  Starlark.
- Do not promise exactly-once effects. Aim for clear at-most-once or
  at-least-once behavior with idempotency tools.
- Do not use the current `memory` module as a durable queue implementation.
- Do not require a distributed message broker for the first version. SQLite can
  carry a single-node durable queue if the semantics are scoped honestly.

## Core Model

Use four host-owned layers:

```text
Ingress adapter
  MQTT, timer, HTTP hook, internal runtime, admin action
      |
      v
Admission controller
  auth, policy, payload limits, topic limits, rate limits, quotas
      |
      v
Pipeline store
  transient bus, durable queue, durable stream, dead-letter queue
      |
      v
Dispatch workers
  invoke Starlark, apply returned effects, call host services
```

The adapter only converts an outside event into an internal event proposal. The
admission controller decides whether the event may enter a pipeline. The store
defines the durability and ordering semantics. Dispatch workers perform bounded
delivery attempts and apply returned effects. Host modules that mutate state
during an invocation, such as `memory.*`, still need the same limit and
idempotency treatment as returned effects.

## Event Envelope

Every event should be represented as data before it reaches a handler:

```go
type Event struct {
    ID             string
    Site           string
    Topic          string
    SourceKind     string // mqtt, timer, websocket, http, internal
    SourceName     string // adapter/profile name
    Key            string // optional ordering/idempotency key
    ContentType    string
    Payload        []byte
    Headers        map[string]string
    CreatedAt      time.Time
    NotBefore      time.Time
    ExpiresAt      time.Time
    IdempotencyKey string
    TraceID        string
    Attempt        int
    MaxAttempts    int
    Durability     DurabilityClass
}
```

Keep broker-specific metadata in `Headers` or a typed source detail field. Do
not let MQTT topic names, QoS flags, or retained-message flags become universal
pipeline concepts.

## Durability Classes

Quack likely needs more than one delivery class:

```text
transient
  Current `events.publish` behavior. In-process, live subscribers only, no
  replay. Good for UI hints and best-effort push.

durable_queue
  Persist before acking the producer. Each event is consumed by a named worker
  group. Supports retry, lease timeout, dead-letter, and bounded retention.

durable_stream
  Persist ordered events for replay by offset. Multiple consumers can keep
  independent cursors. Useful for collaborative apps, catch-up, audit, and
  rebuilding derived state.
```

The first implementation can support `transient` and a SQLite-backed
`durable_queue`. Add `durable_stream` only when replay/catch-up semantics are
needed beyond recent application state.

## Back Pressure And Limits

Back pressure must be enforced at each boundary, not only at WebSocket send
time.

Ingress limits:

- max adapter connections
- max message bytes
- max messages per second
- max unacknowledged broker messages
- max topic subscriptions per site
- max topic cardinality

Pipeline limits:

- max queued events per site
- max queued bytes per site
- max queued events per topic
- max event age
- max attempts
- max dead-letter bytes
- max worker concurrency

Dispatch limits:

- max Starlark invocations per site
- max in-flight events per consumer group
- max effects returned per invocation
- max WebSocket enqueue attempts per event
- max memory growth through event handlers

Back-pressure actions should be explicit per pipeline:

```text
reject
  Refuse the event before it enters the system. For MQTT, do not acknowledge
  QoS 1 delivery until the event is durably accepted.

pause
  Stop or slow reads from the adapter while queues drain.

drop_new
  Reject new events when the buffer is full.

drop_old
  Discard old transient events, useful for presence/cursor/state hints.

coalesce
  Keep only the latest event for a key, useful for sensor readings or cursor
  position.

dead_letter
  Move repeatedly failing durable events to an inspectable failure queue.
```

Do not silently degrade a durable queue into a best-effort queue when limits are
hit. That makes failure analysis impossible.

## Ordering And Delivery

Avoid global ordering. It is expensive and rarely what apps need.

Reasonable initial guarantees:

- transient events have no ordering guarantee beyond local goroutine behavior
- durable queues are at-least-once
- ordering is only per `(site, topic, key)` when a pipeline opts into it
- retry can reorder events unless ordered delivery is enabled
- handlers must tolerate duplicate delivery for durable events

The event envelope should expose `event.id` and `event.idempotency_key` so
Starlark and host services can de-duplicate when needed.

## Persistence Shape

SQLite is a good first durable coordinator for a single Quack node. A minimal
schema could be:

```text
pipeline_events
  id
  site_sha
  topic
  source_kind
  source_name
  key
  content_type
  payload
  headers_json
  created_at
  not_before
  expires_at
  idempotency_key
  trace_id
  max_attempts
  state              queued | leased | done | dead
  attempt
  lease_owner
  lease_expires_at
  last_error

pipeline_consumers
  site_sha
  topic
  group_name
  handler_kind       starlark | websocket | memory | service
  handler_ref
  max_concurrency
  retry_policy_json
  created_at

pipeline_dead_letters
  event_id
  site_sha
  topic
  failed_at
  attempts
  error
  event_json_or_payload_ref
```

For streams, add append-only sequence numbers and consumer offsets instead of
state transitions on each event. Do not keep queue rows forever: every durable
pipeline needs retention by age, count, and bytes.

## Starlark Integration

Keep the effect model. Add durable event operations as new declarative effects
instead of changing `events.publish` semantics under existing apps.

Current Starlark has two kinds of host interaction:

- returned effects such as `events.publish`, `ws.send`, and future
  `events.enqueue`
- direct host-module calls such as `memory.set` and `memory.incr`

Durable retry semantics must account for both. If an event handler updates
memory and then fails while applying a returned effect, replaying the event can
run the memory write again. Durable handlers should therefore be idempotent, or
the host should eventually provide transactional effect application for the
specific effect types that need it.

Possible API shape:

```python
def on_event(ctx, event):
    if event.topic == "sensors.temperature":
        memory.set("latest_temp", event.payload)
        return events.publish("ui:sensors", {
            "type": "temperature",
            "value": event.payload,
        })
    return []

def on_message(ctx, msg):
    return events.enqueue("jobs:resize", msg, key = msg["image_id"])
```

Potential host modules:

```text
events.publish(topic, payload)
  transient local fanout, current behavior

events.enqueue(topic, payload, key = None, idempotency_key = None)
  durable at-least-once queue

events.append(topic, payload, key = None)
  durable replayable stream, future
```

`on_event` should receive the normalized event envelope, not an MQTT-specific
object. If source-specific metadata is needed, expose it as data:

```python
event.source_kind       # "mqtt"
event.source_name       # "building-a"
event.headers["mqtt.topic"]
event.headers["mqtt.qos"]
event.payload
```

## WebSocket Integration

WebSockets should remain a live delivery surface, not the durable store.

Good pattern:

1. Durable event enters a pipeline.
2. Worker invokes Starlark `on_event`.
3. Handler updates durable or memory state if needed.
4. Handler returns `events.publish` or `ws.broadcast`.
5. Current subscribers receive best-effort socket messages.
6. Reconnecting clients fetch state or replay a stream, depending on app design.

Do not hold durable events hostage to slow WebSocket clients. A WebSocket send
failure should close/unregister the connection; it should not retry the entire
durable source event unless the application explicitly models client ack.

## Memory Integration

Memory is an application state sink/source, not the queue itself.

Allowed:

- event handler updates memory with `memory.set`, `memory.incr`, or collection
  helpers
- handler publishes a transient WebSocket update after changing memory
- future host-owned memory watches generate events

Avoid:

- using `memory.list_push` as the general system queue
- making memory mutation automatically imply durable event emission
- letting memory persistence settings define pipeline durability

If memory watches are added, they should compile into host-owned subscriptions:

```text
watch memory key prefix -> emit normalized internal event -> pipeline admission
```

## Policy And Manifest Integration

New event features should be separately gated. Do not let
`runtime.websocket` or `runtime.http` imply broker access or durable queue
access.

Possible capabilities/settings:

```text
features.runtime.events.transient.enabled
features.runtime.events.durable.enabled
features.ingress.mqtt.enabled
runtime.events.max_event_bytes
runtime.events.max_queue_bytes_per_site
runtime.events.max_queue_events_per_site
runtime.events.max_dead_letter_bytes_per_site
runtime.events.max_worker_concurrency
```

`ingress:` should not replace normal HTTP routing. HTTP-shaped ingress such as
webhooks can stay as standard Starlark runtime routes. That gives the site a
normal request handler for validation, cleanup, authentication decisions,
payload reshaping, and explicit `events.enqueue` calls.

Use `ingress:` first as a place to declare named pipeline resources. A simple
pipe resource has no external listener; it is just an admitted topic/queue that
Starlark or host services may publish into.

Use adapter-shaped `ingress:` entries only for producers that are not naturally
Quack HTTP routes, such as MQTT broker subscriptions. For MQTT, broker
connection profiles should be admin-owned server settings or server config, not
uploaded site secrets. A site can request a binding to an allowed profile and
topic pattern, but the host owns credentials.

Sketch:

```yaml
events:
  consumers:
    - topic: sensors.temperature
      runtime: starlark
      entrypoint: api/sensors.star
      durability: durable_queue

routes:
  - path: /hooks/temperature
    kind: http
    runtime: starlark
    entrypoint: api/temperature_hook.star
    methods: [POST]

ingress:
  - name: sensors.temperature
    kind: pipe
    topic: sensors.temperature
    durability: durable_queue

  - name: building-a-mqtt
    kind: mqtt
    profile: building-a
    subscribe: sensors/+/temperature
    publish_to: sensors.temperature
    durability: durable_queue
    key: "{{ mqtt.topic }}"
```

The webhook route can do whatever cleanup is needed and then publish into the
pipe:

```python
def handle(req):
    payload = json.decode(req.body)
    cleaned = normalize_temperature(payload)
    return events.enqueue("sensors.temperature", cleaned)
```

Because `site.yml` uses strict known-field validation today, these fields must
be added deliberately to `internal/manifest` before users can deploy them.
Upload should persist normalized pipeline, consumer, and non-HTTP adapter
metadata the same way runtime route metadata is persisted today.

## MQTT Adapter Example

MQTT should be an edge adapter:

```text
MQTT broker
  -> MQTT adapter using admin-owned profile
  -> normalize MQTT message into Event
  -> admission checks
  -> durable append
  -> MQTT ack after durable acceptance
  -> worker invokes Starlark on_event
  -> handler updates memory and returns WebSocket/event effects
```

For QoS mapping:

```text
MQTT QoS 0
  Can map to transient or best-effort durable admission. If the local queue is
  full, dropping is acceptable only when the pipeline policy says so.

MQTT QoS 1
  Ack only after the event is accepted into the configured pipeline. Duplicate
  delivery is possible, so use event IDs/idempotency keys.

MQTT QoS 2
  Defer initially unless there is a real requirement. Quack can still process
  effects at-least-once unless every downstream effect is transactional and
  idempotent.
```

Example Starlark handler:

```python
def on_event(ctx, event):
    if event.topic != "sensors.temperature":
        return []

    sensor = event.headers.get("mqtt.topic", "").split("/")[1]
    memory.set("sensor:" + sensor + ":temperature", event.payload)

    return events.publish("ui:sensors", {
        "type": "temperature",
        "sensor": sensor,
        "value": event.payload,
        "event_id": event.id,
    })
```

This keeps MQTT outside the application model. The application sees a Quack
event, not a broker client.

## Package Shape

Likely additions:

```text
internal/pipeline
  domain types, admission service, dispatcher, worker leases, retry policy

internal/pipeline/sqlite
  or methods on internal/sqlitedb if the repo keeps concrete SQLite together

internal/ingress/mqtt
  broker client adapter, profile loading, subscription management

internal/runtime
  event invocation request/effect types, on_event execution path

internal/runtimehttp
  consume transient events for WebSocket fanout, but not own durable queues

internal/uploads
  parse and persist manifest-declared event consumers/ingress bindings

internal/settings and internal/policy
  event and ingress capabilities, numeric limits, profile permissions

internal/server
  composition root wiring for adapters, pipeline service, and workers
```

The key boundary: transport/adapters produce events; application services and
runtime handlers consume events; infrastructure stores and leases events.

## Implementation Phases

1. Define the internal event envelope, durability classes, and policy/limit
   structs without adding MQTT.
2. Refactor current `events.publish` path behind a transient pipeline interface
   while preserving current behavior.
3. Add a SQLite-backed durable queue for internal events and timers.
4. Add worker leases, retry, dead-letter, metrics, and admin inspection.
5. Add Starlark `events.enqueue` and durable `on_event` consumers.
6. Add MQTT as the first external ingress adapter using admin-owned profiles.
7. Add stream/replay semantics only if apps need catch-up beyond snapshots.

This order avoids designing around MQTT too early and proves the core pipeline
with internal producers before introducing broker connection management.

## Open Questions

- Should durable event consumers be declared only in `site.yml`, or can admins
  attach server-level consumers to sites?
- How should topic patterns be authorized across tenants?
- Do durable event handlers run against the current release or the release that
  declared the consumer when the event was accepted?
- Should failed Starlark effects retry the whole event, only the failed effect,
  or move immediately to dead-letter for non-idempotent effect types?
- What admin UI is required before enabling durable queues in production:
  queue depth, oldest event age, retry counts, dead-letter browser, pause/resume?
- Is SQLite enough for expected event volume, or should the interface leave room
  for a broker-backed implementation later?

The architectural answer does not depend on MQTT. MQTT only becomes tractable
once Quack has a clear, bounded, durable, host-owned event pipeline underneath
it.
