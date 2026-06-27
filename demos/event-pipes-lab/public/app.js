(function () {
  const state = {
    flow: "map_reduce",
    session: sessionStorage.getItem("event-pipes-lab-session") || makeSession(),
    socket: null,
    connected: false,
    graphData: {
      map_reduce: {},
      scatter_gather: {},
      sharding: {},
    },
  };

  const graphDefs = {
    map_reduce: {
      title: "Map Reduce Graph",
      nodes: [
        { id: "browser", label: "Browser submit", x: 80, y: 300 },
        { id: "websocket_ingress", label: "api/pipes.star:on_message", x: 360, y: 300 },
        { id: "start_node", label: "api/map_reduce.star:start_node", x: 640, y: 300 },
        { id: "split_node", label: "api/map_reduce.star:split_node", x: 920, y: 300 },
        { id: "map_0_node", label: "api/map_reduce.star:map_0_node", x: 1200, y: 60 },
        { id: "map_1_node", label: "api/map_reduce.star:map_1_node", x: 1200, y: 220 },
        { id: "map_2_node", label: "api/map_reduce.star:map_2_node", x: 1200, y: 380 },
        { id: "map_3_node", label: "api/map_reduce.star:map_3_node", x: 1200, y: 540 },
        { id: "reduce_node", label: "api/map_reduce.star:reduce_node", x: 1480, y: 300 },
        { id: "result", label: "Result", x: 1760, y: 300 },
      ],
      links: [
        { source: "browser", target: "websocket_ingress", label: "WebSocket /ws message" },
        { source: "websocket_ingress", target: "start_node", label: "pipe-demo.map_reduce.start" },
        { source: "start_node", target: "split_node", label: "pipe-demo.map_reduce.split" },
        { source: "split_node", target: "map_0_node", label: "pipe-demo.map_reduce.map_0" },
        { source: "split_node", target: "map_1_node", label: "pipe-demo.map_reduce.map_1" },
        { source: "split_node", target: "map_2_node", label: "pipe-demo.map_reduce.map_2" },
        { source: "split_node", target: "map_3_node", label: "pipe-demo.map_reduce.map_3" },
        { source: "map_0_node", target: "reduce_node", label: "pipe-demo.map_reduce.reduce" },
        { source: "map_1_node", target: "reduce_node", label: "pipe-demo.map_reduce.reduce" },
        { source: "map_2_node", target: "reduce_node", label: "pipe-demo.map_reduce.reduce" },
        { source: "map_3_node", target: "reduce_node", label: "pipe-demo.map_reduce.reduce" },
        { source: "reduce_node", target: "result", label: "pipe-demo.session.<session>.trace" },
      ],
    },
    scatter_gather: {
      title: "Scatter/Gather Graph",
      nodes: [
        { id: "browser", label: "Browser submit", x: 80, y: 300 },
        { id: "websocket_ingress", label: "api/pipes.star:on_message", x: 360, y: 300 },
        { id: "start_node", label: "api/scatter_gather.star:start_node", x: 640, y: 300 },
        { id: "profile_node", label: "api/scatter_gather.star:profile_node", x: 920, y: 60 },
        { id: "pricing_node", label: "api/scatter_gather.star:pricing_node", x: 920, y: 220 },
        { id: "inventory_node", label: "api/scatter_gather.star:inventory_node", x: 920, y: 380 },
        { id: "risk_node", label: "api/scatter_gather.star:risk_node", x: 920, y: 540 },
        { id: "gather_node", label: "api/scatter_gather.star:gather_node", x: 1200, y: 300 },
        { id: "result", label: "Result", x: 1480, y: 300 },
      ],
      links: [
        { source: "browser", target: "websocket_ingress", label: "WebSocket /ws message" },
        { source: "websocket_ingress", target: "start_node", label: "pipe-demo.scatter_gather.start" },
        { source: "start_node", target: "profile_node", label: "pipe-demo.scatter_gather.profile" },
        { source: "start_node", target: "pricing_node", label: "pipe-demo.scatter_gather.pricing" },
        { source: "start_node", target: "inventory_node", label: "pipe-demo.scatter_gather.inventory" },
        { source: "start_node", target: "risk_node", label: "pipe-demo.scatter_gather.risk" },
        { source: "profile_node", target: "gather_node", label: "pipe-demo.scatter_gather.gather" },
        { source: "pricing_node", target: "gather_node", label: "pipe-demo.scatter_gather.gather" },
        { source: "inventory_node", target: "gather_node", label: "pipe-demo.scatter_gather.gather" },
        { source: "risk_node", target: "gather_node", label: "pipe-demo.scatter_gather.gather" },
        { source: "gather_node", target: "result", label: "pipe-demo.session.<session>.trace" },
      ],
    },
    sharding: {
      title: "Sharding Graph",
      nodes: [
        { id: "browser", label: "Browser submit", x: 80, y: 300 },
        { id: "websocket_ingress", label: "api/pipes.star:on_message", x: 360, y: 300 },
        { id: "start_node", label: "api/sharding.star:start_node", x: 640, y: 300 },
        { id: "route_node", label: "api/sharding.star:route_node", x: 920, y: 300 },
        { id: "shard_0_node", label: "api/sharding.star:shard_0_node", x: 1200, y: 60 },
        { id: "shard_1_node", label: "api/sharding.star:shard_1_node", x: 1200, y: 220 },
        { id: "shard_2_node", label: "api/sharding.star:shard_2_node", x: 1200, y: 380 },
        { id: "shard_3_node", label: "api/sharding.star:shard_3_node", x: 1200, y: 540 },
        { id: "merge_node", label: "api/sharding.star:merge_node", x: 1480, y: 300 },
        { id: "result", label: "Result", x: 1760, y: 300 },
      ],
      links: [
        { source: "browser", target: "websocket_ingress", label: "WebSocket /ws message" },
        { source: "websocket_ingress", target: "start_node", label: "pipe-demo.sharding.start" },
        { source: "start_node", target: "route_node", label: "pipe-demo.sharding.route" },
        { source: "route_node", target: "shard_0_node", label: "pipe-demo.sharding.shard_0" },
        { source: "route_node", target: "shard_1_node", label: "pipe-demo.sharding.shard_1" },
        { source: "route_node", target: "shard_2_node", label: "pipe-demo.sharding.shard_2" },
        { source: "route_node", target: "shard_3_node", label: "pipe-demo.sharding.shard_3" },
        { source: "shard_0_node", target: "merge_node", label: "pipe-demo.sharding.merge" },
        { source: "shard_1_node", target: "merge_node", label: "pipe-demo.sharding.merge" },
        { source: "shard_2_node", target: "merge_node", label: "pipe-demo.sharding.merge" },
        { source: "shard_3_node", target: "merge_node", label: "pipe-demo.sharding.merge" },
        { source: "merge_node", target: "result", label: "pipe-demo.session.<session>.trace" },
      ],
    },
  };

  sessionStorage.setItem("event-pipes-lab-session", state.session);

  const els = {
    status: document.querySelector(".status"),
    statusText: document.getElementById("statusText"),
    sessionLabel: document.getElementById("sessionLabel"),
    traceLog: document.getElementById("traceLog"),
    clearLog: document.getElementById("clearLog"),
    toggleTrace: document.getElementById("toggleTrace"),
    debug: document.querySelector(".debug"),
    tabs: Array.from(document.querySelectorAll(".tab")),
    panels: Array.from(document.querySelectorAll(".panel")),
    runners: Array.from(document.querySelectorAll(".runner")),
    results: new Map(Array.from(document.querySelectorAll("[data-result]")).map((el) => [el.dataset.result, el])),
    graphs: new Map(Array.from(document.querySelectorAll("[data-graph]")).map((el) => [el.dataset.graph, el])),
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
      updateGraph(message);
      if (message.type === "result") renderResult(message);
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
    item.className = `trace-item kind-${message.type || "trace"}`;

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

  function renderResult(message) {
    const target = els.results.get(message.flow);
    if (!target) return;
    const detail = message.detail || {};
    if (message.flow === "map_reduce") {
      const counts = [...(detail.counts || [])].sort((a, b) => {
        if (b.count !== a.count) return b.count - a.count;
        return String(a.word).localeCompare(String(b.word));
      });
      target.innerHTML = `
        <div class="result-title">${escapeText(message.title || "Map-reduce complete")}</div>
        <table>
          <thead><tr><th>Word</th><th>Count</th></tr></thead>
          <tbody>${counts.map((row) => `<tr><td>${escapeText(row.word)}</td><td>${escapeText(row.count)}</td></tr>`).join("") || `<tr><td colspan="2">No words</td></tr>`}</tbody>
        </table>
      `;
      return;
    }
    if (message.flow === "scatter_gather") {
      const responses = detail.responses || [];
      const best = detail.best || {};
      target.innerHTML = `
        <div class="result-title">${escapeText(message.title || "Scatter-gather complete")}</div>
        <div class="metric"><span>Best</span><strong>${escapeText(best.item || "none")}</strong><em>${escapeText(best.score ?? 0)} points</em></div>
        <ul>${responses.map((row) => `<li><span>${escapeText(row.item)}</span><strong>${escapeText(row.score)}</strong></li>`).join("")}</ul>
      `;
      return;
    }
    if (message.flow === "sharding") {
      const shards = detail.shards || [];
      target.innerHTML = `
        <div class="result-title">${escapeText(message.title || "Sharding complete")}</div>
        <div class="metric"><span>Total</span><strong>${escapeText(detail.total ?? 0)}</strong><em>${escapeText(detail.records ?? 0)} records</em></div>
        <table>
          <thead><tr><th>Shard</th><th>Records</th><th>Total</th></tr></thead>
          <tbody>${shards.map((row) => `<tr><td>${escapeText(row.shard)}</td><td>${escapeText(row.count)}</td><td>${escapeText(row.total)}</td></tr>`).join("")}</tbody>
        </table>
      `;
    }
  }

  function resetGraph(flow) {
    state.graphData[flow] = {};
    renderGraph(flow);
  }

  function updateGraph(message) {
    const flow = message.flow;
    if (!graphDefs[flow]) return;
    const nodeId = message.type === "result" ? "result" : message.stage;
    state.graphData[flow][nodeId] = {
      title: message.title || nodeId,
      detail: message.detail || {},
      type: message.type || "trace",
    };
    renderGraph(flow);
  }

  function renderGraph(flow) {
    const target = els.graphs.get(flow);
    const def = graphDefs[flow];
    if (!target || !def) return;
    if (!window.d3) {
      target.innerHTML = `<div class="graph-title">${escapeText(def.title)}</div><div class="graph-missing">D3 failed to load.</div>`;
      return;
    }
    const data = state.graphData[flow] || {};
    target.innerHTML = "";
    const nodeWidth = 220;
    const nodeHeight = 132;
    const margin = 28;
    const maxX = Math.max(...def.nodes.map((node) => node.x)) + nodeWidth + margin;
    const maxY = Math.max(...def.nodes.map((node) => node.y)) + nodeHeight + margin;
    const nodes = new Map(def.nodes.map((node) => [node.id, node]));
    const root = d3.select(target);
    root.append("div").attr("class", "graph-title").text(def.title);
    const frame = root.append("div").attr("class", "graph-frame");
    const svg = frame.append("svg")
      .attr("class", "pipeline-graph")
      .attr("viewBox", `0 0 ${maxX} ${maxY}`)
      .attr("width", maxX)
      .attr("height", maxY)
      .attr("role", "img")
      .attr("aria-label", def.title);

    svg.append("defs").append("marker")
      .attr("id", `arrow-${flow}`)
      .attr("viewBox", "0 -5 10 10")
      .attr("refX", 10)
      .attr("refY", 0)
      .attr("markerWidth", 8)
      .attr("markerHeight", 8)
      .attr("orient", "auto")
      .append("path")
      .attr("d", "M0,-5L10,0L0,5")
      .attr("class", "graph-arrow");

    const link = d3.linkHorizontal()
      .source((d) => [nodes.get(d.source).x + nodeWidth, nodes.get(d.source).y + nodeHeight / 2])
      .target((d) => [nodes.get(d.target).x, nodes.get(d.target).y + nodeHeight / 2]);

    svg.append("g")
      .selectAll("path")
      .data(def.links)
      .join("path")
      .attr("class", (d) => data[d.source] ? "graph-link active" : "graph-link")
      .attr("d", link)
      .attr("marker-end", `url(#arrow-${flow})`);

    svg.append("g")
      .selectAll("text")
      .data(def.links)
      .join("text")
      .attr("class", "graph-link-label")
      .attr("x", (d) => (nodes.get(d.source).x + nodeWidth + nodes.get(d.target).x) / 2)
      .attr("y", (d) => (nodes.get(d.source).y + nodes.get(d.target).y + nodeHeight) / 2 - 8)
      .text((d) => d.label.replace("<session>", state.session));

    const node = svg.append("g")
      .selectAll("g")
      .data(def.nodes)
      .join("g")
      .attr("class", (d) => data[d.id] ? "graph-node active" : "graph-node")
      .attr("transform", (d) => `translate(${d.x},${d.y})`);

    node.append("rect")
      .attr("width", nodeWidth)
      .attr("height", nodeHeight)
      .attr("rx", 8);

    const body = node.append("foreignObject")
      .attr("width", nodeWidth)
      .attr("height", nodeHeight);

    body.append("xhtml:div")
      .attr("class", "graph-card")
      .html((d) => graphNodeHTML(d, data[d.id]));
  }

  function graphNodeHTML(node, item) {
    return `
      <div class="graph-card-title">${escapeText(node.label)}</div>
      <div class="graph-card-id">${escapeText(node.id)}</div>
      <pre>${escapeText(item ? summarizeGraphDetail(node.id, item.detail) : "waiting for data")}</pre>
    `;
  }

  function summarizeGraphDetail(nodeId, detail) {
    const compact = {};
    for (const key of ["edge", "incoming_edge", "outgoing_edge", "outgoing_edges", "node", "word_count", "chunk_count", "received", "expected", "unique_words", "response_count", "records", "total"]) {
      if (detail[key] !== undefined) compact[key] = detail[key];
    }
    if (detail.input) compact.input = detail.input;
    if (detail.chunks) compact.chunks = detail.chunks;
    if (detail.pairs) compact.pairs = detail.pairs;
    if (detail.response) compact.response = detail.response;
    if (detail.best) compact.best = detail.best;
    if (detail.summary) compact.summary = detail.summary;
    if (detail.counts) compact.counts = detail.counts;
    if (detail.shards) compact.shards = detail.shards;
    if (Object.keys(compact).length === 0) return JSON.stringify(detail, null, 2);
    return JSON.stringify(compact, null, 2);
  }

  function setResultPending(flow) {
    const target = els.results.get(flow);
    if (!target) return;
    target.innerHTML = `<span class="empty">Running ${escapeText(flow.replace("_", "/"))}...</span>`;
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
      setResultPending(flow);
      resetGraph(flow);
      const browserTrace = {
        type: "trace",
        flow,
        stage: "browser",
        title: "sent websocket start message",
        detail: payload,
      };
      updateGraph(browserTrace);
      appendTrace(browserTrace);
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

  els.toggleTrace.addEventListener("click", () => {
    const collapsed = els.debug.classList.toggle("collapsed");
    els.toggleTrace.textContent = collapsed ? "Expand" : "Collapse";
  });

  Object.keys(graphDefs).forEach(renderGraph);
  connect();
})();
