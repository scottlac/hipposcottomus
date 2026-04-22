package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang"
)

const (
	analyticsBasePath      = "/analytics"
	analyticsFile          = "analytics.json"
	analyticsSaveInterval  = 5 * time.Minute
	analyticsErrTruncateAt = 200

	// Default path for the MaxMind GeoLite2 City DB. Override with
	// GEOIP_DB env var. If the file is missing, geo tracking is silently
	// disabled — analytics still work.
	defaultGeoIPDB = "/data/GeoLite2-City.mmdb"

	// Coordinates are rounded to 1 decimal place (~11km at the equator)
	// before being stored as bucket counters. Raw lat/lon is never persisted.
	cityHeatmapPrecision = 1
)

//go:embed analytics
var analyticsFiles embed.FS

// trackedPaths maps request paths to a short kind label used on the frontend.
// Only these exact paths count as "page views"; everything else (assets,
// api calls, /metrics, /healthz) is ignored.
var trackedPaths = map[string]string{
	"/":                "home",
	"/lakedashboard/":  "lake",
	"/ftlauderdale/":   "ftl",
	"/poker/":          "poker",
	"/astronomy/":      "astro",
	"/analytics/":      "analytics",
}

// APIHealthEntry tracks success/failure counts for one external API source.
type APIHealthEntry struct {
	Success     int64     `json:"success"`
	Failure     int64     `json:"failure"`
	LastSuccess time.Time `json:"lastSuccess"`
	LastFailure time.Time `json:"lastFailure"`
	LastError   string    `json:"lastError,omitempty"`
}

// Analytics holds all site-wide counters and health data.
type Analytics struct {
	mu        sync.RWMutex
	StartedAt time.Time `json:"startedAt"`

	// PageViews[kind][date] = count. date is "2006-01-02".
	PageViews map[string]map[string]int64 `json:"pageViews"`

	// Heatmap[weekday 0-6][hour 0-23] = cumulative count across all time.
	Heatmap [7][24]int64 `json:"heatmap"`

	// APIHealth[source] = aggregate counters + timestamps.
	APIHealth map[string]*APIHealthEntry `json:"apiHealth"`

	// Countries[ISO-3166-1 alpha-2 code] = cumulative count across all time.
	// Only country codes are stored — never IPs. "??" is used for lookups
	// that fail (private IPs, DB gaps, etc.).
	Countries map[string]int64 `json:"countries"`

	// CityHeatmap[bucket] = count. bucket is "lat,lon" rounded to
	// cityHeatmapPrecision decimal places. Raw coordinates are never
	// persisted — only the bucket aggregate.
	CityHeatmap map[string]int64 `json:"cityHeatmap"`
}

var analytics = &Analytics{
	StartedAt:   time.Now(),
	PageViews:   map[string]map[string]int64{},
	APIHealth:   map[string]*APIHealthEntry{},
	Countries:   map[string]int64{},
	CityHeatmap: map[string]int64{},
}

// geoDB is loaded on startup if the MMDB file exists; nil otherwise.
var (
	geoDB      *geoip2.Reader
	geoEnabled bool
)

// geoLookup holds the aggregated, privacy-safe outputs of a City DB lookup.
type geoLookup struct {
	country string // ISO-3166-1 alpha-2 code; "" if unknown.
	bucket  string // "lat,lon" rounded to cityHeatmapPrecision; "" if no coords.
}

// RecordPageView increments counters for a request path if it is tracked.
func (a *Analytics) RecordPageView(path string, geo geoLookup) {
	kind, ok := trackedPaths[path]
	if !ok {
		return
	}
	now := time.Now()
	date := now.Format("2006-01-02")
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.PageViews[kind] == nil {
		a.PageViews[kind] = map[string]int64{}
	}
	a.PageViews[kind][date]++
	a.Heatmap[int(now.Weekday())][now.Hour()]++
	if geoEnabled {
		key := geo.country
		if key == "" {
			key = "??"
		}
		a.Countries[key]++
		if geo.bucket != "" {
			a.CityHeatmap[geo.bucket]++
		}
	}
}

// clientIP extracts the real client IP from request headers. Returns nil if
// no usable address is found. Trusts X-Real-IP and X-Forwarded-For because
// the service is expected to run behind an ingress that sets them.
func clientIP(r *http.Request) net.IP {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
			return ip
		}
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		// Leftmost entry is the original client.
		first := strings.TrimSpace(strings.SplitN(v, ",", 2)[0])
		if ip := net.ParseIP(first); ip != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

