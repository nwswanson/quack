TOPIC = "serial-terminal-pipes.session"
STATE_KEY = "serial-terminal-pipes.state"
TERMINAL_KEY = "serial-terminal-pipes.terminal"
DEBUG_KEY = "serial-terminal-pipes.debug"
MAX_TERMINAL_LINES = 500
MAX_DEBUG_LINES = 240

def _device_summary(device):
    return {
        "id": device.get("id", ""),
        "alias": device.get("alias", device.get("id", "")),
        "kind": device.get("kind", "serial"),
        "label": device.get("label", ""),
        "permissions": device.get("permissions", {}),
    }

def _devices():
    devices = []
    for device in serial.list():
        devices.append(_device_summary(device))
    return devices

def _default_state():
    return {
        "devices": [],
        "selected": "",
        "connected": False,
        "locked": False,
        "status": "idle",
        "error": "",
        "last_error": "",
        "settings": {
            "line_ending": "lf",
        },
    }

def _state():
    state = memory.get(STATE_KEY, None)
    if type(state) != "dict":
        state = _default_state()
        memory.set(STATE_KEY, state)
    defaults = _default_state()
    for key in defaults:
        if key not in state:
            state[key] = defaults[key]
    if type(state.get("settings", None)) != "dict":
        state["settings"] = defaults["settings"]
    if type(state.get("devices", None)) != "list":
        state["devices"] = []
    return state

def _save_state(state):
    memory.set(STATE_KEY, state)
    return state

def _lines(key):
    lines = memory.get(key, [])
    if type(lines) != "list":
        return []
    return lines

def _append_line(key, item, limit):
    lines = _lines(key)
    lines.append(item)
    if len(lines) > limit:
        lines = lines[len(lines) - limit:]
    memory.set(key, lines)
    return lines

def _terminal(kind, text, by = ""):
    if type(text) != "string" or text == "":
        return None
    return {
        "at": "",
        "kind": kind,
        "text": text,
        "by": by,
    }

def _debug(kind, payload):
    return {
        "at": "",
        "kind": kind,
        "payload": payload,
    }

def _remember_debug(kind, payload):
    item = _debug(kind, payload)
    _append_line(DEBUG_KEY, item, MAX_DEBUG_LINES)
    return item

def _snapshot(extra = {}):
    out = {
        "type": "snapshot",
        "state": _state(),
        "terminal": _lines(TERMINAL_KEY),
        "debug": _lines(DEBUG_KEY),
    }
    for key in extra:
        out[key] = extra[key]
    return out

def _publish(message):
    return events.publish(TOPIC, message)

def _state_changed():
    return _publish({
        "type": "state",
        "state": _state(),
    })

def _line_ending(value):
    if value == "crlf":
        return "\r\n"
    if value == "cr":
        return "\r"
    if value == "none":
        return ""
    return "\n"

def _clean_settings(raw):
    current = _state()["settings"]
    if type(raw) != "dict":
        raw = {}
    line_ending = raw.get("line_ending", current.get("line_ending", "lf"))
    if line_ending not in ["lf", "crlf", "cr", "none"]:
        line_ending = "lf"
    return {
        "line_ending": line_ending,
    }

def _refresh_devices():
    state = _state()
    devices = _devices()
    selected = state.get("selected", "")
    valid = False
    for device in devices:
        alias = device.get("alias", device.get("id", ""))
        if alias == selected:
            valid = True
            break
    if not valid:
        selected = ""
        if len(devices) > 0:
            selected = devices[0].get("alias", devices[0].get("id", ""))
    state["devices"] = devices
    state["selected"] = selected
    if not state.get("connected", False):
        state["status"] = "ready" if len(devices) > 0 else "no devices"
    _save_state(state)
    _remember_debug("devices", {"count": len(devices), "selected": selected})
    return _state_changed()

def _append_terminal_and_publish(item):
    if item == None:
        return []
    _append_line(TERMINAL_KEY, item, MAX_TERMINAL_LINES)
    return [_publish({"type": "terminal", "line": item})]

def _append_debug_and_publish(kind, payload):
    item = _remember_debug(kind, payload)
    return [_publish({"type": "debug", "entry": item})]

def _topic_parts(topic):
    parts = topic.split(".")
    if len(parts) < 4:
        return "", ""
    return parts[2], parts[3]

def _serial_text(payload):
    if payload == None:
        return ""
    if type(payload) == "string":
        return payload
    return str(payload)

