const state = {
  socket: null,
  reconnectTimer: null,
  connId: "",
  room: 7,
  name: localStorage.getItem("chat.name") || "",
  messages: [],
  typing: new Map(),
  joined: false,
};

const els = {
  status: document.getElementById("status"),
  statusText: document.getElementById("statusText"),
  roomTitle: document.getElementById("roomTitle"),
  selectorLabel: document.getElementById("selectorLabel"),
  topicLabel: document.getElementById("topicLabel"),
  messageCount: document.getElementById("messageCount"),
  messages: document.getElementById("messages"),
  typing: document.getElementById("typing"),
  joinForm: document.getElementById("joinForm"),
  messageForm: document.getElementById("messageForm"),
  nameInput: document.getElementById("nameInput"),
  roomInput: document.getElementById("roomInput"),
  messageInput: document.getElementById("messageInput"),
  roomButtons: document.getElementById("roomButtons"),
};

function wsUrl() {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${window.location.host}/ws`;
}

function setStatus(kind, text) {
  els.status.dataset.state = kind;
  els.statusText.textContent = text;
}

function connect() {
  if (state.socket && state.socket.readyState === WebSocket.OPEN) return;
  const socket = new WebSocket(wsUrl());
  state.socket = socket;

  socket.addEventListener("open", () => {
    setStatus("open", "Live");
    joinRoom(state.room);
  });

  socket.addEventListener("message", (event) => {
    try {
      handleMessage(JSON.parse(event.data));
    } catch (err) {
      console.warn("bad websocket payload", err);
    }
  });

  socket.addEventListener("close", () => {
    setStatus("closed", "Reconnecting");
    state.socket = null;
    window.clearTimeout(state.reconnectTimer);
    state.reconnectTimer = window.setTimeout(connect, 1200);
  });

  socket.addEventListener("error", () => socket.close());
}

function send(payload) {
  if (!state.socket || state.socket.readyState !== WebSocket.OPEN) return false;
  state.socket.send(JSON.stringify(payload));
  return true;
}

function joinRoom(room) {
  const next = clampRoom(room);
  state.room = next;
  state.name = cleanName(els.nameInput.value);
  localStorage.setItem("chat.name", state.name);
  state.messages = [];
  state.typing.clear();
  state.joined = false;
  render();
  send({ type: "join", room: next, name: state.name });
}

function handleMessage(msg) {
  if (msg.type === "ready") {
    state.connId = msg.conn_id || "";
    els.topicLabel.textContent = msg.selector || "chat.room.*";
    els.selectorLabel.textContent = msg.selector || "chat.room.*";
    return;
  }
  if (msg.type === "joined") {
    state.joined = true;
    state.room = msg.room;
    state.name = msg.name;
    render();
    return;
  }
  if (msg.type === "typing") {
    if (msg.conn_id && msg.conn_id !== currentConnId() && msg.active) {
      state.typing.set(msg.conn_id, msg.name || "Someone");
      window.setTimeout(() => {
        state.typing.delete(msg.conn_id);
        renderTyping();
      }, 1600);
    } else if (msg.conn_id) {
      state.typing.delete(msg.conn_id);
    }
    renderTyping();
    return;
  }
  if (msg.type === "message" || msg.type === "system" || msg.type === "error") {
    state.messages.push({
      type: msg.type,
      name: msg.name || "System",
      text: msg.text || msg.message || "",
      room: msg.room || state.room,
      topic: msg.topic || `chat.room.${state.room}`,
      at: new Date(),
    });
    if (state.messages.length > 80) state.messages.shift();
    renderMessages();
  }
}

function currentConnId() {
  return state.connId;
}

function render() {
  els.roomTitle.textContent = `Room ${state.room}`;
  els.roomInput.value = state.room;
  els.nameInput.value = state.name;
  els.selectorLabel.textContent = `chat.room.${state.room}`;
  renderRooms();
  renderMessages();
  renderTyping();
}

function renderRooms() {
  els.roomButtons.innerHTML = "";
  for (const room of [1, 7, 13, 21, 42, 64, 88, 100]) {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = String(room);
    button.className = room === state.room ? "active" : "";
    button.addEventListener("click", () => joinRoom(room));
    els.roomButtons.appendChild(button);
  }
}

function renderMessages() {
  els.messages.innerHTML = "";
  els.messageCount.textContent = String(state.messages.length);
  if (state.messages.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "No messages";
    els.messages.appendChild(empty);
    return;
  }
  for (const msg of state.messages) {
    const row = document.createElement("article");
    row.className = `msg ${msg.type}`;

    const meta = document.createElement("div");
    meta.className = "meta";
    const name = document.createElement("strong");
    name.textContent = msg.name;
    const topic = document.createElement("span");
    topic.textContent = msg.topic;
    meta.append(name, topic);

    const text = document.createElement("p");
    text.textContent = msg.text;
    row.append(meta, text);
    els.messages.appendChild(row);
  }
  els.messages.scrollTop = els.messages.scrollHeight;
}

function renderTyping() {
  const names = Array.from(state.typing.values());
  els.typing.textContent = names.length ? `${names.slice(0, 2).join(", ")} typing` : "";
}

function clampRoom(value) {
  const n = Number.parseInt(value, 10);
  if (!Number.isFinite(n)) return 1;
  return Math.min(100, Math.max(1, n));
}

function cleanName(value) {
  const name = String(value || "").trim().slice(0, 24);
  return name || "Guest";
}

let typingTimer = null;
els.joinForm.addEventListener("submit", (event) => {
  event.preventDefault();
  joinRoom(els.roomInput.value);
});

els.messageForm.addEventListener("submit", (event) => {
  event.preventDefault();
  const text = els.messageInput.value.trim();
  if (!text) return;
  if (send({ type: "message", text })) {
    els.messageInput.value = "";
    send({ type: "typing", active: false });
  }
});

els.messageInput.addEventListener("input", () => {
  send({ type: "typing", active: true });
  window.clearTimeout(typingTimer);
  typingTimer = window.setTimeout(() => send({ type: "typing", active: false }), 900);
});

render();
connect();
