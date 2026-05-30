// ===== Configuration =====
const REFRESH_INTERVAL = 60_000;
const BASE = document.querySelector("base")?.getAttribute("href")?.replace(/\/$/, "") || "";
const FULL_POOL = 216.0;

// ===== Shared chart defaults =====
const tooltipStyle = {
  backgroundColor: "#1e293b",
  borderColor: "#334155",
  borderWidth: 1,
  titleColor: "#e2e8f0",
  bodyColor: "#e2e8f0",
};

// ===== "Now" line plugin =====
const nowLinePlugin = {
  id: "nowLine",
  afterDraw(chart) {
    const xScale = chart.scales.x;
    const yScale = chart.scales.y;
    if (!xScale || !yScale) return;

    const now = Date.now();
    const pixel = xScale.getPixelForValue(now);
    if (pixel < xScale.left || pixel > xScale.right) return;

    const ctx = chart.ctx;
    ctx.save();
    ctx.beginPath();
    ctx.setLineDash([4, 4]);
    ctx.strokeStyle = "#f472b6";
    ctx.lineWidth = 1.5;
    ctx.moveTo(pixel, yScale.top);
    ctx.lineTo(pixel, yScale.bottom);
    ctx.stroke();
    ctx.restore();

    ctx.save();
    ctx.font = "bold 11px sans-serif";
    ctx.fillStyle = "#f472b6";
    ctx.textAlign = "center";
    ctx.fillText("Now", pixel, yScale.top - 6);
    ctx.restore();
  },
};

// ===== Temperature Chart (water temp reference + air forecast overlay) =====
const tempChart = new Chart(document.getElementById("tempChart"), {
  type: "line",
  plugins: [nowLinePlugin],
  data: {
    datasets: [
      {
        label: "Water Temp (°F)",
        borderColor: "#fb923c",
        backgroundColor: "rgba(251,146,60,0.1)",
        fill: true,
        tension: 0,
        pointRadius: 0,
        borderWidth: 2,
        data: [],
      },
      {
        label: "Air Temp Forecast (°F)",
        borderColor: "#4ade80",
        backgroundColor: "rgba(74,222,128,0.08)",
        borderDash: [5, 3],
        fill: false,
        tension: 0.3,
        pointRadius: 2,
        data: [],
      },
    ],
  },
  options: {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { mode: "index", intersect: false },
    plugins: {
      legend: {
        display: true,
        labels: { color: "#94a3b8", boxWidth: 12, padding: 16 },
      },
      tooltip: {
        ...tooltipStyle,
        callbacks: { label: (ctx) => `${ctx.dataset.label}: ${ctx.parsed.y}°F` },
      },
    },
    scales: {
      x: {
        type: "time",
        time: { tooltipFormat: "MMM d, h:mm a", displayFormats: { hour: "ha", day: "MMM d" } },
        grid: { color: "#1e293b" },
        ticks: { color: "#94a3b8", maxTicksLimit: 10 },
      },
      y: {
        grid: { color: "#1e293b" },
        ticks: { color: "#94a3b8" },
      },
    },
  },
});

// ===== Water Temperature History Chart =====
const waterTempChart = new Chart(document.getElementById("waterTempChart"), {
  type: "line",
  data: {
    datasets: [
      {
        label: "Water Temp (°F)",
        borderColor: "#fb923c",
        backgroundColor: "rgba(251,146,60,0.1)",
        fill: true,
        tension: 0.3,
        pointRadius: 3,
        data: [],
      },
    ],
  },
  options: {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { mode: "index", intersect: false },
    plugins: {
      legend: {
        display: true,
        labels: { color: "#94a3b8", boxWidth: 12, padding: 16 },
      },
      tooltip: {
        ...tooltipStyle,
        callbacks: {
          label: (ctx) => `${ctx.dataset.label}: ${ctx.parsed.y}°F`,
        },
      },
    },
    scales: {
      x: {
        type: "time",
        time: {
          parser: "yyyy-MM-dd",
          tooltipFormat: "MMM d, yyyy",
          unit: "day",
          displayFormats: { day: "MMM d", month: "MMM yyyy" },
        },
        grid: { color: "#1e293b" },
        ticks: { color: "#94a3b8", maxTicksLimit: 8 },
      },
      y: {
        grid: { color: "#1e293b" },
        ticks: { color: "#94a3b8" },
      },
    },
  },
});

