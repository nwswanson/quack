**Don't make a mutex or rwmutex Starlark object the next step**. It is probably the wrong abstraction for Quack users.

The better next step is a **host-owned serializable object/store primitive** or a **single-writer event lane**, not script-level locks.

Starlark itself pushes you toward this. Normal module globals become frozen/immutable after module initialization, specifically so loaded modules can be safely used by multiple Starlark threads without data races. The Go implementation’s docs say mutable values created during module initialization are frozen after initialization, which permits concurrent references from multiple Starlark threads without races. ([Chromium Git Repositories][1]) Bazel’s Starlark docs describe the same model: lists and dicts are mutable only in the current context, and after the context finishes, values become immutable. ([Bazel][2])

So the clean mental model should be:

> A handler invocation is sequential.
> Shared Starlark module state is immutable.
> Any mutable shared state belongs to the host and must define its own concurrency semantics.

That means a raw Starlark mutex creates more problems than it solves.

## Why I would avoid exposing mutex/rwmutex

A mutex object in Starlark would leak Go’s concurrency model into the scripting layer. Users would now need to reason about:

```python
lock.acquire()
# what if fail() happens here?
# what if this calls an effect?
# what if this publishes an event?
# what if cancellation happens?
lock.release()
```

That is a lot of footguns.

The worst cases are:

1. **Deadlock across handlers**
   Handler A locks `x`, publishes an event, waits for something. Handler B needs `x` to finish that something. Deadlock.

2. **Lock held across I/O or effects**
   Quack handlers are supposed to return declarative effects. If a lock can be held while requesting DB, HTTP, serial, websocket, or event effects, your scheduler becomes much harder to reason about.

3. **Cancellation leaks locks**
   Starlark-Go has `Thread.Cancel`, and most `Thread` methods are not generally cross-goroutine safe except the documented cancellation operations. ([Go Packages][3]) If execution aborts while a lock is held, the host must guarantee cleanup. That means you need structured locking, not manual lock/unlock.

4. **RWMutex is probably worse**
   An rwmutex sounds attractive, but it gives users false confidence. Readers still see snapshots at some point in time, not a transaction. Upgrade from read to write is dangerous. Writer starvation is possible. And “I read this value, then later I write based on it” is not safe unless the whole read-modify-write is atomic.

5. **It undermines Quack’s nice model**
   Quack’s model seems to be: bounded Starlark invocation, host-owned effects, explicit event routing. Mutexes turn that into shared-memory scripting.

## Is serializability already guaranteed?

Some, but not enough for the case you are worried about.

You probably have these guarantees already:

```text
inside one handler invocation:
  sequential execution
  no internal Starlark parallelism

loaded module globals:
  effectively immutable after load

host pipe enqueue:
  maybe ordered per pipe/topic, depending on your implementation
```

But you probably do **not** automatically have this guarantee:

```text
two webhook/websocket handlers touching the same host-backed mutable object
  appear as one serializable sequence
```

Unless you deliberately implemented that object as a synchronized host object.

So the answer is:

> Starlark gives you safe immutable sharing.
> Quack handlers give you sequential execution per invocation.
> Event pipes may give you ordering per pipe.
> But shared mutable host objects are only serializable if Quack makes them serializable.

## Better primitive: keyed single-writer lanes

For “multiple websockets coordinate access to a critical memory object,” I would use **keyed serialization**.

Example conceptually:

```yaml
events:
  - selector: "state.room.*"
    mode: serial_by_topic
    on_event: api/room.star:handle_room_event
```

Then all events for:

```text
state.room.123
```

run through one ordered lane. Different rooms can run concurrently:

```text
state.room.123  -> serial lane A
state.room.456  -> serial lane B
state.room.789  -> serial lane C
```

This gives you the thing users usually wanted from a mutex:

```text
only one mutation at a time for this object
```

without exposing locks.

The scripting model becomes:

```python
def handle_room_event(event):
    state = memory.get(event.topic)
    state["count"] += 1
    memory.put(event.topic, state)
```

But the host guarantees that only one handler for that key is running at a time.

This is basically an actor/mailbox model per resource key. For Quack, that fits much better than mutexes.

## Better primitive: versioned CAS store

For non-event access, expose a small optimistic-concurrency store:

```python
item = memory.get("room:123")
ok = memory.cas("room:123", item.version, {
    "users": item.value["users"] + [user_id],
})
```

Or:

```python
ok, current = memory.cas("room:123", expected_version, new_value)
```

This gives you safe coordination without locks. Multiple websocket handlers can race, but only one wins. The losers retry, return a conflict, or send a stale-state response.

