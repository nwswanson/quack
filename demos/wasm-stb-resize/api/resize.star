images = wasm.module("images")

def parse_options(query):
    width = 320
    height = 0
    format = "jpg"
    quality = 90
    parts = query.split("&") if query else []
    for part in parts:
        if part.startswith("w="):
            width = int(part[2:])
        elif part.startswith("h="):
            height = int(part[2:])
        elif part.startswith("format="):
            format = part[7:]
        elif part.startswith("quality="):
            quality = int(part[8:])
    return width, height, format, quality

def handle(req):
    method, path, query, headers, body = req
    if method != "POST":
        return (405, {"content-type": "application/json"}, json.encode({
            "ok": False,
            "error": "POST an image body to resize it",
        }))

    width, height, format, quality = parse_options(query)
    result = images.resize_image({
        "input": body,
        "format": format,
        "width": width,
        "height": height,
        "quality": quality,
    })

    if not result.get("ok", False):
        return (422, {"content-type": "application/json"}, json.encode(result))

    return (200, {"content-type": "application/json"}, json.encode(result))
