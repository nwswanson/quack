# Starlark Concurrency: Locks And Serialized Events

Quack Starlark handlers are short-lived invocations. A WebSocket message, HTTP
request, or event handler runs to completion and returns host-owned effects.
That gives each invocation a simple sequential model, but it does not make a
multi-step update to shared host state atomic.

Use this document when a route has concurrent WebSocket clients, event
publishers, or HTTP callers touching the same memory object, device session, or
other critical resource.

## Mental Model

Starlark module globals are effectively immutable after module initialization.
Mutable shared state belongs to the Go host: memory, websocket subscriptions,
event pipes, devices, and locks.

Individual host operations are synchronized. For example, one call to
`memory.set` or `memory.incr` is safe. A sequence of host operations is not
automatically a transaction:

```python
n = memory.get("hits") or 0
memory.set("hits", n + 1)
```

Two handlers can both read the same old value, then both write the same new
value. That is a lost update.

## Lost Increment Example

This handler is broken under concurrent WebSocket messages:

```python
def on_message(ctx, msg):
    n = memory.get("hits") or 0
    memory.set("hits", n + 1)
    return ws.send(ctx.conn_id, {
        "type": "counter.updated",
        "value": n + 1,
    })
```

After 100 concurrent messages, the intended invariant is:

```text
hits == 100
```

The actual result can be lower because handlers race through the
read-modify-write sequence.

Prefer the atomic helper when one exists:

```python
def on_message(ctx, msg):
    value = memory.incr("hits")
    return ws.send(ctx.conn_id, {
        "type": "counter.updated",
        "value": value,
    })
```

When the update spans multiple keys, multiple reads, or non-counter state, use a
resource lock or a serialized event topic.

## Resource Locks

WebSocket and event contexts expose:

```python
ctx.invocation_id
ctx.locks()
```

`ctx.locks()` returns the locks module for the current invocation and site.
Locks are in-process, site-scoped leases with fencing tokens.

```python
lock = ctx.locks().acquire(
    "memory:rooms:room.123",
    ttl_ms = 1000,
    wait_ms = 50,
)
```

`acquire` returns a lock handle when the lease is acquired, or `None` when the
resource remains busy past `wait_ms`.

Parameters:

- `key`: resource key. Choose a stable name shared by every handler that must
  coordinate on the same resource.
- `ttl_ms`: lease lifetime in milliseconds. Must be positive.
- `wait_ms`: optional time to wait for the current holder before returning
  `None`. Defaults to `0`.

Lock handles expose:

```python
lock.key         # caller-visible key
lock.owner       # invocation owner id
lock.token       # fencing token
lock.expires_at  # Unix epoch milliseconds
lock.release()   # True if this token released the current lock
lock.refresh(ttl_ms = 1000)
```

Release and refresh validate the token. If an old holder stalls, its lease
expires, and a newer holder acquires the same key, the old holder cannot release
or refresh the newer holder's lock.

## Locking Pattern

Keep the critical section small. Do not hold a lock while doing avoidable work.
Compute the response after copying any values you need, release the lock, then
return effects.

```python
def on_message(ctx, msg):
    if msg.get("type") != "join":
        return []

    room_id = msg.get("room_id", "room.123")
    lock = ctx.locks().acquire(
        "memory:rooms:" + room_id,
        ttl_ms = 1000,
        wait_ms = 50,
    )
    if not lock:
        return ws.send(ctx.conn_id, {
            "type": "room.busy",
            "room_id": room_id,
        })

    room = memory.get(room_id) or {"users": [], "count": 0}
    if ctx.conn_id not in room["users"]:
        room["users"].append(ctx.conn_id)
        room["count"] += 1
    memory.set(room_id, room)

    response = {
        "type": "room.updated",
        "room_id": room_id,
        "users": room["users"],
        "count": room["count"],
    }
    lock.release()

    return ws.broadcast("room." + room_id, response)
```

Starlark does not support Python-style `try/finally`, so structure handlers so
there is one clear release path after the mutation. If a handler can fail inside
the critical section, keep the lock TTL short enough that another invocation can
recover.