For example:

```python
def join_room(req):
    room, version = memory.get("room:123")

    if req.user in room["users"]:
        return {"ok": True}

    room["users"].append(req.user)

    if memory.cas("room:123", version, room):
        return {"ok": True}

    return {"ok": False, "error": "state_changed"}
```

That is much easier to reason about than:

```python
mutex.lock()
...
mutex.unlock()
```

## Better primitive: host-side transaction, but do not hold lock while running arbitrary Starlark

You might be tempted to expose:

```python
memory.update("room:123", lambda state: ...)
```

I would be careful. If the host holds a lock while calling back into Starlark, then arbitrary script code runs inside the critical section. That reintroduces deadlock/cancellation/latency issues.

A safer version is declarative:

```python
memory.patch("room:123", [
    {"op": "append_unique", "path": ["users"], "value": user_id},
])
```

or domain-specific:

```python
rooms.join("room:123", user_id)
rooms.leave("room:123", user_id)
rooms.send("room:123", message)
```

The host owns the lock internally and the operation is small, bounded, and atomic.

## Where a mutex might make sense

A mutex can exist inside Go implementation details.

For example:

```go
type MemoryStore struct {
    mu sync.RWMutex
    values map[string]VersionedValue
}
```

Then expose safe Starlark methods:

```python
memory.get(key)
memory.put(key, value)
memory.cas(key, version, value)
memory.patch(key, patch)
```

But I would avoid exposing:

```python
mutex = sync.mutex("room:123")
mutex.lock()
mutex.unlock()
```

as user-facing Starlark.

## My recommendation

For Quack, I’d add these in this order:

### 1. Define runtime guarantees explicitly

Something like:

```text
Each handler invocation executes synchronously on one Starlark thread.

Handlers may run concurrently with other handlers.

Loaded Starlark module globals are immutable after load.

Mutable shared state is only available through host-provided objects.

Host-provided shared objects define their own atomicity and ordering.
```

That alone removes ambiguity.

### 2. Add a host-owned `memory` or `state` object with versions

Minimal API:

```python
value, version = state.get(key)
ok = state.cas(key, version, new_value)
value, version = state.require(key)
state.put_if_absent(key, value)
state.delete(key, version=None)
```

Important: values returned to Starlark should be copies/snapshots, not live shared mutable objects.

### 3. Add serial event lanes

For cases where mutation should be ordered:

```yaml
events:
  - selector: "room.*"
    concurrency: serial_by_topic
    on_event: api/room.star:handle
```

Or:

```yaml
events:
  - selector: "room.*"
    key: "{{ topic }}"
    max_concurrency_per_key: 1
```

This is likely the most Quack-native solution.

### 4. Maybe later add leases, not mutexes

If you truly need cross-websocket “ownership,” expose a lease:

```python
lease = state.acquire("device:rpi", ttl_ms=5000)
if not lease:
    return {"error": "busy"}

state.release(lease)
```

But that should be host-managed, TTL-bound, and probably not used for normal in-memory mutation. It is better for things like “only one websocket session controls this serial device.”

## The punchline

A Starlark mutex is probably **too much complexity and the wrong surface area**.

A **Go mutex inside a host object** is good.

A **Starlark-visible mutex** is risky.

For Quack, the clean answer is:

```text
Use immutable Starlark modules.
Run each handler sequentially.
Allow handlers to run concurrently.
Put shared mutable state behind host APIs.
Provide serial_by_key event lanes and/or CAS.
Avoid user-facing locks unless you later discover a truly unavoidable use case.
```

For your specific example, “multiple websockets coordinate who has access to a critical memory object,” I’d model that as either:

```text
CAS on versioned state
```

or:

```text
a single-writer pipe/topic for that object
```

not as an rwmutex.

[1]: https://chromium.googlesource.com/external/github.com/google/starlark-go/%2B/HEAD/doc/impl.md "Starlark in Go: Implementation"
[2]: https://bazel.build/rules/language "Starlark Language  |  Bazel"
[3]: https://pkg.go.dev/go.starlark.net/starlark "starlark package - go.starlark.net/starlark - Go Packages"



Here is the baseline breaking example I would use: **lost update + broken ownership invariant** on a host-backed shared memory object accessed by concurrent websocket/webhook handlers.

The point is not that Starlark itself has mutable globals. The point is that Quack exposes some host-owned mutable object like `state`, `cache`, `memory`, `session_store`, `pipes`, etc., and two handlers race through a read-modify-write.

## Breaking example 1: lost increment

### Intended invariant

After `N` concurrent websocket messages:

```text
counter == N
```

