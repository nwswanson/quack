const DEFAULT_WIDTH = 48;
const DEFAULT_HEIGHT = 48;
const SPRAY_DROPS = 2;
const SPRAY_MOVE_BOOST = 1;
const SPRAY_MAX_BOOST = 2;
const SPRAY_SIZE = 4;
const SPRAY_INTERVAL_MS = 125;
const EXPORT_FORMAT = "pixeldraw-buffer-v1";
const MAX_LOAD_BATCH_PIXELS = 512;

const canvas = document.querySelector("#canvas");
const ctx = canvas.getContext("2d");
const statusEl = document.querySelector("#status");
const statusText = document.querySelector("#statusText");
const revisionEl = document.querySelector("#revision");
const gridSizeEl = document.querySelector("#gridSize");
const palette = document.querySelector("#palette");
const brushes = document.querySelector("#brushes");
const tabsEl = document.querySelector("#tabs");
const drawingFileInput = document.querySelector("#drawingFile");
const saveDrawingButton = document.querySelector("#saveDrawing");
const loadDrawingButton = document.querySelector("#loadDrawing");
const newDrawingButton = document.querySelector("#newDrawing");
const deleteDrawingButton = document.querySelector("#deleteDrawing");

const state = {
  width: DEFAULT_WIDTH,
  height: DEFAULT_HEIGHT,
  pixels: new Array(DEFAULT_WIDTH * DEFAULT_HEIGHT).fill(0),
  colors: [],
  colorsByCode: new Map(),
  color: 0,
  brush: "square",
  brushSize: 1,
  drawingId: "",
  drawings: [],
  revision: 0,
  socket: null,
  retryTimer: 0,
  statusTimer: 0,
  flushTimer: 0,
  sprayTimer: 0,
  drawing: false,
  lastCell: null,
  sprayCell: null,
  sprayBoost: 0,
  pending: new Map(),
};

