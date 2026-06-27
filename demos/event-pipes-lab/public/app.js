(function () {
  const state = {
    flow: "map_reduce",
    session: sessionStorage.getItem("event-pipes-lab-session") || makeSession(),
    socket: null,
    connected: false,
  };

  sessionStorage.setItem("event-pipes-lab-session", state.session);

  const els = {
    status: document.querySelector(".status"),
    statusText: document.getElementById("statusText"),
    sessionLabel: document.getElementById("sessionLabel"),
    traceLog: document.getElementById("traceLog"),
    clearLog: document.getElementById("clearLog"),
    tabs: Array.from(document.querySelectorAll(".tab")),
    panels: Array.from(document.querySelectorAll(".panel")),
    runners: Array.from(document.querySelectorAll(".runner")),
  };

  els.sessionLabel.textContent = `Session channel: pipe-demo.session.${state.session}.trace`;

  function makeSession() {
    const bytes = new Uint8Array(8);
    crypto.getRandomValues(bytes);
    return "s" + Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  }

  function socketUrl() {
    const scheme = location.protocol === "https:" ? "wss:" : "ws:";
    return `${scheme}//${location.host}/ws`;
  }

  function setStatus(kind, label) {
    els.status.dataset.state = kind;
    els.statusText.textContent = label;
  }

  function connect() {
    if (state.socket && (state.socket.readyState === WebSocket.OPEN || state.socket.readyState === WebSocket.CONNECTING)) {
      return;
    }
    setStatus("connecting", "Connecting");
    const socket = new WebSocket(socketUrl());
    state.socket = socket;

    socket.addEventListener("open", () => {
      state.connected = true;
      setStatus("open", "Live");
      send({ type: "subscribe", session: state.session });
    });

    socket.addEventListener("message", (event) => {
      let message;
      try {
        message = JSON.parse(event.data);
      } catch (err) {
        appendTrace({ type: "error", title: "non-JSON websocket frame", detail: { frame: event.data } });
        return;
      }
      if (message.type === "ready" || message.type === "subscribed") return;
      appendTrace(message);
    });

    socket.addEventListener("close", () => {
      state.connected = false;
      setStatus("closed", "Reconnecting");
      setTimeout(connect, 900);
    });

    socket.addEventListener("error", () => socket.close());
  }

  function send(message) {
    if (!state.socket || state.socket.readyState !== WebSocket.OPEN) return false;
    state.socket.send(JSON.stringify(message));
    return true;
  }

  function appendTrace(message) {
    const item = document.createElement("article");
    item.className = `trace-item ${message.type || "trace"}`;

    const meta = document.createElement("div");
    meta.className = "trace-meta";
    meta.innerHTML = `<span>${escapeText(message.flow || "socket")}</span><span>${escapeText(message.stage || "event")}</span>`;

    const title = document.createElement("h3");
    title.textContent = message.title || message.message || "event";

    const pre = document.createElement("pre");
    pre.textContent = JSON.stringify(message.detail || message, null, 2);

    item.append(meta, title, pre);
    els.traceLog.prepend(item);
  }

  function escapeText(value) {
    return String(value).replace(/[&<>"']/g, (ch) => ({
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#039;",
    }[ch]));
  }

  function activate(flow) {
    state.flow = flow;
    els.tabs.forEach((tab) => tab.classList.toggle("active", tab.dataset.flow === flow));
    els.panels.forEach((panel) => panel.classList.toggle("active", panel.dataset.panel === flow));
  }

  els.tabs.forEach((tab) => {
    tab.addEventListener("click", () => activate(tab.dataset.flow));
  });

  els.runners.forEach((form) => {
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      const flow = form.dataset.runner;
      const data = new FormData(form);
      const payload = {
        type: "start",
        flow,
        session: state.session,
        input: data.get("input") || "",
      };
      appendTrace({
        type: "trace",
        flow,
        stage: "browser",
        title: "sent websocket start message",
        detail: payload,
      });
      if (!send(payload)) {
        appendTrace({
          type: "error",
          flow,
          stage: "browser",
          title: "socket is not open yet",
          detail: { status: state.socket ? state.socket.readyState : "missing" },
        });
      }
    });
  });

  els.clearLog.addEventListener("click", () => {
    els.traceLog.textContent = "";
  });

  connect();
})();
