const state = {
  devices: [],
  selected: "",
  connected: false,
  locked: false,
  status: "idle",
  settings: {
    line_ending: "lf",
  },
  socket: null,
  reconnectTimer: 0,
  reconnectDelay: 600,
  localSettingsWrite: false,
  terminalAtLineStart: true,
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
  clearTerminalBtn: document.getElementById("clearTerminalBtn"),
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
}

function appendTerminalText(text) {
  if (text === "") return;
  els.terminal.textContent += text;
  state.terminalAtLineStart = text.endsWith("\n");
}

function appendTerminalPrefix(line) {
  const action = line.action_id ? ` ${line.action_id}` : "";
  appendTerminalText(`[${stamp(line.at)}${action}] `);
}

function appendReadChunk(line) {
  const text = line.text;
  let start = 0;
  for (let i = 0; i < text.length; i += 1) {
    const char = text[i];
    if (char !== "\n" && char !== "\r") {
      continue;
    }
    if (start < i && state.terminalAtLineStart) {
      appendTerminalPrefix(line);
    }
    const end = char === "\r" && text[i + 1] === "\n" ? i + 2 : i + 1;
    appendTerminalText(text.slice(start, end));
    start = end;
    i = end - 1;
  }
  if (start < text.length) {
    if (state.terminalAtLineStart) {
      appendTerminalPrefix(line);
    }
    appendTerminalText(text.slice(start));
  }
}

function appendTerminalLine(line) {
  if (!state.terminalAtLineStart) {
    appendTerminalText("\n");
  }
  appendTerminalPrefix(line);
  appendTerminalText(line.text);
  if (!state.terminalAtLineStart) {
    appendTerminalText("\n");
  }
}

function appendTerminal(line) {
  if (!line || typeof line.text !== "string") return;
  if (line.kind === "read") {
    appendReadChunk(line);
  } else {
    appendTerminalLine(line);
  }
  els.terminal.scrollTop = els.terminal.scrollHeight;
}

function setTerminal(lines) {
  els.terminal.textContent = "";
  state.terminalAtLineStart = true;
  for (const line of Array.isArray(lines) ? lines : []) {
    appendTerminal(line);
  }
}

function logDebug(entry) {
  if (!entry) return;
  const action = entry.action_id ? ` ${entry.action_id}` : "";
  console.debug(
    `[serial-terminal-pipes] ${stamp(entry.at)} ${entry.kind || "event"}${action}`,
    entry.payload ?? {},
  );
}

function logDebugEntries(entries) {
  for (const entry of Array.isArray(entries) ? entries : []) {
    logDebug(entry);
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
    console.warn("[serial-terminal-pipes] socket_error", { message: event.data || err.message });
    return;
  }

  if (message.type === "snapshot") {
    applySharedState(message.state);
    setTerminal(message.terminal);
    logDebugEntries(message.debug);
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
    logDebug(message.entry);
    return;
  }
  if (message.type === "clear_terminal") {
    els.terminal.textContent = "";
    state.terminalAtLineStart = true;
    return;
  }
  if (message.type === "clear_debug") {
    console.debug("[serial-terminal-pipes] clear_debug");
    return;
  }
  if (message.type === "error") {
    appendTerminal({ kind: "error", text: `! ${message.message || "unknown error"}` });
    return;
  }
  if (message.type !== "ready") {
    logDebug({ kind: "socket_message", payload: message });
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
    setStatus("error", "Offline");
    renderControls();
    state.reconnectTimer = window.setTimeout(connect, state.reconnectDelay);
    state.reconnectDelay = Math.min(state.reconnectDelay * 1.7, 5000);
  });

  socket.addEventListener("error", () => {
    setStatus("error", "Socket error");
  });
}

function syncSettings() {
  if (state.localSettingsWrite || state.locked || state.connected) return;
  send({
    type: "settings",
    settings: {
      line_ending: els.lineEnding.value,
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
els.clearTerminalBtn.addEventListener("click", () => send({ type: "clear_terminal" }));

connect();
