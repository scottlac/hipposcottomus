// QR Code Generator — everything runs client-side. We use the vendored
// qrcode-generator library only to compute the module matrix, then render
// it ourselves so we control colors, quiet zone, plate shape, module style,
// a decorative ring, curved ring text, a center logo, and export formats
// (PNG, SVG with real text outlines, and binary STL for 3D printing).
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

const els = {};
for (const id of [
  "payloadType", "textFields", "wifiFields", "contactFields",
  "text", "wifiSsid", "wifiPass", "wifiSec", "wifiHidden",
  "ctName", "ctOrg", "ctPhone", "ctEmail", "ctUrl",
  "ecLevel", "size", "sizeVal", "fgColor", "bgColor", "transparent",
  "shape", "moduleStyle", "border", "borderVal",
  "ringEnabled", "ringColor",
  "logoGroup", "logoFile", "logoEmoji", "logoSize", "logoSizeVal", "logoClear", "logoNote",
  "ringTextGroup", "ringTextTop", "ringTextBottom", "ringTextColor",
  "stlDiameter", "stlBase", "stlRelief", "downloadStlBase", "downloadStlTop",
  "preview", "error", "contrastWarn",
  "downloadPng", "downloadSvg", "copyImg", "copyLink",
]) {
  els[id] = document.getElementById(id);
}

// Holds the most recent successful render so the action buttons can reuse it.
let current = null; // { count, isDark(r,c) }

// Center logo state: an uploaded image (kept as both a data URL for SVG and a
// decoded Image for canvas) or an emoji string — mutually exclusive.
let logoImageEl = null;
let logoDataUrl = null;

// Outline font for ring text (SVG outlines + STL). Loaded async; until it
// arrives the SVG preview falls back to a <textPath>.
let dejavu = null;
const fontReady = new Promise((resolve) => {
  opentype.load("DejaVuSans-Bold.ttf", (err, font) => {
    if (!err) dejavu = font;
    resolve();
  });
});
fontReady.then(() => paintPreview());

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

// ---- Payload building ------------------------------------------------------

