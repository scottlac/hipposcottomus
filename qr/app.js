// QR Code Generator — everything runs client-side. We use the vendored
// qrcode-generator library only to compute the module matrix, then render
// it ourselves so we control colors, quiet zone, plate shape, module style,
// a decorative ring, curved ring text, and export format.
//
// Important constraint: the *scannable* part of a QR code is always a square
// grid of modules. Reshaping the data grid breaks scanning, so the "shapes"
// here are decorative plates that frame the square code (great for a round
// 3D-printed coaster) — the code itself stays square and the light border
// around it acts as the required quiet zone.

// Encode input as UTF-8 so links with accents/emoji scan correctly.
qrcode.stringToBytes = qrcode.stringToBytesFuncs["UTF-8"];

const SQRT2 = Math.SQRT2;
const HEX_K = 2 / Math.sqrt(3); // circumradius / inscribed-circle radius
const QUIET = 4; // minimum light modules around the code (spec quiet zone)

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
  ringEnabled: document.getElementById("ringEnabled"),
  ringColor: document.getElementById("ringColor"),
  ringTextGroup: document.getElementById("ringTextGroup"),
  ringTextTop: document.getElementById("ringTextTop"),
  ringTextBottom: document.getElementById("ringTextBottom"),
  ringTextColor: document.getElementById("ringTextColor"),
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

function escapeXml(s) {
  return s.replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&apos;" }[c])
  );
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

function hexPoints(cx, cy, rr) {
  const p = [];
  for (let i = 0; i < 6; i++) {
    const a = (Math.PI / 180) * (60 * i - 90); // pointy-top
    p.push([cx + rr * Math.cos(a), cy + rr * Math.sin(a)]);
  }
  return p;
}

// Compute the full render geometry in *module units*: the viewBox size, the
// offset of module (0,0), the outer plate, an optional inner light plate (when
// a colored ring is on), and optional curved ring text (circle only).
function computeLayout() {
  const count = current.count;
  const shape = els.shape.value;
  const border = parseInt(els.border.value, 10); // >= QUIET
  const ringOn = els.ringEnabled.checked;
  const half = count / 2;
  const userExtra = border - QUIET; // rim beyond the mandatory quiet zone

  const topText = els.ringTextTop.value.trim();
  const botText = els.ringTextBottom.value.trim();
  const wantText = shape === "circle" && (topText.length > 0 || botText.length > 0);

  // --- square / rounded plates ---
  if (shape === "square" || shape === "rounded") {
    const dim = count + border * 2;
    const rounded = shape === "rounded";
    const outer = rounded
      ? { type: "rounded", x: 0, y: 0, w: dim, h: dim, r: Math.max(2, border * 0.8) }
      : { type: "rect", x: 0, y: 0, w: dim, h: dim };
    let inner = null;
    if (ringOn && userExtra > 0.001) {
      const io = border - QUIET;
      const is = count + QUIET * 2;
      inner = rounded
        ? { type: "rounded", x: io, y: io, w: is, h: is, r: Math.max(2, QUIET * 0.8) }
        : { type: "rect", x: io, y: io, w: is, h: is };
    }
    return { dim, ox: border, oy: border, outer, inner, textArc: null };
  }

  // --- circle / hexagon plates ---
  // Inner light radius must enclose the square (corners included) with a quiet
  // zone; the corner term (half*SQRT2) dominates for real codes.
  const Ri = Math.max(half + QUIET, half * SQRT2 + 0.5);

  let ringBand = Math.max(0, userExtra);
  let fontSize = 0;
  if (wantText) {
    fontSize = Math.max(2.4, count * 0.07);
    ringBand = Math.max(ringBand, fontSize * 1.7); // guarantee room for text
  }
  const R = Ri + ringBand;

  if (shape === "circle") {
    const dim = 2 * Math.ceil(R);
    const cx = dim / 2;
    const outer = { type: "circle", cx, cy: cx, r: R };
    const inner = ringOn && ringBand > 0.001 ? { type: "circle", cx, cy: cx, r: Ri } : null;
    let textArc = null;
    if (wantText) {
      // Shrink the font if a label is too long for its semicircle.
      const rMid = Ri + ringBand / 2;
      const maxArc = Math.PI * rMid * 0.92;
      const longest = Math.max(topText.length, botText.length);
      const est = longest * fontSize * 0.62;
      if (est > maxArc) fontSize *= maxArc / est;
      textArc = { cx, cy: cx, r: rMid, fontSize, top: topText, bottom: botText };
    }
    return { dim, ox: cx - half, oy: cx - half, outer, inner, textArc };
  }

  // hexagon
  const rHex = R * HEX_K;
  const dim = 2 * Math.ceil(rHex);
  const cx = dim / 2;
  const outer = { type: "hexagon", points: hexPoints(cx, cx, rHex) };
  const inner =
    ringOn && ringBand > 0.001
      ? { type: "hexagon", points: hexPoints(cx, cx, Ri * HEX_K) }
      : null;
  return { dim, ox: cx - half, oy: cx - half, outer, inner, textArc: null };
}

