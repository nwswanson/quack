FLOW = "scatter_gather"
MAX_ITEMS = 18
START = "pipe-demo.scatter_gather.start"
PROFILE = "pipe-demo.scatter_gather.profile"
PRICING = "pipe-demo.scatter_gather.pricing"
INVENTORY = "pipe-demo.scatter_gather.inventory"
RISK = "pipe-demo.scatter_gather.risk"
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

def _service_pipe(index):
    if index == 0:
        return PROFILE
    if index == 1:
        return PRICING
    if index == 2:
        return INVENTORY
    return RISK

def start_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return []
    items = _items(payload.get("input", ""))
    _reset(session, 4)
    effects = [_trace(session, "start_node", "scatter controller sent the same request to four services", {
        "node": "api/scatter_gather.star:start_node",
        "incoming_edge": START,
        "outgoing_edges": [PROFILE, PRICING, INVENTORY, RISK],
        "items": items,
    })]
    if len(items) == 0:
        effects.append(_result(session, "nothing to gather", {"responses": []}))
        return effects
    for i in range(4):
        effects.append(events.publish(_service_pipe(i), {"session": session, "items": items}))
    return effects

def _profile_response(items):
    longest = ""
    for item in items:
        if len(item) > len(longest):
            longest = item
    return {
        "service": "profile",
        "summary": "classified " + str(len(items)) + " items",
        "score": len(items) * 11 + len(longest),
        "longest": longest,
    }

def _pricing_response(items):
    total = 0
    for item in items:
        total += _score(item)
    return {
        "service": "pricing",
        "summary": "estimated blended value",
        "score": total,
        "average": total / len(items) if len(items) > 0 else 0,
    }

def _inventory_response(items):
    available = []
    backorder = []
    for i, item in enumerate(items):
        if i % 2 == 0:
            available.append(item)
        else:
            backorder.append(item)
    return {
        "service": "inventory",
        "summary": "checked availability",
        "score": len(available) * 19 - len(backorder) * 3,
        "available": available,
        "backorder": backorder,
    }

def _risk_response(items):
    flagged = []
    for item in items:
        if len(item) > 7:
            flagged.append(item)
    return {
        "service": "risk",
        "summary": "screened long items",
        "score": 100 - len(flagged) * 13,
        "flagged": flagged,
    }

def _worker_node(payload, node_name, incoming_edge, response):
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return []
    state = _state(session)
    responses = state.get("responses", [])
    responses.append(response)
    state["responses"] = responses
    _save(session, state)
    effects = [_trace(session, node_name, "service returned a partial response", {
        "node": "api/scatter_gather.star:" + node_name,
        "incoming_edge": incoming_edge,
        "outgoing_edge": GATHER,
        "response": response,
        "received": len(responses),
        "expected": state.get("expected", 0),
    })]
    if len(responses) >= state.get("expected", 0):
        effects.append(events.publish(GATHER, {"session": session, "responses": responses}))
    return effects

def profile_node(ctx, event):
    items = event.payload.get("items", []) if type(event.payload) == "dict" else []
    return _worker_node(event.payload, "profile_node", PROFILE, _profile_response(items))

def pricing_node(ctx, event):
    items = event.payload.get("items", []) if type(event.payload) == "dict" else []
    return _worker_node(event.payload, "pricing_node", PRICING, _pricing_response(items))

def inventory_node(ctx, event):
    items = event.payload.get("items", []) if type(event.payload) == "dict" else []
    return _worker_node(event.payload, "inventory_node", INVENTORY, _inventory_response(items))

def risk_node(ctx, event):
    items = event.payload.get("items", []) if type(event.payload) == "dict" else []
    return _worker_node(event.payload, "risk_node", RISK, _risk_response(items))

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
