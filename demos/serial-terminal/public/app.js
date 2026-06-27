const state = {
  devices: [],
  selected: "",
  connected: false,
  busy: false,
  lastEventAt: "",
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

async function api(method, path, body) {
  const started = performance.now();
  const opts = { method, headers: {} };
  if (body != null) {
    opts.headers["content-type"] = "application/json";
    opts.body = JSON.stringify(body);
  }

  debug("request", { method, path, body: body || null });
  const res = await fetch("/api" + path, opts);
  const text = await res.text();
  let data = {};
  try {
    data = text ? JSON.parse(text) : {};
  } catch (err) {
    data = { error: text || err.message };
  }
  debug("response", {
    method,
    path,
    status: res.status,
    elapsed_ms: Math.round(performance.now() - started),
    data,
  });
  if (!res.ok) throw new Error(data.error || `${res.status}`);
  return data;
}

function decodeEscapes(value) {
  return value
    .replaceAll("\\r", "\r")
    .replaceAll("\\n", "\n")
    .replaceAll("\\t", "\t")
    .replaceAll("\\0", "\0");
}

function setStatus(kind, text) {
  els.statePill.dataset.state = kind;
  els.stateText.textContent = text;
}

function setBusy(busy) {
  state.busy = busy;
  renderControls();
}

function renderControls() {
  const hasDevice = Boolean(state.selected);
  els.deviceSelect.disabled = state.connected || state.busy;
  els.refreshBtn.disabled = state.connected || state.busy;
  els.connectBtn.disabled = !hasDevice || state.connected || state.busy;
  els.quitBtn.disabled = !state.connected || state.busy;
  els.commandInput.disabled = !state.connected || state.busy;
  els.sendBtn.disabled = !state.connected || state.busy;
}

function renderDevices() {
  els.deviceSelect.innerHTML = "";

  if (state.devices.length === 0) {
    const option = document.createElement("option");
    option.value = "";
    option.textContent = "No serial devices bound";
    els.deviceSelect.appendChild(option);
    state.selected = "";
    renderControls();
    return;
  }

  for (const device of state.devices) {
    const alias = device.alias || device.id;
    const option = document.createElement("option");
    option.value = alias;
    option.textContent = device.label ? `${device.label} (${alias})` : alias;
    els.deviceSelect.appendChild(option);
  }

  if (!state.devices.find((device) => (device.alias || device.id) === state.selected)) {
    state.selected = state.devices[0].alias || state.devices[0].id;
  }
  els.deviceSelect.value = state.selected;
  renderControls();
}

function unavailableMessage(err) {
  const message = err && err.message ? err.message : String(err);
  if (message.includes("undefined: serial")) {
    return "Serial hardware service is not configured for this server.";
  }
  return message;
}

function appendTerminal(text, className) {
  const stamp = new Date().toLocaleTimeString();
  els.terminal.textContent += `[${stamp}] ${text}\n`;
  els.terminal.scrollTop = els.terminal.scrollHeight;
}

function debug(type, payload) {
  const stamp = new Date().toISOString();
  els.debugLog.textContent += `${stamp} ${type}\n${JSON.stringify(payload, null, 2)}\n\n`;
  els.debugLog.scrollTop = els.debugLog.scrollHeight;
}

function rememberRecent(status) {
  if (!status || !Array.isArray(status.recent)) return;
  for (const event of status.recent) {
    if (event.at && event.at > state.lastEventAt) {
      state.lastEventAt = event.at;
    }
  }
}

function appendRecentReads(status, skipBase64) {
  if (!status || !Array.isArray(status.recent)) return { appended: false, skipped: false };

  let appended = false;
  let skipped = false;
  const chunks = [];
  for (const event of status.recent) {
    if (!event.at || event.at <= state.lastEventAt) continue;
    if (event.type === "read" && event.text) {
      if (!skipped && skipBase64 && event.base64 === skipBase64) {
        skipped = true;
      } else {
        chunks.push(event.text);
        appended = true;
      }
    }
    if (event.at > state.lastEventAt) {
      state.lastEventAt = event.at;
    }
  }

  if (chunks.length > 0) {
    appendTerminal(chunks.join("").replace(/\n$/, "").replace(/\r$/, ""));
  }
  return { appended, skipped };
}

function applyStatus(status) {
  if (!status) return;
  const open = Boolean(status.open);
  state.connected = open;
  setStatus(open ? "open" : status.status === "error" ? "error" : "closed", status.status || (open ? "Open" : "Closed"));
  els.terminalTitle.textContent = state.selected ? `${state.selected} · ${status.status || "unknown"}` : "No device connected";
  if (status.error) appendTerminal(`! ${status.error}`);
  rememberRecent(status);
  renderControls();
}

async function refreshDevices() {
  setBusy(true);
  try {
    const data = await api("GET", "");
    state.devices = data.devices || [];
    renderDevices();
    setStatus(state.devices.length > 0 ? "closed" : "idle", state.devices.length > 0 ? "Ready" : "No devices");
  } catch (err) {
    state.devices = [];
    renderDevices();
    setStatus("error", "List failed");
    appendTerminal(`! ${unavailableMessage(err)}`);
  } finally {
    setBusy(false);
  }
}

async function connectDevice() {
  if (!state.selected) return;
  setBusy(true);
  try {
    const data = await api("POST", "/open", { device: state.selected });
    applyStatus(data.status);
    appendTerminal(`connected to ${state.selected}`);
    els.commandInput.focus();
  } catch (err) {
    setStatus("error", "Open failed");
    appendTerminal(`! ${unavailableMessage(err)}`);
  } finally {
    setBusy(false);
  }
}

async function quitTerminal() {
  if (!state.selected) return;
  setBusy(true);
  try {
    const data = await api("POST", "/close", { device: state.selected });
    applyStatus(data.status);
    appendTerminal(`closed ${state.selected}`);
  } catch (err) {
    setStatus("error", "Close failed");
    appendTerminal(`! ${unavailableMessage(err)}`);
  } finally {
    state.connected = false;
    renderControls();
    setBusy(false);
  }
}

async function drainRecentReads(skipBase64, timeoutMs) {
  const deadline = Date.now() + Math.max(250, Math.min(timeoutMs + 500, 2500));
  let quietSince = Date.now();
  let skip = skipBase64;

  while (Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 75));
    const data = await api("GET", `/status/${encodeURIComponent(state.selected)}`);
    const result = appendRecentReads(data.status, skip);
    applyStatus(data.status);
    if (result.skipped) skip = "";
    if (result.appended) {
      quietSince = Date.now();
    } else if (Date.now() - quietSince >= 225) {
      break;
    }
  }
}

