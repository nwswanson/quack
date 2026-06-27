MAX_ITEMS = 18
MAX_RECORDS = 36

def _trace_topic(session):
    return "pipe-demo.session." + session + ".trace"

def _topic(flow, session, stage):
    return "pipe-demo." + flow + "." + session + "." + stage

def _parts(topic):
    parts = topic.split(".")
    if len(parts) < 4:
        return "", "", ""
    return parts[1], parts[2], parts[3]

def _safe_session(session):
    return type(session) == "string" and session != "" and "." not in session and "/" not in session and "\\" not in session

def _state_key(flow, session):
    return "event-pipes-lab:" + flow + ":" + session

def _reset(flow, session, state):
    memory.set(_state_key(flow, session), state)
    return state

def _state(flow, session, fallback):
    state = memory.get(_state_key(flow, session), None)
    if type(state) != "dict":
        state = fallback
        memory.set(_state_key(flow, session), state)
    return state

def _save(flow, session, state):
    memory.set(_state_key(flow, session), state)
    return state

def _trace(session, flow, stage, title, detail):
    return events.publish(_trace_topic(session), {
        "type": "trace",
        "flow": flow,
        "stage": stage,
        "title": title,
        "detail": detail,
    })

def _result(session, flow, title, detail):
    return events.publish(_trace_topic(session), {
        "type": "result",
        "flow": flow,
        "stage": "result",
        "title": title,
        "detail": detail,
    })

def _words(text):
    if type(text) != "string":
        text = ""
    cleaned = text.lower()
    for mark in [",", ".", ";", ":", "!", "?", "(", ")", "[", "]", "{", "}", "\"", "'"]:
        cleaned = cleaned.replace(mark, " ")
    out = []
    for word in cleaned.split():
        word = word.strip()
        if word != "":
            out.append(word)
    return out

def _chunks(items, size):
    out = []
    current = []
    for item in items:
        current.append(item)
        if len(current) >= size:
            out.append(current)
            current = []
    if len(current) > 0:
        out.append(current)
    return out

def _as_items(value):
    if type(value) == "list":
        return value[:MAX_ITEMS]
    if type(value) != "string":
        value = ""
    out = []
    for raw in value.replace("\n", ",").split(","):
        item = raw.strip()
        if item != "":
            out.append(item)
        if len(out) >= MAX_ITEMS:
            break
    return out

def _score(item):
    score = len(item) * 3
    lower = item.lower()
    for ch in ["a", "e", "i", "o", "u"]:
        score += lower.count(ch) * 7
    return score

def _records(text):
    if type(text) != "string":
        text = ""
    out = []
    for raw in text.replace(",", "\n").splitlines():
        line = raw.strip()
        if line == "":
            continue
        key = line
        value = 1
        if ":" in line:
            left, right = line.split(":", 1)
            key = left.strip()
            raw_value = right.strip()
            if raw_value.isdigit():
                value = int(raw_value)
        if key != "":
            out.append({"key": key, "value": value, "raw": line})
        if len(out) >= MAX_RECORDS:
            break
    return out

def _hash_key(key):
    total = 0
    for ch in key.elems():
        total += ord(ch)
    return total

def _shard_count(value):
    if type(value) == "int" or type(value) == "float":
        value = int(value)
    else:
        value = 3
    if value < 2:
        value = 3
    if value > 6:
        value = 6
    return value

def on_connect(ctx):
    return ws.send(ctx.conn_id, {
        "type": "ready",
        "conn_id": ctx.conn_id,
    })

