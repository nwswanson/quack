WIDTH = 48
HEIGHT = 48

TOPIC = "pixeldraw.canvas"
DRAWINGS_KEY = "pixeldraw:drawings"
DRAWING_NAMES_KEY = "pixeldraw:drawing_names"
DRAWING_PREFIX = "pixeldraw:drawing:"
NAMESPACE_PREFIX = "pixeldraw:namespace:"
MAX_BATCH_PIXELS = 512
DIGITS = "0123456789"
NAMESPACE_CHARS = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-."
MAX_NAMESPACE_LENGTH = 64
MAX_DRAWING_NAME_LENGTH = 48

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

def _valid_namespace(value):
    if type(value) != "string" or value == "" or len(value) > MAX_NAMESPACE_LENGTH:
        return ""
    for i in range(len(value)):
        if value[i] not in NAMESPACE_CHARS:
            return ""
    return value

def _query_param(query, name):
    if type(query) != "string" or query == "":
        return ""
    needle = name + "="
    start = 0
    for i in range(len(query) + 1):
        if i == len(query) or query[i] == "&":
            if i - start >= len(needle) and query[start:start + len(needle)] == needle:
                return query[start + len(needle):i]
            start = i + 1
    return ""

def _namespace_from_query(query):
    namespace = _valid_namespace(_query_param(query, "ns"))
    if namespace != "":
        return namespace
    return _valid_namespace(_query_param(query, "namespace"))

def _namespace_from_message(msg):
    if type(msg) != "dict":
        return ""
    return _valid_namespace(msg.get("namespace", ""))

def _namespace_from_context(ctx):
    return _namespace_from_query(ctx.query)

def _namespace_from_context_or_message(ctx, msg):
    namespace = _namespace_from_message(msg)
    if namespace != "":
        return namespace
    return _namespace_from_context(ctx)

def _namespace_key(namespace, suffix):
    if namespace == "":
        if suffix == "drawings":
            return DRAWINGS_KEY
        if suffix == "drawing_names":
            return DRAWING_NAMES_KEY
        return "pixeldraw:" + suffix
    return NAMESPACE_PREFIX + namespace + ":" + suffix

def _topic(namespace):
    if namespace == "":
        return TOPIC
    return TOPIC + "." + namespace

def _pixel_index(x, y):
    return y * WIDTH + x

def _drawing_key(namespace, drawing_id, suffix):
    if namespace == "":
        return DRAWING_PREFIX + drawing_id + ":" + suffix
    return NAMESPACE_PREFIX + namespace + ":drawing:" + drawing_id + ":" + suffix

def _pixels_key(namespace, drawing_id):
    return _drawing_key(namespace, drawing_id, "pixels")

def _revision_key(namespace, drawing_id):
    return _drawing_key(namespace, drawing_id, "revision")

def _revision_counter_key(namespace, drawing_id):
    return _drawing_key(namespace, drawing_id, "revision_counter")

def _valid_color_code(value):
    code = _as_int(value)
    if code >= 0 and code < COLOR_COUNT:
        return code
    if type(value) == "string":
        return COLOR_BY_ID.get(value, -1)
    return -1

def _pixel_index_key(index):
    return str(index)

def _read_pixels(namespace, drawing_id):
    pixels = memory.get(_pixels_key(namespace, drawing_id), {})
    if type(pixels) != "dict":
        return {}
    return pixels

def _write_pixels(namespace, drawing_id, pixels):
    if len(pixels) == 0:
        memory.delete(_pixels_key(namespace, drawing_id))
        return True
    return memory.set(_pixels_key(namespace, drawing_id), pixels)

def _drawings(namespace):
    drawings = memory.get(_namespace_key(namespace, "drawings"), [])
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

def _save_drawings(namespace, drawings):
    return memory.set(_namespace_key(namespace, "drawings"), drawings)

def _ensure_drawings(namespace):
    drawings = _drawings(namespace)
    if len(drawings) > 0:
        return drawings

    default_id = _new_drawing_id()
    drawings = [default_id]
    _save_drawings(namespace, drawings)
    log.info(message="pixeldraw default drawing created", namespace=namespace, drawing_id=default_id)
    return drawings

