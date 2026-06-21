
Your existing shape is enough for the **handler model**: connect, message, event, disconnect returning side-effect commands. For a collaborative pixel canvas, the bigger question is not “do I need many more data structures?” but “what state is authoritative, how are updates represented, and how do late/reconnecting clients catch up?”

For a basic collaborative pixel drawing app, your current Redis-like store is probably enough, with maybe one important addition: an **append-only stream/log**.

## Useful data structures

You already have:

| Structure | Useful for                                                  |
| --------- | ----------------------------------------------------------- |
| `kv`      | canvas metadata, connection state, serialized tile blobs    |
| `set`     | users/connections in a canvas, dirty tiles                  |
| `counter` | global revision numbers, op IDs                             |
| `zset`    | presence expiry, recent active users, rate limiting buckets |
| `list`    | simple queues, recent events, undo stacks                   |

The one I would strongly consider adding:

| Structure                  | Why                                                                                                                             |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `stream` / append-only log | Lets reconnecting clients catch up from revision N, lets new clients load a snapshot plus recent deltas, helps debugging/replay |

You can fake this with a list, but a stream with monotonically increasing IDs is much nicer.

A **hash/map** type is also convenient, but not strictly necessary if your `kv` values can store JSON.

## Core model for a pixel canvas

Think in terms of **rooms**, **snapshots**, and **deltas**.

A canvas room might be:

```text
canvas:{canvas_id}
```

Each connected user subscribes to:

```text
canvas:{canvas_id}:events
```

The authoritative canvas state lives server-side. Clients never directly own truth. They send drawing operations; the server validates, applies, assigns a revision, and broadcasts.

## State layout

At a high level:

```text
canvas:{id}:meta
  width
  height
  created_by
  created_at
  current_revision

canvas:{id}:tile:{tx}:{ty}
  binary/blob/array of pixels for that tile

canvas:{id}:connections
  set of conn_ids

conn:{conn_id}
  user_id
  canvas_id
  last_seen_revision

canvas:{id}:events
  append-only stream/list of recent drawing operations

canvas:{id}:presence
  zset of user_id -> last_seen timestamp
```

The most important design choice is **tiling**.

Do not store the whole canvas as one giant value unless it is tiny. Split it into tiles, for example:

```text
32x32 pixels per tile
64x64 pixels per tile
128x128 pixels per tile
```

Then a pixel at `(x, y)` maps to:

```text
tile_x = floor(x / TILE_SIZE)
tile_y = floor(y / TILE_SIZE)
local_x = x % TILE_SIZE
local_y = y % TILE_SIZE
```

That gives you nice properties:

* New clients can load the canvas tile-by-tile.
* Only changed tiles need to be updated.
* You can cache or persist dirty tiles.
* Large canvases do not require broadcasting full state.

## Message types

For a pixel app, I would start with just a few message types.

Client to server:

```text
join_canvas
draw_pixels
cursor_move
ping
```

Server to client:

```text
ready
canvas_snapshot
pixels_updated
cursor_updated
presence_updated
error
```

The key message is probably not a single pixel update, but a **batch**:

```text
{
  "type": "draw_pixels",
  "canvas_id": "abc",
  "pixels": [
    {"x": 10, "y": 20, "color": "#ff0000"},
    {"x": 11, "y": 20, "color": "#ff0000"},
    {"x": 12, "y": 20, "color": "#ff0000"}
  ]
}
```

Do not broadcast every mousemove as an individual write. The browser should batch pixels every animation frame or every small interval, like 16–50ms.

## Basic flow

### 1. Connect

On connect:

```text
- store conn:{conn_id}
- add conn_id to canvas:{canvas_id}:connections
- subscribe conn_id to canvas:{canvas_id}:events
- send ready
- send initial snapshot or tile manifest
```

The initial snapshot can be:

```text
- full canvas if small
- visible viewport tiles if large
- tile metadata + lazy tile loading
```

For a first version, full snapshot is fine if the canvas is small.

### 2. Client draws

The browser tracks pointer movement on the canvas, converts it into pixels, batches them, and sends:

```text
draw_pixels
```

The server:

```text
- validates user can edit this canvas
- validates x/y bounds
- validates color format
- optionally rate-limits
- increments canvas revision
- applies pixels to authoritative tile state
- appends operation to event log
- publishes pixels_updated to the canvas room
```

The broadcast should include the assigned revision:

```text
{
  "type": "pixels_updated",
  "canvas_id": "abc",
  "revision": 3912,
  "by": "user_123",
  "pixels": [...]
}
```

### 3. Other clients receive update

Each client applies the update locally to its canvas.

For pixels, you can usually use **last-write-wins**. If two users draw the same pixel at nearly the same time, whichever operation the server orders later wins.

That is much simpler than CRDTs and is usually good enough for collaborative pixel art.

## Event ordering

You want the server to assign a single monotonically increasing revision per canvas:

```text
canvas:{id}:revision -> counter
```

Every mutation gets:

```text
revision = incr(canvas:{id}:revision)
```

Clients keep track of:

```text
last_seen_revision
```

If the client reconnects and says:

```text
last_seen_revision = 3880
```

