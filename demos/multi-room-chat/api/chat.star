TOPIC_PREFIX = "chat.room."
ROOM_MIN = 1
ROOM_MAX = 100
MAX_NAME = 24
MAX_MESSAGE = 280

def _conn_key(conn_id):
    return "conn:" + conn_id

def _topic(room):
    return TOPIC_PREFIX + str(room)

def _room_from_topic(topic):
    if type(topic) != "string" or topic[:len(TOPIC_PREFIX)] != TOPIC_PREFIX:
        return None
    raw = topic[len(TOPIC_PREFIX):]
    return _sanitize_room(raw)

def _trim(value):
    if type(value) != "string":
        return ""
    return value.strip()

def _limit(value, max_len):
    if len(value) <= max_len:
        return value
    return value[:max_len]

def _sanitize_room(value):
    if type(value) == "float":
        value = int(value)
    if type(value) == "int":
        room = value
    elif type(value) == "string" and _is_digits(value):
        room = int(value)
    else:
        return None
    if room < ROOM_MIN or room > ROOM_MAX:
        return None
    return room

def _is_digits(value):
    if value == "":
        return False
    for i in range(len(value)):
        ch = value[i]
        if ch < "0" or ch > "9":
            return False
    return True

def _sanitize_name(value, fallback):
    name = _limit(_trim(value), MAX_NAME)
    if name == "":
        return fallback
    return name

def _sanitize_message(value):
    text = _limit(_trim(value), MAX_MESSAGE)
    if text == "":
        return ""
    return text

def _connection(ctx):
    return memory.get(_conn_key(ctx.conn_id)) or {}

def _set_connection(ctx, record):
    memory.set(_conn_key(ctx.conn_id), record)

def _error(ctx, message):
    ws.send(ctx.conn_id, {
        "type": "error",
        "message": message,
    })

def on_connect(ctx):
    ws.subscribe(ctx.conn_id, "chat.room.*")
    ws.send(ctx.conn_id, {
        "type": "ready",
        "conn_id": ctx.conn_id,
        "rooms": {
            "min": ROOM_MIN,
            "max": ROOM_MAX,
        },
        "selector": "chat.room.*",
    })

def on_message(ctx, msg):
    if type(msg) != "dict":
        _error(ctx, "json object required")
        return

    kind = msg.get("type", "")
    if kind == "join":
        _join(ctx, msg)
        return
    if kind == "message":
        _message(ctx, msg)
        return
    if kind == "typing":
        _typing(ctx, msg)
        return

    _error(ctx, "unknown message type")

def _join(ctx, msg):
    room = _sanitize_room(msg.get("room"))
    if room == None:
        _error(ctx, "room must be 1-100")
        return

    old = _connection(ctx)
    previous_room = old.get("room")
    name = _sanitize_name(msg.get("name"), old.get("name", "Guest " + ctx.conn_id[:5]))
    record = {
        "room": room,
        "name": name,
    }
    _set_connection(ctx, record)

    ws.send(ctx.conn_id, {
        "type": "joined",
        "room": room,
        "name": name,
        "topic": _topic(room),
        "selector": "chat.room.*",
    })
    events.publish(_topic(room), {
        "type": "system",
        "room": room,
        "name": name,
        "text": name + " joined room " + str(room),
    })
    if previous_room != None and previous_room != room:
        events.publish(_topic(previous_room), {
            "type": "system",
            "room": previous_room,
            "name": name,
            "text": name + " moved to room " + str(room),
        })

def _message(ctx, msg):
    conn = _connection(ctx)
    room = conn.get("room")
    if room == None:
        _error(ctx, "join a room first")
        return

    text = _sanitize_message(msg.get("text"))
    if text == "":
        return

    events.publish(_topic(room), {
        "type": "message",
        "room": room,
        "name": conn.get("name", "Guest"),
        "conn_id": ctx.conn_id,
        "text": text,
    })

def _typing(ctx, msg):
    conn = _connection(ctx)
    room = conn.get("room")
    if room == None:
        return
    active = bool(msg.get("active", False))
    events.publish(_topic(room), {
        "type": "typing",
        "room": room,
        "name": conn.get("name", "Guest"),
        "conn_id": ctx.conn_id,
        "active": active,
    })

def on_event(ctx, event):
    _deliver(ctx, event)

def on_chat_event(ctx, event):
    _deliver(ctx, event)

def _deliver(ctx, event):
    room = _room_from_topic(event.topic)
    if room == None:
        return

    conn = _connection(ctx)
    if conn.get("room") != room:
        return

    payload = event.payload
    if type(payload) != "dict":
        return
    payload["topic"] = event.topic
    payload["selector"] = "chat.room.*"
    ws.send(ctx.conn_id, payload)

def on_disconnect(ctx):
    conn = _connection(ctx)
    memory.delete(_conn_key(ctx.conn_id))
    room = conn.get("room")
    name = conn.get("name", "")
    if room == None or name == "":
        ws.unsubscribe_all(ctx.conn_id)
        return
    ws.unsubscribe_all(ctx.conn_id)
    events.publish(_topic(room), {
        "type": "system",
        "room": room,
        "name": name,
        "text": name + " left room " + str(room),
    })
