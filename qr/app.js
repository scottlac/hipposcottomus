// QR Code Generator — everything runs client-side. We use the vendored
// qrcode-generator library only to compute the module matrix, then render
// it ourselves so we control colors, size, quiet zone, plate shape, module
// style, and export format.
//
// Important constraint: the *scannable* part of a QR code is always a square
// grid of modules. Reshaping the data grid breaks scanning, so the "shapes"
// here are decorative plates that frame the square code (great for a round
// 3D-printed coaster) — the code itself stays square and the border/ring acts
// as the required quiet zone.

// Encode input as UTF-8 so links with accents/emoji scan correctly.
qrcode.stringToBytes = qrcode.stringToBytesFuncs["UTF-8"];

const SQRT2 = Math.SQRT2;

const els = {
  text: document.getElementById("text"),
  ecLevel: document.getElementById("ecLevel"),
  size: document.getElementById("size"),
  sizeVal: document.getElementById("sizeVal"),
  fgColor: document.getElementById("fgColor"),
  bgColor: document.getElementById("bgColor"),
  transparent: document.getElementById("transparent"),
  shape: document.getElementById("shape"),
  moduleStyle: document.getElementById("moduleStyle"),
  border: document.getElementById("border"),
  borderVal: document.getElementById("borderVal"),
  preview: document.getElementById("preview"),
  error: document.getElementById("error"),
  downloadPng: document.getElementById("downloadPng"),
  downloadSvg: document.getElementById("downloadSvg"),
  copyImg: document.getElementById("copyImg"),
};

// Holds the most recent successful render so the action buttons can reuse it.
let current = null; // { count, isDark(r,c) }

function bg() {
  return els.transparent.checked ? "transparent" : els.bgColor.value;
}

// A module is part of a finder pattern (the three big corner "eyes") if it
// lands in one of the 7x7 corner blocks. We always render these as solid
// squares — rounding them hurts scan reliability.
function isFinder(r, c, count) {
  return (
    (r < 7 && c < 7) ||
    (r < 7 && c >= count - 7) ||
    (r >= count - 7 && c < 7)
  );
}

// Compute the plate geometry in *module units*. Returns the viewBox size, the
// pixel offset of module (0,0), and a description of the background plate that
// frames the square code.
function computeLayout(count, border, shape) {
  const half = count / 2;

  if (shape === "square" || shape === "rounded") {
    const dim = count + border * 2;
    return {
      dim,
      ox: border,
      oy: border,
      plate:
        shape === "rounded"
          ? { type: "rounded", x: 0, y: 0, w: dim, h: dim, r: Math.max(2, border * 0.8) }
          : { type: "rect", x: 0, y: 0, w: dim, h: dim },
    };
  }

  // For circle/hexagon plates the square code (corners included) must fit
  // inside, with at least a 4-module quiet zone. half*SQRT2 reaches the square
  // corners; the +1 and (border-4) thicken the ring.
  const R = half * SQRT2 + 1 + (border - 4);

  if (shape === "circle") {
    const dim = 2 * Math.ceil(R);
    const c = dim / 2;
    return { dim, ox: c - half, oy: c - half, plate: { type: "circle", cx: c, cy: c, r: R } };
  }

  // Hexagon: use a circumradius whose inscribed circle (apothem) still covers
  // the circle of radius R, so the square is fully contained.
  const rHex = (2 * R) / Math.sqrt(3);
  const dim = 2 * Math.ceil(rHex);
  const cx = dim / 2;
  const pts = [];
  for (let i = 0; i < 6; i++) {
    const a = (Math.PI / 180) * (60 * i - 90); // pointy-top
    pts.push([cx + rHex * Math.cos(a), cx + rHex * Math.sin(a)]);
  }
  return { dim, ox: cx - half, oy: cx - half, plate: { type: "hexagon", points: pts } };
}

function layoutFor() {
  return computeLayout(
    current.count,
    parseInt(els.border.value, 10),
    els.shape.value
  );
}

// ---- SVG rendering -------------------------------------------------------

function plateSvg(plate, fill) {
  switch (plate.type) {
    case "rect":
      return `<rect x="${plate.x}" y="${plate.y}" width="${plate.w}" height="${plate.h}" fill="${fill}"/>`;
    case "rounded":
      return `<rect x="${plate.x}" y="${plate.y}" width="${plate.w}" height="${plate.h}" rx="${plate.r}" ry="${plate.r}" fill="${fill}"/>`;
    case "circle":
      return `<circle cx="${plate.cx}" cy="${plate.cy}" r="${plate.r}" fill="${fill}"/>`;
    case "hexagon":
      return `<polygon points="${plate.points.map((p) => p.join(",")).join(" ")}" fill="${fill}"/>`;
  }
  return "";
}

