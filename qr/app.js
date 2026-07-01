// QR Code Generator — everything runs client-side. We use the vendored
// qrcode-generator library only to compute the module matrix, then render
// it ourselves so we control colors, size, quiet zone, and export format.

// Encode input as UTF-8 so links with accents/emoji scan correctly.
qrcode.stringToBytes = qrcode.stringToBytesFuncs["UTF-8"];

const QUIET = 4; // quiet-zone width, in modules (spec minimum)

const els = {
  text: document.getElementById("text"),
  ecLevel: document.getElementById("ecLevel"),
  size: document.getElementById("size"),
  sizeVal: document.getElementById("sizeVal"),
  fgColor: document.getElementById("fgColor"),
  bgColor: document.getElementById("bgColor"),
  transparent: document.getElementById("transparent"),
  preview: document.getElementById("preview"),
  error: document.getElementById("error"),
  downloadPng: document.getElementById("downloadPng"),
  downloadSvg: document.getElementById("downloadSvg"),
  copyImg: document.getElementById("copyImg"),
};

// Holds the most recent successful render so the action buttons can reuse it.
let current = null; // { count, isDark(r,c), fg, bg, transparent, size }

function bg() {
  return els.transparent.checked ? "transparent" : els.bgColor.value;
}

// Build an SVG string from the current module matrix. `px` is the final
// on-screen/exported edge length in pixels; the viewBox uses module units so
// the code stays crisp at any size.
function buildSvg(px) {
  const { count, isDark, fg } = current;
  const dim = count + QUIET * 2;
  const rects = [];
  for (let r = 0; r < count; r++) {
    for (let c = 0; c < count; c++) {
      if (isDark(r, c)) {
        rects.push(`<rect x="${c + QUIET}" y="${r + QUIET}" width="1" height="1"/>`);
      }
    }
  }
  const bgRect =
    bg() === "transparent"
      ? ""
      : `<rect x="0" y="0" width="${dim}" height="${dim}" fill="${bg()}"/>`;
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" width="${px}" height="${px}" ` +
    `viewBox="0 0 ${dim} ${dim}" shape-rendering="crispEdges">` +
    bgRect +
    `<g fill="${fg}">${rects.join("")}</g></svg>`
  );
}

// Render the current matrix into a canvas at the requested pixel size.
function buildCanvas(px) {
  const { count, isDark, fg } = current;
  const dim = count + QUIET * 2;
  const scale = Math.max(1, Math.floor(px / dim));
  const canvasSize = dim * scale;

  const canvas = document.createElement("canvas");
  canvas.width = canvasSize;
  canvas.height = canvasSize;
  const ctx = canvas.getContext("2d");

  if (bg() !== "transparent") {
    ctx.fillStyle = bg();
    ctx.fillRect(0, 0, canvasSize, canvasSize);
  }
  ctx.fillStyle = fg;
  for (let r = 0; r < count; r++) {
    for (let c = 0; c < count; c++) {
      if (isDark(r, c)) {
        ctx.fillRect((c + QUIET) * scale, (r + QUIET) * scale, scale, scale);
      }
    }
  }
  return canvas;
}

function setActionsEnabled(enabled) {
  els.downloadPng.disabled = !enabled;
  els.downloadSvg.disabled = !enabled;
  // Clipboard image writing isn't universally supported; only enable when we
  // have both a render and the API.
  els.copyImg.disabled = !enabled || !(navigator.clipboard && window.ClipboardItem);
}

function showError(msg) {
  current = null;
  els.error.textContent = msg;
  els.error.hidden = false;
  els.preview.innerHTML = '<div class="preview__empty">—</div>';
  setActionsEnabled(false);
}

function render() {
  const text = els.text.value;
  els.error.hidden = true;

  if (!text) {
    current = null;
    els.preview.innerHTML =
      '<div class="preview__empty">Type something to generate a code</div>';
    setActionsEnabled(false);
    return;
  }

  let qr;
  try {
    // typeNumber 0 => auto-pick the smallest version that fits the data.
    qr = qrcode(0, els.ecLevel.value);
    qr.addData(text);
    qr.make();
  } catch (e) {
    showError(
      "That's too much data for a single QR code. Try shortening the text or lowering the error correction level."
    );
    return;
  }

  current = {
    count: qr.getModuleCount(),
    isDark: (r, c) => qr.isDark(r, c),
    fg: els.fgColor.value,
  };

  const px = parseInt(els.size.value, 10);
  els.preview.innerHTML = buildSvg(px);
  setActionsEnabled(true);
}

function slugFilename(ext) {
  const raw = els.text.value.trim().slice(0, 40);
  const slug =
    raw
      .replace(/^https?:\/\//i, "")
      .replace(/[^a-z0-9]+/gi, "-")
      .replace(/^-+|-+$/g, "")
      .toLowerCase() || "qr-code";
  return `${slug}.${ext}`;
}

function triggerDownload(href, filename) {
  const a = document.createElement("a");
  a.href = href;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
}

els.downloadPng.addEventListener("click", () => {
  if (!current) return;
  const canvas = buildCanvas(parseInt(els.size.value, 10));
  triggerDownload(canvas.toDataURL("image/png"), slugFilename("png"));
});

els.downloadSvg.addEventListener("click", () => {
  if (!current) return;
  const svg = buildSvg(parseInt(els.size.value, 10));
  const blob = new Blob([svg], { type: "image/svg+xml" });
  const url = URL.createObjectURL(blob);
  triggerDownload(url, slugFilename("svg"));
  setTimeout(() => URL.revokeObjectURL(url), 1000);
});

els.copyImg.addEventListener("click", async () => {
  if (!current || !(navigator.clipboard && window.ClipboardItem)) return;
  const canvas = buildCanvas(parseInt(els.size.value, 10));
  const label = els.copyImg.textContent;
  try {
    const blob = await new Promise((resolve) =>
      canvas.toBlob(resolve, "image/png")
    );
    await navigator.clipboard.write([new ClipboardItem({ "image/png": blob })]);
    els.copyImg.textContent = "Copied!";
    els.copyImg.classList.add("btn--copied");
  } catch (e) {
    els.copyImg.textContent = "Copy failed";
  }
  setTimeout(() => {
    els.copyImg.textContent = label;
    els.copyImg.classList.remove("btn--copied");
  }, 1500);
});

// Live-update controls.
let debounce;
els.text.addEventListener("input", () => {
  clearTimeout(debounce);
  debounce = setTimeout(render, 120);
});
els.ecLevel.addEventListener("change", render);
els.fgColor.addEventListener("input", render);
els.bgColor.addEventListener("input", render);
els.transparent.addEventListener("change", render);
els.size.addEventListener("input", () => {
  els.sizeVal.textContent = els.size.value + "px";
  if (current) els.preview.innerHTML = buildSvg(parseInt(els.size.value, 10));
});

// First paint.
render();