def _drawing_names(namespace):
    names = memory.get(_namespace_key(namespace, "drawing_names"), {})
    if type(names) != "dict":
        return {}
    drawings = _drawings(namespace)
    clean = {}
    dirty = False
    for drawing_id in names:
        name = names[drawing_id]
        if drawing_id not in drawings or type(name) != "string" or name == "":
            dirty = True
            continue
        if len(name) > MAX_DRAWING_NAME_LENGTH:
            name = name[:MAX_DRAWING_NAME_LENGTH]
            dirty = True
        clean[drawing_id] = name
    if len(clean) != len(names):
        dirty = True
    if dirty:
        if len(clean) == 0:
            memory.delete(_namespace_key(namespace, "drawing_names"))
        else:
            memory.set(_namespace_key(namespace, "drawing_names"), clean)
    return clean

def _drawing_tabs(namespace, drawings):
    names = _drawing_names(namespace)
    tabs = []
    for drawing_id in drawings:
        tabs.append({"id": drawing_id, "name": names.get(drawing_id, "")})
    return tabs

def _clean_drawing_name(value):
    if type(value) != "string":
        return ""
    out = ""
    for i in range(len(value)):
        ch = value[i]
        if ch == "\n" or ch == "\r" or ch == "\t":
            ch = " "
        out += ch
        if len(out) >= MAX_DRAWING_NAME_LENGTH:
            return out
    return out

def _save_drawing_name(namespace, drawing_id, name):
    names = _drawing_names(namespace)
    clean_name = _clean_drawing_name(name)
    if clean_name == "":
        names.pop(drawing_id, None)
    else:
        names[drawing_id] = clean_name
    if len(names) == 0:
        memory.delete(_namespace_key(namespace, "drawing_names"))
    else:
        memory.set(_namespace_key(namespace, "drawing_names"), names)
    return clean_name

def _snapshot_pixels(namespace, drawing_id):
    out = []
    pixels = _read_pixels(namespace, drawing_id)
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
        log.warn(message="pixeldraw normalized stored pixels", namespace=namespace, drawing_id=drawing_id, before=len(pixels), after=len(normalized))
        _write_pixels(namespace, drawing_id, normalized)
    return out

def _snapshot(namespace, drawing_id = "", missing_requested_id = ""):
    drawings = _ensure_drawings(namespace)
    requested_id = drawing_id
    if drawing_id == "" or drawing_id not in drawings:
        drawing_id = drawings[0]
        if requested_id != "" and missing_requested_id == "":
            missing_requested_id = requested_id
    pixels = _snapshot_pixels(namespace, drawing_id)
    log.debug("pixeldraw snapshot", drawing_id, namespace=namespace, requested=requested_id, drawings=len(drawings), pixels=len(pixels))
    return {
        "type": "canvas_snapshot",
        "width": WIDTH,
        "height": HEIGHT,
        "namespace": namespace,
        "drawing_id": drawing_id,
        "drawings": drawings,
        "drawing_tabs": _drawing_tabs(namespace, drawings),
        "missing_drawing_id": missing_requested_id,
        "revision": memory.get(_revision_counter_key(namespace, drawing_id), 0),
        "pixels": pixels,
    }

def _drawings_changed(namespace, active_id = ""):
    drawings = _ensure_drawings(namespace)
    return {
        "type": "drawings_changed",
        "namespace": namespace,
        "drawings": drawings,
        "drawing_tabs": _drawing_tabs(namespace, drawings),
        "active_drawing_id": active_id,
    }

def _error(message):
    return {"type": "error", "message": message}

def _valid_drawing_id(namespace, value):
    if type(value) != "string":
        return ""
    if value in _ensure_drawings(namespace):
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

def _create_drawing(namespace, name = ""):
    drawings = _ensure_drawings(namespace)
    drawing_id = _new_drawing_id()
    drawings.append(drawing_id)
    _save_drawings(namespace, drawings)
    _save_drawing_name(namespace, drawing_id, name)
    log.info(message="pixeldraw drawing created", namespace=namespace, drawing_id=drawing_id, total=len(drawings))
    return drawing_id

