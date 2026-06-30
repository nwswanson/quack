# WebSocket Runtime

This document explains how Quack's WebSocket runtime works, how to declare a
WebSocket route in `site.yml`, how to write the Starlark handler, and what
operational limits and failure modes exist.

The core design rule is:

```text
Go owns sockets, connection state, subscriptions, event routing, queues, and
timers.

Starlark handles one event at a time and returns declarative effects.
```

Starlark code does not get a raw socket, does not install live callbacks, and
does not keep a long-running listener alive. Each handler invocation runs to
completion. The Go host validates and applies the effects returned by Starlark.

## Manifest Declaration

Declare WebSocket runtime routes in `site.yml` or `site.yaml`:

```yaml
routes:
  - path: /api/somesocket
    kind: websocket
    runtime: starlark
    entrypoint: api/somesocket.star
```

The entrypoint path is relative to the upload root. It must exist in the
uploaded archive.

WebSocket routes participate in the same longest-prefix route matching as static
and HTTP runtime routes. A route declared at `/api/somesocket` matches:

```text
/api/somesocket
/api/somesocket/room/123
```

The path visible to Starlark as `ctx.path` is the path under the route. For
example, `/api/somesocket/room/123` under route `/api/somesocket` becomes
`/room/123`.

## Policy And Settings

WebSocket routes are gated by their own capability:

```text
runtime.websocket
```

The corresponding setting key is:

```text
features.runtime.websocket.enabled
```

This capability is checked twice:

1. At upload/route declaration time, so disallowed sites cannot deploy WebSocket
   runtime routes.
2. At invocation time, so cached route decisions cannot outlive a policy change.

The admin UI exposes a system policy for dynamic WebSocket routes. The default
is deny, so an administrator must explicitly allow the feature before sites can
deploy or execute WebSocket runtime routes.

The server also has connection-limit settings:

```text
runtime.websocket.max_connections
runtime.websocket.max_connections_per_site
```

Defaults:

```text
runtime.websocket.max_connections = 1024
runtime.websocket.max_connections_per_site = 128
```

These are server-level settings. They protect the Go host's live socket
registry. They are separate from Starlark execution limits such as request body
size, response body size, execution steps, duration, and concurrency.

## Starlark Handler Shape

A WebSocket Starlark file may define these functions:

```python
def on_connect(ctx):
    return []

def on_message(ctx, msg):
    return []

def on_event(ctx, event):
    return []

def on_disconnect(ctx):
    return []
```

All handlers are optional. Missing handlers are treated as no-ops.

Handlers return either:

- `None`
- a single effect
- a list of effects
- a tuple of effects

An effect is created through host modules such as `ws`, `events`, and `timers`.
The effect object is just data. Returning `ws.send(...)` does not write to the
socket directly from Starlark; it asks the Go host to enqueue a message.

## Context Object

Each handler receives a `ctx` object with:

```python
ctx.site       # site name
ctx.version    # current release version
ctx.route      # route path, e.g. "/api/somesocket"
ctx.path       # request path under the route
ctx.query      # raw query string
ctx.headers    # sanitized request headers
ctx.conn_id    # host-assigned connection id
ctx.params     # reserved for future route params
ctx.user       # currently anonymous public user info
```

The runtime intentionally does not expose the underlying Go socket.

Concurrent WebSocket handlers can run at the same time. If they mutate shared
memory with a multi-step read-modify-write sequence, use `ctx.locks()` or route
the mutation through an event declared with `concurrency: serial_by_topic`. See
[Starlark Concurrency](concurrency.md) for the advanced patterns and failure
modes.

## Message Values

`on_message(ctx, msg)` receives the client message payload as a Starlark value.

If the client message is valid JSON, Quack decodes it into Starlark values:

```json
{"type":"edit","doc_id":"123","content":"hello"}
```

becomes:

```python
msg["type"]     # "edit"
msg["doc_id"]   # "123"
msg["content"]  # "hello"
```

If the message is not valid JSON, `msg` is a string containing the message body.
Empty messages become `None`.

## Event Values

`on_event(ctx, event)` receives a host event object:

```python
event.id
event.pipe
event.topic
event.type
event.source
event.time
event.seq
event.causation_id
event.correlation_id
event.site
event.version
event.payload
```

