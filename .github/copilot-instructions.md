<!-- Workspace-specific instructions for hipposcottomus -->

## Project Overview
- **Name:** hipposcottomus
- **Repo:** https://github.com/scottlac/hipposcottomus
- **Language:** Go 1.26 backend, vanilla JS frontend with Pico.css v2 baseline + custom CSS (Chart.js 4)
- **Purpose:** Multi-dashboard web app for boating/lake conditions, hosted at **hipposcottomus.com**
- **Module name:** `jordan-lake-scraper` (in go.mod — historical, don't change)

## Dashboards
1. **Jordan Lake Dashboard** (`/lakedashboard/`) — Water temp, water level, weather, wind for B. Everett Jordan Lake, NC. Live data via USACE bejrept.txt + USGS DV backfill.
2. **Lake Gaston Dashboard** (`/gaston/`) — Water temp, water level, weather, wind for Lake Gaston, NC/VA. Live data via USGS IV (station 02079785 at Elams, NC) + USGS DV backfill. Full pool ~200 ft. NWS grid is dynamically discovered from the lake centroid (36.50, -77.90).
3. **Lake Minneola Dashboard** (`/minneola/`) — Water level, *estimated* water temp, weather, wind, UV, AI blurb for Lake Minneola in Clermont, FL (Clermont Chain of Lakes). Lake Minneola has no real-time USGS gauge of its own. Water level comes from canal-connected Lake Minnehaha (station `02236840`, tracks Minneola within ~0.1 ft). Water temperature is **estimated** from a 7-day trailing mean of NWS air temperature via Open-Meteo's archive + forecast endpoints — no nearby USGS station reports lake water temp; the estimate is good to ~±3 °F for a ~10 ft-deep subtropical lake. The card carries an "EST" badge and the blurb prompt instructs the model never to quote the number verbatim. Nominal full pool ~95 ft (NGVD-29). NWS grid is discovered from the lake centroid (28.59, -81.78).
4. **Lake Conway Dashboard** (`/conway/`) — Same shape as Minneola but for the Lake Conway four-lake chain in south Orlando (Pine Castle / Belle Isle / Edgewood). Water level from USGS station `02262800` (Lake Conway at Pine Castle, directly on the lake — no proxy needed); water temperature estimated via the same 7-day air-temp proxy. The chain is weir-controlled at 85.46 ft NAVD-88. NWS grid discovered from (28.47, -81.35).
5. **Ft. Lauderdale Boating** (`/ftlauderdale/`) — Tides, water temp, marine forecast, wind for Fort Lauderdale, FL
6. **Hold'em Equity Calculator** (`/poker/`) — Pick hole cards + board, computes Texas Hold'em win probability
7. **Night Sky** (`/astronomy/`) — Sun/twilight times, moon phase, ISS pass predictions for user location
8. **Site Analytics** (`/analytics/`) — Page views per dashboard, external API health, Go runtime stats, traffic heatmap
9. **Homepage** (`/`) — Landing page linking to all dashboards

## File Structure
- `main.go` — Jordan Lake backend: scraper, USGS backfill, NWS weather proxy, API handlers, history persistence, serves homepage + static files
- `gaston.go` — Lake Gaston backend: USGS IV (live) + DV (5-year backfill), NWS weather proxy, API handlers, history persistence. Mirrors the Jordan Lake handlers/loops but driven entirely by USGS (no USACE source for this reservoir).
- `minneola.go` — Lake Minneola backend: USGS IV + DV pointed at the canal-connected Lake Minnehaha gauge (`02236840`) for water level only (no nearby USGS station reports water temp). Adds NWS forecast + Open-Meteo UV. The `usgsMultiResp` struct and `celsiusToFahrenheit` helper live in `gaston.go` and are reused here.
- `conway.go` — Lake Conway backend: same shape as `minneola.go` but pointed at USGS `02262800` (Lake Conway at Pine Castle, directly on the lake). NWS Melbourne for forecast, Open-Meteo UV. Full pool 85.46 ft NAVD-88 (county weir-controlled).
- `temp_estimate.go` — Generic Open-Meteo water-temp estimator used by Florida lakes that lack a USGS temp sensor. Provides `LakeTempEstimateConfig` (lat/lon, history target, window days, health-source label), plus `backfillLakeTempEstimate` (5-year archive + recent forecast) and `lakeTempEstimateLoop` (refresh on the lake's weather cadence). Each lake gets a thin driver file (`minneola_temp_estimate.go`, `conway_temp_estimate.go`) that wires its own config; the helpers (`fetchOpenMeteoDaily`, `mergeDailyByDate`, `trailingMeanDailyMean`) live here.
- `ftl.go` — Ft. Lauderdale backend: NOAA CO-OPS tides, NWS weather, NDBC marine forecast, water temp
- `poker.go` — Hold'em equity calculator backend (uses `github.com/paulhankin/poker/v2`)
- `astro.go` — Night Sky backend: sun/moon/twilight computations, ISS TLE fetch + pass predictions (uses `github.com/joshuaferrara/go-satellite`)
- `analytics.go` — Site analytics backend: page-view middleware, API health tracker (`TrackAPICall`), heatmap, runtime stats, persistence
- `static/` — Jordan Lake frontend (index.html, app.js, style.css)
- `gaston/` — Lake Gaston frontend; clone of `static/` with `FULL_POOL=200.0` and `<base href="/gaston/">` so the same app.js hits `/gaston/api/*`
- `minneola/` — Lake Minneola frontend; clone of `gaston/` with `FULL_POOL=95.0`, `<base href="/minneola/">`, an AI blurb section, and a footer noting the Minnehaha → Minneola substitution
- `ftl/` — Ft. Lauderdale frontend
- `poker/` — Poker frontend
- `astro/` — Night Sky frontend
- `analytics/` — Site Analytics frontend
- `home/index.html` — Homepage
- `k8s/manifests.yaml` — Full K8s deployment (Deployment, PVC, Service, Ingress, ClusterIssuer)
- `Dockerfile` — Multi-stage build (golang:1.26-alpine → alpine:3.20)

## Data Sources
- **Jordan Lake live:** USACE `https://epec.saw.usace.army.mil/bejrept.txt` (regex extraction, 15-min poll)
- **Jordan Lake history:** USGS API station `02098197`, parameter `62614` (water level, 5-year backfill)
- **Lake Gaston live + history:** USGS station `02079785` ("Lake Gaston near Elams, NC"). Live readings via the IV endpoint every 15 min; 5-year DV backfill at startup. The same request queries elevation under both NGVD-29 (`62614`) and NAVD-88 (`62615`) plus water temp (`00010`, °C → °F conversion in `celsiusToFahrenheit`) — whichever codes the station actually reports get populated; the others come back empty and are silently ignored.
- **Lake Minneola water level:** USGS station `02236840` (Lake Minnehaha at Clermont) — canal-connected to Minneola, tracks within ~0.1 ft. Live readings via the IV endpoint every 15 min; 5-year DV backfill at startup. Same multi-parameter elevation pattern as Gaston (62614/62615/62616/00062).
- **Lake Minneola water temperature:** *Estimated* from a 7-day trailing mean of NWS air temperature via Open-Meteo's archive (5-year backfill, ~5-day lag) and forecast (`past_days=92`, covers the lag + today) endpoints. No nearby USGS station — including the obvious Palatlakaha River outflow gauges — reports water temperature. The estimate is good to ~±3 °F for a ~10 ft-deep subtropical lake; the card shows "(est.)" and the blurb prompt is written to never quote the number verbatim.
- **NWS Weather:** `api.weather.gov` (air temp, wind, forecast)
- **FTL Tides:** NOAA CO-OPS station `8722939` (hi-lo only — cosine-interpolated into smooth curve)
- **FTL Water Temp:** NOAA CO-OPS Virginia Key station `8723214` (closest station with water temp)
- **FTL Marine Forecast:** NDBC `FZUS52.KMFL`

## Key Architecture Details
- Go `embed.FS` for all static assets — each feature file embeds its own directory (`main.go` embeds `static/ home/ poker/`, `ftl.go` embeds `ftl/`, `astro.go` embeds `astro/`, `analytics.go` embeds `analytics/`)
- Each feature exposes an `InitXxx(mux *http.ServeMux)` function called from `main.go` to register routes
- `History` type with `Add`, `AddWithDate`, `Sort`, `SaveToFile`, `LoadFromFile` — no max cap
- Persistence under `/data/` (env-overridable via `DATA_DIR`): `temp_history.json`, `level_history.json`, `analytics.json`
- Prometheus gauges: `jordan_lake_water_temperature_fahrenheit`, `jordan_lake_water_level_feet` — scraped from `/metrics` (distinct from the UI at `/analytics/`)
- Charts use year-selectable overlays with month-day windowing for comparing across years
- "Now" line plugin (pink dashed vertical) on temp, wind, and tide charts
- NWS forecast renderer handles night-first periods and strips "Night" suffix

### AI boating blurb (`blurb.go` + per-lake `blurb_<key>.go`)
- `blurb.go` is the generic framework — `LakeBlurb` payload type, `LLMUsageEntry` cost meter, `lakeBlurbConfig` struct (`Key`, `BasePath`, `SystemPrompt`, `SnapshotFn` + internal mu/current/genMu), the `(cfg) handle / maybeTrigger / generate` methods, `RegisterBlurb`, persistence (`saveLLMUsage` / `loadLLMUsage`), and the `inBlackout` / `blackoutStartFor` / `shouldGenerate` decision functions. `InitBlurb(mux)` is the single wiring point called from `main.go`; it loads the cost meter once, then calls `RegisterBlurb` for each per-lake config.
- Per-lake files (`blurb_jordan.go`, `blurb_minneola.go`) each own their system prompt (a long, lake-specific advisor written for the local geography and recreation patterns) and a `<lake>Snapshot()` function that reads the corresponding history + weather globals. The config var (`jordanBlurbConfig`, `minneolaBlurbConfig`) wires them together. **Adding a new lake = one new `blurb_<key>.go` file + one extra line in `InitBlurb`.**
- Currently mounted: `/lakedashboard/api/blurb` (Jordan Lake) and `/minneola/api/blurb` (Lake Minneola).
- **On-demand generation**: there is no background ticker. `(cfg).handle` calls `cfg.maybeTrigger`, which delegates the decision to the pure `shouldGenerate(now, lastGenAt)` function and uses `cfg.genMu.TryLock` for single-flight *per lake* (different lakes don't block each other). Rules:
  - **Daytime (6am–8pm ET)**: regenerate if the cached blurb is older than `blurbMinInterval` (30 min), or there's no blurb.
  - **Blackout (8pm–6am ET)**: one regeneration allowed per blackout period — when the cached blurb predates the start of *this* blackout window (`blackoutStartFor`), or there's no blurb. After that one refresh, further visits during the same overnight period return the cached blurb unchanged.
  - The triggering request always gets served the current (possibly stale) blurb immediately; the frontend's 60s auto-refresh picks up the new one ~2s later. Cold pod / no blurb yet → 204, frontend hides the section.
- **No prompt caching**: deliberately omitted. Calls are ≥30 min apart but the ephemeral cache TTL is 5 min, so a cached prefix always expires between calls — caching would only ever pay the 1.25× write premium. Plain uncached input is cheapest at this frequency. (Don't add `cache_control` back without also raising call frequency above the TTL.)
- **Cost tracking**: `recordLLMUsage` folds `resp.Usage` into a per-model `LLMUsageEntry` (tokens + USD, Haiku 4.5 pricing constants at the top of `blurb.go`). All lakes using the same model accumulate on one entry (correct for spend tracking). Persisted to `/data/llm_usage.json` so the meter survives restarts; surfaced on `/analytics/` via the `llmUsage` field in the overview. Per-lake failures are routed via `TrackAPICall("Anthropic-Blurb-"+cfg.Key, err)` so the API-health panel shows them per lake (`Anthropic-Blurb-jordan`, `Anthropic-Blurb-minneola`).
- **Key**: `ANTHROPIC_API_KEY` from the `anthropic-api-key` Secret (`optional: true` in the manifest). Never in code/repo/logs. `time/tzdata` is blank-imported so `America/New_York` resolves on alpine.

### Analytics instrumentation
- `analyticsMiddleware` wraps the top-level mux; it counts requests whose exact path matches an entry in `trackedPaths` (home, lake, gaston, minneola, ftl, poker, astro, analytics). Assets, API calls, `/metrics`, and `/healthz` are ignored.
- Heatmap is a `[7][24]int64` cumulative grid keyed by weekday × hour of request time.
- External API health is tracked via `TrackAPICall(source string, err error)` — called at each fetch site (USACE, USGS, NWS-Raleigh, NOAA-Tides, NOAA-WaterTemp, NWS-Miami, NDBC-Marine, CelesTrak-TLE). When adding a new external fetch, call `TrackAPICall` with a stable source label and the error (nil = success).
- **Screen resolutions** are reported by a tiny `navigator.sendBeacon` snippet on every tracked page that POSTs `{"size": "WxH"}` to `/analytics/api/screen`. The handler validates bounds (100–16384px each axis), caps unique-key cardinality at 5000, and bumps `Analytics.ScreenSizes`. Stored independently from page-view, country, and city counters — never linked.
- `analytics.json` is saved every 5 minutes; page views are bucketed by local date (`YYYY-MM-DD`).
- **Privacy:** only aggregate counts are tracked — no IPs, user agents, or session identifiers are stored. Country lookup reads the client IP from `X-Real-IP` / `X-Forwarded-For`, resolves it to an ISO code, and increments a per-code counter. The IP itself is never written to disk. Each metric (page views, country, city heatmap, screen resolution) is a standalone counter — there is no per-visit record connecting them.

### Geo lookup (country + city heatmap)
- Uses `github.com/oschwald/geoip2-golang` against the MaxMind GeoLite2 **City** DB (~70MB, free with a MaxMind account). The City DB includes country info, so both the country list and the visitor heatmap come from a single lookup.
- The DB path defaults to `/data/GeoLite2-City.mmdb` and is overridable via the `GEOIP_DB` env var.
- If the file is missing, geo tracking is silently disabled at startup — analytics keep working without it. The overview payload exposes `geoEnabled` so the frontend shows a placeholder on the map and countries panels instead of broken widgets.
- **Privacy:** raw IPs are discarded after the synchronous lookup. Latitude/longitude are rounded to 1 decimal place (~11km at the equator) before being stored as bucket counters in `Analytics.CityHeatmap` — raw coordinates are never persisted. The constant `cityHeatmapPrecision` controls the rounding.
- **Deploy:** the `geoip-init` initContainer in `k8s/manifests.yaml` downloads the DB to the `/data/` PVC on every Pod start, reading the license key from the `maxmind-license` Secret (`kubectl create secret generic maxmind-license --from-literal=license-key=<KEY>`). The container skips download if the existing DB is less than 3 days old, cleans up any legacy `GeoLite2-Country.mmdb`, and fails open if MaxMind is unreachable.
- **Frontend map:** Leaflet 1.9 + `leaflet.heat` loaded from unpkg, rendered over CartoDB dark-matter tiles. No build step, no API key.
- **Refresh cadence:** tied to pod restarts (deploys, node reschedules). Because the PVC is RWO, a scheduled `CronJob` can't cleanly run alongside the main pod; if a weekly refresh is needed, either `kubectl rollout restart deployment/jordan-lake-scraper` on a schedule, or move the download into the Go app's startup.

## Infrastructure
- **Hosting:** DigitalOcean Kubernetes (`jordan-lake-cluster`, nyc3 region)
- **Registry:** `registry.digitalocean.com/jordan-lake-registry/jordan-lake-scraper:latest`
- **Domain:** `hipposcottomus.com` (Squarespace DNS, A record → 159.89.243.107)
- **TLS:** cert-manager with Let's Encrypt (ClusterIssuer `letsencrypt-prod`)
- **Ingress:** nginx ingress controller
- **Storage:** 1Gi PVC (`do-block-storage`) mounted at `/data/` for history persistence
- **Strategy:** Recreate (required by RWO PVC)

## Homepage layout
- The homepage groups its links into three top-level cards: **Dashboards** (Lake / FTL / Astro), **Tools** (Poker), and **Analytics**. Dashboards and Tools are click-to-expand `<details>`/`<summary>` elements styled as cards — adding a new dashboard or tool just adds a child `<a class="card-mini">` row, no homepage layout change needed.
- An **"Add to Home Screen"** button in the hero section listens for `beforeinstallprompt` and, when fired (Chrome/Edge/Android), reveals itself; clicking it triggers the native install dialog. iOS Safari never fires that event, so a small text hint shows the manual `Share → Add to Home Screen` path on iPhone/iPad. Both the button and the hint hide on `appinstalled`.

## Open Graph link previews
- Every dashboard's HTML head includes `og:*` and `twitter:*` meta tags so links shared on WhatsApp, iMessage, Slack, Discord, Twitter, etc. render with a title, description, and a preview image. Crawlers don't run JS, so anything per-URL has to be server-rendered.
- **Generic `/og.png`** (in `site_og.go`) is a 1200×630 PNG with the hippo wordmark and dashboard list. All non-poker dashboards reference it from a static `<meta property="og:image">` in their HTML.
- **Per-hand `/poker/og.png?hand=…&board=…`** (in `poker_og.go`) draws the actual selected cards onto the same 1200×630 canvas. The poker page is the only one served through a Go template (in `poker.go`'s `servePokerHTML`) — it injects `og:image`, `og:title`, etc. with the current query params so a shared `/poker/?hand=AsKh&board=…` URL gets a preview showing those cards. Other `/poker/*` paths still flow through the static `FileServer` untouched.
- Image rendering uses `golang.org/x/image` with the Go fonts (`gofont/gobold`, `gofont/goregular`) — pure Go, no font files committed. Palette in `poker_og.go` mirrors the site's CSS custom properties.
- `absoluteURL(r, path)` builds full URLs honoring `X-Forwarded-Proto` / `X-Forwarded-Host` from the nginx ingress so OG tags get `https://hipposcottomus.com/...` rather than the in-cluster service hostname.

## Progressive Web App
- The whole site is one installable PWA. `home/manifest.json` advertises `start_url: "/"` and `scope: "/"`, so any of the dashboards can trigger the install prompt. `home/icon.svg` is the SVG icon for browsers that handle SVG; `/apple-touch-icon.png` (180×180) and `/icon-512.png` (512×512, marked `any maskable`) are rendered on demand by `site_og.go` — same pure-Go pipeline as `/og.png`, no committed PNG files. Icon design is the "hippo" wordmark on the dark-navy brand color with a small accent stripe (the Go bold font doesn't include emoji glyphs, so the 🦛 from the favicon SVG isn't reproducible there).
- `home/sw.js` is the service worker, served at `/sw.js`. Strategy:
  - Pre-cache the app shell (home + each dashboard root + manifest + icon) on `install`.
  - **Network-only** for `/api/*`, `/metrics`, `/healthz` — these are time-sensitive and must never be served stale.
  - **Cache-first with runtime caching** for other same-origin GETs. Successful basic responses get added to the cache so subsequent navigations work offline.
  - On total network failure, falls back to the cached `/` so the app at least opens.
- Cross-origin requests (Pico, Chart.js, Leaflet, unpkg) bypass the SW entirely so their own caching rules apply.
- Bump the `CACHE_VERSION` constant in `sw.js` whenever you want to force-refresh cached static assets across all clients. Combined with `skipWaiting()` + `clients.claim()`, updates take effect on the next page load rather than after every tab is closed.
- Each dashboard HTML adds the same boilerplate to `<head>`: `<link rel="manifest">`, `theme-color` meta, and Apple-specific PWA meta tags. The closing `<script>` block now also registers the SW after page load.

## Poker equity calculator
- Monte Carlo iterations live in `pokerIterations` (currently 50000). At 50k the standard error on a 50/50 race is ~0.22%, so percentages stay visually stable across reloads. There's a `BenchmarkCalcEquity` in `poker_bench_test.go` for sizing it; reduce if request latency on the 0.1-CPU pod becomes a problem.
- `computeThreats(hero, board)` enumerates every possible 2-card opponent hand from the remaining deck (C(45,2)=990 on the river), evaluates each against hero with the current board, and groups the losing combos by `HandCategory`. Returns nil when the board has fewer than 3 cards (hero has no 5-card hand to compare against yet). Output is ordered strongest-first.
- `classify5([5]Card) HandCategory` is a small standalone classifier independent of paulhankin's score packing — easy to test against the standard 9 hand categories. `bestCategory` finds the strongest 5-card subset of 5/6/7 cards using Eval5 to pick the winner, then runs `classify5` on those 5.

## Shareable state URLs
- The poker page reads/writes its UI state to query params: `hand`, `board`, `players` (e.g. `/poker/?hand=AsKh&board=QhJcTd&players=8`). Each card is exactly 2 chars (rank + suit), so card-list params are just the cards concatenated.
- `loadFromURL()` runs once at init before the first render. Invalid cards, duplicates, and out-of-range values are silently ignored — bad URLs degrade to "no state set" rather than throwing.
- `syncURL()` is called after every state mutation (`onCardTap`, `clearAll`, `numPlayers` change) and uses `history.replaceState` so each card tap doesn't add a Back-history entry.
- A "Copy share URL" button (`#shareUrl`, styled with `.control__button--share`) writes `location.href` to the clipboard via `navigator.clipboard.writeText`.
- Other dashboards don't currently mirror their state to the URL — the lake/ftl chart range buttons and the astro location are reasonable next candidates.

## Testing
- Tests live alongside source files as `*_test.go` in the `main` package (so they can see unexported types/helpers).
- Prefer local instances over the package-level globals (`tempHistory`, `levelHistory`, `analytics`) — use `newTestAnalytics()` for fresh state. When a test *must* mutate a global, save and restore it via `t.Cleanup` so ordering isn't fragile.
- Network-touching code isn't unit-tested; cover it by testing the pure helpers it composes (regex parsers, interpolation, header extraction, etc.) or via `httptest.NewServer` when exercising handlers/middleware.
- Run tests: `go test -race -count=1 ./...`. CI runs the same command plus `go vet` and `go build` on every PR and push to `main` (`.github/workflows/ci.yml`).
- Deploy (`.github/workflows/deploy.yml`) still runs only on push-to-`main` and doesn't yet depend on the CI job passing — a future change could chain them via `workflow_run` or a `needs:` block if that's desired.

## Build & Deploy
```bash
# Build locally
CGO_ENABLED=0 go build -o scraper .

# Build & push Docker image
docker build --platform linux/amd64 -t registry.digitalocean.com/jordan-lake-registry/jordan-lake-scraper:latest .
docker push registry.digitalocean.com/jordan-lake-registry/jordan-lake-scraper:latest

# Deploy
kubectl apply -f k8s/manifests.yaml
kubectl rollout restart deployment/jordan-lake-scraper
kubectl rollout status deployment/jordan-lake-scraper --timeout=90s
```

## Conventions
- Standard Go project layout, static binary (`CGO_ENABLED=0`)
- Multi-stage Docker builds
- Dark theme UI across all dashboards (`<html data-theme="dark">`)
- Chart.js 4 + chartjs-adapter-date-fns 3 for all charts
- Vanilla JS (no JS frameworks)
- **Pico.css v2** pulled in via CDN on every page for typography, forms, and link defaults; loaded **before** each page's `style.css` so page-specific styles override Pico where needed
- Shared palette tokens (`--bg`, `--surface`, `--border`, `--text`, `--accent-*`, `--radius`) are defined per-page in `:root` and also mapped onto Pico's CSS variables (`--pico-background-color`, `--pico-primary`, `--pico-card-*`, etc.) so Pico harmonizes with the custom design
- Pico v2 defaults buttons to `width: 100%` with bottom margin — custom button classes (`.range-btn`, `.overlay-btn`, `.control__button`, `.mini-card`, `.location-bar__button`) explicitly set `width: auto; margin: 0` to opt out