def _delete_drawing(namespace, drawing_id):
    drawings = _ensure_drawings(namespace)
    if drawing_id not in drawings:
        log.warn(message="pixeldraw delete ignored", namespace=namespace, drawing_id=drawing_id, reason="unknown drawing")
        return drawings[0]
    if len(drawings) == 1:
        log.info(message="pixeldraw delete ignored", namespace=namespace, drawing_id=drawing_id, reason="last drawing")
        return drawing_id

    remaining = []
    for candidate in drawings:
        if candidate != drawing_id:
            remaining.append(candidate)
    _save_drawings(namespace, remaining)
    _save_drawing_name(namespace, drawing_id, "")

    memory.delete(_pixels_key(namespace, drawing_id))
    memory.delete(_revision_key(namespace, drawing_id))
    memory.delete(_revision_counter_key(namespace, drawing_id))
    log.info(message="pixeldraw drawing deleted", namespace=namespace, drawing_id=drawing_id, next_id=remaining[0], total=len(remaining))
    return remaining[0]

def on_connect(ctx):
    namespace = _namespace_from_context(ctx)
    requested_id = _query_param(ctx.query, "tab")
    log.info(message="pixeldraw websocket connected", namespace=namespace, conn_id=ctx.conn_id)
    ws.subscribe(ctx.conn_id, _topic(namespace))
    ws.send(ctx.conn_id, {
        "type": "ready",
        "namespace": namespace,
        "conn_id": ctx.conn_id,
    })
    ws.send(ctx.conn_id, _snapshot(namespace, requested_id))

