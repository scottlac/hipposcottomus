// ===== Configuration =====
const REFRESH = 120_000; // 2 min
const BASE = document.querySelector("base")?.getAttribute("href")?.replace(/\/$/, "") || "";

// Every location is in Florida's Eastern zone. wall() returns a Date whose
// *local* fields read as Eastern wall-clock time, so the charts, tide table
// and cards all show the boat's time even when the viewer is somewhere else.
const TZ = "America/New_York";
function wall(d) {
  return new Date(new Date(d).toLocaleString("en-US", { timeZone: TZ }));
}
const fmtTime = (d) => d.toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit" });
// "today 4:00 PM" / "Thu 4:00 PM", for wall-clock Dates from wall().
const fmtWhen = (d) => {
  const sameDay = d.toDateString() === wall(new Date()).toDateString();
  return `${sameDay ? "today" : d.toLocaleDateString("en-US", { weekday: "short" })} ${fmtTime(d)}`;
};

// ===== Shared tooltip style =====
const tooltipStyle = {
  backgroundColor: "#1e293b",
  borderColor: "#334155",
  borderWidth: 1,
  titleColor: "#e2e8f0",
  bodyColor: "#e2e8f0",
};

// ===== "Now" line plugin for tide chart =====
const nowLinePlugin = {
  id: "nowLine",
  afterDraw(chart) {
    const xScale = chart.scales.x;
    const yScale = chart.scales.y;
    if (!xScale || !yScale) return;

    const now = wall(new Date()).getTime();
    const pixel = xScale.getPixelForValue(now);
    if (pixel < xScale.left || pixel > xScale.right) return;

    const ctx = chart.ctx;

    // Vertical dashed line
    ctx.save();
    ctx.beginPath();
    ctx.setLineDash([4, 4]);
    ctx.strokeStyle = "#f472b6"; // pink
    ctx.lineWidth = 1.5;
    ctx.moveTo(pixel, yScale.top);
    ctx.lineTo(pixel, yScale.bottom);
    ctx.stroke();
    ctx.restore();

    // Interpolate tide height at current time from curve dataset
    const curveData = chart.data.datasets[0].data;
    if (curveData.length >= 2) {
      let yVal = null;
      for (let i = 0; i < curveData.length - 1; i++) {
        const t0 = curveData[i].x.getTime();
        const t1 = curveData[i + 1].x.getTime();
        if (now >= t0 && now <= t1) {
          const frac = (now - t0) / (t1 - t0);
          yVal = curveData[i].y + frac * (curveData[i + 1].y - curveData[i].y);
          break;
        }
      }
      if (yVal !== null) {
        const yPixel = yScale.getPixelForValue(yVal);
        ctx.save();
        ctx.beginPath();
        ctx.arc(pixel, yPixel, 5, 0, Math.PI * 2);
        ctx.fillStyle = "#f472b6";
        ctx.fill();
        ctx.strokeStyle = "#0f172a";
        ctx.lineWidth = 2;
        ctx.stroke();
        ctx.restore();

        // Label
        ctx.save();
        ctx.font = "bold 11px sans-serif";
        ctx.fillStyle = "#f472b6";
        ctx.textAlign = "center";
        ctx.fillText("Now", pixel, yScale.top - 6);
        ctx.restore();
      }
    }
  },
};

