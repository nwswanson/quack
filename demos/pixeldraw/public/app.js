const DEFAULT_WIDTH = 48;
const DEFAULT_HEIGHT = 48;
const COLORS = {
  white: "#ffffff",
};

const canvas = document.querySelector("#canvas");
const ctx = canvas.getContext("2d");
const statusEl = document.querySelector("#status");
const statusText = document.querySelector("#statusText");
const revisionEl = document.querySelector("#revision");
const gridSizeEl = document.querySelector("#gridSize");
const palette = document.querySelector("#palette");

function loadPaletteColors() {
  for (const swatch of palette.querySelectorAll(".swatch")) {
    const color = swatch.dataset.color;
    const chip = swatch.querySelector("span");
    const value = chip && (chip.getAttribute("style") || "").match(/background:\s*([^;]+)/);
    if (color && value) {
      COLORS[color] = value[1].trim();
    }
  }
}

const state = {
  width: DEFAULT_WIDTH,
  height: DEFAULT_HEIGHT,
  pixels: new Array(DEFAULT_WIDTH * DEFAULT_HEIGHT).fill("white"),
  color: "white",
  revision: 0,
  socket: null,
  retryTimer: 0,
  flushTimer: 0,
  drawing: false,
  lastCell: null,
  pending: new Map(),
};

