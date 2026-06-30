TOTAL_KEY = "timer-throttle-bench:total"
BENCH_HTML = """<!doctype html>
<html>
<head><title>Quack timer bench</title></head>
<body>
<h1>ok</h1>
</body>
</html>
"""

def handle(req):
    method, path, query, headers, body = req
    memory.incr(TOTAL_KEY, 1)
    return (
        200,
        {
            "content-type": "text/html; charset=utf-8",
            "cache-control": "no-store",
            "x-quack-demo": "timer-throttle-bench",
        },
        BENCH_HTML,
    )
