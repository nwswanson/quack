ROOM = "kings"
TOPIC = "pie.room." + ROOM
ROOM_KEY = "room:" + ROOM
LOCK_KEY = "memory:rooms:" + ROOM_KEY

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
        "started": False,
        "bites": [],
    }

def _room():
    return memory.get(ROOM_KEY) or _blank_room()

def _name_for(conn_id, count):
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
    return {
        "type": "state",
        "room": ROOM,
        "topic": TOPIC,
        "round": room["round"],
        "started": room["started"],
        "players": _visible_players(room),
        "bites": room["bites"],
        "notice": notice,
    }

def _all_ready(room):
    if len(room["order"]) == 0:
        return False
    for conn_id in room["order"]:
        if not room["ready"].get(conn_id, False):
            return False
    return True

def _bite_order(room):
    bites = []
    bite = 1
    for conn_id in room["order"]:
        name = room["players"].get(conn_id)
        if name:
            bites.append({
                "id": conn_id,
                "name": name,
                "bite": bite,
            })
            bite += 1
    return bites

def _with_room_lock(ctx):
    return ctx.locks().acquire(
        LOCK_KEY,
        ttl_ms = 1000,
        wait_ms = 50,
    )

def on_connect(ctx):
    return [
        ws.subscribe(ctx.conn_id, TOPIC),
        ws.send(ctx.conn_id, {
            "type": "hello",
            "conn_id": ctx.conn_id,
            "room": ROOM,
            "topic": TOPIC,
        }),
    ]

def on_message(ctx, msg):
    if type(msg) != "dict":
        return ws.send(ctx.conn_id, {"type": "error", "message": "json only, monarch"})

    kind = msg.get("type", "")
    if kind == "join":
        return _join(ctx)
    if kind == "ready":
        return _ready(ctx)
    if kind == "reset":
        return _reset(ctx)
    if kind == "sync":
        return ws.send(ctx.conn_id, _state(_room()))

    return ws.send(ctx.conn_id, {"type": "error", "message": "unknown pie ritual"})

def _join(ctx):
    lock = _with_room_lock(ctx)
    if not lock:
        return ws.send(ctx.conn_id, {"type": "busy", "message": "the pie bouncer is occupied"})

    room = _room()
    if ctx.conn_id not in room["players"]:
        room["players"][ctx.conn_id] = _name_for(ctx.conn_id, len(room["order"]))
        room["order"].append(ctx.conn_id)
        room["ready"][ctx.conn_id] = False
    room["started"] = False
    room["bites"] = []
    memory.set(ROOM_KEY, room)
    effect = ws.broadcast(TOPIC, _state(room, room["players"][ctx.conn_id] + " slid into the booth"))
    lock.release()
    return effect

def _ready(ctx):
    lock = _with_room_lock(ctx)
    if not lock:
        return ws.send(ctx.conn_id, {"type": "busy", "message": "too many kings reaching for pie"})

    room = _room()
    if ctx.conn_id not in room["players"]:
        room["players"][ctx.conn_id] = _name_for(ctx.conn_id, len(room["order"]))
        room["order"].append(ctx.conn_id)

    room["ready"][ctx.conn_id] = True
    notice = room["players"][ctx.conn_id] + " said go"

    if _all_ready(room) and not room["started"]:
        room["started"] = True
        room["round"] += 1
        room["bites"] = _bite_order(room)
        notice = "every king has said go. pie protocol engaged."

    memory.set(ROOM_KEY, room)
    effect = ws.broadcast(TOPIC, _state(room, notice))
    lock.release()
    return effect

def _reset(ctx):
    lock = _with_room_lock(ctx)
    if not lock:
        return ws.send(ctx.conn_id, {"type": "busy", "message": "the crumbs are being audited"})

    room = _blank_room()
    room["players"][ctx.conn_id] = _name_for(ctx.conn_id, 0)
    room["order"].append(ctx.conn_id)
    room["ready"][ctx.conn_id] = False
    memory.set(ROOM_KEY, room)
    effect = ws.broadcast(TOPIC, _state(room, "fresh pie, same weird little monarchy"))
    lock.release()
    return effect

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
            room["started"] = False
            room["bites"] = []
        memory.set(ROOM_KEY, room)
        effects.append(ws.broadcast(TOPIC, _state(room, name + " vanished into the liner notes")))
    lock.release()
    return effects
