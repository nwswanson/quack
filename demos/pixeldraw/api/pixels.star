WIDTH = 48
HEIGHT = 48

TOPIC = "pixeldraw:canvas"
DRAWINGS_KEY = "pixeldraw:drawings"
DRAWING_PREFIX = "pixeldraw:drawing:"
MAX_BATCH_PIXELS = 512
DIGITS = "0123456789"

COLOR_COUNT = 24
COLOR_BY_ID = {
    "white": 0,
    "light_gray": 1,
    "gray": 2,
    "black": 3,
    "maroon": 4,
    "red": 5,
    "orange": 6,
    "yellow": 7,
    "olive": 8,
    "green": 9,
    "lime": 10,
    "teal": 11,
    "cyan": 12,
    "blue": 13,
    "navy": 14,
    "purple": 15,
    "magenta": 16,
    "pink": 17,
    "peach": 18,
    "tan": 19,
    "brown": 20,
    "cream": 21,
    "mint": 22,
    "sky": 23,
}

def _as_int(value, default = -1):
    if type(value) == "int":
        return value
    if type(value) == "float":
        return int(value)
    if type(value) == "string":
        if value == "":
            return default
        for i in range(len(value)):
            if value[i] not in DIGITS:
                return default
        return int(value)
    return default

def _new_drawing_id():
    return uuid.uuid4()

def _pixel_index(x, y):
    return y * WIDTH + x

def _drawing_key(drawing_id, suffix):
    return DRAWING_PREFIX + drawing_id + ":" + suffix

def _pixels_key(drawing_id):
    return _drawing_key(drawing_id, "pixels")

def _revision_key(drawing_id):
    return _drawing_key(drawing_id, "revision")

def _revision_counter_key(drawing_id):
    return _drawing_key(drawing_id, "revision_counter")

def _valid_color_code(value):
    code = _as_int(value)
    if code >= 0 and code < COLOR_COUNT:
        return code
    if type(value) == "string":
        return COLOR_BY_ID.get(value, -1)
    return -1

def _pixel_index_key(index):
    return str(index)

def _read_pixels(drawing_id):
    pixels = memory.get(_pixels_key(drawing_id), {})
    if type(pixels) != "dict":
        return {}
    return pixels

def _write_pixels(drawing_id, pixels):
    if len(pixels) == 0:
        memory.delete(_pixels_key(drawing_id))
        return True
    return memory.set(_pixels_key(drawing_id), pixels)

def _drawings():
    drawings = memory.get(DRAWINGS_KEY, [])
    if type(drawings) != "list":
        drawings = []
    valid = []
    seen = {}
    for drawing_id in drawings:
        if type(drawing_id) != "string" or drawing_id == "" or drawing_id in seen:
            continue
        valid.append(drawing_id)
        seen[drawing_id] = True
    return valid

def _save_drawings(drawings):
    return memory.set(DRAWINGS_KEY, drawings)

def _ensure_drawings():
    drawings = _drawings()
    if len(drawings) > 0:
        return drawings

    default_id = _new_drawing_id()
    drawings = [default_id]
    _save_drawings(drawings)
    return drawings

def _snapshot_pixels(drawing_id):
    out = []
    pixels = _read_pixels(drawing_id)
    normalized = {}
    dirty = len(pixels) > WIDTH * HEIGHT

    for i in range(WIDTH * HEIGHT):
        index_key = _pixel_index_key(i)
        if index_key not in pixels:
            continue
        stored = pixels[index_key]
        code = _valid_color_code(stored)
        if code < 0:
            dirty = True
            continue
        if code != 0:
            normalized[_pixel_index_key(i)] = code
            out.append({"i": i, "color": code})
        if stored != code:
            dirty = True

    if len(normalized) != len(pixels):
        dirty = True
    if dirty:
        _write_pixels(drawing_id, normalized)
    return out

def _snapshot(drawing_id = ""):
    drawings = _ensure_drawings()
    if drawing_id == "" or drawing_id not in drawings:
        drawing_id = drawings[0]
    return {
        "type": "canvas_snapshot",
        "width": WIDTH,
        "height": HEIGHT,
        "drawing_id": drawing_id,
        "drawings": drawings,
        "revision": memory.get(_revision_counter_key(drawing_id), 0),
        "pixels": _snapshot_pixels(drawing_id),
    }

