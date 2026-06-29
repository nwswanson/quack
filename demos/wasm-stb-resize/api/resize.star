images = wasm.module("images")

def parse_size(query):
    width = 320
    height = 0
    parts = query.split("&") if query else []
    for part in parts:
        if part.startswith("w="):
            width = int(part[2:])
        elif part.startswith("h="):
            height = int(part[2:])
    return width, height

def handle(req):
    method, path, query, headers, body = req
    if method != "POST":
        return (405, {"content-type": "application/json"}, json.encode({
            "ok": False,
            "error": "POST an image body to resize it",
        }))

    width, height = parse_size(query)
    result = images.resize_image({
        "input": body,
        "width": width,
        "height": height,
    })

    if not result.get("ok", False):
        return (422, {"content-type": "application/json"}, json.encode(result))

    return (200, {"content-type": "application/json"}, json.encode(result))