// ===== Water Temp Range & Overlay State =====
const TEMP_YEAR_COLORS = {
  _current: { border: "#fb923c", bg: "rgba(251,146,60,0.1)" },
  _palette: [
    { border: "#38bdf8", bg: "rgba(56,189,248,0.08)" },
    { border: "#a78bfa", bg: "rgba(167,139,250,0.08)" },
    { border: "#4ade80", bg: "rgba(74,222,128,0.08)" },
    { border: "#f472b6", bg: "rgba(244,114,182,0.08)" },
    { border: "#facc15", bg: "rgba(250,204,21,0.08)" },
  ],
};

let _allTempData = [];
let _tempRange = 30;
let _selectedTempYears = new Set();

function updateTempHistoryChart() {
  const now = new Date();
  const currentYear = now.getFullYear();

  if (_tempRange === "all") {
    waterTempChart.data.datasets = [
      {
        label: "All Data",
        borderColor: "#fb923c",
        backgroundColor: "rgba(251,146,60,0.1)",
        fill: true,
        tension: 0.3,
        pointRadius: 0,
        data: _allTempData.map((p) => ({ x: p.date, y: p.value })),
      },
    ];
    waterTempChart.update("none");
    return;
  }

  const endMD = now.toISOString().slice(5, 10);
  const cutoff = new Date(now);
  cutoff.setDate(cutoff.getDate() - _tempRange);
  const startMD = cutoff.toISOString().slice(5, 10);
  const rangeWraps = startMD > endMD;
  // For a 1Y+ range the start and end month-day land on the same day, which
  // would collapse the window to that single day. Skip the MD filter and let
  // the per-year filter handle scope.
  const fullYear = _tempRange >= 365;

  function inRange(md) {
    if (fullYear) return true;
    if (rangeWraps) return md >= startMD || md <= endMD;
    return md >= startMD && md <= endMD;
  }

  const datasets = [];
  let paletteIdx = 0;

  for (const year of [..._selectedTempYears].sort()) {
    const yearData = _allTempData.filter((p) => {
      if (!p.date.startsWith(String(year))) return false;
      return inRange(p.date.slice(5, 10));
    });
    if (!yearData.length) continue;

    const shifted = yearData.map((p) => ({
      x: String(currentYear) + p.date.slice(4),
      y: p.value,
    }));

    const isCurrent = year === currentYear;
    const colors = isCurrent
      ? TEMP_YEAR_COLORS._current
      : TEMP_YEAR_COLORS._palette[paletteIdx++ % TEMP_YEAR_COLORS._palette.length];

    datasets.push({
      label: String(year),
      borderColor: colors.border,
      backgroundColor: isCurrent ? colors.bg : "transparent",
      fill: isCurrent,
      tension: 0.3,
      pointRadius: shifted.length > 100 ? 0 : (isCurrent ? 3 : 0),
      borderDash: isCurrent ? [] : [5, 3],
      borderWidth: isCurrent ? 2 : 1.5,
      data: shifted,
    });
  }

  waterTempChart.data.datasets = datasets;
  waterTempChart.update("none");
}

function buildTempOverlayButtons() {
  const years = [...new Set(_allTempData.map((p) => parseInt(p.date.slice(0, 4))))].sort();
  const currentYear = new Date().getFullYear();
  const container = document.getElementById("tempOverlayButtons");
  container.innerHTML = "";

  _selectedTempYears.clear();
  _selectedTempYears.add(currentYear);

  for (const year of years) {
    const btn = document.createElement("button");
    btn.className = "overlay-btn" + (year === currentYear ? " active" : "");
    btn.textContent = year;
    btn.dataset.year = year;
    btn.addEventListener("click", () => {
      if (_selectedTempYears.has(year)) {
        if (_selectedTempYears.size <= 1) return;
        _selectedTempYears.delete(year);
        btn.classList.remove("active");
      } else {
        _selectedTempYears.add(year);
        btn.classList.add("active");
      }
      updateTempHistoryChart();
    });
    container.appendChild(btn);
  }
}

document.querySelectorAll("#tempRangeButtons .range-btn").forEach((btn) => {
  btn.addEventListener("click", () => {
    document.querySelectorAll("#tempRangeButtons .range-btn").forEach((b) => b.classList.remove("active"));
    btn.classList.add("active");
    const val = btn.dataset.range;
    _tempRange = val === "all" ? "all" : parseInt(val);
    updateTempHistoryChart();
  });
});

