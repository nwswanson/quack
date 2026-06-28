ROOM = "countdown"
TOPIC = "countdown.room." + ROOM
ROOM_KEY = "room:" + ROOM
APPLIED_KEY = "applied:" + ROOM
LOCK_KEY = "memory:countdown:" + ROOM_KEY
TARGET = 1000

NAMES = [
    "Tiny Tony",
    "Pocket Prince",
    "Low-Rise Larry",
    "Mini Max",
    "Knee-High Kai",
    "Half-Pint Hal",
    "Stubby Syd",
    "Compact Cam",
]

def _blank_room():
    return {
        "players": {},
        "order": [],
        "ready": {},
        "round": 0,
        "running": False,
        "done": False,
        "mode": "locked",
        "total": 0,
        "chunks": [],
    }

def _room():
    return memory.get(ROOM_KEY) or _blank_room()

def _name_for(count):
    return NAMES[count % len(NAMES)] + " #" + str(count + 1)

def _visible_players(room):
    players = []
    for conn_id in room["order"]:
        if conn_id in room["players"]:
            players.append({
                "id": conn_id,
                "name": room["players"][conn_id],
                "ready": room["ready"].get(conn_id, False),
            })
    return players

def _state(room, notice = ""):
    applied = memory.get(APPLIED_KEY) or 0
    lost = applied - room["total"]
    if lost < 0:
        lost = 0
    return {
        "type": "state",
        "room": ROOM,
        "topic": TOPIC,
        "target": TARGET,
        "mode": room["mode"],
        "round": room["round"],
        "running": room["running"],
        "done": room["done"],
        "total": room["total"],
        "applied": applied,
        "lost": lost,
        "players": _visible_players(room),
        "chunks": room["chunks"],
        "notice": notice,
    }

def _all_ready(room):
    if len(room["order"]) == 0:
        return False
    for conn_id in room["order"]:
        if not room["ready"].get(conn_id, False):
            return False
    return True

def _with_room_lock(ctx):
    return ctx.locks().acquire(
        LOCK_KEY,
        ttl_ms = 1000,
        wait_ms = 50,
    )

def _burn():
    # Widen the unlocked read-modify-write window enough that simultaneous
    # websocket messages can visibly stomp each other in unsafe mode.
    x = 0
    for i in range(12000):
        x = (x + i) % 97
    return x

def _sanitize_mode(value):
    if value == "unsafe":
        return "unsafe"
    return "locked"

def _sanitize_amount(value):
    if type(value) == "float":
        value = int(value)
    if type(value) != "int":
        return 0
    if value < 0:
        return 0
    if value > 20:
        return 20
    return value

def _append_chunk(room, chunk):
    chunks = room["chunks"]
    chunks.append(chunk)
    if len(chunks) > 18:
        chunks = chunks[len(chunks) - 18:]
    room["chunks"] = chunks

def _reset_room_for(ctx, mode):
    room = _blank_room()
    room["mode"] = _sanitize_mode(mode)
    room["players"][ctx.conn_id] = _name_for(0)
    room["order"].append(ctx.conn_id)
    room["ready"][ctx.conn_id] = False
    memory.delete(APPLIED_KEY)
    return room

def on_connect(ctx):
    return [
        ws.subscribe(ctx.conn_id, TOPIC),
        ws.send(ctx.conn_id, {
            "type": "hello",
            "conn_id": ctx.conn_id,
            "room": ROOM,
            "topic": TOPIC,
            "target": TARGET,
        }),
    ]

def on_message(ctx, msg):
    if type(msg) != "dict":
        return ws.send(ctx.conn_id, {"type": "error", "message": "json only, your majesty"})

    kind = msg.get("type", "")
    if kind == "join":
        return _join(ctx, msg.get("mode", "locked"))
    if kind == "ready":
        return _ready(ctx)
    if kind == "add":
        return _add(ctx, msg)
    if kind == "mode":
        return _set_mode(ctx, msg.get("mode", "locked"))
    if kind == "reset":
        return _reset(ctx, msg.get("mode", "locked"))
    if kind == "sync":
        return ws.send(ctx.conn_id, _state(_room()))

    return ws.send(ctx.conn_id, {"type": "error", "message": "unknown countdown ritual"})

