FLOW = "map_reduce"
START = "pipe-demo.map_reduce.start"
SPLIT = "pipe-demo.map_reduce.split"
MAP_0 = "pipe-demo.map_reduce.map_0"
MAP_1 = "pipe-demo.map_reduce.map_1"
MAP_2 = "pipe-demo.map_reduce.map_2"
MAP_3 = "pipe-demo.map_reduce.map_3"
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
    events.publish(_trace_topic(session), {
        "type": "trace",
        "flow": FLOW,
        "stage": stage,
        "title": title,
        "detail": detail,
    })

def _result(session, title, detail):
    events.publish(_trace_topic(session), {
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

def _partitions(items):
    out = [[], [], [], []]
    for i, item in enumerate(items):
        out[i % 4].append(item)
    return out

def _map_pipe(slot):
    if slot == 0:
        return MAP_0
    if slot == 1:
        return MAP_1
    if slot == 2:
        return MAP_2
    return MAP_3

def start_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return
    _reset(session)
    _trace(session, "start_node", "accepted start event", {
        "node": "api/map_reduce.star:start_node",
        "incoming_edge": START,
        "outgoing_edge": SPLIT,
    })
    events.publish(SPLIT, {"session": session, "text": payload.get("input", "")})

def split_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return
    words = _words(payload.get("text", ""))
    chunks = _partitions(words)
    state = _state(session)
    state["expected"] = 4
    state["mapped"] = []
    _save(session, state)
    _trace(session, "split_node", "split text across four hardcoded map workers", {
        "node": "api/map_reduce.star:split_node",
        "incoming_edge": SPLIT,
        "outgoing_edges": [MAP_0, MAP_1, MAP_2, MAP_3],
        "word_count": len(words),
        "chunk_count": len(chunks),
        "chunks": chunks,
    })
    if len(chunks) == 0:
        _result(session, "no words to reduce", {"counts": []})
        return
    for i, chunk in enumerate(chunks):
        events.publish(_map_pipe(i), {"session": session, "worker": i, "words": chunk})

def _map_node(payload, node_name, incoming_edge):
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return
    pairs = []
    for word in payload.get("words", []):
        pairs.append({"word": word, "count": 1})
    state = _state(session)
    mapped = state.get("mapped", [])
    mapped.append({"worker": payload.get("worker", 0), "pairs": pairs})
    state["mapped"] = mapped
    _save(session, state)
    _trace(session, node_name, "map worker emitted word-count pairs", {
        "node": "api/map_reduce.star:" + node_name,
        "incoming_edge": incoming_edge,
        "outgoing_edge": REDUCE,
        "worker": payload.get("worker", 0),
        "pairs": pairs,
        "received": len(mapped),
        "expected": state.get("expected", 0),
    })
    if len(mapped) >= state.get("expected", 0):
        events.publish(REDUCE, {"session": session, "mapped": mapped})

def map_0_node(ctx, event):
    _map_node(event.payload, "map_0_node", MAP_0)

def map_1_node(ctx, event):
    _map_node(event.payload, "map_1_node", MAP_1)

def map_2_node(ctx, event):
    _map_node(event.payload, "map_2_node", MAP_2)

def map_3_node(ctx, event):
    _map_node(event.payload, "map_3_node", MAP_3)

def reduce_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return
    counts = {}
    for batch in payload.get("mapped", []):
        for pair in batch.get("pairs", []):
            word = pair.get("word", "")
            counts[word] = counts.get(word, 0) + pair.get("count", 0)
    rows = []
    for word in counts:
        rows.append({"word": word, "count": counts[word]})
    _trace(session, "reduce_node", "reducer combined mapped pairs", {
        "node": "api/map_reduce.star:reduce_node",
        "incoming_edge": REDUCE,
        "unique_words": len(rows),
        "counts": rows,
    })
    _result(session, "map-reduce complete", {"counts": rows})