// ===== Wind Speed Chart =====
const windChart = new Chart(document.getElementById("windChart"), {
  type: "bar",
  plugins: [nowLinePlugin],
  data: {
    datasets: [
      {
        label: "Wind Speed (mph)",
        backgroundColor: "rgba(148,163,184,0.4)",
        borderColor: "#94a3b8",
        borderWidth: 1,
        borderRadius: 3,
        data: [],
      },
    ],
  },
  options: {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { mode: "index", intersect: false },
    plugins: {
      legend: { display: false },
      tooltip: {
        ...tooltipStyle,
        callbacks: {
          label: (ctx) => {
            const raw = ctx.raw;
            return `${raw.y} mph ${raw.dir || ""}`;
          },
        },
      },
    },
    scales: {
      x: {
        type: "time",
        time: { tooltipFormat: "h:mm a", displayFormats: { hour: "ha" } },
        grid: { color: "#1e293b" },
        ticks: { color: "#94a3b8", maxTicksLimit: 12 },
      },
      y: {
        beginAtZero: true,
        grid: { color: "#1e293b" },
        ticks: { color: "#94a3b8" },
      },
    },
  },
});

// ===== Water Level Chart =====
const levelChart = new Chart(document.getElementById("levelChart"), {
  type: "line",
  data: {
    datasets: [
      {
        label: "Water Level vs Full Pool (ft)",
        borderColor: "#38bdf8",
        backgroundColor: "rgba(56,189,248,0.1)",
        fill: true,
        tension: 0.3,
        pointRadius: 2,
        data: [],
      },
    ],
  },
  options: {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { mode: "index", intersect: false },
    plugins: {
      legend: {
        display: true,
        labels: { color: "#94a3b8", boxWidth: 12, padding: 16 },
      },
      tooltip: {
        ...tooltipStyle,
        callbacks: {
          label: (ctx) => {
            const v = ctx.parsed.y;
            return `${ctx.dataset.label}: ${v >= 0 ? "+" : ""}${v} ft`;
          },
        },
      },
    },
    scales: {
      x: {
        type: "time",
        time: {
          parser: "yyyy-MM-dd",
          tooltipFormat: "MMM d, yyyy",
          unit: "day",
          displayFormats: { day: "MMM d", month: "MMM yyyy" },
        },
        grid: { color: "#1e293b" },
        ticks: { color: "#94a3b8", maxTicksLimit: 8 },
      },
      y: {
        grid: { color: "#1e293b" },
        ticks: { color: "#94a3b8" },
      },
    },
  },
});

// ===== Water Level Range & Overlay State =====
const LEVEL_YEAR_COLORS = {
  // Current year gets the main blue; others get assigned from this palette
  _current: { border: "#38bdf8", bg: "rgba(56,189,248,0.1)" },
  _palette: [
    { border: "#fb923c", bg: "rgba(251,146,60,0.08)" },
    { border: "#a78bfa", bg: "rgba(167,139,250,0.08)" },
    { border: "#4ade80", bg: "rgba(74,222,128,0.08)" },
    { border: "#f472b6", bg: "rgba(244,114,182,0.08)" },
    { border: "#facc15", bg: "rgba(250,204,21,0.08)" },
  ],
};

let _allLevelData = [];
let _levelRange = 30;        // days, or "all"
let _selectedYears = new Set(); // years to display; current year added by default

