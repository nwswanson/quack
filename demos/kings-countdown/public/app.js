const state = {
  socket: null,
  connId: "",
  joined: false,
  ready: false,
  running: false,
  done: false,
  mode: "locked",
  target: 1000,
  total: 0,
  applied: 0,
  lost: 0,
  round: 0,
  players: [],
  chunks: [],
  retry: null,
  addTimer: null,
  lastSentRound: 0,
};

const els = {
  dot: document.getElementById("dot"),
  statusText: document.getElementById("statusText"),
  notice: document.getElementById("notice"),
  join: document.getElementById("joinBtn"),
  ready: document.getElementById("readyBtn"),
  reset: document.getElementById("resetBtn"),
  lockedMode: document.getElementById("lockedModeBtn"),
  unsafeMode: document.getElementById("unsafeModeBtn"),
  modeBadge: document.getElementById("modeBadge"),
  roster: document.getElementById("roster"),
  total: document.getElementById("total"),
  applied: document.getElementById("applied"),
  lost: document.getElementById("lost"),
  meterFill: document.getElementById("meterFill"),
  log: document.getElementById("chunkLog"),
  help: document.getElementById("helpBtn"),
  intro: document.getElementById("introModal"),
  closeIntro: document.getElementById("closeIntroBtn"),
};

const introSeenKey = "kings-countdown:intro-seen";

