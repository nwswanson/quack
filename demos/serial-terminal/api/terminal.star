def _payload(body):
    if len(body) == 0:
        return {}
    data = json.decode(request.body_text(body), default = {})
    if type(data) != "dict":
        return {}
    return data

def _json(data):
    return (
        200,
        {"content-type": "application/json; charset=utf-8", "cache-control": "no-store"},
        json.encode_indent(data, indent = "  ") + "\n",
    )

def _err(status, msg):
    return (
        status,
        {"content-type": "application/json; charset=utf-8", "cache-control": "no-store"},
        json.encode({"error": msg}) + "\n",
    )

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

def _serial_response(resp):
    return {
        "id": resp.get("id", ""),
        "text": resp.get("text", ""),
        "base64": resp.get("base64", ""),
        "timeout": resp.get("timeout", False),
    }

def _events_after(before, after):
    cutoff = ""
    for event in before.get("recent", []):
        at = event.get("at", "")
        if at > cutoff:
            cutoff = at

    events = []
    for event in after.get("recent", []):
        if event.get("at", "") > cutoff:
            events.append(event)
    return events

def _devices():
    devices = []
    for device in serial.list():
        devices.append(_device_summary(device))
    return _json({"devices": devices})

def _status(alias):
    if not alias:
        return _err(400, "device alias required")
    return _json({"status": _serial_status(serial.status(alias))})

def _open(alias):
    if not alias:
        return _err(400, "device alias required")
    opened = serial.open(alias)
    status = _serial_status(serial.status(alias))
    return _json({"open": opened, "status": status})

def _close(alias):
    if not alias:
        return _err(400, "device alias required")
    closed = serial.close(alias)
    status = _serial_status(serial.status(alias))
    return _json({"close": closed, "status": status})

def _command(data):
    alias = data.get("device", "")
    text = data.get("text", "")
    if not alias:
        return _err(400, "device alias required")
    if type(text) != "string":
        return _err(400, "text must be a string")

    line_ending = data.get("line_ending", "\n")
    if line_ending == "crlf":
        suffix = "\r\n"
    elif line_ending == "cr":
        suffix = "\r"
    elif line_ending == "none":
        suffix = ""
    else:
        suffix = "\n"

    timeout_ms = data.get("timeout_ms", 1000)
    if type(timeout_ms) != "int":
        timeout_ms = 1000

    max_bytes = data.get("max_bytes", 4096)
    if type(max_bytes) != "int":
        max_bytes = 4096

    until = data.get("until", "\n")
    if type(until) != "string":
        until = "\n"

    before = _serial_status(serial.status(alias))
    resp = serial.request(
        alias,
        text + suffix,
        until = until,
        timeout_ms = timeout_ms,
        max_bytes = max_bytes,
    )
    status = _serial_status(serial.status(alias))
    return _json({
        "response": _serial_response(resp),
        "events": _events_after(before, status),
        "status": status,
    })

def handle(req):
    method, path, query, headers, body = req
    parts = [p for p in path.strip("/").split("/") if p != ""]

    if method == "GET" and len(parts) == 0:
        return _devices()

    if method == "GET" and len(parts) == 2 and parts[0] == "status":
        return _status(parts[1])

    if method != "POST":
        return _err(404, "not found")

    data = _payload(body)
    if len(parts) == 1 and parts[0] == "open":
        return _open(data.get("device", ""))
    if len(parts) == 1 and parts[0] == "close":
        return _close(data.get("device", ""))
    if len(parts) == 1 and parts[0] == "command":
        return _command(data)

    return _err(404, "not found")