def on_message(ctx, msg):
    if type(msg) != "dict":
        return ws.send(ctx.conn_id, {"type": "error", "message": "expected a JSON object"})

    msg_type = msg.get("type", "")
    session = msg.get("session", "")
    flow = msg.get("flow", "")
    if not _safe_session(session):
        return ws.send(ctx.conn_id, {"type": "error", "message": "invalid session id"})

    if msg_type == "subscribe":
        return [
            ws.subscribe(ctx.conn_id, _trace_topic(session)),
            ws.send(ctx.conn_id, {"type": "subscribed", "topic": _trace_topic(session)}),
        ]

    if msg_type == "start":
        if flow not in ["map_reduce", "scatter_gather", "sharding"]:
            return ws.send(ctx.conn_id, {"type": "error", "message": "unknown flow"})
        return [
            ws.subscribe(ctx.conn_id, _trace_topic(session)),
            _trace(session, flow, "client", "websocket message received", {
                "from": ctx.conn_id,
                "next_pipe": _topic(flow, session, "start"),
            }),
            events.publish(_topic(flow, session, "start"), msg),
        ]

    return ws.send(ctx.conn_id, {"type": "error", "message": "unknown message type"})

def on_event(ctx, event):
    return ws.send(ctx.conn_id, event.payload)

def on_disconnect(ctx):
    return ws.unsubscribe_all(ctx.conn_id)

def on_map_reduce_event(ctx, event):
    flow, session, stage = _parts(event.topic)
    payload = event.payload
    if flow != "map_reduce" or not _safe_session(session) or type(payload) != "dict":
        return []

    if stage == "start":
        _reset(flow, session, {"expected": 0, "mapped": []})
        return [
            _trace(session, flow, stage, "published start event", {
                "pipe": event.topic,
                "next_pipe": _topic(flow, session, "split"),
            }),
            events.publish(_topic(flow, session, "split"), {"text": payload.get("input", "")}),
        ]

    if stage == "split":
        words = _words(payload.get("text", ""))
        chunks = _chunks(words, 4)
        state = _state(flow, session, {"expected": 0, "mapped": []})
        state["expected"] = len(chunks)
        state["mapped"] = []
        _save(flow, session, state)
        effects = [_trace(session, flow, stage, "split text into map work units", {
            "word_count": len(words),
            "chunk_count": len(chunks),
            "chunks": chunks,
        })]
        if len(chunks) == 0:
            effects.append(_result(session, flow, "no words to reduce", {"counts": []}))
            return effects
        for i, chunk in enumerate(chunks):
            effects.append(events.publish(_topic(flow, session, "map"), {"chunk": i + 1, "words": chunk}))
        return effects

    if stage == "map":
        pairs = []
        for word in payload.get("words", []):
            pairs.append({"word": word, "count": 1})
        state = _state(flow, session, {"expected": 0, "mapped": []})
        mapped = state.get("mapped", [])
        mapped.append({"chunk": payload.get("chunk", 0), "pairs": pairs})
        state["mapped"] = mapped
        _save(flow, session, state)
        effects = [_trace(session, flow, stage, "map worker emitted word-count pairs", {
            "chunk": payload.get("chunk", 0),
            "pairs": pairs,
            "received": len(mapped),
            "expected": state.get("expected", 0),
        })]
        if len(mapped) >= state.get("expected", 0):
            effects.append(events.publish(_topic(flow, session, "reduce"), {"mapped": mapped}))
        return effects

    if stage == "reduce":
        counts = {}
        for batch in payload.get("mapped", []):
            for pair in batch.get("pairs", []):
                word = pair.get("word", "")
                counts[word] = counts.get(word, 0) + pair.get("count", 0)
        rows = []
        for word in counts:
            rows.append({"word": word, "count": counts[word]})
        return [
            _trace(session, flow, stage, "reducer combined mapped pairs", {
                "unique_words": len(rows),
                "counts": rows,
            }),
            _result(session, flow, "map-reduce complete", {"counts": rows}),
        ]

    return []