function buildSvg(px) {
  const { count, isDark } = current;
  const dots = els.moduleStyle.value === "dots";
  const fg = els.fgColor.value;
  const { dim, ox, oy, plate } = layoutFor();

  const plateEl = bg() === "transparent" ? "" : plateSvg(plate, bg());

  const parts = [];
  for (let r = 0; r < count; r++) {
    for (let c = 0; c < count; c++) {
      if (!isDark(r, c)) continue;
      if (dots && !isFinder(r, c, count)) {
        parts.push(`<circle cx="${ox + c + 0.5}" cy="${oy + r + 0.5}" r="0.5"/>`);
      } else {
        parts.push(`<rect x="${ox + c}" y="${oy + r}" width="1" height="1"/>`);
      }
    }
  }

  return (
    `<svg xmlns="http://www.w3.org/2000/svg" width="${px}" height="${px}" ` +
    `viewBox="0 0 ${dim} ${dim}" shape-rendering="geometricPrecision">` +
    plateEl +
    `<g fill="${fg}">${parts.join("")}</g></svg>`
  );
}

// ---- Canvas rendering (for PNG / clipboard) ------------------------------

function platePath(ctx, plate, scale) {
  ctx.beginPath();
  switch (plate.type) {
    case "rect":
      ctx.rect(plate.x * scale, plate.y * scale, plate.w * scale, plate.h * scale);
      break;
    case "rounded": {
      const x = plate.x * scale, y = plate.y * scale;
      const w = plate.w * scale, h = plate.h * scale, r = plate.r * scale;
      ctx.moveTo(x + r, y);
      ctx.arcTo(x + w, y, x + w, y + h, r);
      ctx.arcTo(x + w, y + h, x, y + h, r);
      ctx.arcTo(x, y + h, x, y, r);
      ctx.arcTo(x, y, x + w, y, r);
      ctx.closePath();
      break;
    }
    case "circle":
      ctx.arc(plate.cx * scale, plate.cy * scale, plate.r * scale, 0, Math.PI * 2);
      break;
    case "hexagon":
      plate.points.forEach(([px, py], i) => {
        const X = px * scale, Y = py * scale;
        if (i === 0) ctx.moveTo(X, Y);
        else ctx.lineTo(X, Y);
      });
      ctx.closePath();
      break;
  }
}

function buildCanvas(px) {
  const { count, isDark } = current;
  const dots = els.moduleStyle.value === "dots";
  const fg = els.fgColor.value;
  const { dim, ox, oy, plate } = layoutFor();

  const scale = Math.max(1, Math.floor(px / dim));
  const size = dim * scale;

  const canvas = document.createElement("canvas");
  canvas.width = size;
  canvas.height = size;
  const ctx = canvas.getContext("2d");

  if (bg() !== "transparent") {
    ctx.fillStyle = bg();
    platePath(ctx, plate, scale);
    ctx.fill();
  }

  ctx.fillStyle = fg;
  for (let r = 0; r < count; r++) {
    for (let c = 0; c < count; c++) {
      if (!isDark(r, c)) continue;
      if (dots && !isFinder(r, c, count)) {
        ctx.beginPath();
        ctx.arc((ox + c + 0.5) * scale, (oy + r + 0.5) * scale, 0.5 * scale, 0, Math.PI * 2);
        ctx.fill();
      } else {
        ctx.fillRect((ox + c) * scale, (oy + r) * scale, scale, scale);
      }
    }
  }
  return canvas;
}

// ---- UI plumbing ---------------------------------------------------------

function setActionsEnabled(enabled) {
  els.downloadPng.disabled = !enabled;
  els.downloadSvg.disabled = !enabled;
  els.copyImg.disabled = !enabled || !(navigator.clipboard && window.ClipboardItem);
}

function showError(msg) {
  current = null;
  els.error.textContent = msg;
  els.error.hidden = false;
  els.preview.innerHTML = '<div class="preview__empty">—</div>';
  setActionsEnabled(false);
}

function paintPreview() {
  if (!current) return;
  els.preview.innerHTML = buildSvg(parseInt(els.size.value, 10));
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
  };

  paintPreview();
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

// Live-update controls. Content changes need a re-encode; everything else is
// just a re-paint of the existing matrix.
let debounce;
els.text.addEventListener("input", () => {
  clearTimeout(debounce);
  debounce = setTimeout(render, 120);
});
els.ecLevel.addEventListener("change", render);

for (const el of [els.fgColor, els.bgColor, els.shape, els.moduleStyle]) {
  el.addEventListener("input", paintPreview);
  el.addEventListener("change", paintPreview);
}
els.transparent.addEventListener("change", paintPreview);
els.border.addEventListener("input", () => {
  els.borderVal.textContent = els.border.value + " modules";
  paintPreview();
});
els.size.addEventListener("input", () => {
  els.sizeVal.textContent = els.size.value + "px";
  paintPreview();
});

// First paint.
render();
