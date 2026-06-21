WIDTH = 48
HEIGHT = 48
TOPIC = "pixeldraw:canvas"
REVISION_KEY = "pixeldraw:revision"
PIXEL_PREFIX = "pixeldraw:px:"
COLORS = {
    "white": "#ffffff",
    "black": "#16191d",
    "red": "#e13a32",
    "green": "#239a57",
    "blue": "#2469d8",
}

def _as_int(value, default = -1):
    if type(value) == "int":
        return value
    if type(value) == "float":
        return int(value)
    return default

def _pixel_index(x, y):
    return y * WIDTH + x

def _pixel_key(index):
    return PIXEL_PREFIX + str(index)

def _snapshot_pixels():
    pixels = []
    for i in range(WIDTH * HEIGHT):
        color = memory.get(_pixel_key(i), "white")
        if color != "white":
            pixels.append({"i": i, "color": color})
    return pixels

def _snapshot():
    return {
        "type": "canvas_snapshot",
        "width": WIDTH,
        "height": HEIGHT,
        "colors": COLORS,
        "revision": memory.get(REVISION_KEY, 0),
        "pixels": _snapshot_pixels(),
    }

def _error(message):
    return {"type": "error", "message": message}

def _valid_pixel(item):
    if type(item) != "dict":
        return None

    x = _as_int(item.get("x", -1))
    y = _as_int(item.get("y", -1))
    color = item.get("color", "")
    if x < 0 or x >= WIDTH or y < 0 or y >= HEIGHT:
        return None
    if color not in COLORS:
        return None

    return {"x": x, "y": y, "i": _pixel_index(x, y), "color": color}

def on_connect(ctx):
    return [
        ws.subscribe(ctx.conn_id, TOPIC),
        ws.send(ctx.conn_id, {
            "type": "ready",
            "conn_id": ctx.conn_id,
        }),
        ws.send(ctx.conn_id, _snapshot()),
    ]

def on_message(ctx, msg):
    if type(msg) != "dict":
        return ws.send(ctx.conn_id, _error("expected a JSON object"))

    if msg.get("type") != "draw_pixels":
        return ws.send(ctx.conn_id, _error("unknown message type"))

    raw_pixels = msg.get("pixels", [])
    if type(raw_pixels) != "list":
        return ws.send(ctx.conn_id, _error("pixels must be a list"))

    changed = []
    seen = {}
    for raw in raw_pixels:
        if len(changed) >= 512:
            break

        pixel = _valid_pixel(raw)
        if pixel == None:
            continue

        seen_key = str(pixel["i"])
        if seen_key in seen:
            continue
        seen[seen_key] = True

        key = _pixel_key(pixel["i"])
        previous = memory.get(key, "white")
        if previous == pixel["color"]:
            continue

        if pixel["color"] == "white":
            memory.delete(key)
        else:
            memory.set(key, pixel["color"])
        changed.append(pixel)

    if len(changed) == 0:
        return []

    revision = memory.incr(REVISION_KEY, 1)
    return events.publish(TOPIC, {
        "type": "pixels_updated",
        "revision": revision,
        "by": ctx.conn_id,
        "pixels": changed,
    })

def on_event(ctx, event):
    if event.topic != TOPIC:
        return []
    return ws.send(ctx.conn_id, event.payload)

def on_disconnect(ctx):
    return ws.unsubscribe_all(ctx.conn_id)