The host assigns the envelope fields when an event is published. `event.id` is
the unique event identity, `event.pipe` is the declared pipe resource,
`event.topic` is the concrete routed address, `event.time` is an RFC 3339 UTC
timestamp, and `event.seq` is a pipe-local sequence number. Nested publishes set
`event.causation_id` to the parent event id and keep the same
`event.correlation_id`.

`event.payload` follows the same decoding rule as messages: JSON payloads become
Starlark values, non-JSON payloads become strings, and empty payloads become
`None`.

## WebSocket Effects

### Accept

```python
ws.accept()
```

Currently this is accepted as a no-op effect. The Go handler performs the actual
HTTP upgrade after `on_connect` succeeds.

### Send

```python
ws.send(ctx.conn_id, {"type": "ready"})
```

Enqueues a text frame for one connection. Strings and bytes are sent as-is.
Lists, tuples, dicts, numbers, booleans, and `None` are JSON-encoded by the Go
host.

### Broadcast

```python
ws.broadcast("doc.123", {"type": "changed"})
```

Enqueues a text frame for every current connection subscribed to the topic.
Broadcast delivery is best-effort for live local subscribers.

### Subscribe

```python
ws.subscribe(ctx.conn_id, "doc.123")
```

Adds the connection to a declared pipe topic. This is the replacement for
in-memory listener callbacks. The subscription is data owned by Go, not a live
Starlark function. The topic must match a `pipes` declaration in `site.yml`;
otherwise the host returns `runtime.pipe_not_declared`.

### Unsubscribe

```python
ws.unsubscribe(ctx.conn_id, "doc.123")
ws.unsubscribe_all(ctx.conn_id)
```

Removes one subscription or all subscriptions for the connection.

Connections are also removed from all subscriptions when they disconnect or are
closed by the host.

### Close

```python
ws.close(ctx.conn_id, code=1000, reason="bye")
```

Enqueues a close frame and then closes the connection.

If no code is supplied, the host uses `1000`.

## Event Effects

### Publish

```python
events.publish("doc.123", {"type": "changed", "doc_id": "123"})
```

Publishes an in-process event to current local subscribers of the topic. For
each subscribed connection, Go invokes that route's `on_event(ctx, event)`.
The topic must match a `pipes` declaration in `site.yml`; otherwise the host
returns `runtime.pipe_not_declared` instead of creating a pipe implicitly.
If the JSON payload has a string `type` field, the host uses it as the semantic
event type; otherwise the event type falls back to the topic.

`events.publish` is not a durable queue:

- it is not persisted
- it is not replayed for later connections
- it is not cross-process or cross-node
- it does not keep history
- it only reaches currently connected local subscribers

Think of it as a local event-bus trigger for server push.

## Timer Effects

```python
timers.set(
    key = "heartbeat:" + ctx.conn_id,
    after = "30s",
    event = {"type": "heartbeat"},
)
```

Timer effects are accepted as durable intents, but the scheduler is currently a
stub. This is intentionally part of the API shape so future heartbeat and
background pump behavior can be added without changing the basic Starlark model.

The important rule remains: Starlark declares the timer intent; Go will own the
actual sleeping, scheduling, and later re-invocation.

## Example: Topic Broadcast

```yaml
pipes:
  - selector: "doc.*"
    retain: 64
    key_by: topic
    max_topics: 256
```

```python
def on_connect(ctx):
    doc_id = ctx.path.strip("/") or "default"
    topic = "doc." + doc_id
    return [
        ws.subscribe(ctx.conn_id, topic),
        ws.send(ctx.conn_id, {
            "type": "ready",
            "doc_id": doc_id,
        }),
    ]

def on_message(ctx, msg):
    if msg["type"] == "edit":
        doc_id = msg["doc_id"]
        return [
            events.publish("doc." + doc_id, {
                "type": "document_updated",
                "doc_id": doc_id,
                "content": msg["content"],
            }),
        ]
    return []

def on_event(ctx, event):
    if event.payload["type"] == "document_updated":
        return [
            ws.send(ctx.conn_id, {
                "type": "document_updated",
                "doc_id": event.payload["doc_id"],
                "content": event.payload["content"],
            }),
        ]
    return []

def on_disconnect(ctx):
    return [
        ws.unsubscribe_all(ctx.conn_id),
    ]
```

This script does not keep a listener function alive. It subscribes the
connection to a topic. Later, `events.publish` causes Go to re-invoke
`on_event`.

## Example: Memory Change Notifications

