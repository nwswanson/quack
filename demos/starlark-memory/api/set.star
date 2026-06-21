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
    data["type"] = memory.type("set:tags")
    return (
        200,
        {"content-type": "application/json; charset=utf-8", "cache-control": "no-store"},
        json.encode_indent(data, indent = "  ") + "\n",
    )

def _state(extra = {}):
    data = {
        "members": memory.set_members("set:tags"),
        "contains_demo": memory.set_contains("set:tags", "demo"),
    }
    data.update(extra)
    return data

def handle(req):
    method, path, query, headers, body = req
    data = _payload(body)
    op = _param(data, "op", "state")
    value = _param(data, "value", "demo")

    if op == "add":
        return _json(_state({"value": value, "added": memory.set_add("set:tags", value)}))

    if op == "remove":
        return _json(_state({"value": value, "removed": memory.set_remove("set:tags", value)}))

    if op == "contains":
        return _json(_state({"value": value, "contains_value": memory.set_contains("set:tags", value)}))

    if op == "delete":
        return _json(_state({"deleted": memory.delete("set:tags")}))

    return _json(_state())
