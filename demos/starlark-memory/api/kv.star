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
    data["usage"] = memory.usage()
    data["quota"] = memory.quota()
    data["type"] = memory.type("kv:message")
    return (
        200,
        {"content-type": "application/json; charset=utf-8", "cache-control": "no-store"},
        json.encode_indent(data, indent = "  ") + "\n",
    )

def _value():
    return memory.get("kv:message", None)

def handle(req):
    method, path, query, headers, body = req
    data = _payload(body)
    op = _param(data, "op", "state")

    if op == "set":
        value = _param(data, "value", "hello from Starlark memory")
        ok = memory.set("kv:message", value)
        return _json({"ok": ok, "value": _value(), "keys": memory.keys(), "items": memory.items()})

    if op == "set_object":
        ok = memory.set("kv:message", {
            "text": _param(data, "value", "structured value"),
            "blob_label": "QUACK",
            "nested": [None, 0, True],
        })
        return _json({"ok": ok, "value": _value(), "keys": memory.keys(), "items": memory.items()})

    if op == "delete":
        deleted = memory.delete("kv:message")
        return _json({"deleted": deleted, "value": _value(), "keys": memory.keys(), "items": memory.items()})

    return _json({"value": _value(), "keys": memory.keys(), "items": memory.items()})
