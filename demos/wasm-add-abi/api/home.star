calculator = wasm.module("calculator")

def handle(req):
    left = 20
    right = 22
    result = calculator.add({"left": left, "right": right})

    return (200, {"content-type": "application/json"}, json.encode({
        "abi": "quack:wasm-v1",
        "left": left,
        "right": right,
        "result": result,
        "exports": calculator.exports(),
    }))