## Choosing Lock Keys

Use keys that name the actual resource, not the route or handler:

```text
memory:counters:hits
memory:rooms:room.123
device:serial.rpi:owner
session:user.42:cart
```

Every writer must use the same key. A lock is advisory: it protects only code
that participates. If one handler uses `memory:rooms:room.123` and another
handler mutates `room.123` without acquiring that lock, the second handler
bypasses the coordination.

Locks are site-scoped internally. Two different sites can use the same visible
key without blocking each other.

## TTL And Wait Time

`ttl_ms` should be long enough for the critical section under normal load, but
short enough to recover from a canceled or failed invocation.

`wait_ms` should be short for interactive WebSocket handlers. A good first value
is usually tens of milliseconds:

```python
lock = ctx.locks().acquire("memory:rooms:room.123", ttl_ms = 1000, wait_ms = 50)
```

If a lock cannot be acquired, return a busy response or ask the client to retry.
Do not spin in Starlark.

For longer ownership, such as a device lease, store the fencing token in the
client-visible state and refresh the lease explicitly:

```python
ok = lock.refresh(ttl_ms = 5000)
```

## Serialized Event Topics

Locks coordinate access to a resource. Serialized event topics coordinate
handler execution order.

Declare an event handler with `concurrency: serial_by_topic`:

```yaml
pipes:
  - selector: "room.*"
    retain: 64
    key_by: topic
    max_topics: 256

events:
  - selector: "room.*"
    concurrency: serial_by_topic
    on_event: app/room.star:on_event
```

All matching events for the same concrete topic run one at a time:

```text
room.123 -> serial lane A
room.456 -> serial lane B
```

Different topics may run concurrently. Lanes are created dynamically and are
removed after they become idle.

This is useful for actor-style state:

```python
def on_message(ctx, msg):
    return events.publish("room." + msg["room_id"], {
        "type": "join",
        "conn_id": ctx.conn_id,
    })

def on_event(ctx, event):
    room = memory.get(event.topic) or {"users": []}
    user = event.payload["conn_id"]
    if user not in room["users"]:
        room["users"].append(user)
    memory.set(event.topic, room)
    return ws.broadcast(event.topic, {
        "type": "room.updated",
        "users": room["users"],
    })
```

The guarantee applies only to the matching event handler for the concrete topic.
It does not lock arbitrary memory. A WebSocket handler that directly mutates the
same room key can still bypass the lane.

## Locks Versus Serialized Topics

Use `memory.incr`, `memory.list_push`, `memory.set_add`, or another atomic
memory helper when the operation already matches your update.

Use `ctx.locks().acquire` when multiple independent handlers may touch the same
resource:

- WebSocket messages updating a shared room map.
- HTTP and WebSocket routes both mutating the same key.
- Device ownership or session ownership.
- Multi-key memory updates that must be consistent.

Use `concurrency: serial_by_topic` when the resource has a natural event owner:

- All room mutations go through `room.<id>` events.
- All device commands go through `device.<id>.control` events.
- A topic behaves like a small actor mailbox.

The two patterns can be combined. For example, a serialized room event can still
take a device lock before touching a physical resource.

## Current Limits

Current locks are in-process leases. They do not coordinate across multiple
Quack server processes, machines, or future distributed runtimes.

Locks do not make memory automatically transactional. They provide exclusive
access only for code that consistently acquires the same lock key.

Locks should not be held across slow external work. Build effects inside the
critical section only when needed, release the lock, then return the effects to
the host.

## Checklist

Before shipping a concurrent WebSocket or event workflow:

- Identify every shared mutable resource.
- Prefer an atomic memory helper if one exactly fits.
- Pick one lock key or one serialized topic per resource.
- Make every writer participate in the same pattern.
- Keep the lock TTL bounded.
- Handle `None` from `acquire`.
- Release the lock before returning effects.
- Add a test that sends concurrent messages and checks the final invariant.
