let state = {
  boards: [],
  currentBoardId: null,
  currentBoard: null,
  lists: [],
  socket: null,
  retryTimer: null,
};

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body != null) {
    opts.headers["content-type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch("/api" + path, opts);
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || `${res.status}`);
  return data;
}

function wsUrl() {
  const p = window.location.protocol === "https:" ? "wss:" : "ws:";
  return p + "//" + window.location.host + "/ws";
}

function setStatus(state, text) {
  const el = document.getElementById("status");
  el.dataset.state = state;
  document.getElementById("statusText").textContent = text;
}

function connectWs() {
  if (state.socket && state.socket.readyState === WebSocket.OPEN) return;
  const ws = new WebSocket(wsUrl());
  state.socket = ws;

  ws.addEventListener("open", () => setStatus("open", "Live"));

  ws.addEventListener("message", (ev) => {
    try {
      const msg = JSON.parse(ev.data);
      handleWsMessage(msg);
    } catch (e) {
      console.warn("WS message parse error", e);
    }
  });

  ws.addEventListener("close", () => {
    setStatus("closed", "Reconnecting");
    state.socket = null;
    state.retryTimer = setTimeout(connectWs, 1500);
  });

  ws.addEventListener("error", () => ws.close());
}

function handleWsMessage(msg) {
  if (msg.type === "connected" && state.currentBoardId) {
    state.socket.send(JSON.stringify({
      type: "subscribe",
      board_id: state.currentBoardId,
    }));
    return;
  }
  if (msg.type === "sync") {
    loadBoard(state.currentBoardId);
  }
}

function wsNotify(boardId) {
  if (state.socket && state.socket.readyState === WebSocket.OPEN) {
    state.socket.send(JSON.stringify({ type: "sync", board_id: boardId }));
  }
}

function subscribeToBoard(boardId) {
  if (state.socket && state.socket.readyState === WebSocket.OPEN) {
    state.socket.send(JSON.stringify({ type: "subscribe", board_id: boardId }));
  }
}

async function loadBoards() {
  try {
    const data = await api("GET", "/boards");
    state.boards = data.boards || [];
    renderBoardList();
    if (state.boards.length > 0) {
      if (!state.currentBoardId || !state.boards.find(b => b.id === state.currentBoardId)) {
        await loadBoard(state.boards[0].id);
      }
    } else {
      document.getElementById("boardTitle").textContent = "No boards";
      document.getElementById("listContainer").innerHTML = "";
    }
  } catch (e) {
    console.error("Failed to load boards", e);
  }
}

async function loadBoard(boardId) {
  if (!boardId) return;
  state.currentBoardId = boardId;
  try {
    const data = await api("GET", "/boards/" + boardId);
    state.currentBoard = data.board;
    state.lists = data.lists || [];
    renderBoard();
    renderBoardList();
    subscribeToBoard(boardId);
  } catch (e) {
    console.error("Failed to load board", e);
  }
}

async function createBoard() {
  const name = prompt("Board name:");
  if (!name || !name.trim()) return;
  try {
    await api("POST", "/boards", { name: name.trim() });
    await loadBoards();
  } catch (e) {
    console.error("Failed to create board", e);
  }
}

async function deleteBoard(boardId) {
  if (!confirm("Delete this board and all its lists and cards?")) return;
  try {
    await api("DELETE", "/boards/" + boardId);
    state.currentBoardId = null;
    state.currentBoard = null;
    state.lists = [];
    await loadBoards();
  } catch (e) {
    console.error("Failed to delete board", e);
  }
}

async function createList(name) {
  if (!state.currentBoardId) return;
  try {
    await api("POST", "/boards/" + state.currentBoardId + "/lists", { name });
    wsNotify(state.currentBoardId);
    await loadBoard(state.currentBoardId);
  } catch (e) {
    console.error("Failed to create list", e);
  }
}

async function updateList(listId, data) {
  try {
    await api("PUT", "/lists/" + listId, data);
    wsNotify(state.currentBoardId);
  } catch (e) {
    console.error("Failed to update list", e);
  }
}

async function deleteList(listId) {
  if (!confirm("Delete this list and all its cards?")) return;
  try {
    await api("DELETE", "/lists/" + listId);
    wsNotify(state.currentBoardId);
    await loadBoard(state.currentBoardId);
  } catch (e) {
    console.error("Failed to delete list", e);
  }
}

