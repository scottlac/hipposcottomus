// Night Sky dashboard
const STORAGE_KEY = "astroCoords";
const DEFAULT_COORDS = { lat: 35.7915, lon: -78.7811, label: "Cary, NC (default)" };

const els = {
  locLabel: document.getElementById("locLabel"),
  useMyLocation: document.getElementById("useMyLocation"),
  highlight: document.getElementById("highlight"),
  highlightIcon: document.getElementById("highlightIcon"),
  highlightTitle: document.getElementById("highlightTitle"),
  highlightDetail: document.getElementById("highlightDetail"),
  sunGrid: document.getElementById("sunGrid"),
  moonSvg: document.getElementById("moonSvg"),
  moonInfo: document.getElementById("moonInfo"),
  passesBody: document.getElementById("passesBody"),
  passesTable: document.getElementById("passesTable"),
  passesEmpty: document.getElementById("passesEmpty"),
};

// ── Coord storage ──────────────────────────────────────────────
function loadStoredCoords() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (typeof parsed.lat === "number" && typeof parsed.lon === "number") {
      return parsed;
    }
  } catch (_) {}
  return null;
}

function storeCoords(lat, lon, label) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ lat, lon, label }));
  } catch (_) {}
}

// ── Fetch & render ─────────────────────────────────────────────
async function refresh(coords) {
  els.locLabel.textContent =
    `${coords.lat.toFixed(2)}, ${coords.lon.toFixed(2)}` +
    (coords.label ? ` · ${coords.label}` : "");
  try {
    const r = await fetch(
      `api/data?lat=${encodeURIComponent(coords.lat)}&lon=${encodeURIComponent(coords.lon)}`
    );
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
    const data = await r.json();
    renderAll(data);
  } catch (err) {
    console.error("astro fetch failed:", err);
    els.sunGrid.innerHTML = `<div class="placeholder">Failed to load data: ${err.message}</div>`;
  }
}

function renderAll(data) {
  renderHighlight(data.highlight);
  renderSun(data.sun);
  renderMoon(data.moon);
  renderPasses(data.issPasses);
}

// ── Highlight ──────────────────────────────────────────────────
function renderHighlight(h) {
  if (!h) return;
  els.highlight.hidden = false;
  els.highlightIcon.textContent = h.icon || "✨";
  els.highlightTitle.textContent = h.title || "";
  els.highlightDetail.textContent = h.detail || "";
}

// ── Sun ────────────────────────────────────────────────────────
function fmtTime(iso) {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d)) return "—";
  return d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

function renderSun(sun) {
  if (!sun) return;
  const rows = [
    { label: "Golden hour (AM start)", value: fmtTime(sun.goldenHourAMStart), cls: "golden" },
    { label: "Sunrise",                value: fmtTime(sun.sunrise),          cls: "rise"   },
    { label: "Golden hour (AM end)",   value: fmtTime(sun.goldenHourAMEnd),  cls: "golden" },
    { label: "Solar noon",             value: fmtTime(sun.solarNoon),        cls: "noon"   },
    { label: "Golden hour (PM start)", value: fmtTime(sun.goldenHourPMStart), cls: "golden" },
    { label: "Sunset",                 value: fmtTime(sun.sunset),           cls: "rise"   },
    { label: "Golden hour (PM end)",   value: fmtTime(sun.goldenHourPMEnd),  cls: "golden" },
    { label: "Civil dusk",             value: fmtTime(sun.civilDusk),        cls: "civil"  },
    { label: "Civil dawn",             value: fmtTime(sun.civilDawn),        cls: "civil"  },
    { label: "Nautical dusk",          value: fmtTime(sun.nauticalDusk),     cls: "nautical" },
    { label: "Nautical dawn",          value: fmtTime(sun.nauticalDawn),     cls: "nautical" },
    { label: "Astronomical dusk",      value: fmtTime(sun.astroDusk),        cls: "astro"  },
    { label: "Astronomical dawn",      value: fmtTime(sun.astroDawn),        cls: "astro"  },
  ];
  els.sunGrid.innerHTML = rows.map(r =>
    `<div class="sun-row sun-row--${r.cls}">
       <span class="sun-row__label">${r.label}</span>
       <span class="sun-row__value">${r.value}</span>
     </div>`
  ).join("");
}

