// ===== Site Analytics frontend =====

const REFRESH_MS = 30_000;

const DASHBOARD_META = {
  home:      { label: "Home",            color: "#38bdf8", emoji: "🦛" },
  lake:      { label: "Jordan Lake",     color: "#4ade80", emoji: "🌊" },
  ftl:       { label: "Ft. Lauderdale",  color: "#fb923c", emoji: "⚓" },
  poker:     { label: "Poker Equity",    color: "#facc15", emoji: "🂡" },
  astro:     { label: "Night Sky",       color: "#c084fc", emoji: "🔭" },
  analytics: { label: "Analytics",       color: "#f472b6", emoji: "📊" },
};

const API_META = {
  "USACE":          { label: "USACE (Jordan Lake report)",   category: "lake" },
  "USGS":           { label: "USGS (water level backfill)",  category: "lake" },
  "NWS-Raleigh":    { label: "NWS Raleigh (lake forecast)",  category: "lake" },
  "NOAA-Tides":     { label: "NOAA CO-OPS (tide predictions)", category: "ftl" },
  "NOAA-WaterTemp": { label: "NOAA CO-OPS (FTL water temp)", category: "ftl" },
  "NWS-Miami":      { label: "NWS Miami (FTL forecast)",     category: "ftl" },
  "NDBC-Marine":    { label: "NDBC (marine forecast)",       category: "ftl" },
  "CelesTrak-TLE":  { label: "CelesTrak (ISS TLE)",          category: "astro" },
};

// ===== Helpers =====

function formatUptime(seconds) {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${Math.floor(seconds)}s`;
}

function formatRelative(iso) {
  if (!iso || iso.startsWith("0001-01-01")) return "never";
  const then = new Date(iso);
  const now = new Date();
  const diff = (now - then) / 1000;
  if (diff < 60) return `${Math.floor(diff)}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

function sumSeries(series) {
  let total = 0;
  for (const s of series) {
    for (const p of s.points) total += p.count;
  }
  return total;
}

// Convert a 2-letter ISO code into a regional-indicator flag emoji.
function flagEmoji(iso) {
  if (!iso || iso.length !== 2 || iso === "??") return "🏳️";
  const base = 0x1F1E6 - "A".charCodeAt(0);
  const codes = [...iso.toUpperCase()].map((c) => base + c.charCodeAt(0));
  return String.fromCodePoint(...codes);
}

const regionNames = new Intl.DisplayNames(["en"], { type: "region" });

function countryName(iso) {
  if (!iso || iso === "??") return "Unknown";
  try {
    return regionNames.of(iso) || iso;
  } catch {
    return iso;
  }
}

// ===== Rendering =====

let pageViewsChart;

function renderCards(data) {
  document.getElementById("totalViews").textContent = sumSeries(data.series).toLocaleString();
  document.getElementById("uptime").textContent = formatUptime(data.uptimeSec);
  document.getElementById("memory").textContent = `${data.runtime.memAllocMB.toFixed(1)} MB`;
  document.getElementById("memDetail").textContent = `of ${data.runtime.memSysMB.toFixed(1)} MB reserved`;
  document.getElementById("goroutines").textContent = data.runtime.goroutines;
  document.getElementById("goVersion").textContent = data.runtime.goVersion;
}

function renderPageViewsChart(data) {
  // Union of all dates across all kinds
  const dateSet = new Set();
  for (const s of data.series) {
    for (const p of s.points) dateSet.add(p.date);
  }
  const dates = Array.from(dateSet).sort();

  const datasets = data.series.map((s) => {
    const meta = DASHBOARD_META[s.kind] || { label: s.kind, color: "#94a3b8" };
    const byDate = Object.fromEntries(s.points.map((p) => [p.date, p.count]));
    return {
      label: `${meta.emoji || ""} ${meta.label}`.trim(),
      data: dates.map((d) => byDate[d] || 0),
      backgroundColor: meta.color + "cc",
      borderColor: meta.color,
      borderWidth: 1,
      fill: true,
      tension: 0.25,
    };
  });

  const ctx = document.getElementById("pageViewsChart");
  if (pageViewsChart) pageViewsChart.destroy();

  pageViewsChart = new Chart(ctx, {
    type: "line",
    data: { labels: dates, datasets },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: "index", intersect: false },
      scales: {
        x: {
          type: "time",
          time: { unit: "day", tooltipFormat: "MMM d, yyyy" },
          grid: { color: "#334155" },
          ticks: { color: "#94a3b8" },
        },
        y: {
          stacked: true,
          beginAtZero: true,
          grid: { color: "#334155" },
          ticks: { color: "#94a3b8", precision: 0 },
        },
      },
      plugins: {
        legend: { labels: { color: "#e2e8f0" } },
        tooltip: {
          callbacks: {
            footer: (items) => {
              const total = items.reduce((sum, i) => sum + i.parsed.y, 0);
              return `Total: ${total}`;
            },
          },
        },
      },
    },
  });
}

