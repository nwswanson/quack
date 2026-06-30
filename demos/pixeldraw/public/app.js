const DEFAULT_WIDTH = 48;
const DEFAULT_HEIGHT = 48;
const SPRAY_DROPS = 2;
const SPRAY_MOVE_BOOST = 1;
const SPRAY_MAX_BOOST = 2;
const SPRAY_SIZE = 4;
const SPRAY_INTERVAL_MS = 125;
const EXPORT_FORMAT = "pixeldraw-buffer-v1";
const MAX_LOAD_BATCH_PIXELS = 512;
const MAX_NAMESPACE_LENGTH = 64;
const MAX_DRAWING_NAME_LENGTH = 48;
const NAMESPACE_PATTERN = /^[A-Za-z0-9_.-]+$/;

const canvas = document.querySelector("#canvas");
const ctx = canvas.getContext("2d");
const statusEl = document.querySelector("#status");
const statusText = document.querySelector("#statusText");
const revisionEl = document.querySelector("#revision");
const gridSizeEl = document.querySelector("#gridSize");
const palette = document.querySelector("#palette");
const brushes = document.querySelector("#brushes");
const tabsEl = document.querySelector("#tabs");
const namespaceForm = document.querySelector("#namespaceForm");
const namespaceInput = document.querySelector("#namespaceInput");
const drawingFileInput = document.querySelector("#drawingFile");
const saveDrawingButton = document.querySelector("#saveDrawing");
const loadDrawingButton = document.querySelector("#loadDrawing");
const renameDrawingButton = document.querySelector("#renameDrawing");
const newDrawingButton = document.querySelector("#newDrawing");
const deleteDrawingButton = document.querySelector("#deleteDrawing");

const initialRoute = readRoute();

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
  requestedDrawingId: initialRoute.drawingId,
  namespace: initialRoute.namespace,
  drawings: [],
  drawingNames: new Map(),
  revision: 0,
  socket: null,
  connectionGeneration: 0,
  retryTimer: 0,
  statusTimer: 0,
  flushTimer: 0,
  sprayTimer: 0,
  drawing: false,
  lastCell: null,
  sprayCell: null,
  sprayBoost: 0,
  pending: new Map(),
  pendingLoadedDrawing: null,
};

function socketUrl() {
  const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
  const url = new URL(`${scheme}//${window.location.host}/ws`);
  if (state.namespace) {
    url.searchParams.set("ns", state.namespace);
    if (state.requestedDrawingId) {
      url.searchParams.set("tab", state.requestedDrawingId);
    }
  }
  return url.toString();
}

function readRoute() {
  const params = new URLSearchParams(window.location.search);
  return {
    namespace: normalizeNamespace(params.get("ns") || params.get("namespace") || ""),
    drawingId: params.get("tab") || "",
  };
}

function normalizeNamespace(value) {
  const namespace = String(value || "").trim().slice(0, MAX_NAMESPACE_LENGTH);
  if (!namespace || !NAMESPACE_PATTERN.test(namespace)) return "";
  return namespace;
}

function cleanDrawingName(value) {
  return String(value || "")
    .replace(/[\r\n\t]+/g, " ")
    .slice(0, MAX_DRAWING_NAME_LENGTH);
}

function messageWithContext(message) {
  if (state.namespace) {
    return { ...message, namespace: state.namespace };
  }
  return message;
}

function sendMessage(message) {
  if (!isOpen()) return;
  state.socket.send(JSON.stringify(messageWithContext(message)));
}

function updateRoute(drawingId = state.drawingId) {
  const url = new URL(window.location.href);
  if (state.namespace) {
    url.searchParams.set("ns", state.namespace);
  } else {
    url.searchParams.delete("ns");
    url.searchParams.delete("namespace");
  }
  if (drawingId) {
    url.searchParams.set("tab", drawingId);
  } else {
    url.searchParams.delete("tab");
  }
  window.history.replaceState(null, "", url);
}

function applyNamespace(namespace) {
  if (namespace === state.namespace && isOpen()) {
    namespaceInput.value = state.namespace;
    updateRoute(state.drawingId);
    return;
  }
  state.namespace = namespace;
  state.requestedDrawingId = "";
  state.drawingId = "";
  state.drawings = [];
  state.drawingNames = new Map();
  state.pending.clear();
  updateRoute("");
  connect();
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

function setDrawings(drawings, drawingTabs = []) {
  const ids = [];
  const names = new Map();
  const seen = new Set();

  const addTab = (id, name = "") => {
    if (typeof id !== "string" || !id || seen.has(id)) return;
    seen.add(id);
    ids.push(id);
    if (typeof name === "string" && name) {
      names.set(id, cleanDrawingName(name));
    }
  };

  if (Array.isArray(drawingTabs)) {
    for (const tab of drawingTabs) {
      if (typeof tab === "string") {
        addTab(tab);
      } else if (tab && typeof tab === "object") {
        addTab(tab.id, tab.name);
      }
    }
  }
  if (Array.isArray(drawings)) {
    for (const drawing of drawings) {
      if (typeof drawing === "string") {
        addTab(drawing);
      } else if (drawing && typeof drawing === "object") {
        addTab(drawing.id, drawing.name);
      }
    }
  }

  state.drawings = ids;
  state.drawingNames = names;
  renderTabs();
}

function renderTabs() {
  tabsEl.replaceChildren();
  for (const drawingId of state.drawings) {
    const name = state.drawingNames.get(drawingId) || "";
    const button = document.createElement("button");
    button.className = "tab";
    button.type = "button";
    button.dataset.drawingId = drawingId;
    button.textContent = name || `tab ${drawingId.slice(0, 5)}`;
    button.title = name ? `${name} (${drawingId})` : drawingId;
    button.setAttribute("role", "tab");
    button.setAttribute("aria-selected", drawingId === state.drawingId ? "true" : "false");
    if (drawingId === state.drawingId) {
      button.classList.add("is-active");
    }
    tabsEl.append(button);
  }
  saveDrawingButton.disabled = !state.drawingId;
  loadDrawingButton.disabled = !state.drawingId;
  renameDrawingButton.disabled = !state.drawingId;
  deleteDrawingButton.disabled = state.drawings.length <= 1 || !state.drawingId;
}

function connect() {
  window.clearTimeout(state.retryTimer);
  state.retryTimer = 0;
  const generation = state.connectionGeneration + 1;
  state.connectionGeneration = generation;
  if (state.socket) {
    state.socket.close(1000, "replaced");
  }
  setStatus("closed", "Connecting");
  namespaceInput.value = state.namespace;

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
    if (state.socket === ws && state.connectionGeneration === generation) {
      setStatus("closed", "Reconnecting");
      state.retryTimer = window.setTimeout(() => {
        if (state.socket === ws && state.connectionGeneration === generation) {
          connect();
        }
      }, 900);
    }
  });

  ws.addEventListener("error", () => {
    ws.close();
  });
}

