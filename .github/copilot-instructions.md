<!-- Workspace-specific instructions for hipposcottomus -->

## Project Overview
- **Name:** hipposcottomus
- **Repo:** https://github.com/scottlac/hipposcottomus
- **Language:** Go 1.26 backend, vanilla JS frontend with Pico.css v2 baseline + custom CSS (Chart.js 4)
- **Purpose:** Multi-dashboard web app for boating/lake conditions, hosted at **hipposcottomus.com**
- **Module name:** `jordan-lake-scraper` (in go.mod — historical, don't change)

## Dashboards
1. **Jordan Lake Dashboard** (`/lakedashboard/`) — Water temp, water level, weather, wind for B. Everett Jordan Lake, NC
2. **Ft. Lauderdale Boating** (`/ftlauderdale/`) — Tides, water temp, marine forecast, wind for Fort Lauderdale, FL
3. **Hold'em Equity Calculator** (`/poker/`) — Pick hole cards + board, computes Texas Hold'em win probability
4. **Night Sky** (`/astronomy/`) — Sun/twilight times, moon phase, ISS pass predictions for user location
5. **Site Analytics** (`/analytics/`) — Page views per dashboard, external API health, Go runtime stats, traffic heatmap
6. **Homepage** (`/`) — Landing page linking to all dashboards

## File Structure
- `main.go` — Jordan Lake backend: scraper, USGS backfill, NWS weather proxy, API handlers, history persistence, serves homepage + static files
- `ftl.go` — Ft. Lauderdale backend: NOAA CO-OPS tides, NWS weather, NDBC marine forecast, water temp
- `poker.go` — Hold'em equity calculator backend (uses `github.com/paulhankin/poker/v2`)
- `astro.go` — Night Sky backend: sun/moon/twilight computations, ISS TLE fetch + pass predictions (uses `github.com/joshuaferrara/go-satellite`)
- `analytics.go` — Site analytics backend: page-view middleware, API health tracker (`TrackAPICall`), heatmap, runtime stats, persistence
- `static/` — Jordan Lake frontend (index.html, app.js, style.css)
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

### Analytics instrumentation
- `analyticsMiddleware` wraps the top-level mux; it counts requests whose exact path matches an entry in `trackedPaths` (home, lake, ftl, poker, astro, analytics). Assets, API calls, `/metrics`, and `/healthz` are ignored.
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