// ===== Tide Chart =====
const tideChart = new Chart(document.getElementById("tideChart"), {
  type: "line",
  plugins: [nowLinePlugin],
  data: {
    datasets: [
      {
        label: "Tide (ft MLLW)",
        borderColor: "#38bdf8",
        backgroundColor: "rgba(56,189,248,0.12)",
        fill: true,
        tension: 0.4,
        pointRadius: 0,
        borderWidth: 2,
        data: [],
      },
      {
        label: "High Tides",
        borderColor: "transparent",
        backgroundColor: "#fb923c",
        pointRadius: 6,
        pointStyle: "triangle",
        showLine: false,
        data: [],
      },
      {
        label: "Low Tides",
        borderColor: "transparent",
        backgroundColor: "#818cf8",
        pointRadius: 6,
        pointStyle: "rectRot",
        showLine: false,
        data: [],
      },
      {
        // Prediction shifted by how far a nearby gauge is reading off its
        // own prediction. Hidden from the legend until there's data.
        label: "Observed (est.)",
        borderColor: "#4ade80",
        backgroundColor: "transparent",
        borderWidth: 2,
        pointRadius: 0,
        tension: 0.3,
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
        labels: {
          color: "#94a3b8",
          boxWidth: 12,
          padding: 16,
          filter: (item, data) => data.datasets[item.datasetIndex].data.length > 0,
        },
      },
      tooltip: {
        ...tooltipStyle,
        callbacks: { label: (ctx) => `${ctx.dataset.label}: ${ctx.parsed.y.toFixed(2)} ft` },
      },
    },
    scales: {
      x: {
        type: "time",
        time: { tooltipFormat: "EEE MMM d, h:mm a", displayFormats: { hour: "ha EEE" } },
        grid: { color: "#1e293b" },
        ticks: { color: "#94a3b8", maxTicksLimit: 10 },
      },
      y: {
        title: { display: true, text: "ft (MLLW)", color: "#94a3b8" },
        grid: { color: "#1e293b" },
        ticks: { color: "#94a3b8" },
      },
    },
  },
});