async function createCard(listId, title) {
  try {
    await api("POST", "/lists/" + listId + "/cards", { title });
    wsNotify(state.currentBoardId);
    await loadBoard(state.currentBoardId);
  } catch (e) {
    console.error("Failed to create card", e);
  }
}

async function updateCard(cardId, data) {
  try {
    await api("PUT", "/cards/" + cardId, data);
    wsNotify(state.currentBoardId);
  } catch (e) {
    console.error("Failed to update card", e);
  }
}

async function deleteCard(cardId) {
  try {
    await api("DELETE", "/cards/" + cardId);
    wsNotify(state.currentBoardId);
    await loadBoard(state.currentBoardId);
  } catch (e) {
    console.error("Failed to delete card", e);
  }
}

async function moveCard(cardId, toListId, position) {
  try {
    await api("PUT", "/cards/" + cardId + "/move", {
      to_list_id: toListId,
      position: position != null ? position : undefined,
    });
  } catch (e) {
    console.error("Failed to move card", e);
  }
}

function renderBoardList() {
  const ul = document.getElementById("boardList");
  ul.innerHTML = "";
  for (const b of state.boards) {
    const li = document.createElement("li");
    li.textContent = b.name;
    li.dataset.id = b.id;
    if (b.id === state.currentBoardId) li.classList.add("active");
    li.addEventListener("click", () => loadBoard(b.id));
    ul.appendChild(li);
  }
}

function renderBoard() {
  if (!state.currentBoard) return;
  document.getElementById("boardTitle").textContent = state.currentBoard.name;
  const container = document.getElementById("listContainer");
  container.innerHTML = "";

  for (const lst of state.lists) {
    const listEl = createListElement(lst);
    container.appendChild(listEl);
  }
}

function createListElement(lst) {
  const div = document.createElement("div");
  div.className = "list";
  div.dataset.listId = lst.id;

  const header = document.createElement("div");
  header.className = "list-header";

  const h2 = document.createElement("h2");
  h2.textContent = lst.name;
  h2.contentEditable = true;
  h2.addEventListener("blur", () => {
    const val = h2.textContent.trim();
    if (val && val !== lst.name) {
      lst.name = val;
      updateList(lst.id, { name: val });
    }
  });
  h2.addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); h2.blur(); }
  });

  const menuBtn = document.createElement("button");
  menuBtn.className = "list-menu-btn";
  menuBtn.textContent = "···";
  menuBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    showListMenu(e, lst.id);
  });

  header.appendChild(h2);
  header.appendChild(menuBtn);

  const cards = document.createElement("div");
  cards.className = "cards";
  cards.dataset.listId = lst.id;

  for (const card of (lst.cards || [])) {
    const cardEl = createCardElement(card);
    cards.appendChild(cardEl);
  }

  cards.addEventListener("dragover", (e) => {
    e.preventDefault();
    cards.classList.add("drag-over");
  });

  cards.addEventListener("dragleave", () => {
    cards.classList.remove("drag-over");
  });

  cards.addEventListener("drop", (e) => {
    e.preventDefault();
    cards.classList.remove("drag-over");
    const cardId = e.dataTransfer.getData("text/plain");
    const fromListId = e.dataTransfer.getData("from-list");
    if (!cardId) return;

    const dropCards = cards.querySelectorAll(".card:not(.dragging)");
    let position = dropCards.length;
    for (let i = 0; i < dropCards.length; i++) {
      const rect = dropCards[i].getBoundingClientRect();
      const mid = rect.top + rect.height / 2;
      if (e.clientY < mid) { position = i; break; }
    }

    if (fromListId === lst.id) {
      const el = document.querySelector(`.card[data-card-id="${cardId}"]`);
      if (el) {
        const ref = dropCards[position];
        if (ref) {
          cards.insertBefore(el, ref);
        } else {
          cards.appendChild(el);
        }
      }
      syncCardOrder(lst.id);
    } else {
      moveCard(cardId, lst.id, position).then(() => {
        wsNotify(state.currentBoardId);
        loadBoard(state.currentBoardId);
      });
    }
  });

  const addBtn = document.createElement("button");
  addBtn.className = "add-card-btn";
  addBtn.textContent = "+ Add Card";
  addBtn.addEventListener("click", () => showInlineInput(cards, lst.id));

  div.appendChild(header);
  div.appendChild(cards);
  div.appendChild(addBtn);

  return div;
}

