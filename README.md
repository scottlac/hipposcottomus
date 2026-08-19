# 🦛 hipposcottomus

**Live at [hipposcottomus.com](https://hipposcottomus.com)** — a multi-app Go web platform built as a hands-on exercise in Go and cloud-native engineering: a single static binary serving six live-data dashboards, interactive tools, and self-hosted analytics, deployed to DigitalOcean Kubernetes through a GitHub Actions CI/CD pipeline. Installable as a PWA.

## Dashboards

| Dashboard | Route | Data sources |
|---|---|---|
| 🌊 **Jordan Lake** (NC) | [`/lakedashboard/`](https://hipposcottomus.com/lakedashboard/) | Live temp & level scraped from a USACE plain-text report every 15 min, 5-year USGS backfill, NWS weather & wind |
| 🚤 **Lake Gaston** (NC/VA) | [`/gaston/`](https://hipposcottomus.com/gaston/) | USGS instantaneous-value live readings + daily-value backfill, NWS weather |
| 🏝️ **Lake Minneola** (FL) | [`/minneola/`](https://hipposcottomus.com/minneola/) | SJRWMD in-lake station via the USF Water Atlas API, estimated water temp from an air-temp trailing mean, UV index |
| 🛥️ **Lake Conway** (FL) | [`/conway/`](https://hipposcottomus.com/conway/) | Orange County data-logger station via the USF Water Atlas API, estimated water temp |
| ⚓ **Ft. Lauderdale Boating** | [`/ftlauderdale/`](https://hipposcottomus.com/ftlauderdale/) | NOAA CO-OPS tides (hi/lo points cosine-interpolated into a smooth curve), water temp, NDBC marine forecast |
| 🔭 **Night Sky** | [`/astronomy/`](https://hipposcottomus.com/astronomy/) | Sun & twilight times, moon phase and rise/set, and visible ISS passes computed from live TLEs |

## Tools & Analytics

- 🂡 **Hold'em Equity Calculator** ([`/poker/`](https://hipposcottomus.com/poker/)) — pick hole cards and a board, see your win probability against random opponents.
- 🔳 **QR Code Generator** ([`/qr/`](https://hipposcottomus.com/qr/)) — payload builders, logo overlay, shaped plates and module styles, curved ring text, SVG and 3D-printable STL export.
- 🗳️ **Ranked Choice Voting** ([`/vote/`](https://hipposcottomus.com/vote/)) — create a poll, share one link, everyone ranks the options. Instant-runoff counting with a visible exhausted-ballot bucket, deterministic tie-breaks (flagged when a different tie-break choice would have changed the winner), and a Condorcet head-to-head cross-check. No accounts; polls expire after 90 days.
- 📊 **Site Analytics** ([`/analytics/`](https://hipposcottomus.com/analytics/)) — self-hosted: page views per app, external-API health tracking, Go runtime stats, and a traffic heatmap.

## Architecture

- **One Go binary, zero frameworks.** Each app is its own Go file (`main.go`, `gaston.go`, `minneola.go`, `conway.go`, `ftl.go`, `poker.go`, `astro.go`, `qr.go`, `analytics.go`) registering its handlers on a shared server; every frontend is embedded with `embed.FS`, so the deployable artifact is a single static binary (`CGO_ENABLED=0`).
- **Vanilla JS + Chart.js frontends**, Pico.css baseline with CSS custom properties for theming — no build step.
- **Integrations with 8+ public data APIs** (USACE, USGS, NOAA CO-OPS, NDBC, NWS, Open-Meteo, USF Water Atlas, ISS TLEs), each wrapped with health tracking that surfaces on the analytics dashboard.
- **Observability:** Prometheus gauges exposed at `/metrics`, with a `ServiceMonitor` for Prometheus Operator auto-discovery and plain scrape annotations as a fallback.
- **Persistence:** scrape history is kept in memory and checkpointed to JSON on a Kubernetes PersistentVolumeClaim, restored on startup.
- **PWA:** web app manifest, service worker, and install prompts for phone home screens.
- **Tests:** table-driven unit tests plus benchmarks for the poker equity engine (`go test ./...`).

## Infrastructure

- **Docker:** multi-stage build (`golang` builder → minimal `alpine` runtime).
- **Kubernetes** ([`k8s/manifests.yaml`](k8s/manifests.yaml)): Deployment with liveness/readiness probes and resource limits, ClusterIP Service, nginx Ingress, and TLS via cert-manager + Let's Encrypt. `Recreate` strategy because the RWO block-storage PVC can't attach to two pods at once.
- **CI/CD** ([`.github/workflows/deploy.yml`](.github/workflows/deploy.yml)): every push to `main` builds the image with Buildx (GitHub Actions cache), pushes it to the DigitalOcean container registry tagged with the commit SHA, applies the manifests, rolls the Deployment to the new image, and dumps pod logs if the rollout fails.

## Run It Locally

```bash
go build -o hipposcottomus .
./hipposcottomus
# → http://localhost:8080
```

Or with Docker:

```bash
docker build -t hipposcottomus .
docker run -p 8080:8080 hipposcottomus
```

## Built With AI Assistance

This project was built in collaboration with [Claude Code](https://claude.com/claude-code) as a deliberate exercise in AI-assisted cloud engineering — you'll see Claude as a co-author throughout the commit history. The architecture, infrastructure decisions, data-source research, and code review are mine; treating an AI agent as a pair programmer (and knowing when to overrule it) is part of the skill set this repo demonstrates.