function handleMessage(msg) {
  if (msg.type === "canvas_snapshot") {
    if (typeof msg.namespace === "string" && msg.namespace !== state.namespace) return;
    state.width = msg.width || DEFAULT_WIDTH;
    state.height = msg.height || DEFAULT_HEIGHT;
    state.drawingId = typeof msg.drawing_id === "string" ? msg.drawing_id : "";
    state.requestedDrawingId = state.drawingId;
    setDrawings(msg.drawings, msg.drawing_tabs);
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
    updateRoute();
    if (msg.missing_drawing_id) {
      flashStatus("Tab not found");
    } else if (state.pendingLoadedDrawing) {
      const drawing = state.pendingLoadedDrawing;
      state.pendingLoadedDrawing = null;
      applyLoadedDrawing(drawing);
    }
    return;
  }

  if (msg.type === "drawings_changed") {
    if (typeof msg.namespace === "string" && msg.namespace !== state.namespace) return;
    setDrawings(msg.drawings, msg.drawing_tabs);
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
  state.requestedDrawingId = drawingId;
  updateRoute(drawingId);
  sendMessage({ type: "get_drawing", drawing_id: drawingId });
}

function serializeCurrentDrawing() {
  return JSON.stringify({
    format: EXPORT_FORMAT,
    width: state.width,
    height: state.height,
    drawing_id: state.drawingId,
    name: state.drawingNames.get(state.drawingId) || "",
    pixels: state.pixels,
  }, null, 2);
}

function filenameLabel(value) {
  const label = String(value || "")
    .trim()
    .replace(/[^A-Za-z0-9_.-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 64);
  return label || "drawing";
}

function saveCurrentDrawing() {
  if (!state.drawingId) return;
  flushPixels();
  const text = serializeCurrentDrawing();
  const blob = new Blob([text], { type: "application/json;charset=utf-8" });
  const link = document.createElement("a");
  const drawingLabel = filenameLabel(state.drawingNames.get(state.drawingId) || state.drawingId.slice(0, 8));
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
  return { name: cleanDrawingName(body.name), pixels };
}

function loadedDrawingName(file, drawing) {
  if (drawing.name) return drawing.name;
  const stem = file.name.replace(/\.[^.]+$/, "").replace(/^pixeldraw[-_]?/i, "");
  return cleanDrawingName(stem);
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
    sendMessage({
      type: "draw_pixels",
      drawing_id: state.drawingId,
      pixels: changed.slice(i, i + MAX_LOAD_BATCH_PIXELS),
    });
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
  sendMessage({
    type: "draw_pixels",
    drawing_id: state.drawingId,
    pixels,
  });
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

tabsEl.addEventListener("dblclick", (event) => {
  const button = event.target.closest(".tab");
  if (!button) return;
  renameDrawing(button.dataset.drawingId);
});

namespaceForm.addEventListener("submit", (event) => {
  event.preventDefault();
  const namespace = normalizeNamespace(namespaceInput.value);
  if (!namespaceInput.value.trim()) {
    applyNamespace("");
    return;
  }
  if (!namespace) {
    flashStatus("Bad namespace");
    window.alert("Use letters, numbers, underscores, hyphens, or periods for namespaces.");
    return;
  }
  applyNamespace(namespace);
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
    drawing.name = loadedDrawingName(file, drawing);
    flushPixels();
    state.pendingLoadedDrawing = drawing;
    sendMessage({ type: "create_drawing", name: drawing.name });
  } catch (error) {
    state.pendingLoadedDrawing = null;
    flashStatus("Load failed");
    window.alert(error.message);
  }
});

newDrawingButton.addEventListener("click", () => {
  if (!isOpen()) return;
  flushPixels();
  sendMessage({ type: "create_drawing" });
});

function renameDrawing(drawingId = state.drawingId) {
  if (!isOpen() || !drawingId) return;
  const currentName = state.drawingNames.get(drawingId) || "";
  const nextName = window.prompt("Tab name", currentName);
  if (nextName === null) return;
  sendMessage({ type: "rename_drawing", drawing_id: drawingId, name: cleanDrawingName(nextName) });
}

renameDrawingButton.addEventListener("click", () => renameDrawing());

deleteDrawingButton.addEventListener("click", () => {
  if (!isOpen() || !state.drawingId || state.drawings.length <= 1) return;
  state.pending.clear();
  sendMessage({ type: "delete_drawing", drawing_id: state.drawingId });
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
