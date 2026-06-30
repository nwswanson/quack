MAP_REDUCE_START = "pipe-demo.map_reduce.start"
SCATTER_GATHER_START = "pipe-demo.scatter_gather.start"
SHARDING_START = "pipe-demo.sharding.start"

def _trace_topic(session):
    return "pipe-demo.session." + session + ".trace"

def _safe_session(session):
    return type(session) == "string" and session != "" and "." not in session and "/" not in session and "\\" not in session

def _start_pipe(flow):
    if flow == "map_reduce":
        return MAP_REDUCE_START
    if flow == "scatter_gather":
        return SCATTER_GATHER_START
    if flow == "sharding":
        return SHARDING_START
    return ""

def _trace(session, flow, stage, title, detail):
    events.publish(_trace_topic(session), {
        "type": "trace",
        "flow": flow,
        "stage": stage,
        "title": title,
        "detail": detail,
    })

def on_connect(ctx):
    ws.send(ctx.conn_id, {
        "type": "ready",
        "conn_id": ctx.conn_id,
    })

def on_message(ctx, msg):
    if type(msg) != "dict":
        ws.send(ctx.conn_id, {"type": "error", "message": "expected a JSON object"})
        return

    msg_type = msg.get("type", "")
    session = msg.get("session", "")
    if not _safe_session(session):
        ws.send(ctx.conn_id, {"type": "error", "message": "invalid session id"})
        return

    if msg_type == "subscribe":
        ws.subscribe(ctx.conn_id, _trace_topic(session))
        ws.send(ctx.conn_id, {"type": "subscribed", "topic": _trace_topic(session)})
        return

    if msg_type == "start":
        flow = msg.get("flow", "")
        start_pipe = _start_pipe(flow)
        if start_pipe == "":
            ws.send(ctx.conn_id, {"type": "error", "message": "unknown flow"})
            return
        ws.subscribe(ctx.conn_id, _trace_topic(session))
        _trace(session, flow, "websocket_ingress", "websocket message became a pipe event", {
            "from": ctx.conn_id,
            "edge": start_pipe,
            "session": session,
        })
        events.publish(start_pipe, msg)
        return

    ws.send(ctx.conn_id, {"type": "error", "message": "unknown message type"})

def on_event(ctx, event):
    ws.send(ctx.conn_id, event.payload)

def on_disconnect(ctx):
    ws.unsubscribe_all(ctx.conn_id)