def _join(ctx, mode):
    lock = _with_room_lock(ctx)
    if not lock:
        return ws.send(ctx.conn_id, {"type": "busy", "message": "the counting crown is occupied"})

    room = _room()
    if len(room["order"]) == 0:
        room["mode"] = _sanitize_mode(mode)
    if ctx.conn_id not in room["players"]:
        room["players"][ctx.conn_id] = _name_for(len(room["order"]))
        room["order"].append(ctx.conn_id)
        room["ready"][ctx.conn_id] = False
    memory.set(ROOM_KEY, room)
    effect = ws.broadcast(TOPIC, _state(room, room["players"][ctx.conn_id] + " joined the count"))
    lock.release()
    return effect

def _ready(ctx):
    lock = _with_room_lock(ctx)
    if not lock:
        return ws.send(ctx.conn_id, {"type": "busy", "message": "too many fingers on the abacus"})

    room = _room()
    if ctx.conn_id not in room["players"]:
        room["players"][ctx.conn_id] = _name_for(len(room["order"]))
        room["order"].append(ctx.conn_id)

    room["ready"][ctx.conn_id] = True
    notice = room["players"][ctx.conn_id] + " said go"

    if _all_ready(room) and not room["running"] and not room["done"]:
        room["running"] = True
        room["round"] += 1
        room["total"] = 0
        room["chunks"] = []
        notice = "everyone said go. race the counter to 1000."

    memory.set(ROOM_KEY, room)
    effect = ws.broadcast(TOPIC, _state(room, notice))
    lock.release()
    return effect

def _set_mode(ctx, mode):
    return _reset(ctx, mode)

def _reset(ctx, mode):
    lock = _with_room_lock(ctx)
    if not lock:
        return ws.send(ctx.conn_id, {"type": "busy", "message": "the reset lever is sticky"})

    room = _reset_room_for(ctx, mode)
    memory.set(ROOM_KEY, room)
    effect = ws.broadcast(TOPIC, _state(room, "new countdown, " + room["mode"] + " mode"))
    lock.release()
    return effect

def _add(ctx, msg):
    room = _room()
    if room["mode"] == "unsafe":
        return _add_unsafe(ctx, msg)
    return _add_locked(ctx, msg)

def _add_locked(ctx, msg):
    lock = _with_room_lock(ctx)
    if not lock:
        return ws.send(ctx.conn_id, {"type": "busy", "message": "locked mode is politely queuing"})

    room = _room()
    effect = _apply_add(ctx, room, msg, True)
    memory.set(ROOM_KEY, room)
    lock.release()
    return effect

def _add_unsafe(ctx, msg):
    room = _room()
    effect = _apply_add(ctx, room, msg, False)
    memory.set(ROOM_KEY, room)
    return effect

def _apply_add(ctx, room, msg, locked):
    if not room["running"] or room["done"]:
        return []

    requested = _sanitize_amount(msg.get("amount", 0))
    before = room["total"]
    _burn()
    remaining = TARGET - before
    amount = requested
    if amount > remaining:
        amount = remaining
    if amount < 0:
        amount = 0
    after = before + amount
    room["total"] = after
    if amount > 0:
        memory.incr(APPLIED_KEY, amount)
    if after >= TARGET:
        room["total"] = TARGET
        room["running"] = False
        room["done"] = True

    name = room["players"].get(ctx.conn_id, "unknown monarch")
    _append_chunk(room, {
        "id": ctx.conn_id,
        "name": name,
        "requested": requested,
        "amount": amount,
        "before": before,
        "after": room["total"],
        "locked": locked,
    })

    notice = name + " added " + str(amount)
    if room["done"]:
        notice = "countdown hit 1000 in " + room["mode"] + " mode"
    return ws.broadcast(TOPIC, _state(room, notice))

def on_event(ctx, event):
    return ws.send(ctx.conn_id, event.payload)

def on_disconnect(ctx):
    lock = _with_room_lock(ctx)
    effects = [ws.unsubscribe_all(ctx.conn_id)]
    if not lock:
        return effects

    room = _room()
    if ctx.conn_id in room["players"]:
        name = room["players"][ctx.conn_id]
        room["players"].pop(ctx.conn_id)
        room["ready"].pop(ctx.conn_id)
        next_order = []
        for conn_id in room["order"]:
            if conn_id != ctx.conn_id:
                next_order.append(conn_id)
        room["order"] = next_order
        if not _all_ready(room):
            room["running"] = False
        memory.set(ROOM_KEY, room)
        effects.append(ws.broadcast(TOPIC, _state(room, name + " left the count")))
    lock.release()
    return effects
