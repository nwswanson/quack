calculator = wasm.module("calculator")

def handle(req):
    result = calculator.add({"left": 20, "right": 22})

    body = json.encode_indent({
        "ok": True,
        "source": "wasm",
        "operation": "20 + 22",
        "result": result,
        "exports": calculator.exports(),
    }, indent = "  ") + "\n"

    return (
        200,
        {"content-type": "application/json; charset=utf-8"},
        body,
    )
