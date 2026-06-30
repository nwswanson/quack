def on_connect(ctx):
    ws.send(ctx.conn_id, {"type": "connected", "conn_id": ctx.conn_id})

def on_message(ctx, msg):
    if type(msg) != "dict":
        return
    t = msg.get("type")
    if t == "subscribe":
        board_id = msg.get("board_id")
        if board_id:
            ws.subscribe(ctx.conn_id, "board_updates:" + board_id)
            return
    if t == "unsubscribe":
        board_id = msg.get("board_id")
        if board_id:
            ws.unsubscribe(ctx.conn_id, "board_updates:" + board_id)
            return
    if t == "sync":
        board_id = msg.get("board_id")
        if board_id:
            events.publish("board_updates:" + board_id, {"type": "sync", "board_id": board_id})
            return

def on_event(ctx, event):
    ws.send(ctx.conn_id, event.payload)

def on_disconnect(ctx):
    ws.unsubscribe_all(ctx.conn_id)