function layoutFor() {
  return computeLayout();
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

function textArcSvg(t) {
  const color = els.ringTextColor.value;
  const fs = t.fontSize;
  const ls = fs * 0.06;
  const defs = [];
  const texts = [];
  const label = (id, str) =>
    `<text font-family="-apple-system, Segoe UI, Roboto, sans-serif" font-weight="700" ` +
    `font-size="${fs}" letter-spacing="${ls}" fill="${color}">` +
    `<textPath href="#${id}" startOffset="50%" text-anchor="middle">${escapeXml(str)}</textPath></text>`;
  if (t.top) {
    // Upper semicircle, left -> right (upright across the top).
    defs.push(`<path id="qrArcTop" fill="none" d="M ${t.cx - t.r},${t.cy} A ${t.r},${t.r} 0 0 1 ${t.cx + t.r},${t.cy}"/>`);
    texts.push(label("qrArcTop", t.top));
  }
  if (t.bottom) {
    // Lower semicircle traversed right -> left (upright across the bottom).
    defs.push(`<path id="qrArcBot" fill="none" d="M ${t.cx + t.r},${t.cy} A ${t.r},${t.r} 0 0 1 ${t.cx - t.r},${t.cy}"/>`);
    texts.push(label("qrArcBot", t.bottom));
  }
  return `<defs>${defs.join("")}</defs>${texts.join("")}`;
}

function buildSvg(px) {
  const { count, isDark } = current;
  const dots = els.moduleStyle.value === "dots";
  const fg = els.fgColor.value;
  const ringOn = els.ringEnabled.checked;
  const { dim, ox, oy, outer, inner, textArc } = layoutFor();

  const outerFill = ringOn ? els.ringColor.value : bg();
  let plateEls = "";
  if (outerFill !== "transparent") plateEls += plateSvg(outer, outerFill);
  if (inner && bg() !== "transparent") plateEls += plateSvg(inner, bg());

  const textEls = textArc ? textArcSvg(textArc) : "";

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
    plateEls +
    textEls +
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

// Draw text along a circular arc, centered at the top or bottom of the circle.
function drawArcText(ctx, str, cx, cy, r, fontPx, color, position) {
  if (!str) return;
  ctx.save();
  ctx.fillStyle = color;
  ctx.font = `700 ${fontPx}px -apple-system, "Segoe UI", Roboto, sans-serif`;
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  const chars = [...str];
  const widths = chars.map((ch) => ctx.measureText(ch).width);
  const spacing = fontPx * 0.06;
  const totalW = widths.reduce((a, b) => a + b, 0) + spacing * Math.max(0, chars.length - 1);
  const totalAngle = totalW / r;

  if (position === "top") {
    let ang = -Math.PI / 2 - totalAngle / 2; // start left of top
    for (let i = 0; i < chars.length; i++) {
      ang += widths[i] / 2 / r;
      ctx.save();
      ctx.translate(cx + r * Math.cos(ang), cy + r * Math.sin(ang));
      ctx.rotate(ang + Math.PI / 2);
      ctx.fillText(chars[i], 0, 0);
      ctx.restore();
      ang += (widths[i] / 2 + spacing) / r;
    }
  } else {
    let ang = Math.PI / 2 + totalAngle / 2; // start right of bottom, go left
    for (let i = 0; i < chars.length; i++) {
      ang -= widths[i] / 2 / r;
      ctx.save();
      ctx.translate(cx + r * Math.cos(ang), cy + r * Math.sin(ang));
      ctx.rotate(ang - Math.PI / 2);
      ctx.fillText(chars[i], 0, 0);
      ctx.restore();
      ang -= (widths[i] / 2 + spacing) / r;
    }
  }
  ctx.restore();
}

function buildCanvas(px) {
  const { count, isDark } = current;
  const dots = els.moduleStyle.value === "dots";
  const fg = els.fgColor.value;
  const ringOn = els.ringEnabled.checked;
  const { dim, ox, oy, outer, inner, textArc } = layoutFor();

  const scale = Math.max(1, Math.floor(px / dim));
  const size = dim * scale;

  const canvas = document.createElement("canvas");
  canvas.width = size;
  canvas.height = size;
  const ctx = canvas.getContext("2d");

  const outerFill = ringOn ? els.ringColor.value : bg();
  if (outerFill !== "transparent") {
    ctx.fillStyle = outerFill;
    platePath(ctx, outer, scale);
    ctx.fill();
  }
  if (inner && bg() !== "transparent") {
    ctx.fillStyle = bg();
    platePath(ctx, inner, scale);
    ctx.fill();
  }

  if (textArc) {
    const col = els.ringTextColor.value;
    drawArcText(ctx, textArc.top, textArc.cx * scale, textArc.cy * scale, textArc.r * scale, textArc.fontSize * scale, col, "top");
    drawArcText(ctx, textArc.bottom, textArc.cx * scale, textArc.cy * scale, textArc.r * scale, textArc.fontSize * scale, col, "bottom");
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

// Reflect current shape/ring state in which controls are usable.
function syncControls() {
  const isCircle = els.shape.value === "circle";
  els.ringTextGroup.classList.toggle("is-disabled", !isCircle);
  for (const el of [els.ringTextTop, els.ringTextBottom, els.ringTextColor]) {
    el.disabled = !isCircle;
  }
  const ringOn = els.ringEnabled.checked;
  els.ringColor.disabled = !ringOn;
  // A colored ring implies an opaque plate, so transparency doesn't apply.
  els.transparent.disabled = ringOn;
  if (ringOn && els.transparent.checked) els.transparent.checked = false;
}

// Live-update controls. Content changes need a re-encode; everything else is
// just a re-paint of the existing matrix.
let debounce;
els.text.addEventListener("input", () => {
  clearTimeout(debounce);
  debounce = setTimeout(render, 120);
});
els.ecLevel.addEventListener("change", render);

for (const el of [
  els.fgColor, els.bgColor, els.shape, els.moduleStyle,
  els.ringColor, els.ringTextTop, els.ringTextBottom, els.ringTextColor,
]) {
  el.addEventListener("input", () => { syncControls(); paintPreview(); });
  el.addEventListener("change", () => { syncControls(); paintPreview(); });
}

els.transparent.addEventListener("change", paintPreview);

els.ringEnabled.addEventListener("change", () => {
  // Give the ring something to show: nudge the border out if it's minimal.
  if (els.ringEnabled.checked && parseInt(els.border.value, 10) < 8) {
    els.border.value = 8;
    els.borderVal.textContent = "8 modules";
  }
  syncControls();
  paintPreview();
});

els.border.addEventListener("input", () => {
  els.borderVal.textContent = els.border.value + " modules";
  paintPreview();
});
els.size.addEventListener("input", () => {
  els.sizeVal.textContent = els.size.value + "px";
  paintPreview();
});

// First paint.
syncControls();
render();
