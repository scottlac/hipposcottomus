# Jordan Lake Scraper

Background scraper that fetches water conditions for [Jordan Lake](https://en.wikipedia.org/wiki/Jordan_Lake_(North_Carolina)) from the US Army Corps of Engineers and exposes them as Prometheus metrics with a live dashboard.

## Dashboard

Open `http://localhost:8080` to see:
- **Current-value cards** for temperature and water level
- **Time-series charts** with full scrape history (up to 7 days)
- Auto-refreshes every 60 seconds

## Metrics

| Metric | Description |
|---|---|
| `jordan_lake_water_temperature_fahrenheit` | Water temperature in °F |
| `jordan_lake_water_level_feet` | Midnight elevation in feet |

Data source: `https://epec.saw.usace.army.mil/bejrept.txt`

## API

| Endpoint | Description |
|---|---|
| `GET /` | Dashboard UI |
| `GET /api/current` | Latest temperature & water level as JSON |
| `GET /api/history` | Full time-series history as JSON |
| `GET /metrics` | Prometheus metrics |

## Quick Start

### Run Locally

```bash
go build -o scraper .
./scraper
# → http://localhost:8080
```

### Docker

```bash
docker build -t jordan-lake-scraper .
docker run -p 8080:8080 jordan-lake-scraper
```

### Kubernetes

```bash
kubectl apply -f k8s/manifests.yaml
```

The manifests include:
- **Deployment** — 1 replica with health probes and resource limits
- **ClusterIP Service** — exposes port 8080 internally
- **ServiceMonitor** — for Prometheus Operator auto-discovery

Standard Prometheus scrape annotations are also added to the pod template for clusters without the Prometheus Operator.

## How It Works

1. On startup, fetches the plain-text report from USACE.
2. Uses regex to find **all** matches for temperature and water level.
3. Takes the **last** numerical match to get the most recent valid reading (skipping `******` placeholders from partially-updated reports).
4. Updates Prometheus gauges and stores timestamped readings in memory (ring buffer, up to 7 days).
5. Repeats every **15 minutes**.

## Project Structure

```
.
├── main.go              # Go application (scraper, API, server)
├── static/
│   ├── index.html       # Dashboard HTML
│   ├── style.css        # Dashboard styles
│   └── app.js           # Dashboard client-side logic (Chart.js)
├── go.mod               # Go module definition
├── go.sum               # Dependency checksums
├── Dockerfile           # Multi-stage Docker build
└── k8s/
    └── manifests.yaml   # Kubernetes Deployment, Service & ServiceMonitor
```
