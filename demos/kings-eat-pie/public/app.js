const state = {
  socket: null,
  connId: "",
  joined: false,
  ready: false,
  round: 0,
  players: [],
  bites: [],
  retry: null,
};

const els = {
  dot: document.getElementById("dot"),
  statusText: document.getElementById("statusText"),
  notice: document.getElementById("notice"),
  join: document.getElementById("joinBtn"),
  ready: document.getElementById("readyBtn"),
  reset: document.getElementById("resetBtn"),
  roster: document.getElementById("roster"),
  pie: document.getElementById("pie"),
  log: document.getElementById("chompLog"),
  help: document.getElementById("helpBtn"),
  intro: document.getElementById("introModal"),
  closeIntro: document.getElementById("closeIntroBtn"),
};

const introSeenKey = "kings-eat-pie:intro-seen";

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
    setStatus("open", "live from the booth");
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
    state.players = msg.players || [];
    state.bites = msg.bites || [];
    const me = state.players.find((player) => player.id === state.connId);
    state.joined = Boolean(me);
    state.ready = Boolean(me && me.ready);
    els.notice.textContent = msg.notice || boothCopy();
    renderRoster();
    setButtons();
    if (msg.started && msg.round !== state.round) {
      state.round = msg.round;
      animatePie(state.bites);
    }
  }
}

function boothCopy() {
  const waiting = state.players.filter((player) => !player.ready).length;
  if (!state.joined) return "The booth has room. The pie has opinions.";
  if (waiting === 0 && state.players.length > 0) return "Everybody said go. Napkins are trembling.";
  return `${waiting} king${waiting === 1 ? "" : "s"} still deciding if pie is a lifestyle.`;
}

function setButtons() {
  els.join.disabled = !state.socket || state.socket.readyState !== WebSocket.OPEN || state.joined;
  els.ready.disabled = !state.joined || state.ready;
  els.reset.disabled = !state.socket || state.socket.readyState !== WebSocket.OPEN;
  els.ready.textContent = state.ready ? "Go said" : "Say go";
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

function renderRoster() {
  els.roster.innerHTML = "";
  if (state.players.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "No kings in the booth yet.";
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

function animatePie(bites) {
  els.log.innerHTML = "";
  els.pie.classList.remove("eaten");
  els.pie.offsetHeight;
  els.pie.classList.add("eating");

  bites.forEach((bite, index) => {
    window.setTimeout(() => {
      const line = document.createElement("div");
      line.className = "chomp";
      line.textContent = `${bite.bite}. ${bite.name} takes the sacred bite`;
      els.log.prepend(line);
      els.pie.dataset.bite = String(index + 1);
      els.pie.classList.add(`bite-${Math.min(index + 1, 4)}`);
      if (index === bites.length - 1) {
        window.setTimeout(() => {
          els.pie.classList.add("eaten");
          els.pie.classList.remove("eating", "bite-1", "bite-2", "bite-3", "bite-4");
        }, 500);
      }
    }, 650 * index);
  });
}

els.join.addEventListener("click", () => send({ type: "join" }));
els.ready.addEventListener("click", () => send({ type: "ready" }));
els.reset.addEventListener("click", () => {
  state.round = 0;
  els.pie.className = "pie";
  els.log.innerHTML = "";
  send({ type: "reset" });
});
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