// ===== Wind Chart =====
const windChart = new Chart(document.getElementById("windChart"), {
  type: "bar",
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
          label: (ctx) => `${ctx.raw.y} mph ${ctx.raw.dir || ""}`,
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

// ===== Weather helpers =====
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
  const nums = str.match(/\d+/g);
  if (!nums) return 0;
  return Math.max(...nums.map(Number));
}

const WIND_DEGREES = {
  N: 0, NNE: 22.5, NE: 45, ENE: 67.5, E: 90, ESE: 112.5, SE: 135, SSE: 157.5,
  S: 180, SSW: 202.5, SW: 225, WSW: 247.5, W: 270, WNW: 292.5, NW: 315, NNW: 337.5,
};

// ===== Fetchers =====

// API times are RFC 3339; chart and table positions use Eastern wall-clock.
function parseNOAATime(t) {
  return wall(t);
}

// Height of the tide right now, interpolated from the 10-minute curve.
function tideHeightNow(curve, now) {
  for (let i = 0; i < curve.length - 1; i++) {
    const t0 = parseNOAATime(curve[i].time).getTime();
    const t1 = parseNOAATime(curve[i + 1].time).getTime();
    if (now >= t0 && now <= t1 && t1 > t0) {
      return curve[i].height + ((now - t0) / (t1 - t0)) * (curve[i + 1].height - curve[i].height);
    }
  }
  return null;
}

async function fetchTides() {
  try {
    const res = await fetch(`${BASE}/api/tides`);
    const data = await res.json();

    // Tide curve
    if (data.curve?.length) {
      tideChart.data.datasets[0].data = data.curve.map((p) => ({
        x: parseNOAATime(p.time),
        y: p.height,
      }));
    }

    // Hi/Lo markers
    if (data.hiLo?.length) {
      const highs = data.hiLo.filter((e) => e.type === "High");
      const lows = data.hiLo.filter((e) => e.type === "Low");

      tideChart.data.datasets[1].data = highs.map((e) => ({
        x: parseNOAATime(e.time),
        y: e.height,
      }));
      tideChart.data.datasets[2].data = lows.map((e) => ({
        x: parseNOAATime(e.time),
        y: e.height,
      }));

      // Find next upcoming tide
      const now = wall(new Date());
      const next = data.hiLo.find((e) => parseNOAATime(e.time) > now);
      if (next) {
        const t = parseNOAATime(next.time);
        document.getElementById("nextTideType").textContent = next.type;
        document.getElementById("nextTideDetail").textContent =
          `${next.height.toFixed(2)} ft at ${fmtTime(t)}`;
        document.getElementById("nextTideTime").textContent = "";
        const h = tideHeightNow(data.curve || [], now.getTime());
        document.getElementById("tideNow").textContent =
          h == null ? "" : `${next.type === "High" ? "Rising ↑" : "Falling ↓"} · predicted ${h.toFixed(1)} ft now`;
      }

      // Tide schedule
      renderTideSchedule(data.hiLo);
    }

    renderWaterLevel(data.level, data.curve || []);

    // Show a fixed window: the last 6 hours through the next 48.
    const nowMs = wall(new Date()).getTime();
    tideChart.options.scales.x.min = nowMs - 6 * 3600_000;
    tideChart.options.scales.x.max = nowMs + 48 * 3600_000;

    tideChart.update("none");
  } catch (err) {
    console.error("Failed to fetch tides:", err);
  }
}

// Observed water level: a nearby gauge's observed-minus-predicted offset,
// applied to this location's predicted curve.
function renderWaterLevel(level, curve) {
  const note = document.getElementById("tideOffset");
  const line = tideChart.data.datasets[3];
  const legend = document.getElementById("tideChartNote");
  if (!level || !Array.isArray(level.series) || !level.series.length) {
    note.textContent = "";
    line.data = [];
    legend.hidden = true;
    renderFloodStatus(null);
    return;
  }
  const off = level.offset;
  if (Math.abs(off) < 0.1) {
    note.textContent = `Water level matching predictions (${level.source} gauge)`;
    note.classList.remove("card__absolute--alert");
  } else {
    note.textContent =
      `Running ${Math.abs(off).toFixed(1)} ft ${off > 0 ? "above" : "below"} predictions (${level.source} gauge)`;
    note.classList.toggle("card__absolute--alert", Math.abs(off) >= 0.5);
  }
  line.data = [];
  for (const p of level.series) {
    const t = parseNOAATime(p.time);
    const h = tideHeightNow(curve, t.getTime());
    if (h != null) line.data.push({ x: t, y: h + p.offset });
  }
  legend.textContent =
    `Green line: the prediction shifted by the difference between observed and predicted water level at the ${level.source} gauge.`;
  legend.hidden = line.data.length === 0;

  renderFloodStatus(level);
}

// The gauge's own reading against its NWS flood levels.
function renderFloodStatus(level) {
  const note = document.getElementById("tideFlood");
  const f = level && level.flood;
  if (!f || f.minor == null) {
    note.textContent = "";
    return;
  }
  const obs = level.observed;
  const at = `${level.source} gauge ${obs.toFixed(1)} ft`;
  let text;
  if (f.major != null && obs >= f.major) text = `${at}: major flood level (${f.major.toFixed(1)} ft)`;
  else if (f.moderate != null && obs >= f.moderate) text = `${at}: moderate flood level (${f.moderate.toFixed(1)} ft)`;
  else if (obs >= f.minor) text = `${at}: above minor flood level (${f.minor.toFixed(1)} ft)`;
  else text = `${at}, ${(f.minor - obs).toFixed(1)} ft below minor flood level`;
  note.textContent = text;
  note.classList.toggle("card__absolute--alert", obs >= f.minor - 0.5);
}

function renderTideSchedule(events) {
  const el = document.getElementById("tideSchedule");
  el.innerHTML = "";

  // Show next 8 events
  const now = wall(new Date());
  const upcoming = events.filter((e) => parseNOAATime(e.time) > new Date(now - 3600_000));
  const show = upcoming.slice(0, 8);

  for (const e of show) {
    const t = parseNOAATime(e.time);
    const isPast = t < now;
    const div = document.createElement("div");
    div.className = `tide-event ${isPast ? "tide-event--past" : ""} tide-event--${e.type.toLowerCase()}`;
    div.innerHTML = `
      <span class="tide-event__icon">${e.type === "High" ? "▲" : "▼"}</span>
      <span class="tide-event__type">${e.type}</span>
      <span class="tide-event__time">${t.toLocaleDateString("en-US", {weekday: "short"})} ${fmtTime(t)}</span>
      <span class="tide-event__height">${e.height.toFixed(2)} ft</span>
    `;
    el.appendChild(div);
  }
}

async function fetchWeather() {
  try {
    const res = await fetch(`${BASE}/api/weather`);
    const data = await res.json();

    // Water temp
    const tempSrc = document.getElementById("waterTempSource");
    if (data.waterTemp != null) {
      document.getElementById("waterTemp").textContent = data.waterTemp.toFixed(1);
      const at = data.waterTempTime ? ` · ${fmtTime(wall(data.waterTempTime))}` : "";
      tempSrc.textContent = data.waterTempSource ? `${data.waterTempSource}${at}` : "";
    } else {
      document.getElementById("waterTemp").textContent = "—";
      tempSrc.textContent = "Sensor offline";
    }

    // Current conditions
    if (data.hourly?.length) {
      const now = data.hourly[0];
      document.getElementById("airTemp").textContent = now.temperature;
      document.getElementById("weatherDesc").textContent = `· ${now.shortForecast}`;
      document.getElementById("weatherIcon").textContent = weatherIcon(now.shortForecast);
      // Prefer the live anemometer; fall back to the hourly forecast.
      const obs = data.wind;
      let deg;
      if (obs) {
        document.getElementById("windSpeed").textContent = `${Math.round(obs.knots)} kt`;
        document.getElementById("windDir").textContent =
          `${obs.direction} · gusts ${Math.round(obs.gustKnots)} kt`;
        document.getElementById("windSource").textContent =
          `Live at ${obs.source} · ${fmtTime(wall(obs.time))}`;
        deg = obs.degrees;
      } else {
        document.getElementById("windSpeed").textContent = now.windSpeed;
        document.getElementById("windDir").textContent = now.windDirection;
        document.getElementById("windSource").textContent = "NWS forecast for this hour";
        deg = WIND_DEGREES[now.windDirection] ?? 0;
      }
      const arrow = document.getElementById("windArrow");
      if (arrow) arrow.style.transform = `translate(-50%, 0) rotate(${deg + 180}deg)`;

      // Wind chart
      windChart.data.datasets[0].data = data.hourly.map((p) => ({
        x: wall(p.startTime),
        y: parseWindSpeed(p.windSpeed),
        dir: p.windDirection,
      }));
      windChart.update("none");
    }

    // 7-day forecast
    if (data.forecast?.length) {
      renderForecast(data.forecast);
    }

    // UV index card
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

function cleanDayName(name) {
  if (name === "Tonight" || name === "Today" || name === "This Afternoon") return name;
  return name.replace(/\s+Night$/i, "");
}

function renderForecast(periods) {
  const grid = document.getElementById("forecastGrid");
  grid.innerHTML = "";
  const cards = [];
  let i = 0;
  while (i < periods.length && cards.length < 7) {
    const p = periods[i];
    if (!p.isDaytime) {
      cards.push({ name: cleanDayName(p.name), hi: null, lo: p.temperature, forecast: p.shortForecast, wind: p.windSpeed, windDir: p.windDirection });
      i++;
    } else {
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

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text != null) node.textContent = text;
  return node;
}

async function fetchMarine() {
  try {
    const res = await fetch(`${BASE}/api/marine`);
    const data = await res.json();
    const zones = data.zones || [];

    // Advisory banner: every distinct advisory across this location's zones.
    const advisories = new Map();
    for (const z of zones) {
      for (const a of z.advisories || []) {
        if (!advisories.has(a)) advisories.set(a, []);
        advisories.get(a).push(z.name);
      }
    }
    const banner = document.getElementById("advisorySection");
    const body = document.getElementById("advisoryBody");
    // Keep whichever alerts the user has opened across refreshes.
    const openAlerts = new Set(
      [...body.querySelectorAll("details[open]")].map((d) => d.dataset.event)
    );
    body.replaceChildren();

    // NWS alerts for the location itself (coastal flood, high surf, rip
    // currents…) first: they're about where the reader is standing.
    const alerts = data.alerts || [];
    for (const a of alerts) {
      const details = el("details", "advisory__alert");
      details.dataset.event = a.event;
      details.open = openAlerts.has(a.event);
      const summary = el("summary", "advisory__row");
      const name = el("strong", /Warning|Emergency/.test(a.event) ? "advisory__warning" : null, a.event);
      const until = a.ends ? ` — until ${fmtWhen(wall(a.ends))}` : " — until further notice";
      summary.append(name, el("span", "advisory__zone", until));
      details.append(summary);
      if (a.description) details.append(el("p", "advisory__text", a.description));
      if (a.instruction) details.append(el("p", "advisory__text", a.instruction));
      body.append(details);
    }

    for (const [text, names] of advisories) {
      const row = el("p", "advisory__row");
      const cls = /WARNING/.test(text) ? "advisory__warning" : null;
      row.append(el("strong", cls, text), el("span", "advisory__zone", ` — ${names.join("; ")}`));
      body.append(row);
    }
    banner.hidden = advisories.size === 0 && alerts.length === 0;

    const synopsis = document.getElementById("marineSynopsis");
    synopsis.textContent = data.synopsis || "";
    synopsis.hidden = !data.synopsis;

    const container = document.getElementById("marineZones");
    if (!zones.length) {
      container.replaceChildren(el("p", "marine-forecast__empty", "Marine forecast unavailable."));
      return;
    }
    // Keep whichever zones the user has opened across refreshes.
    const wasOpen = new Set(
      [...container.querySelectorAll("details[open]")].map((d) => d.dataset.zone)
    );
    const first = container.querySelector("details") === null;
    container.replaceChildren();
    zones.forEach((z, i) => {
      const details = el("details", "marine-zone");
      details.dataset.zone = z.zone;
      details.open = first ? i === 0 : wasOpen.has(z.zone);
      const summary = el("summary", "marine-zone__name", z.name);
      details.append(summary);
      if (z.issued) details.append(el("p", "marine-zone__issued", `Issued ${z.issued}`));
      for (const a of z.advisories || []) details.append(el("p", "marine-zone__advisory", a));
      const list = el("dl", "marine-zone__periods");
      for (const p of z.periods || []) {
        list.append(el("dt", null, p.name), el("dd", null, p.text));
      }
      details.append(list);
      for (const n of z.notes || []) details.append(el("p", "marine-zone__note", n));
      container.append(details);
    });
  } catch (err) {
    console.error("Failed to fetch marine forecast:", err);
  }
}

// ===== AI-generated boating summary =====
// A 204 means the server hasn't generated one yet (fresh pod): show the
// placeholder and poll every 5s for up to a minute, then give up and hide.
const BLURB_RETRY_MS = 5_000;
const BLURB_MAX_RETRIES = 12;
let blurbRetryTimer = null;
let blurbRetryCount = 0;

async function fetchBlurb() {
  const section = document.getElementById("blurbSection");
  const textEl = document.getElementById("blurbText");
  const metaEl = document.getElementById("blurbMeta");
  try {
    const res = await fetch(`${BASE}/api/blurb`);
    if (res.status === 204) {
      if (blurbRetryCount >= BLURB_MAX_RETRIES) {
        section.hidden = true;
        return;
      }
      textEl.textContent = "Generating summary…";
      metaEl.textContent = "";
      section.hidden = false;
      if (!blurbRetryTimer) {
        blurbRetryCount++;
        blurbRetryTimer = setTimeout(() => {
          blurbRetryTimer = null;
          fetchBlurb();
        }, BLURB_RETRY_MS);
      }
      return;
    }
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();
    textEl.textContent = data.text || "";
    const ts = data.generatedAt ? new Date(data.generatedAt) : null;
    if (ts && !isNaN(ts)) {
      const mins = Math.max(0, Math.round((Date.now() - ts.getTime()) / 60000));
      metaEl.textContent =
        mins === 0 ? "just now" : mins < 90 ? `${mins} min ago` : `${Math.round(mins / 60)} h ago`;
    } else {
      metaEl.textContent = "";
    }
    section.hidden = false;
    blurbRetryCount = 0;
  } catch (err) {
    console.warn("blurb fetch failed:", err);
    section.hidden = true;
  }
}

// ===== Init =====
async function refresh() {
  await Promise.all([fetchTides(), fetchWeather(), fetchMarine()]);
  fetchBlurb();
}

refresh();
setInterval(refresh, REFRESH);
