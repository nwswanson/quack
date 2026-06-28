# Quack Concurrency Instructions: Serialized Event Topics and Resource Locks

Quack needs two separate concurrency primitives:

1. **Serialized event topic execution**
2. **Resource locks / leases**

These solve different problems and should not be conflated.

Serialized event topics solve:

> “How do I make all handlers for a topic or resource run one at a time?”

Resource locks solve:

> “How do I safely coordinate access to a shared memory object or critical resource from concurrent handlers?”

## 1. Serialized Event Topics

Quack should support declaring event handlers with topic selectors, not one route per concrete topic.

Example:

```yaml
events:
  - selector: "room.*"
    concurrency: serial_by_topic
    on_event: app/room.star:on_room_event
```

This means:

```text
room.1 -> serialized independently
room.2 -> serialized independently
room.3 -> serialized independently
```

A handler for `room.1` must not run concurrently with another handler for `room.1`.

A handler for `room.1` may run concurrently with a handler for `room.2`.

The selector is a glob-style routing rule. It should match many concrete topics without requiring the site author to predeclare each topic.

Valid examples:

```yaml
events:
  - selector: "room.*"
    concurrency: serial_by_topic
    on_event: app/room.star:on_event

  - selector: "hardware.serial.*.read"
    concurrency: serial_by_topic
    on_event: app/serial.star:on_read

  - selector: "device.*.control"
    concurrency: serial_by_topic
    on_event: app/device.star:on_control
```

### Required behavior

For a matching event:

```text
topic = "room.123"
selector = "room.*"
concurrency = serial_by_topic
```

Quack should derive a serialization key from the concrete topic:

```text
serialization_key = "room.123"
```

All events with the same serialization key are processed in FIFO order.

Events with different serialization keys may run concurrently.

### Lane implementation

Quack should not create a permanent lock object for every possible topic. Topics are dynamic and unbounded.

Instead, the runtime should maintain a bounded or garbage-collectable internal lane registry:

```text
selector: room.*
  topic room.1   -> lane A
  topic room.2   -> lane B
  topic room.123 -> lane C
```

A lane should exist only while it has queued or active work. After the lane is idle, it may be evicted.

The selector may share implementation infrastructure, but serialization must happen at the concrete topic level unless otherwise configured.

### Important limitation

Serialized event topics do not lock arbitrary memory objects.

They only guarantee that matching events for the same concrete topic execute one at a time.

If another webhook, websocket, or handler can directly mutate the same memory object outside that topic lane, then serialization is bypassed.

Safe actor-style usage:

```python
def on_ws_message(ctx, msg):
    return {
        "publish": {
            "topic": "room.%s" % msg["room_id"],
            "event": {
                "type": "join",
                "user": ctx.websocket.id,
            },
        }
    }


def on_room_event(ctx, event):
    # All mutations for this room happen here.
    rooms = ctx.memory("rooms")
    room = rooms.get(event.topic) or {"users": [], "count": 0}

    if event["user"] not in room["users"]:
        room["users"].append(event["user"])
        room["count"] += 1

    rooms.set(event.topic, room)
    return {}
```

Unsafe bypass:

```python
def on_ws_message(ctx, msg):
    # Directly mutates room state outside the serialized room topic.
    rooms = ctx.memory("rooms")
    room = rooms.get(msg["room_id"])
    room["count"] += 1
    rooms.set(msg["room_id"], room)
```

## 2. Resource Locks / Leases

Quack also needs a host-owned lock manager for critical resources.

This is the general solution for safely coordinating access to shared memory objects, devices, sessions, or other critical resources from concurrent handlers.

Example Starlark API:

```python
lock = ctx.locks().acquire("memory:rooms:room.123", ttl_ms=1000)

if not lock:
    return {"error": "busy"}

try:
    room = ctx.memory("rooms").get("room.123") or {"count": 0}
    room["count"] += 1
    ctx.memory("rooms").set("room.123", room)
finally:
    lock.release()
```

Preferred scoped form:

```python
def handler(ctx, msg):
    with ctx.locks().hold("memory:rooms:room.123", ttl_ms=1000):
        room = ctx.memory("rooms").get("room.123") or {"count": 0}
        room["count"] += 1
        ctx.memory("rooms").set("room.123", room)

    return {"ok": True}
```

### Required behavior

Locks should be **leases**, not unbounded mutexes.

A lock record should include:

```python
{
    "key": "memory:rooms:room.123",
    "owner": ctx.invocation.id,
    "token": "unique-fencing-token",
    "expires_at": 1234567890,
}
```

The lock token is required.

Release must only succeed if the caller still owns the current token.

This prevents an expired old holder from accidentally releasing a newer holder’s lock.

### Acquire behavior

```python
lock = ctx.locks().acquire(key, ttl_ms=1000)
```

Returns `None` if the lock is currently held by another live owner.

Returns a lock handle if acquired.