def on_scatter_gather_event(ctx, event):
    flow, session, stage = _parts(event.topic)
    payload = event.payload
    if flow != "scatter_gather" or not _safe_session(session) or type(payload) != "dict":
        return []

    if stage == "start":
        items = _as_items(payload.get("input", ""))
        _reset(flow, session, {"expected": len(items), "responses": []})
        effects = [_trace(session, flow, stage, "scatter controller accepted work", {
            "items": items,
            "next_pipe": _topic(flow, session, "worker"),
        })]
        if len(items) == 0:
            effects.append(_result(session, flow, "nothing to gather", {"responses": []}))
            return effects
        for i, item in enumerate(items):
            effects.append(events.publish(_topic(flow, session, "worker"), {"index": i + 1, "item": item}))
        return effects

    if stage == "worker":
        item = payload.get("item", "")
        response = {
            "index": payload.get("index", 0),
            "item": item,
            "score": _score(item),
            "summary": item.upper() + " / len=" + str(len(item)),
        }
        state = _state(flow, session, {"expected": 0, "responses": []})
        responses = state.get("responses", [])
        responses.append(response)
        state["responses"] = responses
        _save(flow, session, state)
        effects = [_trace(session, flow, stage, "worker returned a partial response", {
            "response": response,
            "received": len(responses),
            "expected": state.get("expected", 0),
        })]
        if len(responses) >= state.get("expected", 0):
            effects.append(events.publish(_topic(flow, session, "gather"), {"responses": responses}))
        return effects

    if stage == "gather":
        responses = payload.get("responses", [])
        best = None
        for response in responses:
            if best == None or response.get("score", 0) > best.get("score", 0):
                best = response
        return [
            _trace(session, flow, stage, "gatherer merged worker responses", {
                "response_count": len(responses),
                "best": best,
            }),
            _result(session, flow, "scatter-gather complete", {
                "best": best,
                "responses": responses,
            }),
        ]

    return []

def on_sharding_event(ctx, event):
    flow, session, stage = _parts(event.topic)
    payload = event.payload
    if flow != "sharding" or not _safe_session(session) or type(payload) != "dict":
        return []

    if stage == "start":
        records = _records(payload.get("input", ""))
        shard_count = _shard_count(payload.get("shards", 3))
        _reset(flow, session, {"expected": shard_count, "done": []})
        return [
            _trace(session, flow, stage, "router received records", {
                "records": records,
                "shards": shard_count,
                "next_pipe": _topic(flow, session, "route"),
            }),
            events.publish(_topic(flow, session, "route"), {"records": records, "shards": shard_count}),
        ]

    if stage == "route":
        shard_count = _shard_count(payload.get("shards", 3))
        shards = []
        for i in range(shard_count):
            shards.append([])
        for record in payload.get("records", []):
            slot = _hash_key(record.get("key", "")) % shard_count
            shards[slot].append(record)
        effects = [_trace(session, flow, stage, "router assigned records to shard pipes", {
            "shards": shards,
        })]
        for i, records in enumerate(shards):
            effects.append(events.publish(_topic(flow, session, "shard"), {
                "shard": i,
                "records": records,
            }))
        return effects

    if stage == "shard":
        total = 0
        keys = []
        for record in payload.get("records", []):
            total += record.get("value", 0)
            keys.append(record.get("key", ""))
        summary = {
            "shard": payload.get("shard", 0),
            "count": len(payload.get("records", [])),
            "total": total,
            "keys": keys,
        }
        state = _state(flow, session, {"expected": 0, "done": []})
        done = state.get("done", [])
        done.append(summary)
        state["done"] = done
        _save(flow, session, state)
        effects = [_trace(session, flow, stage, "shard processed its local partition", {
            "summary": summary,
            "received": len(done),
            "expected": state.get("expected", 0),
        })]
        if len(done) >= state.get("expected", 0):
            effects.append(events.publish(_topic(flow, session, "merge"), {"shards": done}))
        return effects

    if stage == "merge":
        total = 0
        record_count = 0
        for shard in payload.get("shards", []):
            total += shard.get("total", 0)
            record_count += shard.get("count", 0)
        return [
            _trace(session, flow, stage, "merge combined shard summaries", {
                "records": record_count,
                "total": total,
                "shards": payload.get("shards", []),
            }),
            _result(session, flow, "sharding complete", {
                "records": record_count,
                "total": total,
                "shards": payload.get("shards", []),
            }),
        ]

    return []