The server can either:

```text
- replay events from 3881 onward, if still available
- or send a fresh snapshot
```

This is where an append-only event stream is very useful.

## Persistence strategy

There are two common options.

### Option A: Snapshot only

You update the actual tile state immediately and do not keep much history.

Good for:

```text
simple app
small scale
no undo
no replay
```

Downside:

```text
reconnects usually need a fresh snapshot
debugging is harder
```

### Option B: Snapshot + recent event log

Maintain:

```text
- tile snapshots as current truth
- recent event stream for catch-up
```

This is the best basic architecture.

Example:

```text
canvas:{id}:tile:{tx}:{ty} = current tile pixels
canvas:{id}:events = recent draw ops
canvas:{id}:revision = latest revision
```

Trim old events after some limit:

```text
keep last 1,000 / 10,000 / 100,000 operations
```

If a reconnecting client is too far behind, send a fresh snapshot.

## Presence and cursors

Presence is separate from drawing state.

Use a zset:

```text
canvas:{id}:presence
  user_id -> timestamp
```

On cursor movement, do not persist it deeply. Just broadcast ephemeral events:

```text
{
  "type": "cursor_updated",
  "user_id": "u1",
  "x": 123,
  "y": 456
}
```

Throttle these heavily. Maybe 10–20 per second per user.

Presence cleanup can be based on timestamps:

```text
remove users where last_seen < now - 30s
```

## Undo

Undo is deceptively tricky.

For a basic app, avoid global undo at first.

Possible levels:

### Simple personal undo

Store per-user recent operations:

```text
canvas:{id}:user:{user_id}:undo
```

Each draw operation stores the pixels changed plus their previous colors.

Then undo emits a new operation that restores those previous colors.

But this gets weird if another user has drawn over the same pixels since then. You need to decide whether undo should blindly restore old colors or only restore pixels that still match the user’s last operation.

For v1, I would skip undo or make it local-only before commit.

## Rate limiting and abuse protection

Pixel apps need rate limiting because one client can send huge pixel batches.

Use counters or zsets for:

```text
user:{id}:draw_rate
conn:{id}:msg_rate
canvas:{id}:write_rate
```

Put limits on:

```text
max pixels per message
max messages per second
max canvas dimensions
max colors / palette size
max event payload size
```

The server should reject or clamp bad updates.

## Browser-side architecture

In the browser, keep three layers conceptually:

```text
authoritative canvas buffer
pending local strokes
cursor/presence overlay
```

A simple flow:

```text
- Load snapshot.
- Draw snapshot to canvas.
- When user draws, apply locally immediately for responsiveness.
- Batch and send pixels to server.
- When server broadcasts the same update back, reconcile by revision.
```

For v1, you can even apply local pixels optimistically and then also accept the server echo. Since setting the same pixel color twice is harmless, that is fine.

Use a separate overlay canvas for cursors, selections, names, etc. Do not mix cursor rendering into the actual pixel canvas.

## Your handlers could map like this

Conceptually:

```python
def on_connect(ctx):
    return [
        state.put("conn:" + ctx.conn_id, ...),
        state.set_add("canvas:" + canvas_id + ":connections", ctx.conn_id),
        ws.subscribe(ctx.conn_id, "canvas:" + canvas_id),
        ws.send(ctx.conn_id, {"type": "ready", ...}),
        ws.send(ctx.conn_id, {"type": "snapshot", ...}),
    ]
```

For drawing:

```python
def on_message(ctx, msg):
    if msg["type"] == "draw_pixels":
        return [
            validate(...),
            revision.incr("canvas:" + canvas_id + ":revision"),
            canvas.apply_pixels(canvas_id, pixels),
            stream.append("canvas:" + canvas_id + ":events", op),
            events.publish("canvas:" + canvas_id, {
                "type": "pixels_updated",
                "revision": revision,
                "pixels": pixels,
                "by": ctx.user.id,
            }),
        ]
```

For event:

```python
def on_event(ctx, event):
    return [
        ws.broadcast("canvas:" + canvas_id, event)
    ]
```

## Important design recommendation

For this app, do **not** model each pixel as its own key.

Avoid:

```text
canvas:{id}:pixel:{x}:{y}
```

That becomes too many keys and too much overhead.

Prefer:

```text
canvas:{id}:tile:{tx}:{ty}
```

where each tile stores a compact pixel buffer.

For example:

```text
64x64 tile
4096 pixels
4 bytes per pixel RGBA
16 KB per tile
```

That is easy to update, serialize, cache, and send.

## Minimal v1 architecture

A good first version would be:

```text
Client:
  - HTML canvas
  - websocket connection
  - local pixel buffer
  - batched draw_pixels messages

Server:
  - websocket handlers
  - authoritative tile store
  - canvas revision counter
  - room pub/sub
  - recent event log

Store:
  - kv for metadata and tile blobs
  - set for canvas connections
  - zset for presence
  - counter for revisions
  - stream/list for recent operations
```

That is enough to build a solid collaborative pixel canvas without needing CRDTs, complex conflict resolution, or a database-heavy design. The key is to make the server the ordering authority and treat drawing as ordered pixel-delta operations.