### Actual broken result

```text
counter < N
```

because handlers read the same old value and overwrite each other.

### Pseudocode manifest

```yaml
websocket:
  routes:
    - path: /ws/counter
      on_message: app/counter.star:on_message

memory:
  objects:
    - name: counters
      type: map
```

### Pseudocode Starlark handler

```python
# app/counter.star

def on_message(ctx, msg):
    # Host-backed shared mutable object.
    # Assume this is shared across websocket handlers.
    counters = ctx.memory("counters")

    # Broken read-modify-write.
    n = counters.get("hits") or 0

    # Artificial widening of race window.
    # Could be sleep, logging, effect planning, JSON work, pipe publish, etc.
    ctx.debug_yield()

    counters.set("hits", n + 1)

    return {
        "send": {
            "type": "counter.updated",
            "value": n + 1,
        }
    }
```

### Test harness

```python
def test_concurrent_counter_updates():
    server = start_quack_site("counter_site")

    ws_clients = []
    for i in range(100):
        ws_clients.append(connect_ws(server, "/ws/counter"))

    # Release all clients at roughly the same time.
    barrier = Barrier(100)

    def send_increment(client):
        barrier.wait()
        client.send({"type": "increment"})

    run_concurrently([
        lambda c=client: send_increment(c)
        for client in ws_clients
    ])

    wait_until_idle(server)

    final_value = server.memory("counters").get("hits")

    assert final_value == 100
```

On a broken implementation you get results like:

```text
expected: 100
actual:   47
```

or:

```text
expected: 100
actual:   1
```

if the race window is wide enough.

This is the simplest baseline because it does not require complicated semantics. It directly answers:

> Are concurrent handlers serializing access to host memory?

If no, the counter breaks.

---

## Breaking example 2: two websocket clients both acquire exclusive ownership

This is closer to your “who has access to a critical memory object” case.

### Intended invariant

Only one websocket owns the device/session/resource at a time.

```text
owner == exactly one websocket id
```

### Actual broken result

Two clients both believe they acquired ownership.

### Pseudocode manifest

```yaml
websocket:
  routes:
    - path: /ws/device
      on_message: app/device.star:on_message

memory:
  objects:
    - name: device_locks
      type: map
```

### Pseudocode Starlark handler

```python
# app/device.star

def on_message(ctx, msg):
    locks = ctx.memory("device_locks")

    device_id = msg["device"]
    client_id = ctx.websocket.id

    if msg["type"] == "acquire":
        current_owner = locks.get(device_id)

        # Check-then-act race.
        if current_owner != None:
            return {
                "send": {
                    "type": "acquire.failed",
                    "device": device_id,
                    "owner": current_owner,
                }
            }

        # Artificially widen race window.
        ctx.debug_yield()

        # Both handlers can reach this point.
        locks.set(device_id, client_id)

        return {
            "send": {
                "type": "acquire.ok",
                "device": device_id,
                "owner": client_id,
            }
        }

    if msg["type"] == "release":
        current_owner = locks.get(device_id)

        if current_owner == client_id:
            locks.delete(device_id)
            return {
                "send": {
                    "type": "release.ok",
                    "device": device_id,
                }
            }

        return {
            "send": {
                "type": "release.failed",
                "device": device_id,
                "owner": current_owner,
            }
        }
```

### Test harness

```python
def test_two_clients_cannot_acquire_same_device():
    server = start_quack_site("device_site")

    a = connect_ws(server, "/ws/device")
    b = connect_ws(server, "/ws/device")

    barrier = Barrier(2)

    result_a = None
    result_b = None

    def client_a():
        barrier.wait()
        nonlocal result_a
        result_a = a.request({
            "type": "acquire",
            "device": "serial.rpi",
        })

    def client_b():
        barrier.wait()
        nonlocal result_b
        result_b = b.request({
            "type": "acquire",
            "device": "serial.rpi",
        })

    run_concurrently([client_a, client_b])

    assert exactly_one([
        result_a["type"] == "acquire.ok",
        result_b["type"] == "acquire.ok",
    ])
```

Broken result:

```text
client A receives acquire.ok
client B receives acquire.ok
final stored owner is whichever wrote last
```

That is worse than the counter example because the final state may look valid:

```text
device_locks["serial.rpi"] == "client-b"
```

but both clients were told they own the device.

That is the important bug class: **the externally observed history is not serializable even if the final memory value looks sane**.

---

## Breaking example 3: event pipe handler mutates same object concurrently

This tests whether pipes themselves imply serialization. Without an explicit serial-by-topic/key rule, they probably should not.

