def _payload(body):
    if len(body) == 0:
        return {}
    data = json.decode(request.body_text(body), default = {})
    if type(data) != "dict":
        return {}
    return data

def _param(data, name, default = ""):
    return data.get(name, default)

def _json(data):
    return (
        200,
        {"content-type": "application/json; charset=utf-8", "cache-control": "no-store"},
        json.encode_indent(data, indent = "  ") + "\n",
    )

def _state():
    return {
        "usage": memory.usage(),
        "quota": memory.quota(),
        "keys": memory.keys(),
        "items": memory.items(),
    }

def handle(req):
    method, path, query, headers, body = req
    data = _payload(body)
    op = _param(data, "op", "state")

    if op == "clear":
        cleared = memory.clear()
        state = _state()
        state["cleared"] = cleared
        return _json(state)

    return _json(_state())