async function sendCommand(event) {
  event.preventDefault();
  const text = els.commandInput.value;
  if (!state.connected || state.busy || text.length === 0) return;

  els.commandInput.value = "";
  appendTerminal(`> ${text}`);
  setBusy(true);
  try {
    const data = await api("POST", "/command", {
      device: state.selected,
      text,
      line_ending: els.lineEnding.value,
      until: decodeEscapes(els.untilInput.value),
      timeout_ms: Number.parseInt(els.timeoutInput.value, 10) || 0,
      max_bytes: 4096,
    });
    const response = data.response || {};
    const output = response.text || "";
    if (output.length > 0) {
      appendTerminal(output.replace(/\n$/, "").replace(/\r$/, ""));
    } else {
      appendTerminal(response.timeout ? "(timeout)" : "(no output)");
    }
    appendRecentReads(data.status, response.base64);
    applyStatus(data.status);
    await drainRecentReads(response.base64, Number.parseInt(els.timeoutInput.value, 10) || 0);
  } catch (err) {
    setStatus("error", "Command failed");
    appendTerminal(`! ${unavailableMessage(err)}`);
  } finally {
    setBusy(false);
    els.commandInput.focus();
  }
}

els.deviceSelect.addEventListener("change", () => {
  state.selected = els.deviceSelect.value;
  els.terminalTitle.textContent = state.selected ? `${state.selected} · closed` : "No device connected";
  renderControls();
});
els.refreshBtn.addEventListener("click", refreshDevices);
els.connectBtn.addEventListener("click", connectDevice);
els.quitBtn.addEventListener("click", quitTerminal);
els.commandForm.addEventListener("submit", sendCommand);
els.clearTerminalBtn.addEventListener("click", () => {
  els.terminal.textContent = "";
});
els.clearDebugBtn.addEventListener("click", () => {
  els.debugLog.textContent = "";
});

refreshDevices();