The first implementation should be **non-blocking acquire only**. Do not start with blocking waits inside handlers.

Optional later API:

```python
lock = ctx.locks().wait(key, ttl_ms=1000, timeout_ms=100)
```

But this should not be required for the first version.

### Release behavior

```python
lock.release()
```

Release should:

1. Check that the lock key still exists.
2. Check that the current token matches the releasing handle.
3. Delete the lock only if the token matches.
4. Return whether release succeeded.

### Refresh behavior

Optional but useful:

```python
lock.refresh(ttl_ms=1000)
```

Refresh should only succeed if the current token still matches.

If the lock expired and another handler acquired it, refresh must fail.

### Example: safe room mutation

```python
def join_room(ctx, msg):
    room_id = msg["room_id"]
    lock_key = "memory:rooms:%s" % room_id

    with ctx.locks().hold(lock_key, ttl_ms=1000):
        rooms = ctx.memory("rooms")

        room = rooms.get(room_id) or {
            "users": [],
            "count": 0,
        }

        if ctx.websocket.id not in room["users"]:
            room["users"].append(ctx.websocket.id)
            room["count"] += 1

        rooms.set(room_id, room)

    return {"ok": True}
```

### Example: safe device ownership

```python
def acquire_device(ctx, msg):
    device = msg["device"]
    client = ctx.websocket.id

    lock = ctx.locks().acquire("device:%s:owner" % device, ttl_ms=5000)

    if not lock:
        return {
            "ok": False,
            "error": "busy",
        }

    return {
        "ok": True,
        "device": device,
        "owner": client,
        "lock": lock.token,
    }
```

For device ownership, the lock may need to outlive a single handler invocation. In that case, Quack should treat it as a lease that must be refreshed or released explicitly.

For normal memory mutation, prefer scoped locks that are automatically released at the end of the block.

## Host implementation direction

For the current single-node Quack model, the lock manager can be in-process.

Conceptual Go shape:

```go
type LockManager struct {
    mu    sync.Mutex
    locks map[string]LockRecord
}

type LockRecord struct {
    Key       string
    Owner     string
    Token     string
    ExpiresAt time.Time
}
```

Acquire should be atomic under the manager mutex:

```go
func Acquire(key string, owner string, ttl time.Duration) (*LockRecord, bool) {
    mu.Lock()
    defer mu.Unlock()

    now := time.Now()

    current, exists := locks[key]
    if exists && current.ExpiresAt.After(now) {
        return nil, false
    }

    record := LockRecord{
        Key:       key,
        Owner:     owner,
        Token:     randomToken(),
        ExpiresAt: now.Add(ttl),
    }

    locks[key] = record
    return &record, true
}
```

Release should verify the fencing token:

```go
func Release(key string, token string) bool {
    mu.Lock()
    defer mu.Unlock()

    current, exists := locks[key]
    if !exists {
        return false
    }

    if current.Token != token {
        return false
    }

    delete(locks, key)
    return true
}
```

Expired locks may be cleaned lazily during acquire or by a periodic cleanup pass.

## Combined model

Use serialized topics when the resource has a natural event owner:

```yaml
events:
  - selector: "room.*"
    concurrency: serial_by_topic
    on_event: app/room.star:on_event
```

Use locks when multiple independent handlers may touch the same resource:

```python
with ctx.locks().hold("memory:rooms:room.123", ttl_ms=1000):
    ...
```

They are complementary:

```text
serialized topic = ordered event execution
resource lock    = exclusive access to a critical resource
```

The first does not replace the second.

The second does not provide event ordering.

## Baseline tests

### Serialized topic test

Given:

```yaml
events:
  - selector: "room.*"
    concurrency: serial_by_topic
    on_event: app/room.star:on_event
```

When 100 events are published to:

```text
room.1
```

Then only one `room.1` handler may be active at a time.

When 100 events are published across:

```text
room.1
room.2
room.3
```

Then each room is internally serial, but different rooms may run concurrently.

### Lock test: lost update prevention

Given 10,000 concurrent increments:

```python
def increment(ctx, msg):
    with ctx.locks().hold("memory:counter", ttl_ms=1000):
        value = ctx.memory("state").get("counter") or 0
        ctx.memory("state").set("counter", value + 1)
```

The final value must equal 10,000.

Without the lock, this test should fail under contention.

### Lock test: exclusive ownership

Given two concurrent clients:

```python
def acquire(ctx, msg):
    lock = ctx.locks().acquire("device:serial.rpi", ttl_ms=5000)

    if not lock:
        return {"ok": False, "error": "busy"}

    return {"ok": True, "token": lock.token}
```

Only one client may receive `ok: true`.

The other must receive `busy`.

### Token safety test

Given:

```text
handler A acquires lock
handler A stalls
lock expires
handler B acquires same lock
handler A resumes and calls release
```

Then handler A must not release handler B’s lock.

Release must fail because A’s token does not match B’s current token.
