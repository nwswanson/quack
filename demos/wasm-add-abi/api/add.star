math = wasm.module("calculator")

def handle(req):
    result = math.add({
        "left": 2,
        "right": 40,
    })

    return (200, {"content-type": "application/json"}, json.encode({
        "abi": "quack:wasm-v1",
        "result": result,
    }))