function wsUrl() {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${window.location.host}/ws`;
}

function setStatus(kind, text) {
  els.dot.dataset.kind = kind;
  els.statusText.textContent = text;
}

function connect() {
  clearTimeout(state.retry);
  const ws = new WebSocket(wsUrl());
  state.socket = ws;

  ws.addEventListener("open", () => {
    setStatus("open", "live from the count");
    send({ type: "sync" });
  });

  ws.addEventListener("message", (event) => {
    try {
      handleMessage(JSON.parse(event.data));
    } catch (err) {
      console.warn("bad websocket payload", err);
    }
  });

  ws.addEventListener("close", () => {
    setStatus("closed", "reconnecting");
    state.socket = null;
    stopAddLoop();
    state.retry = setTimeout(connect, 900);
  });

  ws.addEventListener("error", () => ws.close());
}

function send(message) {
  if (!state.socket || state.socket.readyState !== WebSocket.OPEN) return;
  state.socket.send(JSON.stringify(message));
}

function handleMessage(msg) {
  if (msg.type === "hello") {
    state.connId = msg.conn_id;
    state.target = msg.target || 1000;
    setButtons();
    return;
  }
  if (msg.type === "busy") {
    els.notice.textContent = msg.message;
    els.notice.classList.add("shake");
    setTimeout(() => els.notice.classList.remove("shake"), 360);
    return;
  }
  if (msg.type === "state") {
    state.mode = msg.mode || "locked";
    state.target = msg.target || 1000;
    state.total = msg.total || 0;
    state.applied = msg.applied || 0;
    state.lost = msg.lost || 0;
    state.running = Boolean(msg.running);
    state.done = Boolean(msg.done);
    state.players = msg.players || [];
    state.chunks = msg.chunks || [];
    const me = state.players.find((player) => player.id === state.connId);
    state.joined = Boolean(me);
    state.ready = Boolean(me && me.ready);
    els.notice.textContent = msg.notice || countdownCopy();
    render();
    setButtons();
    if (state.running && !state.done) {
      ensureAddLoop(msg.round || 0);
    } else {
      stopAddLoop();
    }
  }
}

function countdownCopy() {
  const waiting = state.players.filter((player) => !player.ready).length;
  if (!state.joined) return "Pick a mode, join the room, and race to 1000.";
  if (state.done) return `${state.mode} mode landed on ${state.total}.`;
  if (state.running) return `${state.mode} mode is counting. Watch for vanished work.`;
  return `${waiting} king${waiting === 1 ? "" : "s"} still holding the starting flag.`;
}

function setButtons() {
  const open = state.socket && state.socket.readyState === WebSocket.OPEN;
  els.join.disabled = !open || state.joined;
  els.ready.disabled = !state.joined || state.ready || state.running || state.done;
  els.reset.disabled = !open;
  els.ready.textContent = state.ready ? "Go said" : "Say go";
  els.lockedMode.classList.toggle("active", state.mode === "locked");
  els.unsafeMode.classList.toggle("active", state.mode === "unsafe");
}

function stopAddLoop() {
  clearTimeout(state.addTimer);
  state.addTimer = null;
}

function ensureAddLoop(round) {
  if (!state.joined || !state.ready || !state.running || state.done) return;
  if (state.total >= state.target) return;
  if (state.addTimer) return;
  state.lastSentRound = round;
  sendNextChunk();
}

function sendNextChunk() {
  if (!state.joined || !state.ready || !state.running || state.done) {
    stopAddLoop();
    return;
  }
  if (state.total >= state.target) {
    stopAddLoop();
    return;
  }
  const remaining = Math.max(0, state.target - state.total);
  const amount = remaining < 20 ? remaining : 1 + Math.floor(Math.random() * 20);
  send({ type: "add", amount });
  const delay = state.mode === "unsafe" ? 4 + Math.random() * 8 : 10 + Math.random() * 22;
  state.addTimer = setTimeout(sendNextChunk, delay);
}

function render() {
  renderCounter();
  renderRoster();
  renderChunks();
}

function renderCounter() {
  els.total.textContent = String(state.total);
  els.total.classList.remove("pop");
  els.total.offsetHeight;
  els.total.classList.add("pop");
  els.modeBadge.textContent = state.mode === "unsafe" ? "UNSAFE" : "LOCKED";
  els.modeBadge.dataset.mode = state.mode;
  els.applied.textContent = String(state.applied);
  els.lost.textContent = String(state.lost);
  els.lost.dataset.active = state.lost > 0 ? "true" : "false";
  const pct = Math.max(0, Math.min(100, (state.total / state.target) * 100));
  els.meterFill.style.width = `${pct}%`;
}

function renderRoster() {
  els.roster.innerHTML = "";
  if (state.players.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "No kings in the room yet.";
    els.roster.appendChild(empty);
    return;
  }
  state.players.forEach((player, index) => {
    const card = document.createElement("article");
    card.className = `king ${player.ready ? "ready" : ""}`;
    card.style.setProperty("--tilt", `${index % 2 === 0 ? -2 : 2}deg`);
    const badge = document.createElement("span");
    badge.className = "badge";
    badge.textContent = player.ready ? "GO" : "WAIT";
    const name = document.createElement("strong");
    name.textContent = player.name;
    const note = document.createElement("small");
    note.textContent = player.id === state.connId ? "this browser" : "remote royalty";
    card.append(badge, name, note);
    els.roster.appendChild(card);
  });
}

function renderChunks() {
  els.log.innerHTML = "";
  const chunks = [...state.chunks].reverse();
  chunks.forEach((chunk) => {
    const line = document.createElement("div");
    line.className = `chunk ${chunk.locked ? "locked" : "unsafe"}`;
    line.innerHTML = `<strong>${chunk.name}</strong><span>+${chunk.amount}</span><small>${chunk.before} -> ${chunk.after}</small>`;
    els.log.appendChild(line);
  });
}

function setMode(mode) {
  state.mode = mode;
  send({ type: "mode", mode });
}

function openIntro() {
  els.intro.hidden = false;
  document.body.classList.add("modal-open");
}

function closeIntro() {
  els.intro.hidden = true;
  document.body.classList.remove("modal-open");
  try {
    window.localStorage.setItem(introSeenKey, "yes");
  } catch (err) {
    console.warn("could not persist intro state", err);
  }
}

function maybeShowIntro() {
  let seen = false;
  try {
    seen = window.localStorage.getItem(introSeenKey) === "yes";
  } catch (err) {
    seen = false;
  }
  if (!seen) openIntro();
}

els.join.addEventListener("click", () => send({ type: "join", mode: state.mode }));
els.ready.addEventListener("click", () => send({ type: "ready" }));
els.reset.addEventListener("click", () => {
  stopAddLoop();
  send({ type: "reset", mode: state.mode });
});
els.lockedMode.addEventListener("click", () => setMode("locked"));
els.unsafeMode.addEventListener("click", () => setMode("unsafe"));
els.help.addEventListener("click", openIntro);
els.closeIntro.addEventListener("click", closeIntro);
els.intro.addEventListener("click", (event) => {
  if (event.target === els.intro) closeIntro();
});
window.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !els.intro.hidden) closeIntro();
});

maybeShowIntro();
connect();