function createCardElement(card) {
  const div = document.createElement("div");
  div.className = "card";
  div.draggable = true;
  div.dataset.cardId = card.id;

  const title = document.createElement("div");
  title.className = "card-title";
  title.textContent = card.title;
  title.contentEditable = true;

  title.addEventListener("blur", () => {
    const val = title.textContent.trim();
    if (val && val !== card.title) {
      card.title = val;
      updateCard(card.id, { title: val });
    }
  });

  title.addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); title.blur(); }
  });

  const del = document.createElement("button");
  del.className = "delete-card";
  del.textContent = "×";
  del.title = "Delete card";
  del.addEventListener("click", (e) => {
    e.stopPropagation();
    if (confirm("Delete this card?")) deleteCard(card.id);
  });

  div.appendChild(title);
  div.appendChild(del);

  div.addEventListener("dragstart", (e) => {
    e.dataTransfer.setData("text/plain", card.id);
    e.dataTransfer.setData("from-list", card.list_id);
    div.classList.add("dragging");
  });

  div.addEventListener("dragend", () => {
    div.classList.remove("dragging");
    document.querySelectorAll(".drag-over").forEach(el => el.classList.remove("drag-over"));
  });

  return div;
}

function syncCardOrder(listId) {
  const cardsEl = document.querySelector(`.cards[data-list-id="${listId}"]`);
  if (!cardsEl) return;
  const cardEls = cardsEl.querySelectorAll(".card");
  const cardIds = Array.from(cardEls).map(el => el.dataset.cardId);
  cardIds.forEach((id, i) => {
    moveCard(id, listId, i);
  });
  wsNotify(state.currentBoardId);
}

function showInlineInput(cardsEl, listId) {
  const input = document.createElement("input");
  input.id = "inlineInput";
  input.type = "text";
  input.placeholder = "Enter card title…";
  input.autofocus = true;

  const addBtn = cardsEl.parentElement.querySelector(".add-card-btn");
  addBtn.style.display = "none";
  cardsEl.appendChild(input);
  input.focus();

  function commit(val) {
    input.remove();
    addBtn.style.display = "";
    const v = (val || input.value).trim();
    if (v) createCard(listId, v);
  }

  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); commit(); }
    if (e.key === "Escape") { commit(""); }
  });

  input.addEventListener("blur", () => commit());
}

function showListMenu(e, listId) {
  const existing = document.querySelector(".menu");
  if (existing) existing.remove();
  const overlay = document.getElementById("overlay");
  overlay.classList.remove("hidden");

  const menu = document.createElement("div");
  menu.className = "menu";

  const renameBtn = document.createElement("button");
  renameBtn.textContent = "Rename";
  renameBtn.addEventListener("click", () => {
    menu.remove();
    overlay.classList.add("hidden");
    const h2 = document.querySelector(`.list[data-list-id="${listId}"] h2`);
    if (h2) h2.focus();
  });
  menu.appendChild(renameBtn);

  const delBtn = document.createElement("button");
  delBtn.textContent = "Delete List";
  delBtn.className = "danger";
  delBtn.addEventListener("click", () => {
    menu.remove();
    overlay.classList.add("hidden");
    deleteList(listId);
  });
  menu.appendChild(delBtn);

  positionMenu(menu, e.clientX, e.clientY);
  document.body.appendChild(menu);

  overlay.addEventListener("click", () => {
    menu.remove();
    overlay.classList.add("hidden");
  }, { once: true });
}

function positionMenu(menu, x, y) {
  menu.style.left = x + "px";
  menu.style.top = y + "px";
  requestAnimationFrame(() => {
    const r = menu.getBoundingClientRect();
    if (r.right > window.innerWidth) menu.style.left = (window.innerWidth - r.width - 8) + "px";
    if (r.bottom > window.innerHeight) menu.style.top = (window.innerHeight - r.height - 8) + "px";
  });
}

document.addEventListener("DOMContentLoaded", () => {
  document.getElementById("newBoardBtn").addEventListener("click", createBoard);
  document.getElementById("addListBtn").addEventListener("click", () => {
    const name = prompt("List name:");
    if (name && name.trim()) createList(name.trim());
  });
  connectWs();
  loadBoards();
});
