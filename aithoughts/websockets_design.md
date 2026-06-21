Model this almost exactly like the AWS Lambda + API Gateway pattern, but inside your own Go runtime.

The important design move is:

```text id="uyefio"
Starlark does not own the WebSocket.
Starlark does not hold long-lived listeners.
Starlark does not keep authoritative memory between events.

Go owns sockets, state stores, subscriptions, timers, event routing, and re-invocation.

Starlark is a stateless event handler that returns effects.
```

That is probably the cleanest mental model.

## The shape of the system

You want something like this:

```text id="r2pbjc"
Client
  ⇅ WebSocket

Go WebSocket runtime
  - accepts socket
  - assigns connection ID
  - stores connection metadata
  - receives messages
  - sends messages
  - tracks subscriptions/listeners

Starlark handler
  - on_connect
  - on_message
  - on_disconnect
  - on_event
  - maybe on_timer

State / event layer
  - database
  - KV store
  - pub/sub
  - subscription registry
```

The Go application should be the “API Gateway equivalent.” The Starlark script should be the “Lambda equivalent.”

So instead of a Starlark script doing this:

```python id="ke944i"
# Not ideal
def on_connect(conn):
    conn.on("document_changed", lambda event: conn.send(event))
```

It should do something more like this:

```python id="tebzx2"
def on_connect(ctx):
    doc_id = ctx.params["doc_id"]

    return [
        ws.subscribe(ctx.conn_id, "document:" + doc_id),
        ws.send(ctx.conn_id, {
            "type": "connected",
            "doc_id": doc_id,
        }),
    ]
```

The script is not installing an actual in-memory callback. It is returning a **durable effect**:

```text id="4tbc5z"
"subscribe this connection to document:abc123"
```

Your Go runtime records that subscription somewhere.

Later, when `document:abc123` changes, the Go runtime re-invokes Starlark with a new event.

## Core abstraction: Starlark returns effects

I would strongly consider making the Starlark layer effect-based.

For example:

```python id="q9z4mu"
def on_connect(ctx):
    return [
        state.put("conn:" + ctx.conn_id, {
            "user_id": ctx.user.id,
            "doc_id": ctx.params["doc_id"],
        }),

        ws.subscribe(ctx.conn_id, "doc:" + ctx.params["doc_id"]),

        ws.send(ctx.conn_id, {
            "type": "ready",
        }),
    ]


def on_message(ctx, msg):
    if msg["type"] == "edit":
        doc_id = msg["doc_id"]

        return [
            db.update("documents", doc_id, {
                "content": msg["content"],
            }),

            events.publish("doc:" + doc_id, {
                "type": "document_updated",
                "doc_id": doc_id,
                "by": ctx.user.id,
            }),
        ]

    return []


def on_event(ctx, event):
    if event["type"] == "document_updated":
        return [
            ws.broadcast("doc:" + event["doc_id"], {
                "type": "document_updated",
                "doc_id": event["doc_id"],
                "by": event["by"],
            })
        ]

    return []


def on_disconnect(ctx):
    return [
        ws.unsubscribe_all(ctx.conn_id),
        state.delete("conn:" + ctx.conn_id),
    ]
```

The important thing is that these functions **run to completion**. They do not sit around.

Each function invocation is like:

```text id="dlszuo"
input event + loaded script + external state access
    ↓
Starlark runs
    ↓
returns effects
    ↓
Go validates and applies effects
    ↓
Starlark exits
```

This keeps the Starlark model close to your existing request/response architecture.

## What “listeners” become

You said:

> the server based on the connection may want to set listeners for when something changes

In this model, a “listener” is not a live function pointer. It is a persisted subscription.

For example:

```text id="ub6wuf"
connection c1 subscribes to topic doc:123
connection c2 subscribes to topic doc:123
connection c3 subscribes to topic user:456:notifications
```

Then your Go runtime has a registry:

```text id="p0zedg"
topic                         connections
------------------------------------------------
doc:123                       c1, c2
user:456:notifications         c3
```

When something changes:

```text id="xwkvxi"
document 123 changes
    ↓
Go emits event: topic = doc:123
    ↓
Go finds connections subscribed to doc:123
    ↓
Go invokes Starlark on_event
    ↓
Starlark returns broadcast/send effect
    ↓
Go sends over the live sockets
```

So “listener” means:

```text id="s346h3"
A durable routing rule saying:
"When event X happens, these connections/scripts are interested."
```

Not:

```text id="ds0fr8"
A Starlark closure kept alive in memory.
```

That distinction is critical.

## Minimal set of runtime primitives

You probably need these host-owned concepts:

```text id="hlwmre"
connection_id
  Unique ID for an open socket.

connection registry
  connection_id -> user/session/socket metadata.

subscription registry
  topic -> connection_ids
  connection_id -> topics

event bus
  publish(topic, payload)
  dispatch(topic, payload)

state store
  durable data available across Starlark invocations.

socket sender
  send(connection_id, payload)
  broadcast(topic, payload)
```

The Go runtime owns the real socket object:

```go id="49w3j0"
type Connection struct {
    ID     string
    UserID string
    Socket *websocket.Conn
}
```

Starlark should only ever see something like:

```python id="2h7jsc"
ctx.conn_id
ctx.user
ctx.params
```

Not the raw Go socket.

## How server push works in your app

A full flow might look like this:

```text id="jv8ozb"
1. Client opens WebSocket /docs/123

2. Go accepts socket and creates connection ID c1

3. Go invokes Starlark:
   on_connect(ctx)

4. Starlark returns:
   subscribe(c1, "doc:123")
   send(c1, {"type": "ready"})

5. Go records subscription:
   doc:123 -> c1

6. Some server-side thing changes document 123:
   - another HTTP request
   - a background job
   - a DB update
   - another WebSocket message
   - an internal Go event

7. Go publishes:
   topic = "doc:123"
   event = {"type": "document_updated", ...}

8. Go invokes Starlark:
   on_event(ctx, event)

9. Starlark returns:
   broadcast("doc:123", {"type": "document_updated", ...})

10. Go sends the message to all sockets subscribed to doc:123
```

The client did not request the update. But the server also did not keep a Starlark script alive. The server re-ran Starlark in response to a backend event.

## Where should state live?

Depends on deployment topology.

### Single-process version

If your Go app is one process, you can start simple:

```text id="9dbsdg"
Go memory:
  connection_id -> socket
  topic -> connection_ids
  connection_id -> topics

Database:
  durable app data
```

This is fine for a prototype or a single-node app.

But note: connection state is still ephemeral. If the process restarts, all sockets die anyway, so losing the in-memory connection registry is acceptable.

### Multi-process / multi-node version

If you have multiple Go servers, then you need a shared coordination layer:

```text id="bb9n2t"
Go node A owns sockets c1, c2
Go node B owns sockets c3, c4

Shared registry:
  c1 -> node A
  c2 -> node A
  c3 -> node B
  c4 -> node B

Shared pub/sub:
  event doc:123 goes to all nodes
```

Then each node sends only to the sockets it actually owns.

In that world, you might use:

```text id="hz6brj"
Redis / Valkey:
  pub/sub
  presence
  ephemeral subscriptions
  connection routing
  rate limits
  short-lived state

Postgres:
  durable app state
  LISTEN/NOTIFY for modest eventing
  relational queries

NATS:
  clean event bus / pub-sub
  good if your app becomes event-heavy

Kafka:
  durable event log
  probably too much unless you already need it

SQLite / embedded DB:
  good for single-node durable state

Plain Go maps:
  totally fine for local connection objects
```

If the WebSocket connection itself only exists inside a Go process, you will always need **some local memory** for the actual socket. The question is only whether routing/subscription metadata also lives locally or in a shared store.

## Do not make Starlark listeners real goroutines

I would avoid an API like this:

```python id="bx8ccl"
def on_connect(ctx):
    @listen("doc:" + ctx.params["doc_id"])
    def changed(event):
        ws.send(ctx.conn_id, event)
```

That looks ergonomic, but it implies the Starlark function persists after `on_connect` returns. That creates hard questions:

```text id="2pc2n9"
Where does the closure live?
What happens if the script is reloaded?
What happens if the server restarts?
Can this listener capture mutable state?
Can it leak memory?
Can it block a Go goroutine?
How do you inspect/debug registered listeners?
How do you scale it across nodes?
```

Much cleaner:

```python id="84s4ja"
def on_connect(ctx):
    return [
        ws.subscribe(ctx.conn_id, "doc:" + ctx.params["doc_id"])
    ]


def on_event(ctx, event):
    return [
        ws.send(ctx.conn_id, {
            "type": "changed",
            "value": event["value"],
        })
    ]
```

The “listener” is data, not a live closure.

## You need to decide the dispatch granularity

There are two reasonable designs.

### Option A: invoke Starlark once per event

```text id="3p53jl"
event doc:123 happened
    ↓
invoke on_event(event)
    ↓
Starlark returns broadcast("doc:123", payload)
    ↓
Go sends to all subscribers
```

This is efficient. Good for simple broadcast-style cases.

Example:

```python id="8rdmat"
def on_event(ctx, event):
    return [
        ws.broadcast(event.topic, {
            "type": "changed",
            "payload": event.payload,
        })
    ]
```

