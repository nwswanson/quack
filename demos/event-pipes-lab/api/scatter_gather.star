FLOW = "scatter_gather"
MAX_ITEMS = 18
START = "pipe-demo.scatter_gather.start"
WORKER = "pipe-demo.scatter_gather.worker"
GATHER = "pipe-demo.scatter_gather.gather"

def _trace_topic(session):
    return "pipe-demo.session." + session + ".trace"

def _safe_session(session):
    return type(session) == "string" and session != "" and "." not in session and "/" not in session and "\\" not in session

def _state_key(session):
    return "event-pipes-lab:scatter_gather:" + session

def _reset(session, expected):
    state = {"expected": expected, "responses": []}
    memory.set(_state_key(session), state)
    return state

def _state(session):
    state = memory.get(_state_key(session), None)
    if type(state) != "dict":
        state = _reset(session, 0)
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

def _items(value):
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

def start_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return []
    items = _items(payload.get("input", ""))
    _reset(session, len(items))
    effects = [_trace(session, "start_node", "scatter controller accepted work", {
        "node": "api/scatter_gather.star:start_node",
        "incoming_edge": START,
        "outgoing_edge": WORKER,
        "items": items,
    })]
    if len(items) == 0:
        effects.append(_result(session, "nothing to gather", {"responses": []}))
        return effects
    for i, item in enumerate(items):
        effects.append(events.publish(WORKER, {"session": session, "index": i + 1, "item": item}))
    return effects

def worker_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return []
    item = payload.get("item", "")
    response = {
        "index": payload.get("index", 0),
        "item": item,
        "score": _score(item),
        "summary": item.upper() + " / len=" + str(len(item)),
    }
    state = _state(session)
    responses = state.get("responses", [])
    responses.append(response)
    state["responses"] = responses
    _save(session, state)
    effects = [_trace(session, "worker_node", "worker returned a partial response", {
        "node": "api/scatter_gather.star:worker_node",
        "incoming_edge": WORKER,
        "outgoing_edge": GATHER,
        "response": response,
        "received": len(responses),
        "expected": state.get("expected", 0),
    })]
    if len(responses) >= state.get("expected", 0):
        effects.append(events.publish(GATHER, {"session": session, "responses": responses}))
    return effects

def gather_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return []
    responses = payload.get("responses", [])
    best = None
    for response in responses:
        if best == None or response.get("score", 0) > best.get("score", 0):
            best = response
    return [
        _trace(session, "gather_node", "gatherer merged worker responses", {
            "node": "api/scatter_gather.star:gather_node",
            "incoming_edge": GATHER,
            "response_count": len(responses),
            "best": best,
        }),
        _result(session, "scatter-gather complete", {
            "best": best,
            "responses": responses,
        }),
    ]
