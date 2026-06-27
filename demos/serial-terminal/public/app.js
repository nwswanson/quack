const state = {
  devices: [],
  selected: "",
  connected: false,
  locked: false,
  status: "idle",
  settings: {
    line_ending: "lf",
    until: "\n",
    timeout_ms: 1000,
  },
  socket: null,
  reconnectTimer: 0,
  reconnectDelay: 600,
  pollTimer: 0,
  localSettingsWrite: false,
};

const els = {
  statePill: document.getElementById("statePill"),
  stateText: document.getElementById("stateText"),
  deviceSelect: document.getElementById("deviceSelect"),
  refreshBtn: document.getElementById("refreshBtn"),
  connectBtn: document.getElementById("connectBtn"),
  quitBtn: document.getElementById("quitBtn"),
  terminalTitle: document.getElementById("terminalTitle"),
  terminal: document.getElementById("terminal"),
  commandForm: document.getElementById("commandForm"),
  commandInput: document.getElementById("commandInput"),
  sendBtn: document.getElementById("sendBtn"),
  lineEnding: document.getElementById("lineEnding"),
  untilInput: document.getElementById("untilInput"),
  timeoutInput: document.getElementById("timeoutInput"),
  debugLog: document.getElementById("debugLog"),
  clearTerminalBtn: document.getElementById("clearTerminalBtn"),
  clearDebugBtn: document.getElementById("clearDebugBtn"),
};