def _apply_serial_event(topic, payload):
    alias, kind = _topic_parts(topic)
    state = _state()
    if alias == "" or alias != state.get("selected", ""):
        return []

    effects = []
    effects += _append_debug_and_publish("serial_event", {
        "topic": topic,
        "device": alias,
        "kind": kind,
        "payload": payload,
    })

    if kind == "opened":
        state["connected"] = True
        state["locked"] = True
        state["status"] = "open"
        state["error"] = ""
        _save_state(state)
        effects.append(_state_changed())
        return effects

    if kind == "closed":
        state["connected"] = False
        state["locked"] = False
        state["status"] = "closed"
        state["error"] = ""
        _save_state(state)
        effects.append(_state_changed())
        return effects

    if kind == "read":
        return effects + _append_terminal_and_publish(_terminal("read", _serial_text(payload)))

    if kind == "read_error" or kind == "write_error" or kind == "disconnected":
        text = _serial_text(payload)
        if text == "":
            text = kind
        state["connected"] = False
        state["locked"] = False
        state["status"] = "error"
        state["error"] = text
        state["last_error"] = text
        _save_state(state)
        effects.append(_state_changed())
        effects += _append_terminal_and_publish(_terminal("error", "! " + text))
        return effects

    if kind == "overflow":
        effects += _append_terminal_and_publish(_terminal("error", "! serial event overflow"))
        return effects

    return effects

def on_connect(ctx):
    state = _state()
    effects = [
        ws.subscribe(ctx.conn_id, TOPIC),
        ws.send(ctx.conn_id, {"type": "ready", "conn_id": ctx.conn_id}),
    ]
    if len(state.get("devices", [])) == 0 and not state.get("locked", False):
        effects.append(_refresh_devices())
    effects.append(ws.send(ctx.conn_id, _snapshot()))
    return effects

def on_message(ctx, msg):
    if type(msg) != "dict":
        return ws.send(ctx.conn_id, {"type": "error", "message": "expected a JSON object"})

    msg_type = msg.get("type", "")
    state = _state()

    if msg_type == "refresh":
        if state.get("locked", False):
            return ws.send(ctx.conn_id, {"type": "error", "message": "session is locked"})
        return _refresh_devices()

    if msg_type == "select":
        if state.get("locked", False):
            return ws.send(ctx.conn_id, {"type": "error", "message": "session is locked"})
        alias = msg.get("device", "")
        valid = False
        for device in state.get("devices", []):
            if alias == device.get("alias", device.get("id", "")):
                valid = True
                break
        if not valid:
            return ws.send(ctx.conn_id, {"type": "error", "message": "unknown device"})
        state["selected"] = alias
        _save_state(state)
        return [_state_changed()] + _append_debug_and_publish("select", {"device": alias, "by": ctx.conn_id})

    if msg_type == "settings":
        if state.get("locked", False):
            return ws.send(ctx.conn_id, {"type": "error", "message": "session is locked"})
        state["settings"] = _clean_settings(msg.get("settings", {}))
        _save_state(state)
        return [_state_changed()] + _append_debug_and_publish("settings", {"settings": state["settings"], "by": ctx.conn_id})

    if msg_type == "open":
        alias = state.get("selected", "")
        if alias == "":
            return ws.send(ctx.conn_id, {"type": "error", "message": "select a device first"})
        serial.open(alias)
        state = _state()
        state["selected"] = alias
        state["connected"] = True
        state["locked"] = True
        state["status"] = "open"
        state["error"] = ""
        state["last_error"] = ""
        _save_state(state)
        effects = [_state_changed()]
        effects += _append_debug_and_publish("open", {"device": alias, "by": ctx.conn_id})
        effects += _append_terminal_and_publish(_terminal("system", "connected to " + alias, ctx.conn_id))
        return effects

    if msg_type == "close":
        alias = state.get("selected", "")
        if alias == "":
            return ws.send(ctx.conn_id, {"type": "error", "message": "no device selected"})
        serial.close(alias)
        state = _state()
        state["connected"] = False
        state["locked"] = False
        state["status"] = "closed"
        state["error"] = ""
        state["last_error"] = ""
        _save_state(state)
        effects = [_state_changed()]
        effects += _append_debug_and_publish("close", {"device": alias, "by": ctx.conn_id})
        effects += _append_terminal_and_publish(_terminal("system", "closed " + alias, ctx.conn_id))
        return effects

    if msg_type == "write":
        alias = state.get("selected", "")
        if alias == "" or not state.get("connected", False):
            return ws.send(ctx.conn_id, {"type": "error", "message": "not connected"})
        text = msg.get("text", "")
        if type(text) != "string" or text == "":
            return []
        settings = _clean_settings(state.get("settings", {}))
        data = text + _line_ending(settings.get("line_ending", "lf"))
        serial.write(alias, data)
        effects = _append_debug_and_publish("write", {"device": alias, "text": text, "by": ctx.conn_id})
        effects += _append_terminal_and_publish(_terminal("input", "> " + text, ctx.conn_id))
        return effects

    if msg_type == "clear_terminal":
        memory.set(TERMINAL_KEY, [])
        return _publish({"type": "clear_terminal"})

    if msg_type == "clear_debug":
        memory.set(DEBUG_KEY, [])
        return _publish({"type": "clear_debug"})

    if msg_type == "snapshot":
        return ws.send(ctx.conn_id, _snapshot())

    return ws.send(ctx.conn_id, {"type": "error", "message": "unknown message type"})

def on_event(ctx, event):
    return ws.send(ctx.conn_id, event.payload)

def on_hardware_event(ctx, event):
    return _apply_serial_event(event.topic, event.payload)

def on_disconnect(ctx):
    return ws.unsubscribe_all(ctx.conn_id)