The `memory` module does not automatically emit change events. If a script wants
memory-backed state changes to notify WebSocket clients, it should publish an
event after mutating memory:

```yaml
pipes:
  - name: memory.counter
    retain: 64
```

```python
def on_connect(ctx):
    return [
        ws.subscribe(ctx.conn_id, "memory.counter"),
        ws.send(ctx.conn_id, {"type": "counter", "value": memory.get("counter", 0)}),
    ]

def on_message(ctx, msg):
    if msg["type"] == "increment":
        value = memory.incr("counter", 1)
        return [
            events.publish("memory.counter", {
                "type": "counter",
                "value": value,
            }),
        ]
    return []

def on_event(ctx, event):
    return [
        ws.send(ctx.conn_id, event.payload),
    ]
```

Future memory-watch APIs should preserve the same ownership boundary: Starlark
may declare watch intent, but Go must own the actual watch registry and event
routing.

## Back Pressure And Failure Modes

The Go host owns delivery and back pressure.

Each connection has a bounded outbound queue. Starlark `ws.send` and
`ws.broadcast` effects enqueue messages; they do not write directly to the
socket from the Starlark invocation goroutine.

Each connection also has a writer goroutine. That goroutine:

1. Reads frames from the outbound queue.
2. Applies a write deadline.
3. Writes the WebSocket frame to the socket.
4. Closes and unregisters the connection if the write fails.

If a connection's outbound queue is full, Quack treats that client as slow and
closes/unregisters it. This prevents one slow client from blocking a Starlark
invocation, a broadcast, or other subscribers.

Broadcast behavior is best-effort:

- each subscriber gets an enqueue attempt
- a slow subscriber can be closed
- healthy subscribers still receive the broadcast
- failed subscribers are removed from the connection and subscription registries

`events.publish` dispatches per subscribed connection. If one subscriber's
`on_event` invocation or returned effects fail, that subscriber is closed and
the host continues dispatching to the remaining subscribers.

Current limitations:

- no durable retry
- no replay
- no delivery acknowledgements
- no per-topic history
- no cross-node fanout
- no configurable queue depth yet
- no coalescing policy yet

## Transport Notes

The runtime currently implements a small server-side WebSocket transport in Go.
It handles:

- HTTP `Upgrade: websocket`
- text frames
- binary frames, delivered as message payloads
- ping/pong
- close frames
- masked client frames
- unmasked server frames
- basic oversized-message rejection

Fragmented client messages are rejected. If an application needs large or
streaming payloads, add explicit host support before relying on fragmentation.

## Implementation Map

Primary files:

- `internal/manifest/manifest.go`: validates `kind: websocket` route
  declarations.
- `internal/uploads/service.go`: turns manifest routes into runtime route
  metadata and required capabilities.
- `internal/policy/policy.go`: evaluates `runtime.websocket` capability.
- `internal/publichttp/routes.go`: dispatches WebSocket route decisions to the
  runtime HTTP adapter.
- `internal/runtime/types.go`: event, effect, service, and limit types.
- `internal/runtime/service.go`: route lookup, policy checks, limits, and
  executor invocation for WebSocket events.
- `internal/runtime/starlark_websocket.go`: Starlark handler invocation and
  effect parsing.
- `internal/runtimehttp/websocket.go`: HTTP upgrade, frame loop, connection
  registry, subscriptions, event dispatch, queues, and socket writes.
- `internal/settings/registry.go`: setting definitions and defaults.
- `internal/sqlitedb/sqlite.go`: setting and route metadata persistence.

The tests in `internal/runtime/runtime_test.go`,
`internal/runtimehttp/handler_test.go`, `internal/publichttp/routes_test.go`,
`internal/policy/policy_test.go`, and `internal/uploads/service_test.go` cover
the main runtime, routing, policy, upload, and back-pressure behavior.

## Design Constraints To Preserve

When extending this runtime, keep these constraints:

- Do not expose raw sockets to Starlark.
- Do not keep Starlark functions alive as listeners.
- Do not let Starlark sleep or own timers.
- Do not let `runtime.websocket` imply unrelated privileges.
- Keep event delivery host-owned and inspectable.
- Keep Starlark invocations short and bounded by runtime limits.
- Prefer adding new declarative effects over adding long-lived Starlark state.

This keeps WebSocket push compatible with Quack's request/response runtime
model: events in, effects out, host applies the effects.