def _drawings_changed(active_id = ""):
    return {
        "type": "drawings_changed",
        "drawings": _ensure_drawings(),
        "active_drawing_id": active_id,
    }

def _error(message):
    return {"type": "error", "message": message}

def _valid_drawing_id(value):
    if type(value) != "string":
        return ""
    if value in _ensure_drawings():
        return value
    return ""

def _valid_pixel(item):
    if type(item) != "dict":
        return None

    x = _as_int(item.get("x", -1))
    y = _as_int(item.get("y", -1))
    color = _valid_color_code(item.get("color", -1))
    if x < 0 or x >= WIDTH or y < 0 or y >= HEIGHT:
        return None
    if color < 0:
        return None

    return {"x": x, "y": y, "i": _pixel_index(x, y), "color": color}

def _create_drawing():
    drawings = _ensure_drawings()
    drawing_id = _new_drawing_id()
    drawings.append(drawing_id)
    _save_drawings(drawings)
    return drawing_id

def _delete_drawing(drawing_id):
    drawings = _ensure_drawings()
    if drawing_id not in drawings:
        return drawings[0]
    if len(drawings) == 1:
        return drawing_id

    remaining = []
    for candidate in drawings:
        if candidate != drawing_id:
            remaining.append(candidate)
    _save_drawings(remaining)

    memory.delete(_pixels_key(drawing_id))
    memory.delete(_revision_key(drawing_id))
    memory.delete(_revision_counter_key(drawing_id))
    return remaining[0]

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

    msg_type = msg.get("type")
    if msg_type == "get_drawing" or msg_type == "select_drawing":
        drawing_id = _valid_drawing_id(msg.get("drawing_id", ""))
        if drawing_id == "":
            return ws.send(ctx.conn_id, _error("unknown drawing"))
        return ws.send(ctx.conn_id, _snapshot(drawing_id))

    if msg_type == "create_drawing":
        drawing_id = _create_drawing()
        return [
            events.publish(TOPIC, _drawings_changed(drawing_id)),
            ws.send(ctx.conn_id, _snapshot(drawing_id)),
        ]

    if msg_type == "delete_drawing":
        drawing_id = _valid_drawing_id(msg.get("drawing_id", ""))
        if drawing_id == "":
            return ws.send(ctx.conn_id, _error("unknown drawing"))
        next_id = _delete_drawing(drawing_id)
        return [
            events.publish(TOPIC, _drawings_changed(next_id)),
            ws.send(ctx.conn_id, _snapshot(next_id)),
        ]

    if msg_type != "draw_pixels":
        return ws.send(ctx.conn_id, _error("unknown message type"))

    drawing_id = _valid_drawing_id(msg.get("drawing_id", ""))
    if drawing_id == "":
        drawing_id = _ensure_drawings()[0]

    raw_pixels = msg.get("pixels", [])
    if type(raw_pixels) != "list":
        return ws.send(ctx.conn_id, _error("pixels must be a list"))

    changed = []
    seen = {}
    stored_pixels = _read_pixels(drawing_id)
    for raw in raw_pixels:
        if len(changed) >= MAX_BATCH_PIXELS:
            break

        pixel = _valid_pixel(raw)
        if pixel == None:
            continue

        seen_key = str(pixel["i"])
        if seen_key in seen:
            continue
        seen[seen_key] = True

        previous = _valid_color_code(stored_pixels.get(seen_key, 0))
        if previous == pixel["color"]:
            continue

        if pixel["color"] == 0:
            stored_pixels.pop(seen_key, None)
        else:
            stored_pixels[seen_key] = pixel["color"]
        changed.append(pixel)

    if len(changed) == 0:
        return []

    _write_pixels(drawing_id, stored_pixels)
    revision = memory.incr(_revision_counter_key(drawing_id), 1)
    return events.publish(TOPIC, {
        "type": "pixels_updated",
        "drawing_id": drawing_id,
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