function socketUrl() {
  const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${window.location.host}/ws`;
}

function isOpen() {
  return state.socket && state.socket.readyState === WebSocket.OPEN;
}

function send(message) {
  if (!isOpen()) return;
  state.socket.send(JSON.stringify(message));
}

function decodeEscapes(value) {
  return String(value || "")
    .replaceAll("\\r", "\r")
    .replaceAll("\\n", "\n")
    .replaceAll("\\t", "\t")
    .replaceAll("\\0", "\0");
}

function encodeEscapes(value) {
  return String(value ?? "")
    .replaceAll("\\", "\\\\")
    .replaceAll("\r", "\\r")
    .replaceAll("\n", "\\n")
    .replaceAll("\t", "\\t")
    .replaceAll("\0", "\\0");
}

function stamp(value) {
  if (value) {
    const parsed = new Date(value);
    if (!Number.isNaN(parsed.valueOf())) return parsed.toLocaleTimeString();
  }
  return new Date().toLocaleTimeString();
}

function setStatus(kind, text) {
  els.statePill.dataset.state = kind;
  els.stateText.textContent = text;
}

function renderControls() {
  const socketReady = isOpen();
  const hasDevice = Boolean(state.selected);
  const locked = state.locked || state.connected;

  els.deviceSelect.disabled = !socketReady || locked;
  els.refreshBtn.disabled = !socketReady || locked;
  els.connectBtn.disabled = !socketReady || !hasDevice || locked;
  els.quitBtn.disabled = !socketReady || !state.connected;
  els.commandInput.disabled = !socketReady || !state.connected;
  els.sendBtn.disabled = !socketReady || !state.connected;
  els.lineEnding.disabled = !socketReady || locked;
  els.untilInput.disabled = !socketReady || locked;
  els.timeoutInput.disabled = !socketReady || locked;
}

function renderDevices() {
  const previous = els.deviceSelect.value;
  els.deviceSelect.innerHTML = "";

  if (state.devices.length === 0) {
    const option = document.createElement("option");
    option.value = "";
    option.textContent = "No serial devices bound";
    els.deviceSelect.appendChild(option);
  } else {
    for (const device of state.devices) {
      const alias = device.alias || device.id;
      const option = document.createElement("option");
      option.value = alias;
      option.textContent = device.label ? `${device.label} (${alias})` : alias;
      els.deviceSelect.appendChild(option);
    }
  }

  els.deviceSelect.value = state.selected || previous || "";
}

function renderSettings() {
  state.localSettingsWrite = true;
  els.lineEnding.value = state.settings.line_ending || "lf";
  els.untilInput.value = encodeEscapes(state.settings.until ?? "\n");
  els.timeoutInput.value = String(state.settings.timeout_ms ?? 1000);
  state.localSettingsWrite = false;
}

function renderState() {
  renderDevices();
  renderSettings();

  const statusText = state.status || (state.connected ? "open" : "closed");
  if (!isOpen()) {
    setStatus("error", "Offline");
  } else if (state.connected) {
    setStatus("open", "Connected");
  } else if (state.devices.length === 0) {
    setStatus("idle", "No devices");
  } else {
    setStatus(statusText === "error" ? "error" : "closed", state.locked ? "Locked" : "Ready");
  }

  const lockLabel = state.locked ? "locked" : "editable";
  els.terminalTitle.textContent = state.selected
    ? `${state.selected} · ${statusText} · ${lockLabel}`
    : "No device connected";
  renderControls();
  updatePolling();
}

function appendTerminal(line) {
  if (!line || typeof line.text !== "string") return;
  const prefix = `[${stamp(line.at)}] `;
  const text = line.text.endsWith("\n") ? line.text : `${line.text}\n`;
  els.terminal.textContent += `${prefix}${text}`;
  els.terminal.scrollTop = els.terminal.scrollHeight;
}

function setTerminal(lines) {
  els.terminal.textContent = "";
  for (const line of Array.isArray(lines) ? lines : []) {
    appendTerminal(line);
  }
}

function appendDebug(entry) {
  if (!entry) return;
  els.debugLog.textContent += `${stamp(entry.at)} ${entry.kind || "event"}\n${JSON.stringify(entry.payload ?? {}, null, 2)}\n\n`;
  els.debugLog.scrollTop = els.debugLog.scrollHeight;
}

function setDebug(entries) {
  els.debugLog.textContent = "";
  for (const entry of Array.isArray(entries) ? entries : []) {
    appendDebug(entry);
  }
}

function applySharedState(next) {
  if (!next || typeof next !== "object") return;
  state.devices = Array.isArray(next.devices) ? next.devices : [];
  state.selected = next.selected || "";
  state.connected = Boolean(next.connected);
  state.locked = Boolean(next.locked);
  state.status = next.status || "";
  state.settings = {
    ...state.settings,
    ...(next.settings && typeof next.settings === "object" ? next.settings : {}),
  };
  renderState();
}

function handleMessage(event) {
  let message = {};
  try {
    message = JSON.parse(event.data);
  } catch (err) {
    appendDebug({ kind: "socket_error", payload: { message: event.data || err.message } });
    return;
  }

  if (message.type === "snapshot") {
    applySharedState(message.state);
    setTerminal(message.terminal);
    setDebug(message.debug);
    return;
  }
  if (message.type === "state") {
    applySharedState(message.state);
    return;
  }
  if (message.type === "terminal") {
    appendTerminal(message.line);
    return;
  }
  if (message.type === "debug") {
    appendDebug(message.entry);
    return;
  }
  if (message.type === "clear_terminal") {
    els.terminal.textContent = "";
    return;
  }
  if (message.type === "clear_debug") {
    els.debugLog.textContent = "";
    return;
  }
  if (message.type === "error") {
    appendTerminal({ kind: "error", text: `! ${message.message || "unknown error"}` });
    return;
  }
  if (message.type !== "ready") {
    appendDebug({ kind: "socket_message", payload: message });
  }
}

function connect() {
  window.clearTimeout(state.reconnectTimer);
  state.reconnectTimer = 0;

  if (state.socket) {
    state.socket.onopen = null;
    state.socket.onmessage = null;
    state.socket.onclose = null;
    state.socket.onerror = null;
    state.socket.close();
  }

  setStatus("idle", "Connecting");
  renderControls();
  const socket = new WebSocket(socketUrl());
  state.socket = socket;

  socket.addEventListener("open", () => {
    state.reconnectDelay = 600;
    send({ type: "snapshot" });
    renderControls();
  });

  socket.addEventListener("message", handleMessage);

  socket.addEventListener("close", () => {
    if (state.socket !== socket) return;
    stopPolling();
    setStatus("error", "Offline");
    renderControls();
    state.reconnectTimer = window.setTimeout(connect, state.reconnectDelay);
    state.reconnectDelay = Math.min(state.reconnectDelay * 1.7, 5000);
  });

  socket.addEventListener("error", () => {
    setStatus("error", "Socket error");
  });
}

function stopPolling() {
  window.clearInterval(state.pollTimer);
  state.pollTimer = 0;
}

function updatePolling() {
  if (state.connected && isOpen()) {
    if (!state.pollTimer) {
      state.pollTimer = window.setInterval(() => send({ type: "poll" }), 125);
    }
  } else {
    stopPolling();
  }
}

function syncSettings() {
  if (state.localSettingsWrite || state.locked || state.connected) return;
  send({
    type: "settings",
    settings: {
      line_ending: els.lineEnding.value,
      until: decodeEscapes(els.untilInput.value),
      timeout_ms: Number.parseInt(els.timeoutInput.value, 10) || 0,
    },
  });
}

els.deviceSelect.addEventListener("change", () => {
  send({ type: "select", device: els.deviceSelect.value });
});
els.refreshBtn.addEventListener("click", () => send({ type: "refresh" }));
els.connectBtn.addEventListener("click", () => send({ type: "open" }));
els.quitBtn.addEventListener("click", () => send({ type: "close" }));
els.commandForm.addEventListener("submit", (event) => {
  event.preventDefault();
  const text = els.commandInput.value;
  if (!state.connected || text.length === 0) return;
  els.commandInput.value = "";
  send({ type: "write", text });
  els.commandInput.focus();
});
els.lineEnding.addEventListener("change", syncSettings);
els.untilInput.addEventListener("change", syncSettings);
els.timeoutInput.addEventListener("change", syncSettings);
els.clearTerminalBtn.addEventListener("click", () => send({ type: "clear_terminal" }));
els.clearDebugBtn.addEventListener("click", () => send({ type: "clear_debug" }));

connect();
