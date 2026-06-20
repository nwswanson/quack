def _header(headers, key):
    values = headers.get(key.lower())
    if not values:
        return ""
    return values[0]

def handle(req):
    method, path, query, headers, body = req

    response = json.encode_indent({
        "ok": True,
        "runtime": "starlark",
        "method": method,
        "path": path,
        "query": query,
        "body_size": len(body),
        "user_agent": _header(headers, "user-agent"),
    }, indent = "  ") + "\n"

    return (
        200,
        {
            "content-type": "application/json; charset=utf-8",
            "x-quack-runtime": "starlark",
        },
        response,
    )