// lookupGeo resolves an IP to a country code and a rounded lat/lon bucket.
// Returns the zero value (both fields "") if geo is disabled, the lookup
// fails, or the City record has no usable coordinates. The raw IP is never
// stored — only the aggregate labels returned here.
func lookupGeo(ip net.IP) geoLookup {
	var out geoLookup
	if !geoEnabled || ip == nil {
		return out
	}
	rec, err := geoDB.City(ip)
	if err != nil {
		return out
	}
	out.country = rec.Country.IsoCode
	lat := rec.Location.Latitude
	lon := rec.Location.Longitude
	// Null island (0, 0) is the default when the DB has no coords — drop it.
	if lat == 0 && lon == 0 {
		return out
	}
	// Round to cityHeatmapPrecision decimal places; this is the only form
	// in which coordinates are ever stored.
	format := fmt.Sprintf("%%.%df,%%.%df", cityHeatmapPrecision, cityHeatmapPrecision)
	out.bucket = fmt.Sprintf(format, lat, lon)
	return out
}

// TrackAPICall records a success or failure for an external API fetch.
// Call it from each fetch site with the error (nil = success).
func TrackAPICall(source string, err error) {
	analytics.mu.Lock()
	defer analytics.mu.Unlock()
	entry, ok := analytics.APIHealth[source]
	if !ok {
		entry = &APIHealthEntry{}
		analytics.APIHealth[source] = entry
	}
	now := time.Now()
	if err == nil {
		entry.Success++
		entry.LastSuccess = now
		return
	}
	entry.Failure++
	entry.LastFailure = now
	msg := err.Error()
	if len(msg) > analyticsErrTruncateAt {
		msg = msg[:analyticsErrTruncateAt] + "…"
	}
	entry.LastError = msg
}

// ── Persistence ─────────────────────────────────────────────────

func analyticsPath() string {
	return filepath.Join(getDataDir(), analyticsFile)
}

func (a *Analytics) SaveToFile() error {
	a.mu.RLock()
	data, err := json.MarshalIndent(a, "", "  ")
	a.mu.RUnlock()
	if err != nil {
		return err
	}
	path := analyticsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (a *Analytics) LoadFromFile() error {
	data, err := os.ReadFile(analyticsPath())
	if err != nil {
		return err
	}
	var loaded Analytics
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// StartedAt stays as the current process's boot time.
	if loaded.PageViews != nil {
		a.PageViews = loaded.PageViews
	}
	a.Heatmap = loaded.Heatmap
	if loaded.APIHealth != nil {
		a.APIHealth = loaded.APIHealth
	}
	if loaded.Countries != nil {
		a.Countries = loaded.Countries
	}
	if loaded.CityHeatmap != nil {
		a.CityHeatmap = loaded.CityHeatmap
	}
	return nil
}

// loadGeoDB opens the MaxMind GeoLite2 Country DB if it exists. Missing DB
// is not an error — geo tracking simply stays disabled.
func loadGeoDB() {
	path := os.Getenv("GEOIP_DB")
	if path == "" {
		path = defaultGeoIPDB
	}
	db, err := geoip2.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[Analytics] geo disabled: %s not found (set GEOIP_DB or place the MMDB at the default path to enable)", path)
		} else {
			log.Printf("[Analytics] geo disabled: %v", err)
		}
		return
	}
	geoDB = db
	geoEnabled = true
	log.Printf("[Analytics] geo enabled: loaded %s", path)
}

func analyticsPersistLoop() {
	ticker := time.NewTicker(analyticsSaveInterval)
	defer ticker.Stop()
	for range ticker.C {
		if err := analytics.SaveToFile(); err != nil {
			log.Printf("[Analytics] save failed: %v", err)
		}
	}
}

// ── HTTP middleware ─────────────────────────────────────────────

// analyticsMiddleware wraps a handler so every request is checked against
// trackedPaths and counted if it matches.
func analyticsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var geo geoLookup
		if _, tracked := trackedPaths[r.URL.Path]; tracked && geoEnabled {
			geo = lookupGeo(clientIP(r))
		}
		analytics.RecordPageView(r.URL.Path, geo)
		next.ServeHTTP(w, r)
	})
}

// ── Response types ──────────────────────────────────────────────

type runtimeStats struct {
	Goroutines int     `json:"goroutines"`
	MemAllocMB float64 `json:"memAllocMB"`
	MemSysMB   float64 `json:"memSysMB"`
	GCCount    uint32  `json:"gcCount"`
	LastGCMs   float64 `json:"lastGCPauseMs"`
	GoVersion  string  `json:"goVersion"`
	NumCPU     int     `json:"numCPU"`
}

