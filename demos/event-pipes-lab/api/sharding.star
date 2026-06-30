FLOW = "sharding"
MAX_RECORDS = 36
START = "pipe-demo.sharding.start"
ROUTE = "pipe-demo.sharding.route"
SHARD_0 = "pipe-demo.sharding.shard_0"
SHARD_1 = "pipe-demo.sharding.shard_1"
SHARD_2 = "pipe-demo.sharding.shard_2"
SHARD_3 = "pipe-demo.sharding.shard_3"
MERGE = "pipe-demo.sharding.merge"

def _trace_topic(session):
    return "pipe-demo.session." + session + ".trace"

def _safe_session(session):
    return type(session) == "string" and session != "" and "." not in session and "/" not in session and "\\" not in session

def _state_key(session):
    return "event-pipes-lab:sharding:" + session

def _reset(session):
    state = {"expected": 4, "done": []}
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

def _shard_pipe(slot):
    if slot == 0:
        return SHARD_0
    if slot == 1:
        return SHARD_1
    if slot == 2:
        return SHARD_2
    return SHARD_3

def start_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return
    records = _records(payload.get("input", ""))
    _reset(session)
    _trace(session, "start_node", "router received records", {
        "node": "api/sharding.star:start_node",
        "incoming_edge": START,
        "outgoing_edge": ROUTE,
        "records": records,
        "shards": 4,
    })
    events.publish(ROUTE, {"session": session, "records": records})

def route_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return
    shards = [[], [], [], []]
    for record in payload.get("records", []):
        slot = _hash_key(record.get("key", "")) % 4
        shards[slot].append(record)
    _trace(session, "route_node", "router assigned records to hardcoded shard edges", {
        "node": "api/sharding.star:route_node",
        "incoming_edge": ROUTE,
        "outgoing_edges": [SHARD_0, SHARD_1, SHARD_2, SHARD_3],
        "shards": shards,
    })
    for i, records in enumerate(shards):
        events.publish(_shard_pipe(i), {
            "session": session,
            "shard": i,
            "records": records,
        })

def _shard_node(payload, node_name, incoming_edge):
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return
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
    state = _state(session)
    done = state.get("done", [])
    done.append(summary)
    state["done"] = done
    _save(session, state)
    _trace(session, node_name, "shard processed its local partition", {
        "node": "api/sharding.star:" + node_name,
        "incoming_edge": incoming_edge,
        "outgoing_edge": MERGE,
        "summary": summary,
        "received": len(done),
        "expected": state.get("expected", 4),
    })
    if len(done) >= state.get("expected", 4):
        events.publish(MERGE, {"session": session, "shards": done})

def shard_0_node(ctx, event):
    _shard_node(event.payload, "shard_0_node", SHARD_0)

def shard_1_node(ctx, event):
    _shard_node(event.payload, "shard_1_node", SHARD_1)

def shard_2_node(ctx, event):
    _shard_node(event.payload, "shard_2_node", SHARD_2)

def shard_3_node(ctx, event):
    _shard_node(event.payload, "shard_3_node", SHARD_3)

def merge_node(ctx, event):
    payload = event.payload
    session = payload.get("session", "") if type(payload) == "dict" else ""
    if not _safe_session(session):
        return
    total = 0
    record_count = 0
    for shard in payload.get("shards", []):
        total += shard.get("total", 0)
        record_count += shard.get("count", 0)
    _trace(session, "merge_node", "merge combined shard summaries", {
        "node": "api/sharding.star:merge_node",
        "incoming_edge": MERGE,
        "records": record_count,
        "total": total,
        "shards": payload.get("shards", []),
    })
    _result(session, "sharding complete", {
        "records": record_count,
        "total": total,
        "shards": payload.get("shards", []),
    })
