TOPIC = "serial-terminal:session"
STATE_KEY = "serial-terminal:state"
TERMINAL_KEY = "serial-terminal:terminal"
DEBUG_KEY = "serial-terminal:debug"
MAX_TERMINAL_LINES = 500
MAX_DEBUG_LINES = 240

def _as_int(value, default):
    if type(value) == "int":
        return value
    if type(value) == "float":
        return int(value)
    if type(value) == "string":
        if value == "":
            return default
        digits = "0123456789"
        for i in range(len(value)):
            if value[i] not in digits:
                return default
        return int(value)
    return default

def _device_summary(device):
    return {
        "id": device.get("id", ""),
        "alias": device.get("alias", device.get("id", "")),
        "kind": device.get("kind", "serial"),
        "label": device.get("label", ""),
        "permissions": device.get("permissions", {}),
    }

def _serial_event(event):
    return {
        "at": event.get("at", ""),
        "type": event.get("type", ""),
        "text": event.get("text", ""),
        "base64": event.get("base64", ""),
        "error": event.get("error", ""),
    }

def _serial_status(status):
    recent = []
    for event in status.get("recent", []):
        recent.append(_serial_event(event))
    return {
        "id": status.get("id", ""),
        "open": status.get("open", False),
        "status": status.get("status", ""),
        "error": status.get("error", ""),
        "recent": recent,
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
        "last_event_at": "",
        "settings": {
            "line_ending": "lf",
            "until": "\n",
            "timeout_ms": 1000,
        },
    }

def _state():
    state = memory.get(STATE_KEY, None)
    if type(state) != "dict":
        state = _default_state()
        memory.set(STATE_KEY, state)
    if "settings" not in state or type(state["settings"]) != "dict":
        state["settings"] = _default_state()["settings"]
    if "devices" not in state or type(state["devices"]) != "list":
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
    state = _state()
    out = {
        "type": "snapshot",
        "state": state,
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
    until = raw.get("until", current.get("until", "\n"))
    if type(until) != "string":
        until = "\n"
    timeout_ms = _as_int(raw.get("timeout_ms", current.get("timeout_ms", 1000)), 1000)
    if timeout_ms < 0:
        timeout_ms = 0
    if timeout_ms > 30000:
        timeout_ms = 30000
    return {
        "line_ending": line_ending,
        "until": until,
        "timeout_ms": timeout_ms,
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

def _events_after(status, cutoff):
    events = []
    last = cutoff
    for event in status.get("recent", []):
        at = event.get("at", "")
        if at != "" and at > cutoff:
            events.append(event)
            if at > last:
                last = at
    return events, last

def _last_event_at(status):
    last = ""
    for event in status.get("recent", []):
        at = event.get("at", "")
        if at > last:
            last = at
    return last

def _drain_status(alias):
    state = _state()
    was_connected = state.get("connected", False)
    old_status = state.get("status", "")
    old_error = state.get("error", "")
    status = _serial_status(serial.status(alias))
    state["connected"] = status.get("open", False)
    state["locked"] = status.get("open", False)
    state["status"] = status.get("status", "")
    state["error"] = status.get("error", "")

    events, last = _events_after(status, state.get("last_event_at", ""))
    state["last_event_at"] = last
    _save_state(state)

    effects = []
    if was_connected != state.get("connected", False) or old_status != state.get("status", "") or old_error != state.get("error", ""):
        effects.append(_state_changed())
    if status.get("error", "") != "":
        effects += _append_terminal_and_publish(_terminal("error", "! " + status.get("error", "")))

    for event in events:
        effects += _append_debug_and_publish("serial_event", event)
        if event.get("type", "") == "read" and event.get("text", "") != "":
            effects += _append_terminal_and_publish(_terminal("read", event.get("text", "")))
        elif event.get("type", "") == "error" and event.get("error", "") != "":
            effects += _append_terminal_and_publish(_terminal("error", "! " + event.get("error", "")))
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
        before = _serial_status(serial.status(alias))
        serial.open(alias)
        state = _state()
        state["selected"] = alias
        state["connected"] = True
        state["locked"] = True
        state["status"] = "open"
        state["error"] = ""
        state["last_event_at"] = _last_event_at(before)
        _save_state(state)
        effects = [_state_changed()]
        effects += _append_debug_and_publish("open", {"device": alias, "by": ctx.conn_id})
        effects += _append_terminal_and_publish(_terminal("system", "connected to " + alias, ctx.conn_id))
        effects += _drain_status(alias)
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
        effects += _drain_status(alias)
        return effects

    if msg_type == "poll":
        alias = state.get("selected", "")
        if alias == "" or not state.get("connected", False):
            return []
        return _drain_status(alias)

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

def on_disconnect(ctx):
    return ws.unsubscribe_all(ctx.conn_id)
