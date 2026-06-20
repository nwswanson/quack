def _json_escape(value):
    text = str(value)
    text = text.replace("\\", "\\\\")
    text = text.replace('"', '\\"')
    text = text.replace("\n", "\\n")
    return text

def _header(headers, key):
    values = headers.get(key.lower())
    if not values:
        return ""
    return values[0]

def handle(req):
    method, path, query, headers, body = req
    body_size = len(body)
    user_agent = _header(headers, "user-agent")

    response = """{
  "ok": true,
  "runtime": "starlark",
  "method": "%s",
  "path": "%s",
  "query": "%s",
  "body_size": %d,
  "user_agent": "%s"
}
""" % (
        _json_escape(method),
        _json_escape(path),
        _json_escape(query),
        body_size,
        _json_escape(user_agent),
    )

    return (
        200,
        {
            "content-type": "application/json; charset=utf-8",
            "x-quack-runtime": "starlark",
        },
        response,
    )