function updateLevelChart() {
  const now = new Date();
  const currentYear = now.getFullYear();

  // "All" mode: single chronological line, ignore year selection
  if (_levelRange === "all") {
    levelChart.data.datasets = [
      {
        label: "All Data",
        borderColor: "#38bdf8",
        backgroundColor: "rgba(56,189,248,0.1)",
        fill: true,
        tension: 0.3,
        pointRadius: 0,
        data: _allLevelData.map((p) => ({
          x: p.date,
          y: +(p.value - FULL_POOL).toFixed(2),
        })),
      },
    ];
    levelChart.update("none");
    return;
  }

  // Determine the month-day window: today back N days
  const endMD = now.toISOString().slice(5, 10);   // "04-19"
  const cutoff = new Date(now);
  cutoff.setDate(cutoff.getDate() - _levelRange);
  const startMD = cutoff.toISOString().slice(5, 10); // e.g. "03-20"
  const rangeWraps = startMD > endMD; // crosses year boundary (e.g. Dec→Jan)
  // For a 1Y+ range the start and end month-day land on the same day, which
  // would collapse the window to that single day. Skip the MD filter and let
  // the per-year filter handle scope.
  const fullYear = _levelRange >= 365;

  function inRange(md) {
    if (fullYear) return true;
    if (rangeWraps) return md >= startMD || md <= endMD;
    return md >= startMD && md <= endMD;
  }

  // Build one dataset per selected year, all shifted to current year
  const datasets = [];
  const sortedYears = [..._selectedYears].sort();
  let paletteIdx = 0;

  for (const year of sortedYears) {
    const yearData = _allLevelData.filter((p) => {
      if (!p.date.startsWith(String(year))) return false;
      return inRange(p.date.slice(5, 10));
    });
    if (!yearData.length) continue;

    // Shift dates to current year for alignment
    const shifted = yearData.map((p) => ({
      x: String(currentYear) + p.date.slice(4),
      y: +(p.value - FULL_POOL).toFixed(2),
    }));

    const isCurrent = year === currentYear;
    const colors = isCurrent
      ? LEVEL_YEAR_COLORS._current
      : LEVEL_YEAR_COLORS._palette[paletteIdx++ % LEVEL_YEAR_COLORS._palette.length];

    datasets.push({
      label: String(year),
      borderColor: colors.border,
      backgroundColor: isCurrent ? colors.bg : "transparent",
      fill: isCurrent,
      tension: 0.3,
      pointRadius: shifted.length > 100 ? 0 : (isCurrent ? 2 : 0),
      borderDash: isCurrent ? [] : [5, 3],
      borderWidth: isCurrent ? 2 : 1.5,
      data: shifted,
    });
  }

  levelChart.data.datasets = datasets;
  levelChart.update("none");
}

// Build year selector buttons (all years including current)
function buildOverlayButtons() {
  const years = [...new Set(_allLevelData.map((p) => parseInt(p.date.slice(0, 4))))].sort();
  const currentYear = new Date().getFullYear();
  const container = document.getElementById("overlayButtons");
  container.innerHTML = "";

  // Default: only current year selected
  _selectedYears.clear();
  _selectedYears.add(currentYear);

  for (const year of years) {
    const btn = document.createElement("button");
    btn.className = "overlay-btn" + (year === currentYear ? " active" : "");
    btn.textContent = year;
    btn.dataset.year = year;
    btn.addEventListener("click", () => {
      if (_selectedYears.has(year)) {
        // Don't allow deselecting the last year
        if (_selectedYears.size <= 1) return;
        _selectedYears.delete(year);
        btn.classList.remove("active");
      } else {
        _selectedYears.add(year);
        btn.classList.add("active");
      }
      updateLevelChart();
    });
    container.appendChild(btn);
  }
}

// Range button handlers
document.querySelectorAll("#rangeButtons .range-btn").forEach((btn) => {
  btn.addEventListener("click", () => {
    document.querySelectorAll("#rangeButtons .range-btn").forEach((b) => b.classList.remove("active"));
    btn.classList.add("active");
    const val = btn.dataset.range;
    _levelRange = val === "all" ? "all" : parseInt(val);
    updateLevelChart();
  });
});

// ===== Data Fetching =====
let _overlayButtonsBuilt = false;
let _tempOverlayButtonsBuilt = false;

async function fetchCurrent() {
  try {
    const res = await fetch(`${BASE}/api/current`);
    const data = await res.json();

    if (data.temperature) {
      document.getElementById("currentTemp").textContent =
        data.temperature.value.toFixed(1);
      document.getElementById("tempUpdated").textContent = data.temperature.date;
    }

    if (data.waterLevel) {
      const abs = data.waterLevel.value;
      const delta = abs - FULL_POOL;
      const sign = delta >= 0 ? "+" : "";
      document.getElementById("currentLevel").textContent =
        `${sign}${delta.toFixed(2)}`;
      document.getElementById("absoluteLevel").textContent =
        `Elevation: ${abs.toFixed(2)} ft`;
      document.getElementById("levelUpdated").textContent = data.waterLevel.date;
    }
  } catch (err) {
    console.error("Failed to fetch current data:", err);
  }
}

