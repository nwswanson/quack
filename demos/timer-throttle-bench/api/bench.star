STATS_TOPIC = "__timer_bench.stats"
TICK_TOPIC = "__timer_bench.tick"
TIMER_KEY = "timer-throttle-bench:pump"
TOTAL_KEY = "timer-throttle-bench:total"
LAST_TOTAL_KEY = "timer-throttle-bench:last_total"
SAMPLES_KEY = "timer-throttle-bench:samples"
WINDOW_MS_KEY = "timer-throttle-bench:window_ms"
PUSH_MS_KEY = "timer-throttle-bench:push_ms"
DEFAULT_PUSH_MS = 25
DEFAULT_WINDOW_MS = 1000
MAX_SAMPLES = 240

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

def _samples():
    samples = memory.get(SAMPLES_KEY, [])
    if type(samples) != "list":
        samples = []
    return samples

def _window_stats(samples, window_ms):
    elapsed = 0
    hits = 0
    for offset in range(len(samples)):
        if elapsed >= window_ms:
            break
        sample = samples[len(samples) - 1 - offset]
        if type(sample) == "dict":
            elapsed += _int(sample.get("elapsed_ms", 0), 0)
            hits += _int(sample.get("hits", 0), 0)
    rps = 0
    if elapsed > 0:
        rps = (hits * 1000) // elapsed
    return {
        "elapsed_ms": elapsed,
        "hits": hits,
        "rps": rps,
    }

def _snapshot(extra = {}):
    samples = _samples()
    push_ms = memory.get(PUSH_MS_KEY, DEFAULT_PUSH_MS)
    window_ms = memory.get(WINDOW_MS_KEY, DEFAULT_WINDOW_MS)
    window = _window_stats(samples, window_ms)
    last = {}
    if len(samples) > 0 and type(samples[-1]) == "dict":
        last = samples[-1]
    out = {
        "type": "stats",
        "topic": STATS_TOPIC,
        "total": memory.get(TOTAL_KEY, 0),
        "push_ms": push_ms,
        "window_ms": window_ms,
        "window": window,
        "last": last,
        "sample_count": len(samples),
    }
    for key in extra:
        out[key] = extra[key]
    return out

def _schedule(push_ms, window_ms):
    return timers.after(
        ms = push_ms,
        key = TIMER_KEY,
        mode = "keep_existing",
        topic = TICK_TOPIC,
        payload = {
            "push_ms": push_ms,
            "window_ms": window_ms,
        },
    )

def _record_sample(push_ms, window_ms):
    total = memory.get(TOTAL_KEY, 0)
    last_total = memory.get(LAST_TOTAL_KEY, total)
    hits = total - last_total
    if hits < 0:
        hits = 0
    memory.set(LAST_TOTAL_KEY, total)

    samples = _samples()
    sample = {
        "seq": memory.incr("timer-throttle-bench:seq", 1),
        "elapsed_ms": push_ms,
        "hits": hits,
    }
    samples.append(sample)
    if len(samples) > MAX_SAMPLES:
        samples = samples[len(samples) - MAX_SAMPLES:]
    memory.set(SAMPLES_KEY, samples)
    return _snapshot({"last": sample})

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

def on_connect(ctx):
    push_ms, window_ms = _settings_from_query(ctx.query)
    return [
        ws.subscribe(ctx.conn_id, STATS_TOPIC),
        ws.send(ctx.conn_id, _snapshot({"connected": True})),
        _schedule(push_ms, window_ms),
    ]

def on_message(ctx, msg):
    push_ms = memory.get(PUSH_MS_KEY, DEFAULT_PUSH_MS)
    window_ms = memory.get(WINDOW_MS_KEY, DEFAULT_WINDOW_MS)
    if type(msg) == "dict":
        push_ms = _clamp(_int(msg.get("push_ms", push_ms), push_ms), 1, 1000)
        window_ms = _clamp(_int(msg.get("window_ms", window_ms), window_ms), 100, 10000)
        if msg.get("type", "") == "reset":
            memory.set(TOTAL_KEY, 0)
            memory.set(LAST_TOTAL_KEY, 0)
            memory.set(SAMPLES_KEY, [])
            memory.set("timer-throttle-bench:seq", 0)
        memory.set(PUSH_MS_KEY, push_ms)
        memory.set(WINDOW_MS_KEY, window_ms)
    return [
        ws.send(ctx.conn_id, _snapshot({"configured": True})),
        _schedule(push_ms, window_ms),
    ]

def on_event(ctx, event):
    return ws.send(ctx.conn_id, event.payload)

def on_disconnect(ctx):
    return ws.unsubscribe_all(ctx.conn_id)

def on_timer(ctx, event):
    payload = event.payload
    push_ms = memory.get(PUSH_MS_KEY, DEFAULT_PUSH_MS)
    window_ms = memory.get(WINDOW_MS_KEY, DEFAULT_WINDOW_MS)
    if type(payload) == "dict":
        push_ms = _clamp(_int(payload.get("push_ms", push_ms), push_ms), 1, 1000)
        window_ms = _clamp(_int(payload.get("window_ms", window_ms), window_ms), 100, 10000)
    stats = _record_sample(push_ms, window_ms)
    return [
        events.publish(STATS_TOPIC, stats),
        _schedule(push_ms, window_ms),
    ]
