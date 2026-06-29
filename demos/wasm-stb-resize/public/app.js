const form = document.querySelector("#resize-form");
const fileInput = document.querySelector("#file");
const widthInput = document.querySelector("#width");
const heightInput = document.querySelector("#height");
const before = document.querySelector("#before");
const after = document.querySelector("#after");
const statusEl = document.querySelector("#status");
const maxUploadBytes = 10 * 1024 * 1024;

function dataUrl(contentType, base64) {
  return `data:${contentType};base64,${base64}`;
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const file = fileInput.files[0];
  if (!file) return;

  before.src = URL.createObjectURL(file);
  after.removeAttribute("src");

  if (file.size > maxUploadBytes) {
    statusEl.textContent = "This demo accepts images up to 10 MiB. Pick a smaller PNG or JPEG.";
    return;
  }

  statusEl.textContent = "Resizing in WASM...";

  const body = await file.arrayBuffer();
  const params = new URLSearchParams({
    w: widthInput.value || "320",
    h: heightInput.value || "0",
    format: "jpg",
    quality: "90",
  });

  let response;
  try {
    response = await fetch(`/api/resize?${params}`, {
      method: "POST",
      headers: { "content-type": file.type || "application/octet-stream" },
      body,
    });
  } catch (error) {
    statusEl.textContent = `Request failed: ${error.message}`;
    return;
  }

  const text = await response.text();
  let result = {};
  try {
    result = JSON.parse(text);
  } catch {
    result = { ok: false, error: text.trim() };
  }
  if (!response.ok || !result.ok) {
    statusEl.textContent = result.error || `Resize failed with HTTP ${response.status}`;
    return;
  }

  after.src = dataUrl(result.content_type, result.output);
  statusEl.textContent = `${result.width} x ${result.height} ${result.content_type} returned from WASM`;
});
