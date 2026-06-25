GRAVITY = 1800.0
JUMP_VELOCITY = -620.0
MOVE_SPEED = 320.0
GROUND_Y = 550.0
PLAYER_WIDTH = 28.0
PLAYER_HEIGHT = 42.0
CANVAS_WIDTH = 800.0
TOPIC = "jump:state"
PHYSICS_DT = 1.0 / 60.0
MAX_PLAYERS = 10

COLORS = [
    "0xFF6B6B", "0x4ECDC4", "0x45B7D1", "0x96CEB4",
    "0xFFD93D", "0xDDA0DD", "0x6BCB77", "0xFF8C42",
    "0x74B9FF", "0xA29BFE",
]


def _used_colors():
    colors = memory.get("jump:used_colors", [])
    if type(colors) != "list":
        return []
    return colors


def _next_color():
    colors = _used_colors()
    for c in COLORS:
        if c not in colors:
            colors.append(c)
            memory.set("jump:used_colors", colors)
            return c
    return COLORS[0]


def _free_color(color):
    colors = _used_colors()
    if color in colors:
        colors.remove(color)
        memory.set("jump:used_colors", colors)


def _players():
    ps = memory.get("jump:players", {})
    if type(ps) != "dict":
        return {}
    return ps


def _save_players(ps):
    return memory.set("jump:players", ps)


def _new_player(conn_id, color):
    return {
        "id": conn_id,
        "x": 200.0,
        "y": GROUND_Y - PLAYER_HEIGHT,
        "vx": 0.0,
        "vy": 0.0,
        "color": color,
        "left": False,
        "right": False,
        "jump": False,
        "on_ground": True,
    }


def _tick_players(players):
    for pid in players:
        p = players[pid]
        left = p.get("left", False)
        right = p.get("right", False)
        jump = p.get("jump", False)
        on_ground = p.get("on_ground", True)

        if left:
            p["vx"] = -MOVE_SPEED
        elif right:
            p["vx"] = MOVE_SPEED
        else:
            p["vx"] = 0.0

        if jump and on_ground:
            p["vy"] = JUMP_VELOCITY
            p["on_ground"] = False

        if not p["on_ground"]:
            p["vy"] += GRAVITY * PHYSICS_DT

        p["x"] += p["vx"] * PHYSICS_DT
        p["y"] += p["vy"] * PHYSICS_DT

        if p["y"] >= GROUND_Y - PLAYER_HEIGHT:
            p["y"] = GROUND_Y - PLAYER_HEIGHT
            p["vy"] = 0.0
            p["on_ground"] = True

        if p["x"] < 0.0:
            p["x"] = 0.0
        max_x = CANVAS_WIDTH - PLAYER_WIDTH
        if p["x"] > max_x:
            p["x"] = max_x


def on_connect(ctx):
    players = _players()
    if len(players) >= MAX_PLAYERS:
        return ws.send(ctx.conn_id, {
            "type": "error",
            "message": "room is full",
        })

    color = _next_color()
    player = _new_player(ctx.conn_id, color)
    players[ctx.conn_id] = player
    _save_players(players)

    log.info(message="jump player connected", conn_id=ctx.conn_id, total=len(players))

    return [
        ws.subscribe(ctx.conn_id, TOPIC),
        ws.send(ctx.conn_id, {
            "type": "ready",
            "conn_id": ctx.conn_id,
            "color": color,
            "players": list(players.values()),
        }),
        events.publish(TOPIC, {
            "type": "state",
            "players": list(players.values()),
        }),
    ]


TICK_INTERVAL = 3


def on_message(ctx, msg):
    if type(msg) != "dict":
        return []

    if msg.get("type") != "input":
        return []

    players = _players()
    player = players.get(ctx.conn_id)
    if not player:
        return []

    player["left"] = bool(msg.get("left", False))
    player["right"] = bool(msg.get("right", False))
    player["jump"] = bool(msg.get("jump", False))

    _tick_players(players)

    tick = memory.get("jump:tick", 0) + 1
    memory.set("jump:tick", tick)

    if tick % TICK_INTERVAL == 0:
        _save_players(players)
        return events.publish(TOPIC, {
            "type": "state",
            "players": list(players.values()),
        })

    return []


def on_event(ctx, event):
    if event.topic != TOPIC:
        return []
    return ws.send(ctx.conn_id, event.payload)


def on_disconnect(ctx):
    players = _players()
    player = players.pop(ctx.conn_id, None)
    if player:
        _free_color(player.get("color", ""))
    _save_players(players)

    log.info(message="jump player disconnected", conn_id=ctx.conn_id, remaining=len(players))

    return [
        events.publish(TOPIC, {
            "type": "state",
            "players": list(players.values()),
        }),
        ws.unsubscribe_all(ctx.conn_id),
    ]