function socketUrl() {
  const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${window.location.host}/ws`;
}

function setStatus(mode, label) {
  window.clearTimeout(state.statusTimer);
  state.statusTimer = 0;
  statusEl.dataset.state = mode;
  statusText.textContent = label;
}

function flashStatus(label) {
  if (!isOpen()) return;
  window.clearTimeout(state.statusTimer);
  statusEl.dataset.state = "open";
  statusText.textContent = label;
  state.statusTimer = window.setTimeout(() => {
    statusEl.dataset.state = "open";
    statusText.textContent = "Live";
    state.statusTimer = 0;
  }, 1400);
}

function colorHex(code) {
  return state.colorsByCode.get(code)?.hex || "#ffffff";
}

function colorLabel(color) {
  return color.id
    .split("_")
    .map((part) => part.slice(0, 1).toUpperCase() + part.slice(1))
    .join(" ");
}

async function loadColors() {
  const response = await fetch("/api/colors", { headers: { accept: "application/json" } });
  if (!response.ok) {
    throw new Error(`colors request failed with ${response.status}`);
  }
  const body = await response.json();
  const colors = Array.isArray(body.colors) ? body.colors : [];
  state.colors = colors
    .filter((color) => Number.isInteger(color.code) && typeof color.hex === "string")
    .sort((a, b) => a.code - b.code);
  state.colorsByCode = new Map(state.colors.map((color) => [color.code, color]));
  state.color = state.colors[0]?.code ?? 0;
  renderPalette();
}

function renderPalette() {
  palette.replaceChildren();
  for (const color of state.colors) {
    const button = document.createElement("button");
    button.className = "swatch";
    button.type = "button";
    button.dataset.color = String(color.code);
    button.setAttribute("role", "radio");
    button.setAttribute("aria-checked", color.code === state.color ? "true" : "false");
    button.title = colorLabel(color);

    const chip = document.createElement("span");
    chip.style.background = color.hex;
    button.append(chip);
    if (color.code === state.color) {
      button.classList.add("is-active");
    }
    palette.append(button);
  }
}

function setDrawings(drawings) {
  state.drawings = Array.isArray(drawings)
    ? drawings.filter((id) => typeof id === "string" && id.length > 0)
    : [];
  renderTabs();
}

function renderTabs() {
  tabsEl.replaceChildren();
  for (const drawingId of state.drawings) {
    const button = document.createElement("button");
    button.className = "tab";
    button.type = "button";
    button.dataset.drawingId = drawingId;
    button.textContent = `tab ${drawingId.slice(0, 5)}`;
    button.setAttribute("role", "tab");
    button.setAttribute("aria-selected", drawingId === state.drawingId ? "true" : "false");
    if (drawingId === state.drawingId) {
      button.classList.add("is-active");
    }
    tabsEl.append(button);
  }
  saveDrawingButton.disabled = !state.drawingId;
  loadDrawingButton.disabled = !state.drawingId;
  deleteDrawingButton.disabled = state.drawings.length <= 1 || !state.drawingId;
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
    state.drawingId = typeof msg.drawing_id === "string" ? msg.drawing_id : "";
    setDrawings(msg.drawings);
    state.pending.clear();
    state.pixels = new Array(state.width * state.height).fill(0);
    if (Array.isArray(msg.pixels)) {
      for (const pixel of msg.pixels) {
        const color = normalizeColor(pixel.color);
        if (Number.isInteger(pixel.i) && state.colorsByCode.has(color)) {
          state.pixels[pixel.i] = color;
        }
      }
    }
    state.revision = msg.revision || 0;
    revisionEl.textContent = String(state.revision);
    gridSizeEl.textContent = `${state.width} x ${state.height}`;
    render();
    return;
  }

  if (msg.type === "drawings_changed") {
    setDrawings(msg.drawings);
    if (state.drawingId && !state.drawings.includes(state.drawingId)) {
      requestDrawing(state.drawings[0]);
    }
    return;
  }

  if (msg.type === "pixels_updated" && Array.isArray(msg.pixels)) {
    if (msg.drawing_id !== state.drawingId) return;
    for (const pixel of msg.pixels) {
      const color = normalizeColor(pixel.color);
      if (Number.isInteger(pixel.i) && state.colorsByCode.has(color)) {
        state.pixels[pixel.i] = color;
      }
    }
    state.revision = msg.revision || state.revision;
    revisionEl.textContent = String(state.revision);
    render();
  }
}

function normalizeColor(value) {
  return Number.isInteger(value) ? value : Number.parseInt(value, 10);
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

  ctx.fillStyle = colorHex(0);
  ctx.fillRect(0, 0, size, size);

  for (let y = 0; y < state.height; y += 1) {
    for (let x = 0; x < state.width; x += 1) {
      const color = state.pixels[y * state.width + x];
      if (color === 0) continue;
      ctx.fillStyle = colorHex(color);
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

function requestDrawing(drawingId) {
  if (!isOpen() || !drawingId || drawingId === state.drawingId) return;
  state.pending.clear();
  state.socket.send(JSON.stringify({ type: "get_drawing", drawing_id: drawingId }));
}

function serializeCurrentDrawing() {
  return JSON.stringify({
    format: EXPORT_FORMAT,
    width: state.width,
    height: state.height,
    drawing_id: state.drawingId,
    pixels: state.pixels,
  }, null, 2);
}

function saveCurrentDrawing() {
  if (!state.drawingId) return;
  flushPixels();
  const text = serializeCurrentDrawing();
  const blob = new Blob([text], { type: "application/json;charset=utf-8" });
  const link = document.createElement("a");
  const drawingLabel = state.drawingId ? state.drawingId.slice(0, 8) : "drawing";
  link.href = URL.createObjectURL(blob);
  link.download = `pixeldraw-${drawingLabel}.json`;
  document.body.append(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(link.href);
  flashStatus("Saved");
}

function parseLoadedDrawing(text) {
  let body;
  try {
    body = JSON.parse(text);
  } catch (error) {
    throw new Error("Drawing file is not valid JSON");
  }

  if (!body || body.format !== EXPORT_FORMAT) {
    throw new Error(`Drawing file must use ${EXPORT_FORMAT}`);
  }
  if (body.width !== state.width || body.height !== state.height) {
    throw new Error(`Drawing is ${body.width} x ${body.height}; this canvas is ${state.width} x ${state.height}`);
  }
  if (!Array.isArray(body.pixels) || body.pixels.length !== state.width * state.height) {
    throw new Error("Drawing pixel buffer has the wrong length");
  }

  const pixels = body.pixels.map((value) => {
    const color = normalizeColor(value);
    if (!state.colorsByCode.has(color)) {
      throw new Error(`Drawing contains unknown color ${value}`);
    }
    return color;
  });
  return { pixels };
}

function applyLoadedDrawing(drawing) {
  if (!isOpen() || !state.drawingId) return;
  state.pending.clear();

  const changed = [];
  for (let i = 0; i < drawing.pixels.length; i += 1) {
    const color = drawing.pixels[i];
    if (state.pixels[i] === color) continue;
    state.pixels[i] = color;
    changed.push({
      x: i % state.width,
      y: Math.floor(i / state.width),
      color,
    });
  }

  render();
  for (let i = 0; i < changed.length; i += MAX_LOAD_BATCH_PIXELS) {
    state.socket.send(JSON.stringify({
      type: "draw_pixels",
      drawing_id: state.drawingId,
      pixels: changed.slice(i, i + MAX_LOAD_BATCH_PIXELS),
    }));
  }
  flashStatus(changed.length > 0 ? "Loaded" : "Already loaded");
}

function paintCell(x, y) {
  if (!isOpen() || !state.drawingId) return;
  const i = y * state.width + x;
  state.pixels[i] = state.color;
  state.pending.set(i, { x, y, color: state.color });
}

function paintSquareStamp(x, y) {
  const offset = Math.floor((state.brushSize - 1) / 2);
  const startX = x - offset;
  const startY = y - offset;

  for (let stampY = startY; stampY < startY + state.brushSize; stampY += 1) {
    if (stampY < 0 || stampY >= state.height) continue;
    for (let stampX = startX; stampX < startX + state.brushSize; stampX += 1) {
      if (stampX < 0 || stampX >= state.width) continue;
      paintCell(stampX, stampY);
    }
  }
}

function paintRoundStamp(x, y) {
  const offset = Math.floor((state.brushSize - 1) / 2);
  const startX = x - offset;
  const startY = y - offset;
  const center = (state.brushSize - 1) / 2;
  const radius = center + 0.25;

  for (let stampY = 0; stampY < state.brushSize; stampY += 1) {
    const yDistance = stampY - center;
    for (let stampX = 0; stampX < state.brushSize; stampX += 1) {
      const xDistance = stampX - center;
      if (Math.hypot(xDistance, yDistance) > radius) continue;

      const paintX = startX + stampX;
      const paintY = startY + stampY;
      if (paintX < 0 || paintX >= state.width || paintY < 0 || paintY >= state.height) continue;
      paintCell(paintX, paintY);
    }
  }
}

function paintSprayStamp(x, y, drops = SPRAY_DROPS) {
  const offset = Math.floor((SPRAY_SIZE - 1) / 2);
  const startX = x - offset;
  const startY = y - offset;

  for (let i = 0; i < drops; i += 1) {
    const sprayX = startX + Math.floor(Math.random() * SPRAY_SIZE);
    const sprayY = startY + Math.floor(Math.random() * SPRAY_SIZE);
    if (sprayX < 0 || sprayX >= state.width || sprayY < 0 || sprayY >= state.height) continue;
    paintCell(sprayX, sprayY);
  }
}

function paintStamp(x, y, sprayDrops = SPRAY_DROPS) {
  if (state.brush === "spray") {
    paintSprayStamp(x, y, sprayDrops);
  } else if (state.brush === "round") {
    paintRoundStamp(x, y);
  } else {
    paintSquareStamp(x, y);
  }
  scheduleFlush();
  render();
}

function paintLine(from, to) {
  if (!from) {
    paintStamp(to.x, to.y);
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
    paintStamp(x0, y0);
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
  if (!isOpen() || state.pending.size === 0 || !state.drawingId) return;
  const pixels = Array.from(state.pending.values());
  state.pending.clear();
  state.socket.send(JSON.stringify({
    type: "draw_pixels",
    drawing_id: state.drawingId,
    pixels,
  }));
}

function startSpray(cell) {
  if (state.brush !== "spray") return;
  state.sprayCell = cell;
  window.clearInterval(state.sprayTimer);
  state.sprayTimer = window.setInterval(() => {
    if (!state.drawing || !state.sprayCell) return;
    paintStamp(state.sprayCell.x, state.sprayCell.y, SPRAY_DROPS + state.sprayBoost);
    state.sprayBoost = Math.max(0, state.sprayBoost - 1);
  }, SPRAY_INTERVAL_MS);
}

function stopSpray() {
  window.clearInterval(state.sprayTimer);
  state.sprayTimer = 0;
  state.sprayCell = null;
  state.sprayBoost = 0;
}

palette.addEventListener("click", (event) => {
  const button = event.target.closest(".swatch");
  if (!button) return;

  const color = normalizeColor(button.dataset.color);
  if (!state.colorsByCode.has(color)) return;
  state.color = color;
  for (const swatch of palette.querySelectorAll(".swatch")) {
    const active = swatch === button;
    swatch.classList.toggle("is-active", active);
    swatch.setAttribute("aria-checked", active ? "true" : "false");
  }
});

brushes.addEventListener("click", (event) => {
  const button = event.target.closest(".brush-button");
  if (!button) return;

  const brush = button.dataset.brush;
  const size = Number.parseInt(button.dataset.size, 10);
  if (!["square", "round", "spray"].includes(brush) || ![1, 2, 4].includes(size)) return;
  state.brush = brush;
  state.brushSize = size;
  for (const brush of brushes.querySelectorAll(".brush-button")) {
    const active = brush === button;
    brush.classList.toggle("is-active", active);
    brush.setAttribute("aria-checked", active ? "true" : "false");
  }
});

tabsEl.addEventListener("click", (event) => {
  const button = event.target.closest(".tab");
  if (!button) return;
  flushPixels();
  requestDrawing(button.dataset.drawingId);
});

saveDrawingButton.addEventListener("click", saveCurrentDrawing);

loadDrawingButton.addEventListener("click", () => {
  if (!state.drawingId) return;
  drawingFileInput.value = "";
  drawingFileInput.click();
});

drawingFileInput.addEventListener("change", async () => {
  const [file] = drawingFileInput.files || [];
  if (!file || !state.drawingId) return;

  try {
    const drawing = parseLoadedDrawing(await file.text());
    flushPixels();
    applyLoadedDrawing(drawing);
  } catch (error) {
    flashStatus("Load failed");
    window.alert(error.message);
  }
});

newDrawingButton.addEventListener("click", () => {
  if (!isOpen()) return;
  flushPixels();
  state.socket.send(JSON.stringify({ type: "create_drawing" }));
});

deleteDrawingButton.addEventListener("click", () => {
  if (!isOpen() || !state.drawingId || state.drawings.length <= 1) return;
  state.pending.clear();
  state.socket.send(JSON.stringify({ type: "delete_drawing", drawing_id: state.drawingId }));
});

canvas.addEventListener("pointerdown", (event) => {
  const cell = cellFromEvent(event);
  if (!cell) return;
  event.preventDefault();
  canvas.setPointerCapture(event.pointerId);
  state.drawing = true;
  state.lastCell = cell;
  paintStamp(cell.x, cell.y);
  startSpray(cell);
});

canvas.addEventListener("pointermove", (event) => {
  if (!state.drawing) return;
  const cell = cellFromEvent(event);
  if (!cell) return;
  event.preventDefault();
  if (state.brush === "spray") {
    const movedCell = !state.lastCell || state.lastCell.x !== cell.x || state.lastCell.y !== cell.y;
    state.lastCell = cell;
    state.sprayCell = cell;
    if (movedCell) {
      state.sprayBoost = Math.min(SPRAY_MAX_BOOST, state.sprayBoost + SPRAY_MOVE_BOOST);
    }
    return;
  }
  paintLine(state.lastCell, cell);
  state.lastCell = cell;
});

function endStroke() {
  if (!state.drawing) return;
  state.drawing = false;
  state.lastCell = null;
  stopSpray();
  flushPixels();
}

canvas.addEventListener("pointerup", endStroke);
canvas.addEventListener("pointercancel", endStroke);
canvas.addEventListener("lostpointercapture", endStroke);

async function init() {
  try {
    await loadColors();
  } catch (error) {
    setStatus("closed", "Colors failed");
    return;
  }
  new ResizeObserver(resizeCanvas).observe(canvas);
  resizeCanvas();
  connect();
}

init();
