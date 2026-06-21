def _payload(body):
    if len(body) == 0:
        return {}
    data = json.decode(request.body_text(body), default = {})
    if type(data) != "dict":
        return {}
    return data

def _param(data, name, default = ""):
    return data.get(name, default)

def _score(data):
    raw = _param(data, "score", "1")
    if raw == "0" or raw == 0:
        return 0.0
    if raw == "1" or raw == 1:
        return 1.0
    if raw == "2" or raw == 2:
        return 2.0
    if raw == "3" or raw == 3:
        return 3.0
    if raw == "4" or raw == 4:
        return 4.0
    if raw == "5" or raw == 5:
        return 5.0
    return 1.0

def _json(data):
    data["usage"] = memory.usage()
    data["quota"] = memory.quota()
    data["type"] = memory.type("zset:leaderboard")
    return (
        200,
        {"content-type": "application/json; charset=utf-8", "cache-control": "no-store"},
        json.encode_indent(data, indent = "  ") + "\n",
    )

def _state(extra = {}):
    data = {
        "ranked": memory.zrange("zset:leaderboard", with_scores = True),
        "top_three": memory.zrange("zset:leaderboard", 0, 2, with_scores = True),
    }
    data.update(extra)
    return data

def handle(req):
    method, path, query, headers, body = req
    data = _payload(body)
    op = _param(data, "op", "state")
    value = _param(data, "value", "Ada")

    if op == "add":
        return _json(_state({"value": value, "score": _score(data), "added": memory.zadd("zset:leaderboard", _score(data), value)}))

    if op == "remove":
        return _json(_state({"value": value, "removed": memory.zremove("zset:leaderboard", value)}))

    if op == "score":
        return _json(_state({"value": value, "score": memory.zscore("zset:leaderboard", value)}))

    if op == "delete":
        return _json(_state({"deleted": memory.delete("zset:leaderboard")}))

    return _json(_state())
