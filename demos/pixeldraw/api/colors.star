COLORS = [
    {"id": "white", "code": 0, "hex": "#ffffff"},
    {"id": "light_gray", "code": 1, "hex": "#c7ccd1"},
    {"id": "gray", "code": 2, "hex": "#7f858d"},
    {"id": "black", "code": 3, "hex": "#16191d"},
    {"id": "maroon", "code": 4, "hex": "#8f1d2c"},
    {"id": "red", "code": 5, "hex": "#e13a32"},
    {"id": "orange", "code": 6, "hex": "#f07f24"},
    {"id": "yellow", "code": 7, "hex": "#ffd447"},
    {"id": "olive", "code": 8, "hex": "#8f9738"},
    {"id": "green", "code": 9, "hex": "#239a57"},
    {"id": "lime", "code": 10, "hex": "#5ac85a"},
    {"id": "teal", "code": 11, "hex": "#1e8f8f"},
    {"id": "cyan", "code": 12, "hex": "#48c7d8"},
    {"id": "blue", "code": 13, "hex": "#2469d8"},
    {"id": "navy", "code": 14, "hex": "#173a8f"},
    {"id": "purple", "code": 15, "hex": "#7246b8"},
    {"id": "magenta", "code": 16, "hex": "#cf4eb8"},
    {"id": "pink", "code": 17, "hex": "#f58db2"},
    {"id": "peach", "code": 18, "hex": "#f5a15d"},
    {"id": "tan", "code": 19, "hex": "#d7b37a"},
    {"id": "brown", "code": 20, "hex": "#8a5938"},
    {"id": "cream", "code": 21, "hex": "#fff2b5"},
    {"id": "mint", "code": 22, "hex": "#a9e6b0"},
    {"id": "sky", "code": 23, "hex": "#8ec9ff"},
]

def _json(data, status = 200):
    return (
        status,
        {"content-type": "application/json; charset=utf-8", "cache-control": "no-store"},
        json.encode_indent(data, indent = "  ") + "\n",
    )

def handle(req):
    method, path, query, headers, body = req
    if method != "GET":
        return _json({"error": "method not allowed"}, 405)
    return _json({"colors": COLORS})
