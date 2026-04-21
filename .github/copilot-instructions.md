<!-- Workspace-specific Copilot instructions for jordan-lake-scraper -->

## Project Overview
- **Name:** jordan-lake-scraper
- **Language:** Go
- **Purpose:** Background scraper that fetches water conditions for Jordan Lake and exposes them as Prometheus metrics.
- **Data Source:** `https://epec.saw.usace.army.mil/bejrept.txt`

## Architecture
- Go application using `github.com/prometheus/client_golang`
- Two Prometheus gauges: `jordan_lake_water_temperature_fahrenheit`, `jordan_lake_water_level_feet`
- Regex-based extraction of last valid numerical values from plain-text reports
- Polling interval: 15 minutes
- Metrics endpoint: `:8080/metrics`

## Build & Run
- `go build -o scraper .`
- `docker build -t jordan-lake-scraper .`
- K8s manifests in `k8s/manifests.yaml`

## Conventions
- Use standard Go project layout
- Static binary compilation (`CGO_ENABLED=0`)
- Multi-stage Docker builds
- Kubernetes manifests with Prometheus scrape annotations
