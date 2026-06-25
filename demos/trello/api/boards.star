def _payload(body):
    if len(body) == 0:
        return {}
    data = json.decode(request.body_text(body), default = {})
    if type(data) != "dict":
        return {}
    return data

def _json(data):
    return (
        200,
        {"content-type": "application/json; charset=utf-8", "cache-control": "no-store"},
        json.encode_indent(data, indent = "  ") + "\n",
    )

def _created(data):
    return (
        201,
        {"content-type": "application/json; charset=utf-8", "cache-control": "no-store"},
        json.encode_indent(data, indent = "  ") + "\n",
    )

def _err(status, msg):
    return (
        status,
        {"content-type": "application/json; charset=utf-8"},
        json.encode({"error": msg}),
    )

def _seed():
    if memory.get("_seeded"):
        return
    memory.set("_seeded", True)
    bid = uuid.uuid4()
    board = {"id": bid, "name": "My Trello Board"}
    memory.set("board:" + bid, board)
    memory.set("boards", [bid])

    todo_id = uuid.uuid4()
    wip_id = uuid.uuid4()
    done_id = uuid.uuid4()

    memory.set("list:" + todo_id, {"id": todo_id, "name": "To Do", "board_id": bid})
    memory.set("list:" + wip_id, {"id": wip_id, "name": "In Progress", "board_id": bid})
    memory.set("list:" + done_id, {"id": done_id, "name": "Done", "board_id": bid})
    memory.set("board_lists:" + bid, [todo_id, wip_id, done_id])

    c1 = uuid.uuid4()
    c2 = uuid.uuid4()
    c3 = uuid.uuid4()
    c4 = uuid.uuid4()
    c5 = uuid.uuid4()

    memory.set("card:" + c1, {"id": c1, "title": "Set up project", "list_id": todo_id})
    memory.set("card:" + c2, {"id": c2, "title": "Design the UI", "list_id": todo_id})
    memory.set("card:" + c3, {"id": c3, "title": "Write backend", "list_id": wip_id})
    memory.set("card:" + c4, {"id": c4, "title": "Add drag and drop", "list_id": wip_id})
    memory.set("card:" + c5, {"id": c5, "title": "Deploy to production", "list_id": done_id})

    memory.set("list_cards:" + todo_id, [c1, c2])
    memory.set("list_cards:" + wip_id, [c3, c4])
    memory.set("list_cards:" + done_id, [c5])

def _list_boards():
    _seed()
    ids = memory.get("boards", [])
    boards = []
    for bid in ids:
        b = memory.get("board:" + bid)
        if b:
            boards.append(b)
    return _json({"boards": boards})

def _create_board(data):
    bid = uuid.uuid4()
    name = data.get("name", "Untitled Board")
    board = {"id": bid, "name": name}
    memory.set("board:" + bid, board)
    ids = memory.get("boards", [])
    ids.append(bid)
    memory.set("boards", ids)
    return _created(board)

def _delete_board(bid):
    board = memory.get("board:" + bid)
    if not board:
        return _err(404, "Board not found")
    lids = memory.get("board_lists:" + bid, [])
    for lid in lids:
        cids = memory.get("list_cards:" + lid, [])
        for cid in cids:
            memory.delete("card:" + cid)
        memory.delete("list_cards:" + lid)
        memory.delete("list:" + lid)
    memory.delete("board_lists:" + bid)
    memory.delete("board:" + bid)
    ids = memory.get("boards", [])
    remaining = [i for i in ids if i != bid]
    memory.set("boards", remaining)
    return _json({"ok": True})

def _get_board_lists(bid):
    board = memory.get("board:" + bid)
    if not board:
        return _err(404, "Board not found")
    lids = memory.get("board_lists:" + bid, [])
    result = []
    for lid in lids:
        lst = memory.get("list:" + lid)
        if lst:
            cids = memory.get("list_cards:" + lid, [])
            cards = []
            for cid in cids:
                card = memory.get("card:" + cid)
                if card:
                    cards.append(card)
            lst["cards"] = cards
            result.append(lst)
    return _json({"board": board, "lists": result})

def _create_list(bid, data):
    board = memory.get("board:" + bid)
    if not board:
        return _err(404, "Board not found")
    lid = uuid.uuid4()
    name = data.get("name", "Untitled List")
    lst = {"id": lid, "name": name, "board_id": bid, "cards": []}
    memory.set("list:" + lid, {"id": lid, "name": name, "board_id": bid})
    memory.set("list_cards:" + lid, [])
    lids = memory.get("board_lists:" + bid, [])
    lids.append(lid)
    memory.set("board_lists:" + bid, lids)
    return _created(lst)

