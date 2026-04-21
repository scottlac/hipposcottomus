// ===== Configuration =====
const REFRESH = 120_000; // 2 min
const BASE = document.querySelector("base")?.getAttribute("href")?.replace(/\/$/, "") || "";

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

    const now = Date.now();
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
    ],
  },
  options: {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { mode: "index", intersect: false },
    plugins: {
      legend: { labels: { color: "#94a3b8", boxWidth: 12, padding: 16 } },
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

// Parse NOAA local-time string "2026-04-17 14:00" as local Date
function parseNOAATime(t) {
  return new Date(t.replace(" ", "T"));
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
      const now = new Date();
      const next = data.hiLo.find((e) => parseNOAATime(e.time) > now);
      if (next) {
        const t = parseNOAATime(next.time);
        document.getElementById("nextTideType").textContent = next.type;
        document.getElementById("nextTideDetail").textContent =
          `${next.height.toFixed(2)} ft`;
        document.getElementById("nextTideTime").textContent =
          t.toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit" });
      }

      // Tide schedule
      renderTideSchedule(data.hiLo);
    }

    tideChart.update("none");
  } catch (err) {
    console.error("Failed to fetch tides:", err);
  }
}

function renderTideSchedule(events) {
  const el = document.getElementById("tideSchedule");
  el.innerHTML = "";

  // Show next 8 events
  const now = new Date();
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
      <span class="tide-event__time">${t.toLocaleDateString("en-US", {weekday: "short"})} ${t.toLocaleTimeString("en-US", {hour: "numeric", minute: "2-digit"})}</span>
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
    if (data.waterTemp != null) {
      document.getElementById("waterTemp").textContent = data.waterTemp.toFixed(1);
    }

    // Current conditions
    if (data.hourly?.length) {
      const now = data.hourly[0];
      document.getElementById("airTemp").textContent = now.temperature;
      document.getElementById("weatherDesc").textContent = `· ${now.shortForecast}`;
      document.getElementById("weatherIcon").textContent = weatherIcon(now.shortForecast);
      document.getElementById("windSpeed").textContent = now.windSpeed;
      document.getElementById("windDir").textContent = now.windDirection;

      const deg = WIND_DEGREES[now.windDirection] ?? 0;
      const arrow = document.getElementById("windArrow");
      if (arrow) arrow.style.transform = `translate(-50%, 0) rotate(${deg + 180}deg)`;

      // Wind chart
      windChart.data.datasets[0].data = data.hourly.map((p) => ({
        x: new Date(p.startTime),
        y: parseWindSpeed(p.windSpeed),
        dir: p.windDirection,
      }));
      windChart.update("none");
    }

    // 7-day forecast
    if (data.forecast?.length) {
      renderForecast(data.forecast);
    }
  } catch (err) {
    console.error("Failed to fetch weather:", err);
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

async function fetchMarine() {
  try {
    const res = await fetch(`${BASE}/api/marine`);
    const data = await res.json();
    const el = document.getElementById("marineForecast");
    el.textContent = data.forecast || "Marine forecast unavailable.";
  } catch (err) {
    console.error("Failed to fetch marine forecast:", err);
  }
}

// ===== Init =====
async function refresh() {
  await Promise.all([fetchTides(), fetchWeather(), fetchMarine()]);
}

refresh();
setInterval(refresh, REFRESH);
