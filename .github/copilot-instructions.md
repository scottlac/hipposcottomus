<!-- Workspace-specific instructions for hipposcottomus -->

## Project Overview
- **Name:** hipposcottomus
- **Repo:** https://github.com/scottlac/hipposcottomus
- **Language:** Go 1.26 backend, vanilla JS/CSS frontend (Chart.js 4)
- **Purpose:** Multi-dashboard web app for boating/lake conditions, hosted at **hipposcottomus.com**
- **Module name:** `jordan-lake-scraper` (in go.mod — historical, don't change)

## Dashboards
1. **Jordan Lake Dashboard** (`/lakedashboard/`) — Water temp, water level, weather, wind for B. Everett Jordan Lake, NC
2. **Ft. Lauderdale Boating** (`/ftlauderdale/`) — Tides, water temp, marine forecast, wind for Fort Lauderdale, FL
3. **Homepage** (`/`) — Landing page linking to both dashboards

## File Structure
- `main.go` (~616 lines) — Jordan Lake backend: scraper, USGS backfill, NWS weather proxy, API handlers, history persistence, serves homepage + static files
- `ftl.go` (~461 lines) — Ft. Lauderdale backend: NOAA CO-OPS tides, NWS weather, NDBC marine forecast, water temp
- `static/` — Jordan Lake frontend (index.html, app.js, style.css)
- `ftl/` — Ft. Lauderdale frontend (index.html, app.js, style.css)
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
- Go `embed.FS` for all static assets (`static/`, `ftl/`, `home/`)
- `History` type with `Add`, `AddWithDate`, `Sort`, `SaveToFile`, `LoadFromFile` — no max cap
- Persistence: `/data/temp_history.json` and `/data/level_history.json` (saved on scrape + hourly)
- Prometheus gauges: `jordan_lake_water_temperature_fahrenheit`, `jordan_lake_water_level_feet`
- Charts use year-selectable overlays with month-day windowing for comparing across years
- "Now" line plugin (pink dashed vertical) on temp, wind, and tide charts
- NWS forecast renderer handles night-first periods and strips "Night" suffix

## Infrastructure
- **Hosting:** DigitalOcean Kubernetes (`jordan-lake-cluster`, nyc3 region)
- **Registry:** `registry.digitalocean.com/jordan-lake-registry/jordan-lake-scraper:latest`
- **Domain:** `hipposcottomus.com` (Squarespace DNS, A record → 159.89.243.107)
- **TLS:** cert-manager with Let's Encrypt (ClusterIssuer `letsencrypt-prod`)
- **Ingress:** nginx ingress controller
- **Storage:** 1Gi PVC (`do-block-storage`) mounted at `/data/` for history persistence
- **Strategy:** Recreate (required by RWO PVC)

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
- Dark theme UI across all dashboards
- Chart.js 4 + chartjs-adapter-date-fns 3 for all charts
- Vanilla JS (no frameworks), CSS custom properties for theming