function renderAPIHealth(data) {
  const container = document.getElementById("apiHealth");
  const sources = Object.keys(data.apiHealth).sort();
  if (sources.length === 0) {
    container.innerHTML = `<p class="muted">No API calls recorded yet.</p>`;
    return;
  }

  container.innerHTML = sources.map((key) => {
    const entry = data.apiHealth[key];
    const meta = API_META[key] || { label: key };
    const total = entry.success + entry.failure;
    const successRate = total === 0 ? 100 : (100 * entry.success / total);
    const status = entry.failure === 0
      ? "ok"
      : entry.lastSuccess > entry.lastFailure ? "recovered" : "failing";
    return `
      <div class="api-row api-row--${status}">
        <div class="api-row__main">
          <div class="api-row__name">${meta.label}</div>
          <div class="api-row__stats">
            <span class="api-row__rate">${successRate.toFixed(1)}%</span>
            <span class="muted">${entry.success.toLocaleString()} ok · ${entry.failure.toLocaleString()} fail</span>
          </div>
        </div>
        <div class="api-row__meta">
          <div>Last ok: <strong>${formatRelative(entry.lastSuccess)}</strong></div>
          <div>Last fail: <strong>${formatRelative(entry.lastFailure)}</strong></div>
          ${entry.lastError ? `<div class="api-row__error" title="${entry.lastError.replace(/"/g, "&quot;")}">${entry.lastError}</div>` : ""}
        </div>
      </div>
    `;
  }).join("");
}

function renderHeatmap(data) {
  const container = document.getElementById("heatmap");
  const grid = data.heatmap; // [7][24]
  let max = 0;
  for (let d = 0; d < 7; d++) for (let h = 0; h < 24; h++) if (grid[d][h] > max) max = grid[d][h];

  const days = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  const cells = [];
  // Header row: hour labels
  cells.push(`<div class="heatmap__corner"></div>`);
  for (let h = 0; h < 24; h++) {
    cells.push(`<div class="heatmap__hour">${h}</div>`);
  }
  // Data rows
  for (let d = 0; d < 7; d++) {
    cells.push(`<div class="heatmap__day">${days[d]}</div>`);
    for (let h = 0; h < 24; h++) {
      const v = grid[d][h];
      const intensity = max === 0 ? 0 : v / max;
      cells.push(
        `<div class="heatmap__cell" style="background: rgba(56, 189, 248, ${intensity.toFixed(2)})" title="${days[d]} ${h}:00 — ${v} views"></div>`
      );
    }
  }
  container.innerHTML = cells.join("");
}

function renderRuntime(data) {
  const dl = document.getElementById("runtime");
  const rt = data.runtime;
  dl.innerHTML = `
    <dt>Go version</dt><dd>${rt.goVersion}</dd>
    <dt>CPUs</dt><dd>${rt.numCPU}</dd>
    <dt>Goroutines</dt><dd>${rt.goroutines}</dd>
    <dt>Heap alloc</dt><dd>${rt.memAllocMB.toFixed(2)} MB</dd>
    <dt>OS reserved</dt><dd>${rt.memSysMB.toFixed(2)} MB</dd>
    <dt>GC cycles</dt><dd>${rt.gcCount.toLocaleString()}</dd>
    <dt>Last GC pause</dt><dd>${rt.lastGCPauseMs.toFixed(3)} ms</dd>
    <dt>Process started</dt><dd>${new Date(data.startedAt).toLocaleString()}</dd>
  `;
}

let leafletMap;
let heatLayer;
let markerLayer;
let mapDisabledNote;

function renderMap(data) {
  const container = document.getElementById("map");

  if (!data.geoEnabled) {
    if (!mapDisabledNote) {
      container.innerHTML = `
        <div class="map__disabled">
          <p class="muted">
            Geo lookup is disabled on this server — the MaxMind GeoLite2 City
            DB is not loaded. The map will appear here once the initContainer
            has downloaded the DB.
          </p>
        </div>`;
      mapDisabledNote = true;
    }
    return;
  }

  // Lazy-init the map + tile layer on the first enabled render.
  if (!leafletMap) {
    container.innerHTML = "";
    mapDisabledNote = false;
    leafletMap = L.map(container, {
      attributionControl: true,
      worldCopyJump: true,
      minZoom: 1,
    }).setView([20, 0], 2);
    L.tileLayer("https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}.png", {
      attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> &copy; <a href="https://carto.com/attributions">CARTO</a>',
      maxZoom: 19,
    }).addTo(leafletMap);
  }

  const raw = data.cityHeatmap || [];

  // Tear down any existing layers before re-rendering.
  if (heatLayer) {
    leafletMap.removeLayer(heatLayer);
    heatLayer = null;
  }
  if (markerLayer) {
    leafletMap.removeLayer(markerLayer);
    markerLayer = null;
  }
  if (raw.length === 0) return;

  // Circle markers: always visible, regardless of heat-layer tuning.
  const maxCount = raw.reduce((m, p) => Math.max(m, p.count), 1);
  markerLayer = L.layerGroup(
    raw.map((p) => {
      // Radius scales with sqrt(count) so a 4x-count bucket is 2x the
      // visual area.
      const radius = Math.max(6, Math.min(22, 4 + 3 * Math.sqrt(p.count)));
      return L.circleMarker([p.lat, p.lon], {
        radius,
        color: "#f87171",
        weight: 1.5,
        fillColor: "#facc15",
        fillOpacity: 0.65,
      }).bindTooltip(
        `${p.count} ${p.count === 1 ? "visit" : "visits"} near ${p.lat.toFixed(1)}, ${p.lon.toFixed(1)}`
      );
    })
  ).addTo(leafletMap);

  // Heat layer: density visualization on top of the markers. Normalized to
  // [0, 1] per render so the gradient scales with relative traffic.
  const points = raw.map((p) => [p.lat, p.lon, p.count / maxCount]);
  heatLayer = L.heatLayer(points, {
    radius: 32,
    blur: 22,
    maxZoom: 10,
    max: 1.0,
    minOpacity: 0.45,
    gradient: {
      0.0: "#38bdf8",
      0.3: "#c084fc",
      0.6: "#fb923c",
      0.85: "#f87171",
      1.0: "#facc15",
    },
  }).addTo(leafletMap);

  // Guard against the map being created at 0x0 before CSS settles.
  setTimeout(() => leafletMap.invalidateSize(), 0);
}

function renderCountries(data) {
  const container = document.getElementById("countries");
  if (!data.geoEnabled) {
    container.innerHTML = `
      <p class="muted">
        Geo lookup is disabled on this server — the MaxMind GeoLite2 DB is not
        loaded. See <code>analytics.go</code> and the deployment notes for how
        to enable it.
      </p>`;
    return;
  }
  const countries = data.countries || [];
  if (countries.length === 0) {
    container.innerHTML = `<p class="muted">No page views recorded yet with a resolvable country.</p>`;
    return;
  }

  const total = countries.reduce((sum, c) => sum + c.count, 0);
  const top = countries.slice(0, 20);
  const max = top[0].count;

  container.innerHTML = `
    <ul class="countries">
      ${top.map((c) => {
        const pct = total === 0 ? 0 : (100 * c.count) / total;
        const widthPct = max === 0 ? 0 : (100 * c.count) / max;
        return `
          <li class="countries__row">
            <span class="countries__flag">${flagEmoji(c.code)}</span>
            <span class="countries__name">${countryName(c.code)}</span>
            <span class="countries__bar">
              <span class="countries__bar-fill" style="width: ${widthPct.toFixed(1)}%"></span>
            </span>
            <span class="countries__count">${c.count.toLocaleString()}</span>
            <span class="countries__pct muted">${pct.toFixed(1)}%</span>
          </li>
        `;
      }).join("")}
    </ul>
    ${countries.length > 20 ? `<p class="muted">Showing top 20 of ${countries.length} countries.</p>` : ""}
  `;
}

function classifyScreen(size) {
  const [w] = size.split("x").map(Number);
  if (!Number.isFinite(w)) return "";
  if (w < 600) return "📱";    // phone-ish
  if (w < 1024) return "📱"; // small tablet / phablet
  if (w < 1600) return "💻";  // laptop
  return "🖥️";                  // desktop / large monitor
}

function renderScreens(data) {
  const container = document.getElementById("screens");
  const screens = data.screens || [];
  if (screens.length === 0) {
    container.innerHTML = `<p class="muted">No screen sizes reported yet.</p>`;
    return;
  }

  const total = screens.reduce((sum, s) => sum + s.count, 0);
  const top = screens.slice(0, 20);
  const max = top[0].count;

  container.innerHTML = `
    <ul class="screens">
      ${top.map((s) => {
        const pct = total === 0 ? 0 : (100 * s.count) / total;
        const widthPct = max === 0 ? 0 : (100 * s.count) / max;
        return `
          <li class="screens__row">
            <span class="screens__icon">${classifyScreen(s.size)}</span>
            <span class="screens__size">${s.size}</span>
            <span class="screens__bar">
              <span class="screens__bar-fill" style="width: ${widthPct.toFixed(1)}%"></span>
            </span>
            <span class="screens__count">${s.count.toLocaleString()}</span>
            <span class="screens__pct muted">${pct.toFixed(1)}%</span>
          </li>
        `;
      }).join("")}
    </ul>
    ${screens.length > 20 ? `<p class="muted">Showing top 20 of ${screens.length} resolutions.</p>` : ""}
  `;
}

async function refresh() {
  try {
    const resp = await fetch("api/overview");
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    const data = await resp.json();
    renderCards(data);
    renderPageViewsChart(data);
    renderAPIHealth(data);
    renderHeatmap(data);
    renderMap(data);
    renderCountries(data);
    renderScreens(data);
    renderRuntime(data);
  } catch (err) {
    console.error("Failed to refresh analytics:", err);
  }
}

refresh();
setInterval(refresh, REFRESH_MS);
