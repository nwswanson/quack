TOTAL_KEY = "timer-throttle-bench:total"
PUSH_MS_KEY = "timer-throttle-bench:push_ms"
WINDOW_MS_KEY = "timer-throttle-bench:window_ms"
DEFAULT_PUSH_MS = 25
DEFAULT_WINDOW_MS = 1000

def _int(value, fallback):
    if type(value) == "int":
        return value
    return fallback

def _clamp(value, low, high):
    if value < low:
        return low
    if value > high:
        return high
    return value

def _settings_from_query(query):
    push_ms = _clamp(_int(memory.get(PUSH_MS_KEY, DEFAULT_PUSH_MS), DEFAULT_PUSH_MS), 1, 1000)
    window_ms = _clamp(_int(memory.get(WINDOW_MS_KEY, DEFAULT_WINDOW_MS), DEFAULT_WINDOW_MS), 100, 10000)
    memory.set(PUSH_MS_KEY, push_ms)
    memory.set(WINDOW_MS_KEY, window_ms)
    return push_ms, window_ms

def handle(req):
    method, path, query, headers, body = req
    total = memory.incr(TOTAL_KEY, 1)
    push_ms, window_ms = _settings_from_query(query)
    html = """<!doctype html>
<html>
<head><title>Quack timer bench</title></head>
<body>
<h1>ok</h1>
<p>total=%d</p>
<p>push_ms=%d window_ms=%d</p>
</body>
</html>
""" % (total, push_ms, window_ms)
    return (
        200,
        {
            "content-type": "text/html; charset=utf-8",
            "cache-control": "no-store",
            "x-quack-demo": "timer-throttle-bench",
        },
        html,
    )
