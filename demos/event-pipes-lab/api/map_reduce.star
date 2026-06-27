FLOW = "map_reduce"
START = "pipe-demo.map_reduce.start"
SPLIT = "pipe-demo.map_reduce.split"
MAP = "pipe-demo.map_reduce.map"
REDUCE = "pipe-demo.map_reduce.reduce"

def _trace_topic(session):
    return "pipe-demo.session." + session + ".trace"

def _safe_session(session):
    return type(session) == "string" and session != "" and "." not in session and "/" not in session and "\\" not in session

def _state_key(session):
    return "event-pipes-lab:map_reduce:" + session

def _reset(session):
    state = {"expected": 0, "mapped": []}
    memory.set(_state_key(session), state)
    return state

def _state(session):
    state = memory.get(_state_key(session), None)
    if type(state) != "dict":
        state = _reset(session)
    return state

def _save(session, state):
    memory.set(_state_key(session), state)
    return state

def _trace(session, stage, title, detail):
    return events.publish(_trace_topic(session), {
        "type": "trace",
        "flow": FLOW,
        "stage": stage,
        "title": title,
        "detail": detail,
    })

def _result(session, title, detail):
    return events.publish(_trace_topic(session), {
        "type": "result",
        "flow": FLOW,
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

def start_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return []
    _reset(session)
    return [
        _trace(session, "start_node", "accepted start event", {
            "node": "api/map_reduce.star:start_node",
            "incoming_edge": START,
            "outgoing_edge": SPLIT,
        }),
        events.publish(SPLIT, {"session": session, "text": payload.get("input", "")}),
    ]

def split_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return []
    words = _words(payload.get("text", ""))
    chunks = _chunks(words, 4)
    state = _state(session)
    state["expected"] = len(chunks)
    state["mapped"] = []
    _save(session, state)
    effects = [_trace(session, "split_node", "split text into fixed map work units", {
        "node": "api/map_reduce.star:split_node",
        "incoming_edge": SPLIT,
        "outgoing_edge": MAP,
        "word_count": len(words),
        "chunk_count": len(chunks),
        "chunks": chunks,
    })]
    if len(chunks) == 0:
        effects.append(_result(session, "no words to reduce", {"counts": []}))
        return effects
    for i, chunk in enumerate(chunks):
        effects.append(events.publish(MAP, {"session": session, "chunk": i + 1, "words": chunk}))
    return effects

def map_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return []
    pairs = []
    for word in payload.get("words", []):
        pairs.append({"word": word, "count": 1})
    state = _state(session)
    mapped = state.get("mapped", [])
    mapped.append({"chunk": payload.get("chunk", 0), "pairs": pairs})
    state["mapped"] = mapped
    _save(session, state)
    effects = [_trace(session, "map_node", "map worker emitted word-count pairs", {
        "node": "api/map_reduce.star:map_node",
        "incoming_edge": MAP,
        "outgoing_edge": REDUCE,
        "chunk": payload.get("chunk", 0),
        "pairs": pairs,
        "received": len(mapped),
        "expected": state.get("expected", 0),
    })]
    if len(mapped) >= state.get("expected", 0):
        effects.append(events.publish(REDUCE, {"session": session, "mapped": mapped}))
    return effects

def reduce_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return []
    counts = {}
    for batch in payload.get("mapped", []):
        for pair in batch.get("pairs", []):
            word = pair.get("word", "")
            counts[word] = counts.get(word, 0) + pair.get("count", 0)
    rows = []
    for word in counts:
        rows.append({"word": word, "count": counts[word]})
    return [
        _trace(session, "reduce_node", "reducer combined mapped pairs", {
            "node": "api/map_reduce.star:reduce_node",
            "incoming_edge": REDUCE,
            "unique_words": len(rows),
            "counts": rows,
        }),
        _result(session, "map-reduce complete", {"counts": rows}),
    ]
