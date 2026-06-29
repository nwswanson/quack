const form = document.querySelector("#resize-form");
const fileInput = document.querySelector("#file");
const widthInput = document.querySelector("#width");
const heightInput = document.querySelector("#height");
const before = document.querySelector("#before");
const after = document.querySelector("#after");
const statusEl = document.querySelector("#status");

function dataUrl(contentType, base64) {
  return `data:${contentType};base64,${base64}`;
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const file = fileInput.files[0];
  if (!file) return;

  before.src = URL.createObjectURL(file);
  after.removeAttribute("src");
  statusEl.textContent = "Resizing in WASM...";

  const body = await file.arrayBuffer();
  const params = new URLSearchParams({
    w: widthInput.value || "320",
    h: heightInput.value || "0",
  });

  const response = await fetch(`/api/resize?${params}`, {
    method: "POST",
    headers: { "content-type": file.type || "application/octet-stream" },
    body,
  });
  const result = await response.json();
  if (!response.ok || !result.ok) {
    statusEl.textContent = result.error || "Resize failed";
    return;
  }

  after.src = dataUrl(result.content_type, result.output);
  statusEl.textContent = `${result.width} x ${result.height} PNG returned from WASM`;
});