### Option B: invoke Starlark once per connection

```text id="fa68g4"
event doc:123 happened
    ↓
for each subscribed connection:
    invoke on_event(ctx_for_connection, event)
    Starlark returns send(connection_id, personalized_payload)
```

This is more flexible. It allows per-user authorization, filtering, and personalized payloads.

Example:

```python id="ogm73a"
def on_event(ctx, event):
    if not auth.can_see(ctx.user, event["doc_id"]):
        return []

    return [
        ws.send(ctx.conn_id, {
            "type": "changed",
            "doc_id": event["doc_id"],
            "visible_fields": event["public_fields"],
        })
    ]
```

The downside is that it is more expensive if 10,000 clients are subscribed to the same topic.

A nice compromise is to support both:

```python id="hj6yjq"
ws.broadcast(topic, payload)
ws.send(conn_id, payload)
```

And have your runtime decide whether a route is global or per-connection.

## Timers are also durable effects

You may eventually want something like:

```python id="4llp53"
def on_connect(ctx):
    return [
        timers.set(
            key = "heartbeat:" + ctx.conn_id,
            after = "30s",
            event = {
                "type": "heartbeat",
                "conn_id": ctx.conn_id,
            },
        )
    ]
```

But again, this should not mean “Starlark sleeps for 30 seconds.”

It means:

```text id="bj3cvy"
Starlark asks host to create timer.
Host stores/schedules timer.
Later timer fires.
Host invokes Starlark again.
```

So timers, subscriptions, jobs, and listeners are all the same kind of thing:

```text id="zvi2ev"
Starlark returns a durable intent.
Go owns the actual long-lived behavior.
```

## Recommended API shape

I’d design the Starlark surface around handlers and effects:

```python id="47w49j"
def on_connect(ctx):
    return [
        ws.accept(),
        ws.subscribe(ctx.conn_id, "user:" + ctx.user.id),
        ws.send(ctx.conn_id, {"type": "connected"}),
    ]


def on_message(ctx, msg):
    return [
        events.publish("user:" + ctx.user.id, {
            "type": "client_message",
            "body": msg,
        })
    ]


def on_event(ctx, event):
    return [
        ws.send(ctx.conn_id, {
            "type": "server_event",
            "event": event,
        })
    ]


def on_disconnect(ctx):
    return [
        ws.unsubscribe_all(ctx.conn_id)
    ]
```

And in Go, something roughly like:

```go id="x8hw7i"
type Effect struct {
    Type string
    Args map[string]any
}

func HandleWebSocketEvent(script Script, event Event) error {
    effects, err := RunStarlark(script, event)
    if err != nil {
        return err
    }

    for _, effect := range effects {
        if err := ApplyEffect(effect); err != nil {
            return err
        }
    }

    return nil
}
```

Where `ApplyEffect` is host-controlled:

```go id="e65hbw"
func ApplyEffect(e Effect) error {
    switch e.Type {
    case "ws.send":
        return socketManager.Send(e.ConnID, e.Payload)

    case "ws.broadcast":
        conns := subscriptions.Connections(e.Topic)
        return socketManager.SendMany(conns, e.Payload)

    case "ws.subscribe":
        return subscriptions.Add(e.Topic, e.ConnID)

    case "events.publish":
        return eventBus.Publish(e.Topic, e.Payload)

    case "state.put":
        return state.Put(e.Key, e.Value)

    default:
        return fmt.Errorf("unknown effect: %s", e.Type)
    }
}
```

## The core rule

The rule I would use is:

```text id="rq3yyd"
Starlark may describe durable behavior.
Go must implement durable behavior.
```

So Starlark can say:

```text id="uhke92"
subscribe me to this topic
send this message
publish this event
store this value
schedule this timer
start this job
```

But Starlark should not directly:

```text id="t6x6yc"
hold sockets
spawn goroutines
sleep until something changes
own listener lifetimes
keep authoritative mutable state in globals
```

## Final mental model

Your current request/response Starlark model:

```text id="vr6y4g"
HTTP request
    ↓
run Starlark handler
    ↓
return response
    ↓
handler exits
```

The WebSocket/event model:

```text id="ujw0fd"
WebSocket connect/message/disconnect/backend event
    ↓
run Starlark handler
    ↓
return effects
    ↓
Go applies effects
    ↓
handler exits
```

Server push is just another event path:

```text id="5q3gdo"
backend change
    ↓
Go event bus
    ↓
subscription lookup
    ↓
run Starlark on_event
    ↓
return ws.send/ws.broadcast effect
    ↓
Go writes to socket
```

That gives you WebSocket push without violating the “Starlark runs to completion and exits” model.
