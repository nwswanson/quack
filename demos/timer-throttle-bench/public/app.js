const els = {
  state: document.querySelector("#socket-state"),
  rps: document.querySelector("#rps"),
  hits: document.querySelector("#hits"),
  total: document.querySelector("#total"),
  pushReadout: document.querySelector("#push-readout"),
  push: document.querySelector("#push-ms"),
  window: document.querySelector("#window-ms"),
  benchUrl: document.querySelector("#bench-url"),
  ab: document.querySelector("#ab-command"),
  samples: document.querySelector("#samples"),
  sampleCount: document.querySelector("#sample-count"),
  reset: document.querySelector("#reset"),
};

let socket = null;
let reconnectTimer = 0;
const recent = [];

function number(value) {
  return new Intl.NumberFormat().format(value || 0);
}

function wsURL() {
  const url = new URL("/ws", window.location.href);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.searchParams.set("push_ms", els.push.value);
  url.searchParams.set("window_ms", els.window.value);
  return url;
}

function benchURL() {
  const url = new URL("/bench", window.location.href);
  url.searchParams.set("push_ms", els.push.value);
  url.searchParams.set("window_ms", els.window.value);
  if (url.hostname === "localhost") {
    url.hostname = "127.0.0.1";
  }
  return url;
}

function renderCommand() {
  const url = benchURL();
  els.benchUrl.textContent = url.pathname + url.search;
  els.ab.textContent = `ab -n 10000 -c 64 ${url.href}`;
  els.pushReadout.textContent = `${els.push.value} ms`;
}

function connect() {
  clearTimeout(reconnectTimer);
  if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
    return;
  }
  els.state.textContent = "connecting";
  socket = new WebSocket(wsURL());
  socket.addEventListener("open", () => {
    els.state.textContent = "connected";
    sendConfig();
  });
  socket.addEventListener("message", (event) => {
    try {
      renderStats(JSON.parse(event.data));
    } catch {
      // Ignore non-JSON frames; this demo only renders structured stats.
    }
  });
  socket.addEventListener("close", () => {
    els.state.textContent = "reconnecting";
    reconnectTimer = setTimeout(connect, 800);
  });
  socket.addEventListener("error", () => {
    els.state.textContent = "socket error";
  });
}

function sendConfig(extra = {}) {
  renderCommand();
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }
  socket.send(JSON.stringify({
    type: "configure",
    push_ms: Number(els.push.value),
    window_ms: Number(els.window.value),
    ...extra,
  }));
}

function renderStats(stats) {
  if (!stats || stats.type !== "stats") {
    return;
  }
  const win = stats.window || {};
  const last = stats.last || {};
  els.rps.textContent = number(win.rps);
  els.hits.textContent = number(win.hits);
  els.total.textContent = number(stats.total);
  els.sampleCount.textContent = number(stats.sample_count);
  if (stats.push_ms) {
    els.pushReadout.textContent = `${stats.push_ms} ms`;
  }
  if (last.seq) {
    recent.unshift(last);
    while (recent.length > 28) {
      recent.pop();
    }
    renderSamples();
  }
}

function renderSamples() {
  els.samples.replaceChildren(...recent.map((sample) => {
    const row = document.createElement("li");
    const seq = document.createElement("span");
    const hits = document.createElement("span");
    const elapsed = document.createElement("span");
    const rate = document.createElement("span");
    seq.textContent = `#${sample.seq}`;
    hits.textContent = `${number(sample.hits)} hits`;
    elapsed.textContent = `${number(sample.elapsed_ms)} ms`;
    const rps = sample.elapsed_ms > 0 ? Math.round((sample.hits * 1000) / sample.elapsed_ms) : 0;
    rate.textContent = `${number(rps)} instant rps`;
    row.append(seq, hits, elapsed, rate);
    return row;
  }));
}

els.push.addEventListener("input", () => sendConfig());
els.window.addEventListener("input", () => sendConfig());
els.reset.addEventListener("click", () => {
  recent.length = 0;
  renderSamples();
  sendConfig({ type: "reset" });
});

renderCommand();
connect();