def _delete_list(lid):
    lst = memory.get("list:" + lid)
    if not lst:
        return _err(404, "List not found")
    bid = lst["board_id"]
    cids = memory.get("list_cards:" + lid, [])
    for cid in cids:
        memory.delete("card:" + cid)
    memory.delete("list_cards:" + lid)
    memory.delete("list:" + lid)
    lids = memory.get("board_lists:" + bid, [])
    remaining = [i for i in lids if i != lid]
    memory.set("board_lists:" + bid, remaining)
    return _json({"ok": True})

def _get_list_cards(lid):
    lst = memory.get("list:" + lid)
    if not lst:
        return _err(404, "List not found")
    cids = memory.get("list_cards:" + lid, [])
    cards = []
    for cid in cids:
        card = memory.get("card:" + cid)
        if card:
            cards.append(card)
    return _json({"list": lst, "cards": cards})

def _update_list(lid, data):
    lst = memory.get("list:" + lid)
    if not lst:
        return _err(404, "List not found")
    if "name" in data:
        lst["name"] = data["name"]
    memory.set("list:" + lid, lst)
    return _json(lst)

def _create_card(lid, data):
    lst = memory.get("list:" + lid)
    if not lst:
        return _err(404, "List not found")
    cid = uuid.uuid4()
    title = data.get("title", "Untitled Card")
    card = {"id": cid, "title": title, "list_id": lid}
    memory.set("card:" + cid, card)
    cids = memory.get("list_cards:" + lid, [])
    cids.append(cid)
    memory.set("list_cards:" + lid, cids)
    return _created(card)

def _update_card(cid, data):
    card = memory.get("card:" + cid)
    if not card:
        return _err(404, "Card not found")
    if "title" in data:
        card["title"] = data["title"]
    memory.set("card:" + cid, card)
    return _json(card)

def _delete_card(cid):
    card = memory.get("card:" + cid)
    if not card:
        return _err(404, "Card not found")
    lid = card["list_id"]
    lst = memory.get("list:" + lid)
    bid = lst["board_id"] if lst else ""
    memory.delete("card:" + cid)
    cids = memory.get("list_cards:" + lid, [])
    remaining = [c for c in cids if c != cid]
    memory.set("list_cards:" + lid, remaining)
    return _json({"ok": True})

def _move_card(cid, data):
    card = memory.get("card:" + cid)
    if not card:
        return _err(404, "Card not found")
    to_lid = data.get("to_list_id")
    position = data.get("position")
    if not to_lid:
        return _err(400, "to_list_id required")
    to_list = memory.get("list:" + to_lid)
    if not to_list:
        return _err(404, "Target list not found")
    old_lid = card["list_id"]
    bid = to_list["board_id"]
    cids = memory.get("list_cards:" + old_lid, [])
    remaining = [c for c in cids if c != cid]
    memory.set("list_cards:" + old_lid, remaining)
    to_cids = memory.get("list_cards:" + to_lid, [])
    if position != None and type(position) == "int":
        before = to_cids[:position]
        after = to_cids[position:]
        to_cids = before + [cid] + after
    else:
        to_cids.append(cid)
    memory.set("list_cards:" + to_lid, to_cids)
    card["list_id"] = to_lid
    memory.set("card:" + cid, card)
    return _json(card)

def bad_append(x):
      x.append(1)

def handle(req):
    method, path, query, headers, body = req
    bad_append("hey")
    parts = [p for p in path.strip("/").split("/") if p != ""]

    if len(parts) == 0:
        if method == "GET":
            return _list_boards()
        return _err(404, "not found")

    if parts[0] == "boards":
        if len(parts) == 1:
            if method == "GET":
                return _list_boards()
            elif method == "POST":
                return _create_board(_payload(body))
        elif len(parts) == 2:
            if method == "DELETE":
                return _delete_board(parts[1])
            elif method == "GET":
                return _get_board_lists(parts[1])
        elif len(parts) == 3 and parts[2] == "lists":
            if method == "GET":
                return _get_board_lists(parts[1])
            elif method == "POST":
                return _create_list(parts[1], _payload(body))

    if parts[0] == "lists":
        if len(parts) == 2:
            if method == "DELETE":
                return _delete_list(parts[1])
            elif method == "PUT":
                return _update_list(parts[1], _payload(body))
        elif len(parts) == 3 and parts[2] == "cards":
            if method == "GET":
                return _get_list_cards(parts[1])
            if method == "POST":
                return _create_card(parts[1], _payload(body))

    if parts[0] == "cards":
        if len(parts) == 2:
            if method == "PUT":
                return _update_card(parts[1], _payload(body))
            elif method == "DELETE":
                return _delete_card(parts[1])
        elif len(parts) == 3 and parts[2] == "move":
            if method in ("PUT", "POST"):
                return _move_card(parts[1], _payload(body))

    return _err(404, "not found: " + path)
