const GAME_W = 800;
const GAME_H = 600;
const GROUND_Y = 550;
const PLAYER_W = 28;
const PLAYER_H = 42;
const INPUT_INTERVAL = 50;
const LERP = 0.18;
const MAX_PLAYERS = 10;

const app = new PIXI.Application({
  width: GAME_W,
  height: GAME_H,
  backgroundColor: 0x87ceeb,
  antialias: true,
  resolution: Math.max(1, window.devicePixelRatio || 1),
  autoDensity: true,
});

document.getElementById("game").appendChild(app.view);

const statusEl = document.getElementById("status");
const infoEl = document.getElementById("info");

function setStatus(label, cls) {
  statusEl.textContent = label;
  statusEl.className = "ui-overlay " + cls;
}

const floor = new PIXI.Graphics();
floor.beginFill(0x8b4513);
floor.drawRect(0, GROUND_Y, GAME_W, GAME_H - GROUND_Y);
floor.endFill();
floor.beginFill(0x228b22);
floor.drawRect(0, GROUND_Y, GAME_W, 6);
floor.endFill();
app.stage.addChild(floor);

function drawCloud(g, x, y) {
  const alpha = 0.6;
  g.beginFill(0xffffff, alpha);
  g.drawEllipse(x, y, 50, 22);
  g.drawEllipse(x - 30, y + 6, 34, 18);
  g.drawEllipse(x + 32, y + 4, 36, 18);
  g.drawEllipse(x - 8, y - 10, 30, 16);
  g.drawEllipse(x + 14, y - 8, 28, 14);
  g.endFill();
}

const clouds = new PIXI.Graphics();
drawCloud(clouds, 110, 80);
drawCloud(clouds, 350, 55);
drawCloud(clouds, 620, 90);
drawCloud(clouds, 480, 140);
app.stage.addChild(clouds);

function parseColor(s) {
  return parseInt(s, 16);
}

const state = {
  myId: null,
  myColor: null,
  sprites: {},
  keys: {},
  socket: null,
  retryTimer: null,
  inputTimer: null,
};

function makePlayerSprite() {
  const c = new PIXI.Container();

  const body = new PIXI.Graphics();
  c.addChild(body);

  const label = new PIXI.Text("", {
    fontSize: 10,
    fill: 0xffffff,
    stroke: 0x000000,
    strokeThickness: 2.5,
    align: "center",
  });
  label.anchor.set(0.5, 0);
  label.x = PLAYER_W / 2;
  label.y = -14;
  c.addChild(label);

  return { container: c, body, label };
}

function applyPlayerStyle(sprite, colorStr) {
  const g = sprite.body;
  const color = parseColor(colorStr);
  g.clear();
  g.beginFill(color);
  g.drawRoundedRect(0, 0, PLAYER_W, PLAYER_H, 5);
  g.endFill();
  g.beginFill(0xffffff, 0.25);
  g.drawRoundedRect(2, 2, PLAYER_W - 4, PLAYER_H / 2 - 2, 4);
  g.endFill();
}

function spawnPlayer(id, colorStr) {
  if (state.sprites[id]) return;
  const sp = makePlayerSprite();
  applyPlayerStyle(sp, colorStr);
  sp.container.x = 0;
  sp.container.y = 0;
  sp.label.text = id.slice(0, 6);
  state.sprites[id] = {
    container: sp.container,
    body: sp.body,
    label: sp.label,
    targetX: 0,
    targetY: 0,
  };
  app.stage.addChild(sp.container);
}

function removePlayer(id) {
  const sp = state.sprites[id];
  if (!sp) return;
  app.stage.removeChild(sp.container);
  delete state.sprites[id];
}

function updatePlayers(list) {
  const ids = new Set();
  for (const p of list || []) {
    ids.add(p.id);
    if (!state.sprites[p.id]) {
      spawnPlayer(p.id, p.color);
    }
    const sp = state.sprites[p.id];
    sp.label.text = p.id.slice(0, 6);
    sp.targetX = p.x;
    sp.targetY = p.y;

  }
  for (const id of Object.keys(state.sprites)) {
    if (!ids.has(id)) {
      removePlayer(id);
    }
  }
  const count = ids.size;
  infoEl.textContent = count + " online";
}

function sockUrl() {
  const s = location.protocol === "https:" ? "wss:" : "ws:";
  return s + "//" + location.host + "/ws";
}

function connect() {
  clearTimeout(state.retryTimer);
  setStatus("Connecting", "connecting");

  const ws = new WebSocket(sockUrl());
  state.socket = ws;

  ws.addEventListener("open", () => {
    setStatus("Connected", "ok");
    state.inputTimer = setInterval(sendInput, INPUT_INTERVAL);
  });

  ws.addEventListener("message", (e) => {
    let msg;
    try {
      msg = JSON.parse(e.data);
    } catch {
      return;
    }
    if (msg.type === "ready") {
      state.myId = msg.conn_id;
      state.myColor = msg.color;
      updatePlayers(msg.players);
    } else if (msg.type === "state") {
      updatePlayers(msg.players);
    } else if (msg.type === "error") {
      setStatus("Error: " + (msg.message || "unknown"), "error");
    }
  });

  ws.addEventListener("close", () => {
    clearInterval(state.inputTimer);
    state.inputTimer = null;
    if (state.socket === ws) {
      setStatus("Reconnecting", "connecting");
      state.retryTimer = setTimeout(connect, 1500);
    }
  });

  ws.addEventListener("error", () => ws.close());
}

function sendInput() {
  if (!state.socket || state.socket.readyState !== WebSocket.OPEN) return;
  const k = state.keys;
  state.socket.send(JSON.stringify({
    type: "input",
    left: !!(k.ArrowLeft || k.KeyA),
    right: !!(k.ArrowRight || k.KeyD),
    jump: !!(k.ArrowUp || k.KeyW || k.Space),
  }));
}

document.addEventListener("keydown", (e) => {
  state.keys[e.code] = true;
});

document.addEventListener("keyup", (e) => {
  state.keys[e.code] = false;
});

app.ticker.add(() => {
  for (const id of Object.keys(state.sprites)) {
    const sp = state.sprites[id];
    sp.container.x += (sp.targetX - sp.container.x) * LERP;
    sp.container.y += (sp.targetY - sp.container.y) * LERP;
  }
});

connect();
