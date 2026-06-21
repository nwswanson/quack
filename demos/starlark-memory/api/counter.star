def _payload(body):
    if len(body) == 0:
        return {}
    data = json.decode(request.body_text(body), default = {})
    if type(data) != "dict":
        return {}
    return data

def _param(data, name, default = ""):
    return data.get(name, default)

def _delta(data):
    raw = _param(data, "delta", "1")
    if raw == "5" or raw == 5:
        return 5
    if raw == "10" or raw == 10:
        return 10
    if raw == "25" or raw == 25:
        return 25
    return 1

def _json(data):
    data["usage"] = memory.usage()
    data["quota"] = memory.quota()
    data["type"] = memory.type("counter:hits")
    return (
        200,
        {"content-type": "application/json; charset=utf-8", "cache-control": "no-store"},
        json.encode_indent(data, indent = "  ") + "\n",
    )

def _state(extra = {}):
    data = {"value": memory.get("counter:hits", 0)}
    data.update(extra)
    return data

def handle(req):
    method, path, query, headers, body = req
    data = _payload(body)
    op = _param(data, "op", "state")
    delta = _delta(data)

    if op == "incr":
        return _json(_state({"delta": delta, "value": memory.incr("counter:hits", delta)}))

    if op == "decr":
        return _json(_state({"delta": delta, "value": memory.decr("counter:hits", delta)}))

    if op == "delete":
        return _json(_state({"deleted": memory.delete("counter:hits")}))

    return _json(_state())