func currentRuntimeStats() runtimeStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	lastGCPauseNs := uint64(0)
	if m.NumGC > 0 {
		lastGCPauseNs = m.PauseNs[(m.NumGC+255)%256]
	}
	return runtimeStats{
		Goroutines: runtime.NumGoroutine(),
		MemAllocMB: float64(m.Alloc) / 1024 / 1024,
		MemSysMB:   float64(m.Sys) / 1024 / 1024,
		GCCount:    m.NumGC,
		LastGCMs:   float64(lastGCPauseNs) / 1e6,
		GoVersion:  runtime.Version(),
		NumCPU:     runtime.NumCPU(),
	}
}

type pageViewSeries struct {
	Kind   string           `json:"kind"`
	Points []pageViewPoint  `json:"points"`
}

type pageViewPoint struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

type countryCount struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

type heatPoint struct {
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
	Count int64   `json:"count"`
}

type analyticsOverview struct {
	StartedAt   time.Time                  `json:"startedAt"`
	UptimeSec   int64                      `json:"uptimeSec"`
	Series      []pageViewSeries           `json:"series"`
	Heatmap     [7][24]int64               `json:"heatmap"`
	APIHealth   map[string]*APIHealthEntry `json:"apiHealth"`
	Runtime     runtimeStats               `json:"runtime"`
	Countries   []countryCount             `json:"countries"`
	CityHeatmap []heatPoint                `json:"cityHeatmap"`
	GeoEnabled  bool                       `json:"geoEnabled"`
}

// ── Handlers ────────────────────────────────────────────────────

func handleAnalyticsOverview(w http.ResponseWriter, r *http.Request) {
	analytics.mu.RLock()
	// Build sorted per-kind series.
	kinds := make([]string, 0, len(analytics.PageViews))
	for k := range analytics.PageViews {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	series := make([]pageViewSeries, 0, len(kinds))
	for _, k := range kinds {
		byDate := analytics.PageViews[k]
		dates := make([]string, 0, len(byDate))
		for d := range byDate {
			dates = append(dates, d)
		}
		sort.Strings(dates)
		points := make([]pageViewPoint, len(dates))
		for i, d := range dates {
			points[i] = pageViewPoint{Date: d, Count: byDate[d]}
		}
		series = append(series, pageViewSeries{Kind: k, Points: points})
	}

	// Copy API health to release the lock quickly.
	health := make(map[string]*APIHealthEntry, len(analytics.APIHealth))
	for k, v := range analytics.APIHealth {
		cp := *v
		health[k] = &cp
	}
	heatmap := analytics.Heatmap
	startedAt := analytics.StartedAt

	// Build sorted country list (descending by count).
	countries := make([]countryCount, 0, len(analytics.Countries))
	for code, count := range analytics.Countries {
		countries = append(countries, countryCount{Code: code, Count: count})
	}

	// Expand the bucket map into flat (lat, lon, count) points for the map.
	points := make([]heatPoint, 0, len(analytics.CityHeatmap))
	for bucket, count := range analytics.CityHeatmap {
		parts := strings.SplitN(bucket, ",", 2)
		if len(parts) != 2 {
			continue
		}
		lat, errLat := strconv.ParseFloat(parts[0], 64)
		lon, errLon := strconv.ParseFloat(parts[1], 64)
		if errLat != nil || errLon != nil {
			continue
		}
		points = append(points, heatPoint{Lat: lat, Lon: lon, Count: count})
	}
	analytics.mu.RUnlock()

	sort.Slice(countries, func(i, j int) bool {
		return countries[i].Count > countries[j].Count
	})

	resp := analyticsOverview{
		StartedAt:   startedAt,
		UptimeSec:   int64(time.Since(startedAt).Seconds()),
		Series:      series,
		Heatmap:     heatmap,
		APIHealth:   health,
		Runtime:     currentRuntimeStats(),
		Countries:   countries,
		CityHeatmap: points,
		GeoEnabled:  geoEnabled,
	}
	writeJSON(w, resp)
}

// ── Init ────────────────────────────────────────────────────────

// InitAnalytics registers the /analytics routes, loads persisted data, and
// starts the periodic save loop.
func InitAnalytics(mux *http.ServeMux) {
	if err := analytics.LoadFromFile(); err != nil {
		if os.IsNotExist(err) {
			log.Println("[Analytics] no persisted data, starting fresh")
		} else {
			log.Printf("[Analytics] load failed: %v", err)
		}
	} else {
		log.Println("[Analytics] loaded persisted data")
	}

	loadGeoDB()

	go analyticsPersistLoop()

	mux.HandleFunc(analyticsBasePath+"/api/overview", handleAnalyticsOverview)

	staticSub, err := fs.Sub(analyticsFiles, "analytics")
	if err != nil {
		log.Fatalf("[Analytics] failed to load embedded static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle(analyticsBasePath+"/", http.StripPrefix(analyticsBasePath, fileServer))

	log.Printf("[Analytics] registered at %s/", analyticsBasePath)
}
