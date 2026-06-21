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
    data["type"] = memory.type("list:events")
    return (
        200,
        {"content-type": "application/json; charset=utf-8", "cache-control": "no-store"},
        json.encode_indent(data, indent = "  ") + "\n",
    )

def _state(extra = {}):
    data = {
        "length": memory.list_len("list:events"),
        "items": memory.list_range("list:events"),
        "first_three": memory.list_range("list:events", 0, 2),
        "last_two": memory.list_range("list:events", -2, -1),
    }
    data.update(extra)
    return data

def handle(req):
    method, path, query, headers, body = req
    data = _payload(body)
    op = _param(data, "op", "state")
    value = _param(data, "value", "event")

    if op == "push_left":
        length = memory.list_push("list:events", value, side = "left")
        return _json(_state({"pushed": value, "length_after_push": length}))

    if op == "push_right":
        length = memory.list_push("list:events", value, side = "right")
        return _json(_state({"pushed": value, "length_after_push": length}))

    if op == "pop_left":
        return _json(_state({"popped": memory.list_pop("list:events", side = "left")}))

    if op == "pop_right":
        return _json(_state({"popped": memory.list_pop("list:events", side = "right")}))

    if op == "delete":
        return _json(_state({"deleted": memory.delete("list:events")}))

    return _json(_state())