### Pseudocode manifest

```yaml
events:
  - selector: "room.*"
    on_event: app/room.star:on_event

websocket:
  routes:
    - path: /ws/room
      on_message: app/room.star:on_ws_message

memory:
  objects:
    - name: rooms
      type: map
```

### Pseudocode Starlark

```python
# app/room.star

def on_ws_message(ctx, msg):
    # Multiple websocket clients publish concurrently.
    return {
        "publish": {
            "topic": "room.123",
            "event": {
                "type": "join",
                "user": ctx.websocket.id,
            }
        }
    }


def on_event(ctx, event):
    rooms = ctx.memory("rooms")

    room = rooms.get("room.123") or {
        "users": [],
        "count": 0,
    }

    if event["type"] == "join":
        if event["user"] not in room["users"]:
            room["users"].append(event["user"])
            room["count"] = room["count"] + 1

    ctx.debug_yield()

    rooms.set("room.123", room)

    return {}
```

### Test harness

```python
def test_room_join_count_matches_users():
    server = start_quack_site("room_site")

    clients = [
        connect_ws(server, "/ws/room")
        for _ in range(100)
    ]

    barrier = Barrier(100)

    def join(client):
        barrier.wait()
        client.send({"type": "join"})

    run_concurrently([
        lambda c=client: join(c)
        for client in clients
    ])

    wait_until_idle(server)

    room = server.memory("rooms").get("room.123")

    assert len(room["users"]) == 100
    assert room["count"] == 100
```

Broken outcomes:

```text
len(users) == 31
count == 29
```

or:

```text
len(users) == 1
count == 1
```

or even:

```text
len(users) != count
```

depending on whether `rooms.get()` returns a copy or a live object.

---

## The most useful experimental version

I’d make the test case intentionally hostile:

```python
def on_message(ctx, msg):
    state = ctx.memory("test")

    value = state.get("x") or 0

    ctx.debug_yield()
    ctx.debug_yield()
    ctx.debug_yield()

    state.set("x", value + 1)

    return {}
```

Then test with:

```text
clients: 100
messages per client: 100
expected: 10,000
```

Harness:

```python
def test_many_concurrent_increments():
    server = start_quack_site("race_site")

    clients = [
        connect_ws(server, "/ws/counter")
        for _ in range(100)
    ]

    barrier = Barrier(100)

    def spam(client):
        barrier.wait()
        for i in range(100):
            client.send({"type": "increment"})

    run_concurrently([
        lambda c=client: spam(c)
        for client in clients
    ])

    wait_until_idle(server)

    assert server.memory("test").get("x") == 10000
```

This should become your baseline.

Then you can run the same test under different proposed semantics:

## Baseline A: current unsafe memory

```python
n = state.get("x") or 0
state.set("x", n + 1)
```

Expected:

```text
fails under concurrency
```

## Baseline B: host-level atomic increment

```python
state.incr("x", 1)
```

Expected:

```text
passes
```

## Baseline C: CAS

```python
while True:
    value, version = state.get_versioned("x")
    if state.cas("x", version, value + 1):
        break
```

Expected:

```text
passes, but may retry under contention
```

## Baseline D: serial-by-topic pipe

```yaml
events:
  - selector: "counter.increment"
    concurrency: serial_by_topic
    on_event: app/counter.star:on_increment
```

```python
def on_increment(ctx, event):
    n = ctx.memory("test").get("x") or 0
    ctx.memory("test").set("x", n + 1)
```

Expected:

```text
passes for one topic
```

## Baseline E: mutex object

```python
lock = ctx.mutex("counter:x")

lock.lock()
try:
    n = state.get("x") or 0
    state.set("x", n + 1)
finally:
    lock.unlock()
```

Expected:

```text
passes, but introduces deadlock/cancellation/effect-boundary complexity
```

That gives you a clean experimental matrix.

## The single best baseline

Use this one:

```python
def on_message(ctx, msg):
    state = ctx.memory("race")

    n = state.get("counter") or 0

    ctx.debug_yield()

    state.set("counter", n + 1)

    return {}
```

with this assertion:

```python
assert state.get("counter") == number_of_messages_sent
```

Then add the ownership test as the second baseline:

```python
if locks.get(device) == None:
    ctx.debug_yield()
    locks.set(device, client_id)
    return "acquired"
else:
    return "busy"
```

with this assertion:

```python
assert only_one_client_received_acquired
```

The counter test catches lost updates.
The ownership test catches non-serializable histories.

Together they tell you whether Quack needs:

```text
CAS
serial_by_key
host atomic operations
leases
or user-visible locks
```