// Escape the characters the WIFI: format treats specially.
function wifiEscape(s) {
  return s.replace(/([\\;,:"])/g, "\\$1");
}

function buildPayload() {
  switch (els.payloadType.value) {
    case "wifi": {
      const ssid = els.wifiSsid.value.trim();
      if (!ssid) return "";
      const sec = els.wifiSec.value;
      let out = `WIFI:T:${sec};S:${wifiEscape(ssid)};`;
      if (sec !== "nopass" && els.wifiPass.value) {
        out += `P:${wifiEscape(els.wifiPass.value)};`;
      }
      if (els.wifiHidden.checked) out += "H:true;";
      return out + ";";
    }
    case "contact": {
      const name = els.ctName.value.trim();
      const org = els.ctOrg.value.trim();
      const phone = els.ctPhone.value.trim();
      const email = els.ctEmail.value.trim();
      const url = els.ctUrl.value.trim();
      if (!name && !phone && !email) return "";
      const lines = ["BEGIN:VCARD", "VERSION:3.0"];
      if (name) lines.push(`FN:${name}`);
      if (org) lines.push(`ORG:${org}`);
      if (phone) lines.push(`TEL:${phone}`);
      if (email) lines.push(`EMAIL:${email}`);
      if (url) lines.push(`URL:${url}`);
      lines.push("END:VCARD");
      return lines.join("\r\n");
    }
    default:
      return els.text.value;
  }
}

function emptyPrompt() {
  switch (els.payloadType.value) {
    case "wifi": return "Enter the network name (SSID) to generate a code";
    case "contact": return "Enter a name, phone, or email to generate a code";
    default: return "Type something to generate a code";
  }
}

// ---- Layout ----------------------------------------------------------------

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
    // The inner light plate is drawn whenever the ring is on — even at zero
    // ring width — so the quiet zone can never be painted in the ring color.
    let inner = null;
    if (ringOn) {
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
    const inner = ringOn ? { type: "circle", cx, cy: cx, r: Ri } : null;
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
  const inner = ringOn
    ? { type: "hexagon", points: hexPoints(cx, cx, Ri * HEX_K) }
    : null;
  return { dim, ox: cx - half, oy: cx - half, outer, inner, textArc: null };
}

function layoutFor() {
  return computeLayout();
}

function hasLogo() {
  return !!(logoImageEl || els.logoEmoji.value.trim());
}

// Center-logo geometry in module units: the logo box and the knockout pad
// behind it. Capped at 30% of the code width; error correction is switched to
// High when a logo is set so the covered modules stay recoverable.
function logoSpec(layout) {
  if (!hasLogo() || !current) return null;
  const count = current.count;
  const pct = parseInt(els.logoSize.value, 10);
  const m = (count * pct) / 100;
  const pad = m * 1.18;
  const cx = layout.ox + count / 2;
  const cy = layout.oy + count / 2;
  return {
    x: cx - m / 2, y: cy - m / 2, w: m, h: m, cx, cy,
    pad: { x: cx - pad / 2, y: cy - pad / 2, w: pad, h: pad, r: Math.min(1.2, pad * 0.15) },
  };
}

// ---- Curved-text placement (shared by SVG outlines and STL) ----------------

// Per-character positions along the top or bottom semicircle. Same math as
// the canvas renderer: characters are placed center-out so both labels read
// upright. widthOf(ch) supplies advance widths in module units.
function arcGlyphPlacements(str, cx, cy, r, fs, position, widthOf) {
  const chars = [...str];
  const widths = chars.map(widthOf);
  const spacing = fs * 0.06;
  const total = widths.reduce((a, b) => a + b, 0) + spacing * Math.max(0, chars.length - 1);
  const totalAngle = total / r;
  const out = [];
  if (position === "top") {
    let ang = -Math.PI / 2 - totalAngle / 2;
    for (let i = 0; i < chars.length; i++) {
      ang += widths[i] / 2 / r;
      out.push({ ch: chars[i], w: widths[i], x: cx + r * Math.cos(ang), y: cy + r * Math.sin(ang), rot: ang + Math.PI / 2 });
      ang += (widths[i] / 2 + spacing) / r;
    }
  } else {
    let ang = Math.PI / 2 + totalAngle / 2;
    for (let i = 0; i < chars.length; i++) {
      ang -= widths[i] / 2 / r;
      out.push({ ch: chars[i], w: widths[i], x: cx + r * Math.cos(ang), y: cy + r * Math.sin(ang), rot: ang - Math.PI / 2 });
      ang -= (widths[i] / 2 + spacing) / r;
    }
  }
  return out;
}

// Vertical offset that centers glyphs on the arc line (canvas "middle"
// baseline equivalent).
function fontMiddleOffset(fs) {
  return ((dejavu.ascender + dejavu.descender) / 2 / dejavu.unitsPerEm) * fs;
}

// ---- SVG rendering ----------------------------------------------------------

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

// Ring text as real glyph outlines — survives import into slicers and CAD
// tools, which drop <text> elements.
function textArcOutlineSvg(t) {
  const color = els.ringTextColor.value;
  const fs = t.fontSize;
  const midOff = fontMiddleOffset(fs);
  let out = "";
  for (const [str, pos] of [[t.top, "top"], [t.bottom, "bottom"]]) {
    if (!str) continue;
    const places = arcGlyphPlacements(str, t.cx, t.cy, t.r, fs, pos,
      (ch) => dejavu.getAdvanceWidth(ch, fs));
    for (const p of places) {
      const d = dejavu.getPath(p.ch, -p.w / 2, midOff, fs).toPathData(3);
      if (!d) continue;
      const deg = (p.rot * 180) / Math.PI;
      out += `<path fill="${color}" transform="translate(${p.x.toFixed(3)} ${p.y.toFixed(3)}) rotate(${deg.toFixed(2)})" d="${d}"/>`;
    }
  }
  return out;
}

// Fallback while the outline font is still loading.
function textArcTextPathSvg(t) {
  const color = els.ringTextColor.value;
  const fs = t.fontSize;
  const ls = fs * 0.06;
  const defs = [];
  const texts = [];
  const label = (id, str) =>
    `<text font-family="-apple-system, Segoe UI, Roboto, sans-serif" font-weight="700" ` +
    `font-size="${fs}" letter-spacing="${ls}" fill="${color}">` +
    `<textPath href="#${id}" xlink:href="#${id}" startOffset="50%" text-anchor="middle">${escapeXml(str)}</textPath></text>`;
  if (t.top) {
    defs.push(`<path id="qrArcTop" fill="none" d="M ${t.cx - t.r},${t.cy} A ${t.r},${t.r} 0 0 1 ${t.cx + t.r},${t.cy}"/>`);
    texts.push(label("qrArcTop", t.top));
  }
  if (t.bottom) {
    defs.push(`<path id="qrArcBot" fill="none" d="M ${t.cx + t.r},${t.cy} A ${t.r},${t.r} 0 0 1 ${t.cx - t.r},${t.cy}"/>`);
    texts.push(label("qrArcBot", t.bottom));
  }
  return `<defs>${defs.join("")}</defs>${texts.join("")}`;
}

function logoSvg(spec) {
  const padFill = els.bgColor.value; // always solid so the logo sits cleanly
  let out = `<rect x="${spec.pad.x}" y="${spec.pad.y}" width="${spec.pad.w}" height="${spec.pad.h}" rx="${spec.pad.r}" ry="${spec.pad.r}" fill="${padFill}"/>`;
  if (logoDataUrl) {
    out += `<image x="${spec.x}" y="${spec.y}" width="${spec.w}" height="${spec.h}" preserveAspectRatio="xMidYMid meet" href="${logoDataUrl}"/>`;
  } else {
    const emoji = els.logoEmoji.value.trim();
    out += `<text x="${spec.cx}" y="${spec.cy}" font-size="${spec.w * 0.85}" text-anchor="middle" dominant-baseline="central">${escapeXml(emoji)}</text>`;
  }
  return out;
}

function buildSvg(px) {
  const { count, isDark } = current;
  const dots = els.moduleStyle.value === "dots";
  const fg = els.fgColor.value;
  const ringOn = els.ringEnabled.checked;
  const layout = layoutFor();
  const { dim, ox, oy, outer, inner, textArc } = layout;

  const outerFill = ringOn ? els.ringColor.value : bg();
  let plateEls = "";
  if (outerFill !== "transparent") plateEls += plateSvg(outer, outerFill);
  if (inner && bg() !== "transparent") plateEls += plateSvg(inner, bg());

  const textEls = textArc ? (dejavu ? textArcOutlineSvg(textArc) : textArcTextPathSvg(textArc)) : "";

  // Rects render crisp (no hairline seams between adjacent squares); circles
  // and plates keep smooth anti-aliased curves.
  const rects = [];
  const circles = [];
  for (let r = 0; r < count; r++) {
    for (let c = 0; c < count; c++) {
      if (!isDark(r, c)) continue;
      if (dots && !isFinder(r, c, count)) {
        circles.push(`<circle cx="${ox + c + 0.5}" cy="${oy + r + 0.5}" r="0.5"/>`);
      } else {
        rects.push(`<rect x="${ox + c}" y="${oy + r}" width="1" height="1"/>`);
      }
    }
  }

  const spec = logoSpec(layout);
  const logoEls = spec ? logoSvg(spec) : "";

  return (
    `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" ` +
    `width="${px}" height="${px}" ` +
    `viewBox="0 0 ${dim} ${dim}" shape-rendering="geometricPrecision">` +
    plateEls +
    textEls +
    `<g fill="${fg}" shape-rendering="crispEdges">${rects.join("")}</g>` +
    `<g fill="${fg}">${circles.join("")}</g>` +
    logoEls +
    `</svg>`
  );
}

// ---- Canvas rendering (for PNG / clipboard) ---------------------------------

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
    let ang = -Math.PI / 2 - totalAngle / 2;
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
    let ang = Math.PI / 2 + totalAngle / 2;
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

function drawLogoCanvas(ctx, spec, scale) {
  // Knockout pad.
  ctx.fillStyle = els.bgColor.value;
  const p = spec.pad;
  const x = p.x * scale, y = p.y * scale, w = p.w * scale, h = p.h * scale, r = p.r * scale;
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
  ctx.fill();

  if (logoImageEl) {
    // Fit the image inside the logo box, preserving aspect ratio.
    const bw = spec.w * scale, bh = spec.h * scale;
    const ar = logoImageEl.naturalWidth / logoImageEl.naturalHeight || 1;
    let dw = bw, dh = bh;
    if (ar > 1) dh = bw / ar;
    else dw = bh * ar;
    ctx.drawImage(
      logoImageEl,
      spec.cx * scale - dw / 2,
      spec.cy * scale - dh / 2,
      dw, dh
    );
  } else {
    const emoji = els.logoEmoji.value.trim();
    ctx.font = `${spec.w * 0.85 * scale}px sans-serif`;
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.fillText(emoji, spec.cx * scale, spec.cy * scale);
  }
}

function buildCanvas(px) {
  const { count, isDark } = current;
  const dots = els.moduleStyle.value === "dots";
  const fg = els.fgColor.value;
  const ringOn = els.ringEnabled.checked;
  const layout = layoutFor();
  const { dim, ox, oy, outer, inner, textArc } = layout;

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

  const spec = logoSpec(layout);
  if (spec) drawLogoCanvas(ctx, spec, scale);

  return canvas;
}

// ---- STL export --------------------------------------------------------------

function stlSettings() {
  const clamp = (v, lo, hi, dflt) => {
    const n = parseFloat(v);
    return isFinite(n) ? Math.min(hi, Math.max(lo, n)) : dflt;
  };
  return {
    diameter: clamp(els.stlDiameter.value, 30, 200, 90),
    base: clamp(els.stlBase.value, 0.4, 6, 2.4),
    relief: clamp(els.stlRelief.value, 0.2, 3, 0.8),
  };
}

function plateOutlinePts(outer) {
  switch (outer.type) {
    case "rect": return qrStl.rectPts(outer.x, outer.y, outer.w, outer.h);
    case "rounded": return qrStl.roundedRectPts(outer.x, outer.y, outer.w, outer.h, outer.r, 8);
    case "circle": return qrStl.ngon(outer.cx, outer.cy, outer.r, 96);
    case "hexagon": return outer.points.map((p) => p.slice());
  }
  return [];
}

// Build the triangle soup for one STL part ('base' or 'top'), in millimetres.
// Module coordinates are y-down (SVG convention); STL space is y-up, so points
// are mirrored through T — extrudePolygon re-normalizes winding, and the flip
// keeps the code un-mirrored when viewed from +Z (the printed face).
function collectStlTris(which) {
  const { diameter, base, relief } = stlSettings();
  const layout = layoutFor();
  const { dim, ox, oy, textArc } = layout;
  const s = diameter / dim;
  const T = ([x, y]) => [x * s, (dim - y) * s];

  const tris = [];

  if (which === "base") {
    const poly = plateOutlinePts(layout.outer).map(T);
    qrStl.extrudePolygon(poly, [], 0, base, tris);
    return tris;
  }

  const z0 = base;
  const z1 = base + relief;
  const { count, isDark } = current;
  const dots = els.moduleStyle.value === "dots";

  // Square cells (every dark module in square mode; only the finder eyes in
  // dots mode), merged into horizontal runs to keep triangle counts down.
  const isSquareCell = (r, c) => isDark(r, c) && (!dots || isFinder(r, c, count));
  for (let r = 0; r < count; r++) {
    let c = 0;
    while (c < count) {
      if (!isSquareCell(r, c)) { c++; continue; }
      let e = c;
      while (e < count && isSquareCell(r, e)) e++;
      const poly = qrStl.rectPts(ox + c, oy + r, e - c, 1).map(T);
      qrStl.extrudePolygon(poly, [], z0, z1, tris);
      c = e;
    }
  }

  if (dots) {
    for (let r = 0; r < count; r++) {
      for (let c = 0; c < count; c++) {
        if (!isDark(r, c) || isFinder(r, c, count)) continue;
        const poly = qrStl.ngon(ox + c + 0.5, oy + r + 0.5, 0.5, 16).map(T);
        qrStl.extrudePolygon(poly, [], z0, z1, tris);
      }
    }
  }

  // Ring text, extruded from real glyph outlines.
  if (textArc && dejavu) {
    const fs = textArc.fontSize;
    const midOff = fontMiddleOffset(fs);
    for (const [str, pos] of [[textArc.top, "top"], [textArc.bottom, "bottom"]]) {
      if (!str) continue;
      const places = arcGlyphPlacements(str, textArc.cx, textArc.cy, textArc.r, fs, pos,
        (ch) => dejavu.getAdvanceWidth(ch, fs));
      for (const p of places) {
        const path = dejavu.getPath(p.ch, -p.w / 2, midOff, fs);
        const groups = qrStl.glyphToPolys(path.commands, 8);
        const cos = Math.cos(p.rot), sin = Math.sin(p.rot);
        const place = ([gx, gy]) => T([p.x + gx * cos - gy * sin, p.y + gx * sin + gy * cos]);
        for (const g of groups) {
          qrStl.extrudePolygon(g.outer.map(place), g.holes.map((h) => h.map(place)), z0, z1, tris);
        }
      }
    }
  }

  return tris;
}

async function downloadStl(which) {
  if (!current) return;
  await fontReady; // glyph outlines need the font; resolves even on failure
  const tris = collectStlTris(which);
  const buf = qrStl.writeBinary(tris, `hipposcottomus qr ${which}`);
  const blob = new Blob([buf], { type: "model/stl" });
  const url = URL.createObjectURL(blob);
  triggerDownload(url, slugFilename(which === "base" ? "base.stl" : "top.stl"));
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// ---- Contrast warning ---------------------------------------------------------

function relLuminance(hex) {
  const n = parseInt(hex.slice(1), 16);
  const lin = (v) => {
    v /= 255;
    return v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * lin((n >> 16) & 255) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255);
}

function updateContrastWarn() {
  let msg = "";
  if (current) {
    const fgL = relLuminance(els.fgColor.value);
    // A transparent code usually lands on a light surface; assume white.
    const bgL = els.transparent.checked ? 1 : relLuminance(els.bgColor.value);
    if (fgL > bgL) {
      msg = "⚠️ Light-on-dark (inverted) codes fail in many scanner apps — consider swapping the colors.";
    } else if ((bgL + 0.05) / (fgL + 0.05) < 3) {
      msg = "⚠️ Low contrast between the code and its background — this may not scan reliably.";
    }
  }
  els.contrastWarn.textContent = msg;
  els.contrastWarn.hidden = !msg;
}

// ---- Share link -----------------------------------------------------------------

const SHARE_FIELDS = [
  ["pt", "payloadType"], ["d", "text"],
  ["ws", "wifiSsid"], ["wp", "wifiPass"], ["wsec", "wifiSec"],
  ["cn", "ctName"], ["co", "ctOrg"], ["cp", "ctPhone"], ["ce", "ctEmail"], ["cu", "ctUrl"],
  ["ec", "ecLevel"], ["sz", "size"], ["fg", "fgColor"], ["bg", "bgColor"],
  ["sh", "shape"], ["ms", "moduleStyle"], ["bd", "border"],
  ["rc", "ringColor"], ["tt", "ringTextTop"], ["tb", "ringTextBottom"], ["tc", "ringTextColor"],
  ["le", "logoEmoji"], ["ls", "logoSize"],
  ["sd", "stlDiameter"], ["sb", "stlBase"], ["sr", "stlRelief"],
];
const SHARE_CHECKS = [["tr", "transparent"], ["wh", "wifiHidden"], ["ri", "ringEnabled"]];

function settingsToHash() {
  const p = new URLSearchParams();
  for (const [key, id] of SHARE_FIELDS) {
    if (els[id].value) p.set(key, els[id].value);
  }
  for (const [key, id] of SHARE_CHECKS) {
    if (els[id].checked) p.set(key, "1");
  }
  return p.toString();
}

function applySettingsFromHash() {
  if (!location.hash || location.hash.length < 2) return;
  let p;
  try {
    p = new URLSearchParams(location.hash.slice(1));
  } catch (e) {
    return;
  }
  for (const [key, id] of SHARE_FIELDS) {
    const v = p.get(key);
    if (v === null) continue;
    const el = els[id];
    if (el.tagName === "SELECT" && ![...el.options].some((o) => o.value === v)) continue;
    el.value = v;
  }
  for (const [key, id] of SHARE_CHECKS) {
    els[id].checked = p.get(key) === "1";
  }
  els.sizeVal.textContent = els.size.value + "px";
  els.borderVal.textContent = els.border.value + " modules";
  els.logoSizeVal.textContent = els.logoSize.value + "%";
}

// ---- UI plumbing ------------------------------------------------------------------

function setActionsEnabled(enabled) {
  els.downloadPng.disabled = !enabled;
  els.downloadSvg.disabled = !enabled;
  els.downloadStlBase.disabled = !enabled;
  els.downloadStlTop.disabled = !enabled;
  els.copyLink.disabled = !enabled;
  els.copyImg.disabled = !enabled || !(navigator.clipboard && window.ClipboardItem);
}

function showError(msg) {
  current = null;
  els.error.textContent = msg;
  els.error.hidden = false;
  els.preview.innerHTML = '<div class="preview__empty">—</div>';
  setActionsEnabled(false);
  updateContrastWarn();
}

function paintPreview() {
  if (!current) return;
  els.preview.innerHTML = buildSvg(parseInt(els.size.value, 10));
  updateContrastWarn();
}

function render() {
  const payload = buildPayload();
  els.error.hidden = true;

  if (!payload) {
    current = null;
    els.preview.innerHTML = `<div class="preview__empty">${emptyPrompt()}</div>`;
    setActionsEnabled(false);
    updateContrastWarn();
    return;
  }

  let qr;
  try {
    // typeNumber 0 => auto-pick the smallest version that fits the data.
    qr = qrcode(0, els.ecLevel.value);
    qr.addData(payload);
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
  let raw;
  switch (els.payloadType.value) {
    case "wifi": raw = "wifi-" + els.wifiSsid.value.trim(); break;
    case "contact": raw = "contact-" + (els.ctName.value.trim() || els.ctEmail.value.trim()); break;
    default: raw = els.text.value.trim();
  }
  raw = raw.slice(0, 40);
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

els.copyLink.addEventListener("click", async () => {
  if (!current) return;
  const url = location.origin + location.pathname + "#" + settingsToHash();
  const label = els.copyLink.textContent;
  try {
    await navigator.clipboard.writeText(url);
    els.copyLink.textContent = "Copied!";
    els.copyLink.classList.add("btn--copied");
  } catch (e) {
    els.copyLink.textContent = "Copy failed";
  }
  setTimeout(() => {
    els.copyLink.textContent = label;
    els.copyLink.classList.remove("btn--copied");
  }, 1500);
});

els.downloadStlBase.addEventListener("click", () => downloadStl("base"));
els.downloadStlTop.addEventListener("click", () => downloadStl("top"));

// Reflect current shape/ring/logo state in which controls are usable.
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

  const type = els.payloadType.value;
  els.textFields.hidden = type !== "text";
  els.wifiFields.hidden = type !== "wifi";
  els.contactFields.hidden = type !== "contact";

  els.logoClear.disabled = !hasLogo();
}

// Switching error correction to High keeps the code readable under the logo.
function forceHighEcForLogo() {
  if (hasLogo() && els.ecLevel.value !== "H") {
    els.ecLevel.value = "H";
    els.logoNote.hidden = false;
  }
}

els.logoFile.addEventListener("change", () => {
  const file = els.logoFile.files && els.logoFile.files[0];
  if (!file) return;
  const reader = new FileReader();
  reader.onload = () => {
    const img = new Image();
    img.onload = () => {
      logoImageEl = img;
      logoDataUrl = reader.result;
      els.logoEmoji.value = "";
      forceHighEcForLogo();
      syncControls();
      render();
    };
    img.src = reader.result;
  };
  reader.readAsDataURL(file);
});

els.logoEmoji.addEventListener("input", () => {
  if (els.logoEmoji.value.trim()) {
    logoImageEl = null;
    logoDataUrl = null;
    els.logoFile.value = "";
    forceHighEcForLogo();
  }
  syncControls();
  render();
});

els.logoClear.addEventListener("click", () => {
  logoImageEl = null;
  logoDataUrl = null;
  els.logoFile.value = "";
  els.logoEmoji.value = "";
  els.logoNote.hidden = true;
  syncControls();
  paintPreview();
});

els.logoSize.addEventListener("input", () => {
  els.logoSizeVal.textContent = els.logoSize.value + "%";
  paintPreview();
});

// Live-update controls. Payload changes need a re-encode; everything else is
// just a re-paint of the existing matrix.
let debounce;
function debouncedRender() {
  clearTimeout(debounce);
  debounce = setTimeout(render, 120);
}

for (const id of ["text", "wifiSsid", "wifiPass", "ctName", "ctOrg", "ctPhone", "ctEmail", "ctUrl"]) {
  els[id].addEventListener("input", debouncedRender);
}
els.wifiSec.addEventListener("change", render);
els.wifiHidden.addEventListener("change", render);
els.payloadType.addEventListener("change", () => { syncControls(); render(); });
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

// First paint (restoring shared-link settings if present).
applySettingsFromHash();
if (els.logoEmoji.value.trim()) forceHighEcForLogo();
syncControls();
render();
