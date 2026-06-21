const outputs = {
  kv: document.querySelector("#kvOut"),
  list: document.querySelector("#listOut"),
  set: document.querySelector("#setOut"),
  zset: document.querySelector("#zsetOut"),
  counter: document.querySelector("#counterOut"),
  meta: document.querySelector("#metaOut"),
};

const state = {
  usage: 0,
  quota: 0,
  keys: [],
};

function bytes(value) {
  if (!value) return "0 B";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${(value / 1024 / 1024).toFixed(1)} MiB`;
}

async function callApi(area, op, params = {}) {
  const res = await fetch(`/api/${area}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ op, ...params }),
  });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  const data = await res.json();
  updateStatus(data);
  return data;
}

async function refreshMeta() {
  const res = await fetch("/api/meta", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ op: "state" }),
  });
  const data = await res.json();
  updateStatus(data);
  outputs.meta.textContent = JSON.stringify(data, null, 2);
  return data;
}

function updateStatus(data) {
  if (typeof data.usage === "number") state.usage = data.usage;
  if (typeof data.quota === "number") state.quota = data.quota;
  if (Array.isArray(data.keys)) state.keys = data.keys;
  document.querySelector("#usageValue").textContent = bytes(state.usage);
  document.querySelector("#quotaValue").textContent = bytes(state.quota);
  document.querySelector("#keysValue").textContent = String(state.keys.length);
  drawUsage();
}

function drawUsage() {
  const canvas = document.querySelector("#usageCanvas");
  const ctx = canvas.getContext("2d");
  const w = canvas.width;
  const h = canvas.height;
  ctx.clearRect(0, 0, w, h);
  ctx.fillStyle = "#dbe6ea";
  ctx.fillRect(0, 0, w, h);

  const pct = state.quota > 0 ? Math.min(state.usage / state.quota, 1) : 0;
  const bars = 24;
  const gap = 3;
  const barW = (w - 28 - gap * (bars - 1)) / bars;
  for (let i = 0; i < bars; i += 1) {
    const active = i / bars <= pct;
    ctx.fillStyle = active ? (pct > 0.82 ? "#b45309" : "#0f766e") : "#a9bbc3";
    const barH = 18 + ((i * 7) % 30);
    const x = 14 + i * (barW + gap);
    ctx.fillRect(x, h - 16 - barH, barW, barH);
  }

  ctx.fillStyle = "#172026";
  ctx.font = "13px ui-monospace, Menlo, Consolas, monospace";
  ctx.fillText(`${bytes(state.usage)} / ${bytes(state.quota)}`, 14, 22);
}

async function run(action) {
  const [area, op] = action.split(":");
  const params = {};
  if (area === "kv") params.value = document.querySelector("#kvValue").value;
  if (area === "list") params.value = document.querySelector("#listValue").value;
  if (area === "set") params.value = document.querySelector("#setValue").value;
  if (area === "zset") {
    params.value = document.querySelector("#zsetValue").value;
    params.score = document.querySelector("#zsetScore").value;
  }
  if (area === "counter") params.delta = document.querySelector("#counterDelta").value;

  const out = outputs[area];
  out.textContent = "Calling backend...";
  try {
    const data = await callApi(area, op, params);
    out.textContent = JSON.stringify(data, null, 2);
    await refreshMeta();
  } catch (err) {
    out.textContent = String(err);
  }
}

document.addEventListener("click", async (event) => {
  const button = event.target.closest("button");
  if (!button) return;
  const action = button.dataset.action;
  if (action) {
    await run(action);
    return;
  }
  if (button.id === "refreshAll") {
    await refreshMeta();
    return;
  }
  if (button.id === "clearAll") {
    const res = await fetch("/api/meta", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ op: "clear" }),
    });
    const data = await res.json();
    updateStatus(data);
    outputs.meta.textContent = JSON.stringify(data, null, 2);
    await loadCurrentState();
  }
});

async function loadCurrentState() {
  outputs.kv.textContent = JSON.stringify(await callApi("kv", "state"), null, 2);
  outputs.list.textContent = JSON.stringify(await callApi("list", "state"), null, 2);
  outputs.set.textContent = JSON.stringify(await callApi("set", "state"), null, 2);
  outputs.zset.textContent = JSON.stringify(await callApi("zset", "state"), null, 2);
  outputs.counter.textContent = JSON.stringify(await callApi("counter", "state"), null, 2);
  await refreshMeta();
}

drawUsage();
loadCurrentState().catch((err) => {
  outputs.meta.textContent = String(err);
});