async function fetchHistory() {
  try {
    const res = await fetch(`${BASE}/api/history`);
    const data = await res.json();

    if (data.temperature) {
      _allTempData = data.temperature;
      if (!_tempOverlayButtonsBuilt) {
        buildTempOverlayButtons();
        _tempOverlayButtonsBuilt = true;
      }
      updateTempHistoryChart();

      // Store latest water temp for use in the forecast temp chart.
      if (data.temperature.length > 0) {
        window._latestWaterTemp = data.temperature[data.temperature.length - 1].value;
      }
    }

    if (data.waterLevel) {
      _allLevelData = data.waterLevel;
      if (!_overlayButtonsBuilt) {
        buildOverlayButtons();
        _overlayButtonsBuilt = true;
      }
      updateLevelChart();
    }
  } catch (err) {
    console.error("Failed to fetch history:", err);
  }
}

// ===== Weather =====
const WEATHER_ICONS = {
  Sunny: "☀️", "Mostly Sunny": "🌤️", "Partly Sunny": "⛅",
  "Mostly Cloudy": "🌥️", Cloudy: "☁️", "Partly Cloudy": "⛅",
  "Mostly Clear": "🌙", Clear: "🌙",
  Rain: "🌧️", "Light Rain": "🌦️", "Chance Rain Showers": "🌦️",
  "Rain Showers": "🌧️", "Showers And Thunderstorms": "⛈️",
  Thunderstorms: "⛈️", Snow: "🌨️", Fog: "🌫️", Windy: "💨",
};

function weatherIcon(desc) {
  for (const [key, icon] of Object.entries(WEATHER_ICONS)) {
    if (desc.toLowerCase().includes(key.toLowerCase())) return icon;
  }
  return "🌤️";
}

function parseWindSpeed(str) {
  // "5 mph" or "5 to 10 mph" → take the higher number
  const nums = str.match(/\d+/g);
  if (!nums) return 0;
  return Math.max(...nums.map(Number));
}

const WIND_DEGREES = {
  N: 0, NNE: 22.5, NE: 45, ENE: 67.5, E: 90, ESE: 112.5, SE: 135, SSE: 157.5,
  S: 180, SSW: 202.5, SW: 225, WSW: 247.5, W: 270, WNW: 292.5, NW: 315, NNW: 337.5,
};

async function fetchWeather() {
  try {
    const res = await fetch(`${BASE}/api/weather`);
    const data = await res.json();

    // --- Current conditions card (first hourly period) ---
    if (data.hourly?.length) {
      const now = data.hourly[0];
      document.getElementById("airTemp").textContent = now.temperature;
      document.getElementById("weatherDesc").textContent = `· ${now.shortForecast}`;
      document.getElementById("weatherIcon").textContent = weatherIcon(now.shortForecast);
      document.getElementById("weatherPeriod").textContent = now.name || "";

      const speed = parseWindSpeed(now.windSpeed);
      document.getElementById("windSpeed").textContent = now.windSpeed;
      document.getElementById("windDir").textContent = now.windDirection;

      // Rotate compass arrow — arrow points the direction wind is coming FROM
      const deg = WIND_DEGREES[now.windDirection] ?? 0;
      const arrow = document.getElementById("windArrow");
      if (arrow) arrow.style.transform = `translate(-50%, 0) rotate(${deg + 180}deg)`;
    }

    // --- Air temp overlay on temp chart (hourly data) ---
    if (data.hourly?.length) {
      const hourlyPoints = data.hourly.map((p) => ({
        x: new Date(p.startTime),
        y: p.temperature,
      }));
      tempChart.data.datasets[1].data = hourlyPoints;

      // Draw water temp as a flat reference line across the same time range
      const waterTemp = window._latestWaterTemp;
      if (waterTemp != null && hourlyPoints.length >= 2) {
        const firstTime = hourlyPoints[0].x;
        const lastTime = hourlyPoints[hourlyPoints.length - 1].x;
        tempChart.data.datasets[0].data = [
          { x: firstTime, y: waterTemp },
          { x: lastTime, y: waterTemp },
        ];
      }

      tempChart.update("none");
    }

    // --- Wind speed chart ---
    if (data.hourly?.length) {
      windChart.data.datasets[0].data = data.hourly.map((p) => ({
        x: new Date(p.startTime),
        y: parseWindSpeed(p.windSpeed),
        dir: p.windDirection,
      }));
      windChart.update("none");
    }

    // --- 7-day forecast grid ---
    if (data.forecast?.length) {
      renderForecast(data.forecast);
    }

    // --- UV index card ---
    renderUVCard(data.uv);
  } catch (err) {
    console.error("Failed to fetch weather:", err);
  }
}