// ── Moon ───────────────────────────────────────────────────────
function drawMoon(illumination, ageDays) {
  // Waxing: age < synodic/2 → illuminated from the right.
  // Waning: age > synodic/2 → illuminated from the left.
  const synodic = 29.530588853;
  const waxing = ageDays < synodic / 2;
  const r = 48;
  const k = Math.max(0, Math.min(1, illumination));

  // phase cos: -1 at new, +1 at full.
  // Terminator x-radius = r * (1 - 2*k), which ellipse is
  //   - drawn on lit side (right for waxing, left for waning).
  const termRx = r * (1 - 2 * k);
  const absRx = Math.abs(termRx);

  // Base disc (unlit).
  const parts = [
    `<circle cx="0" cy="0" r="${r}" fill="#1e1b4b" stroke="#334155" stroke-width="1" />`,
  ];

  // Build the lit crescent/gibbous as a single path.
  if (k <= 0.001) {
    // New moon: nothing lit.
  } else if (k >= 0.999) {
    parts.push(
      `<circle cx="0" cy="0" r="${r}" fill="var(--moon)" stroke="#fde68a" stroke-width="1" />`
    );
  } else {
    // Outer arc: semicircle on the lit side.
    //   Waxing → from (0,-r) via right side to (0,r).
    //   Waning → from (0,-r) via left side to (0,r).
    const outerSweep = waxing ? 1 : 0;
    // Terminator arc sweeps back from (0,r) to (0,-r) along an ellipse.
    // For gibbous (k > 0.5), termRx is negative → we invert sweep so arc
    // bulges toward the lit side (covering more than half the disc).
    let innerSweep;
    if (waxing) {
      innerSweep = termRx >= 0 ? 0 : 1;
    } else {
      innerSweep = termRx >= 0 ? 1 : 0;
    }
    const path = [
      `M 0 ${-r}`,
      `A ${r} ${r} 0 0 ${outerSweep} 0 ${r}`,
      `A ${absRx} ${r} 0 0 ${innerSweep} 0 ${-r}`,
      "Z",
    ].join(" ");
    parts.push(`<path d="${path}" fill="var(--moon)" />`);
    parts.push(
      `<circle cx="0" cy="0" r="${r}" fill="none" stroke="#fde68a" stroke-opacity="0.35" stroke-width="1" />`
    );
  }

  els.moonSvg.innerHTML = parts.join("");
}

function fmtDate(iso) {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d)) return "—";
  return d.toLocaleDateString([], { month: "short", day: "numeric" });
}

function renderMoon(m) {
  if (!m) return;
  drawMoon(m.illumination, m.ageDays);
  const rows = [
    ["Phase",         m.phaseName || "—"],
    ["Illumination",  `${Math.round((m.illumination || 0) * 100)}%`],
    ["Moon age",      `${(m.ageDays || 0).toFixed(1)} days`],
    ["Moonrise",      fmtTime(m.moonrise)],
    ["Moonset",       fmtTime(m.moonset)],
    ["Next new",      fmtDate(m.nextNewMoon)],
    ["Next full",     fmtDate(m.nextFullMoon)],
  ];
  els.moonInfo.innerHTML = rows.map(([l, v]) =>
    `<div class="moon-info__row">
       <span class="moon-info__label">${l}</span>
       <span class="moon-info__value">${v}</span>
     </div>`
  ).join("");
}

// ── ISS passes ─────────────────────────────────────────────────
const COMPASS = ["N","NNE","NE","ENE","E","ESE","SE","SSE","S","SSW","SW","WSW","W","WNW","NW","NNW"];
function compass(deg) {
  const idx = Math.floor(((deg % 360) / 22.5) + 0.5) % 16;
  return COMPASS[(idx + 16) % 16];
}

function fmtDuration(sec) {
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return m > 0 ? `${m}m ${s}s` : `${s}s`;
}

function peakClass(alt) {
  if (alt >= 50) return "peak-high";
  if (alt >= 25) return "peak-mid";
  return "peak-low";
}

function renderPasses(passes) {
  if (!passes || passes.length === 0) {
    els.passesTable.hidden = true;
    els.passesEmpty.hidden = false;
    return;
  }
  els.passesTable.hidden = false;
  els.passesEmpty.hidden = true;
  els.passesBody.innerHTML = passes.map(p => {
    const start = new Date(p.start);
    const peak = new Date(p.peak);
    const dateStr = start.toLocaleDateString([], { weekday: "short", month: "short", day: "numeric" });
    return `<tr>
      <td>${dateStr}</td>
      <td>${fmtTime(p.start)}</td>
      <td>${fmtTime(p.peak)}</td>
      <td class="${peakClass(p.peakAltDeg)}">${p.peakAltDeg.toFixed(0)}°</td>
      <td>${compass(p.startAzDeg)} → ${compass(p.endAzDeg)}</td>
      <td>${fmtDuration(p.durationSec)}</td>
    </tr>`;
  }).join("");
}

// ── Geolocation bootstrap ──────────────────────────────────────
function requestGeolocation() {
  if (!navigator.geolocation) return;
  navigator.geolocation.getCurrentPosition(
    (pos) => {
      const c = {
        lat: pos.coords.latitude,
        lon: pos.coords.longitude,
        label: "your location",
      };
      storeCoords(c.lat, c.lon, c.label);
      refresh(c);
    },
    (err) => {
      console.info("geolocation denied/failed:", err && err.message);
    },
    { timeout: 10000, maximumAge: 10 * 60 * 1000 }
  );
}

els.useMyLocation.addEventListener("click", requestGeolocation);

// Initial load: stored coords, else default; then try geolocation silently.
const startCoords = loadStoredCoords() || DEFAULT_COORDS;
refresh(startCoords);
if (!loadStoredCoords()) {
  requestGeolocation();
}