def on_message(ctx, msg):
    if type(msg) != "dict":
        log.warn(message="pixeldraw rejected websocket payload", conn_id=ctx.conn_id, got=type(msg))
        ws.send(ctx.conn_id, _error("expected a JSON object"))
        return

    msg_type = msg.get("type")
    namespace = _namespace_from_context_or_message(ctx, msg)
    log.debug("pixeldraw message", msg_type, namespace=namespace, conn_id=ctx.conn_id)
    if msg_type == "get_drawing" or msg_type == "select_drawing":
        requested_id = msg.get("drawing_id", "")
        drawing_id = _valid_drawing_id(namespace, requested_id)
        if drawing_id == "":
            log.warn(message="pixeldraw unknown drawing requested", namespace=namespace, conn_id=ctx.conn_id, requested=requested_id)
            if namespace != "":
                ws.send(ctx.conn_id, _snapshot(namespace, "", requested_id))
                return
            ws.send(ctx.conn_id, _error("unknown drawing"))
            return
        log.info(message="pixeldraw drawing selected", namespace=namespace, conn_id=ctx.conn_id, drawing_id=drawing_id)
        ws.send(ctx.conn_id, _snapshot(namespace, drawing_id))
        return

    if msg_type == "create_drawing":
        drawing_id = _create_drawing(namespace, msg.get("name", ""))
        log.info("pixeldraw create_drawing", drawing_id, namespace=namespace, conn_id=ctx.conn_id)
        events.publish(_topic(namespace), _drawings_changed(namespace, drawing_id))
        ws.send(ctx.conn_id, _snapshot(namespace, drawing_id))
        return

    if msg_type == "rename_drawing":
        drawing_id = _valid_drawing_id(namespace, msg.get("drawing_id", ""))
        if drawing_id == "":
            log.warn(message="pixeldraw rename rejected", namespace=namespace, conn_id=ctx.conn_id, requested=msg.get("drawing_id", ""))
            ws.send(ctx.conn_id, _error("unknown drawing"))
            return
        name = _save_drawing_name(namespace, drawing_id, msg.get("name", ""))
        log.info(message="pixeldraw drawing renamed", namespace=namespace, conn_id=ctx.conn_id, drawing_id=drawing_id, name=name)
        events.publish(_topic(namespace), _drawings_changed(namespace, drawing_id))
        return

    if msg_type == "delete_drawing":
        drawing_id = _valid_drawing_id(namespace, msg.get("drawing_id", ""))
        if drawing_id == "":
            log.warn(message="pixeldraw delete rejected", namespace=namespace, conn_id=ctx.conn_id, requested=msg.get("drawing_id", ""))
            ws.send(ctx.conn_id, _error("unknown drawing"))
            return
        next_id = _delete_drawing(namespace, drawing_id)
        log.info(message="pixeldraw delete_drawing", namespace=namespace, conn_id=ctx.conn_id, drawing_id=drawing_id, next_id=next_id)
        events.publish(_topic(namespace), _drawings_changed(namespace, next_id))
        ws.send(ctx.conn_id, _snapshot(namespace, next_id))
        return

    if msg_type != "draw_pixels":
        log.warn(message="pixeldraw unknown message type", conn_id=ctx.conn_id, msg_type=msg_type)
        ws.send(ctx.conn_id, _error("unknown message type"))
        return

    drawing_id = _valid_drawing_id(namespace, msg.get("drawing_id", ""))
    if drawing_id == "":
        drawing_id = _ensure_drawings(namespace)[0]
        log.debug(message="pixeldraw draw defaulted drawing", namespace=namespace, conn_id=ctx.conn_id, drawing_id=drawing_id)

    raw_pixels = msg.get("pixels", [])
    if type(raw_pixels) != "list":
        log.warn(message="pixeldraw rejected draw batch", namespace=namespace, conn_id=ctx.conn_id, drawing_id=drawing_id, reason="pixels must be a list")
        ws.send(ctx.conn_id, _error("pixels must be a list"))
        return

    changed = []
    seen = {}
    invalid = 0
    duplicates = 0
    unchanged = 0
    truncated = len(raw_pixels) > MAX_BATCH_PIXELS
    stored_pixels = _read_pixels(namespace, drawing_id)
    for raw in raw_pixels:
        if len(changed) >= MAX_BATCH_PIXELS:
            break

        pixel = _valid_pixel(raw)
        if pixel == None:
            invalid += 1
            continue

        seen_key = str(pixel["i"])
        if seen_key in seen:
            duplicates += 1
            continue
        seen[seen_key] = True

        previous = _valid_color_code(stored_pixels.get(seen_key, 0))
        if previous == pixel["color"]:
            unchanged += 1
            continue

        if pixel["color"] == 0:
            stored_pixels.pop(seen_key, None)
        else:
            stored_pixels[seen_key] = pixel["color"]
        changed.append(pixel)

    if len(changed) == 0:
        log.debug(
            message="pixeldraw draw batch no-op",
            conn_id=ctx.conn_id,
            drawing_id=drawing_id,
            received=len(raw_pixels),
            invalid=invalid,
            duplicates=duplicates,
            unchanged=unchanged,
        )
        return

    _write_pixels(namespace, drawing_id, stored_pixels)
    revision = memory.incr(_revision_counter_key(namespace, drawing_id), 1)
    log.info(
        message="pixeldraw pixels updated",
        conn_id=ctx.conn_id,
        namespace=namespace,
        drawing_id=drawing_id,
        revision=revision,
        changed=len(changed),
        received=len(raw_pixels),
        invalid=invalid,
        duplicates=duplicates,
        unchanged=unchanged,
        truncated=truncated,
    )
    events.publish(_topic(namespace), {
        "type": "pixels_updated",
        "namespace": namespace,
        "drawing_id": drawing_id,
        "revision": revision,
        "by": ctx.conn_id,
        "pixels": changed,
    })

def on_event(ctx, event):
    namespace = _namespace_from_context(ctx)
    if event.topic != _topic(namespace):
        log.debug(message="pixeldraw ignored event", conn_id=ctx.conn_id, topic=event.topic)
        return
    log.debug(message="pixeldraw forwarding event", conn_id=ctx.conn_id, topic=event.topic)
    ws.send(ctx.conn_id, event.payload)

def on_disconnect(ctx):
    log.info(message="pixeldraw websocket disconnected", conn_id=ctx.conn_id)
    ws.unsubscribe_all(ctx.conn_id)