function socketUrl() {
  const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${window.location.host}/ws`;
}

function setStatus(mode, label) {
  statusEl.dataset.state = mode;
  statusText.textContent = label;
}

function connect() {
  window.clearTimeout(state.retryTimer);
  setStatus("closed", "Connecting");

  const ws = new WebSocket(socketUrl());
  state.socket = ws;

  ws.addEventListener("open", () => {
    setStatus("open", "Live");
  });

  ws.addEventListener("message", (event) => {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch {
      return;
    }
    handleMessage(msg);
  });

  ws.addEventListener("close", () => {
    if (state.socket === ws) {
      setStatus("closed", "Reconnecting");
      state.retryTimer = window.setTimeout(connect, 900);
    }
  });

  ws.addEventListener("error", () => {
    ws.close();
  });
}

function handleMessage(msg) {
  if (msg.type === "canvas_snapshot") {
    state.width = msg.width || DEFAULT_WIDTH;
    state.height = msg.height || DEFAULT_HEIGHT;
    state.pixels = new Array(state.width * state.height).fill("white");
    if (Array.isArray(msg.pixels)) {
      for (const pixel of msg.pixels) {
        if (Number.isInteger(pixel.i) && COLORS[pixel.color]) {
          state.pixels[pixel.i] = pixel.color;
        }
      }
    }
    state.revision = msg.revision || 0;
    revisionEl.textContent = String(state.revision);
    gridSizeEl.textContent = `${state.width} x ${state.height}`;
    render();
    return;
  }

  if (msg.type === "pixels_updated" && Array.isArray(msg.pixels)) {
    for (const pixel of msg.pixels) {
      if (Number.isInteger(pixel.i) && COLORS[pixel.color]) {
        state.pixels[pixel.i] = pixel.color;
      }
    }
    state.revision = msg.revision || state.revision;
    revisionEl.textContent = String(state.revision);
    render();
  }
}

function resizeCanvas() {
  const rect = canvas.getBoundingClientRect();
  const dpr = Math.max(1, window.devicePixelRatio || 1);
  const size = Math.max(320, Math.floor(rect.width * dpr));
  if (canvas.width !== size || canvas.height !== size) {
    canvas.width = size;
    canvas.height = size;
    render();
  }
}

function render() {
  const size = canvas.width;
  const cell = size / state.width;

  ctx.fillStyle = COLORS.white;
  ctx.fillRect(0, 0, size, size);

  for (let y = 0; y < state.height; y += 1) {
    for (let x = 0; x < state.width; x += 1) {
      const color = state.pixels[y * state.width + x];
      if (color === "white") continue;
      ctx.fillStyle = COLORS[color] || COLORS.white;
      ctx.fillRect(Math.floor(x * cell), Math.floor(y * cell), Math.ceil(cell), Math.ceil(cell));
    }
  }

  ctx.beginPath();
  ctx.strokeStyle = "rgba(90, 103, 114, 0.42)";
  ctx.lineWidth = Math.max(1, Math.floor(size / 900));
  for (let i = 0; i <= state.width; i += 1) {
    const p = Math.round(i * cell) + 0.5;
    ctx.moveTo(p, 0);
    ctx.lineTo(p, size);
    ctx.moveTo(0, p);
    ctx.lineTo(size, p);
  }
  ctx.stroke();
}

function cellFromEvent(event) {
  const rect = canvas.getBoundingClientRect();
  const x = Math.floor(((event.clientX - rect.left) / rect.width) * state.width);
  const y = Math.floor(((event.clientY - rect.top) / rect.height) * state.height);
  if (x < 0 || x >= state.width || y < 0 || y >= state.height) return null;
  return { x, y };
}

function isOpen() {
  return state.socket && state.socket.readyState === WebSocket.OPEN;
}

function paintCell(x, y) {
  if (!isOpen()) return;
  const i = y * state.width + x;
  state.pixels[i] = state.color;
  state.pending.set(i, { x, y, color: state.color });
  scheduleFlush();
  render();
}

function paintLine(from, to) {
  if (!from) {
    paintCell(to.x, to.y);
    return;
  }

  let x0 = from.x;
  let y0 = from.y;
  const x1 = to.x;
  const y1 = to.y;
  const dx = Math.abs(x1 - x0);
  const sx = x0 < x1 ? 1 : -1;
  const dy = -Math.abs(y1 - y0);
  const sy = y0 < y1 ? 1 : -1;
  let err = dx + dy;

  for (;;) {
    paintCell(x0, y0);
    if (x0 === x1 && y0 === y1) break;
    const e2 = 2 * err;
    if (e2 >= dy) {
      err += dy;
      x0 += sx;
    }
    if (e2 <= dx) {
      err += dx;
      y0 += sy;
    }
  }
}

function scheduleFlush() {
  if (state.flushTimer) return;
  state.flushTimer = window.setTimeout(flushPixels, 35);
}

function flushPixels() {
  state.flushTimer = 0;
  if (!isOpen() || state.pending.size === 0) return;
  const pixels = Array.from(state.pending.values());
  state.pending.clear();
  state.socket.send(JSON.stringify({ type: "draw_pixels", pixels }));
}

palette.addEventListener("click", (event) => {
  const button = event.target.closest(".swatch");
  if (!button) return;

  state.color = button.dataset.color;
  for (const swatch of palette.querySelectorAll(".swatch")) {
    const active = swatch === button;
    swatch.classList.toggle("is-active", active);
    swatch.setAttribute("aria-checked", active ? "true" : "false");
  }
});

canvas.addEventListener("pointerdown", (event) => {
  const cell = cellFromEvent(event);
  if (!cell) return;
  event.preventDefault();
  canvas.setPointerCapture(event.pointerId);
  state.drawing = true;
  state.lastCell = cell;
  paintCell(cell.x, cell.y);
});

canvas.addEventListener("pointermove", (event) => {
  if (!state.drawing) return;
  const cell = cellFromEvent(event);
  if (!cell) return;
  event.preventDefault();
  paintLine(state.lastCell, cell);
  state.lastCell = cell;
});

function endStroke() {
  if (!state.drawing) return;
  state.drawing = false;
  state.lastCell = null;
  flushPixels();
}

canvas.addEventListener("pointerup", endStroke);
canvas.addEventListener("pointercancel", endStroke);
canvas.addEventListener("lostpointercapture", endStroke);

loadPaletteColors();
new ResizeObserver(resizeCanvas).observe(canvas);
resizeCanvas();
connect();