// WHO UV index tiers — color, label, sunscreen guidance abbreviated.
function uvTier(value) {
  if (value < 3)  return { label: "Low",       color: "#4ade80" };
  if (value < 6)  return { label: "Moderate",  color: "#facc15" };
  if (value < 8)  return { label: "High",      color: "#fb923c" };
  if (value < 11) return { label: "Very High", color: "#f87171" };
  return            { label: "Extreme",   color: "#c084fc" };
}

function renderUVCard(uv) {
  const card = document.getElementById("uvCard");
  if (!card) return;
  if (!uv || typeof uv.current !== "number") {
    document.getElementById("uvValue").textContent = "—";
    document.getElementById("uvTier").textContent = "";
    document.getElementById("uvPeak").textContent = "";
    return;
  }
  const current = uv.current;
  const tier = uvTier(current);
  const valueEl = document.getElementById("uvValue");
  valueEl.textContent = current.toFixed(1);
  valueEl.style.color = tier.color;
  document.getElementById("uvTier").textContent = tier.label;
  // Peak (max) for the rest of today out of the hourly forecast.
  if (Array.isArray(uv.hourly) && uv.hourly.length) {
    const today = (uv.updatedAt || "").slice(0, 10);
    let peak = -1;
    for (const h of uv.hourly) {
      if (today && !h.time.startsWith(today)) continue;
      if (typeof h.uvIndex === "number" && h.uvIndex > peak) peak = h.uvIndex;
    }
    if (peak >= 0) {
      document.getElementById("uvPeak").textContent =
        `Peak today: ${peak.toFixed(1)}`;
    } else {
      document.getElementById("uvPeak").textContent = "";
    }
  } else {
    document.getElementById("uvPeak").textContent = "";
  }
}

function renderForecast(periods) {
  const grid = document.getElementById("forecastGrid");
  grid.innerHTML = "";

  // NWS periods alternate day/night (or start with "Tonight" at night).
  // Group them into cards showing high/low for each day.
  const cards = [];
  let i = 0;

  while (i < periods.length && cards.length < 7) {
    const p = periods[i];
    const isNight = !p.isDaytime;

    if (isNight) {
      // Night-first (e.g. "Tonight") — show as standalone with only low temp
      cards.push({ name: cleanDayName(p.name), hi: null, lo: p.temperature, forecast: p.shortForecast, wind: p.windSpeed, windDir: p.windDirection });
      i++;
    } else {
      // Daytime — pair with following night if available
      const night = (i + 1 < periods.length && !periods[i + 1].isDaytime) ? periods[i + 1] : null;
      cards.push({ name: cleanDayName(p.name), hi: p.temperature, lo: night ? night.temperature : null, forecast: p.shortForecast, wind: p.windSpeed, windDir: p.windDirection });
      i += night ? 2 : 1;
    }
  }

  for (const card of cards) {
    const el = document.createElement("div");
    el.className = "forecast__day";
    const temps = card.hi != null
      ? `<span class="forecast__day-hi">${card.hi}°</span>${card.lo != null ? `<span class="forecast__day-lo"> / ${card.lo}°</span>` : ""}`
      : `<span class="forecast__day-lo">${card.lo}°</span>`;
    el.innerHTML = `
      <div class="forecast__day-name">${card.name}</div>
      <div class="forecast__day-icon">${weatherIcon(card.forecast)}</div>
      <div class="forecast__day-temps">${temps}</div>
      <div class="forecast__day-wind">💨 ${card.wind} ${card.windDir}</div>
      <div class="forecast__day-desc">${card.forecast}</div>
    `;
    grid.appendChild(el);
  }
}

// Strip "Night" suffix from NWS period names, keep "Tonight" and "Today" as-is
function cleanDayName(name) {
  if (name === "Tonight" || name === "Today" || name === "This Afternoon") return name;
  return name.replace(/\s+Night$/i, "");
}

// ===== Init =====
async function refresh() {
  // Fetch current & history first so _latestWaterTemp is set
  // before fetchWeather draws the reference line on the temp chart.
  await Promise.all([fetchCurrent(), fetchHistory()]);
  await fetchWeather();
}

refresh();
setInterval(refresh, REFRESH_INTERVAL);
