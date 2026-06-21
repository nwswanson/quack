def on_connect(ctx):
    return ws.send(ctx.conn_id, {
        "type": "ready",
        "path": ctx.path,
    })

def on_message(ctx, msg):
    if type(msg) == "dict" and msg.get("type") == "ping":
        return ws.send(ctx.conn_id, {
            "type": "pong",
            "seq": msg.get("seq", 0),
            "clientSentAt": msg.get("sentAt", ""),
            "serverConn": ctx.conn_id,
        })

    return ws.send(ctx.conn_id, {
        "type": "error",
        "message": "send JSON like {\"type\":\"ping\",\"seq\":1}",
    })

def on_disconnect(ctx):
    return ws.unsubscribe_all(ctx.conn_id)
