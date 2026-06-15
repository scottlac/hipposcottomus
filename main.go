package main

import (
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	dataURL       = "https://epec.saw.usace.army.mil/bejrept.txt"
	pollInterval  = 15 * time.Minute
	listenAddr    = ":8080"
	basePath      = "/lakedashboard"
	fullPoolLevel = 216.0 // full pool elevation in feet

	// Jordan Lake centroid for UV lookups (lat/lon)
	jordanLat = 35.73
	jordanLon = -79.02

	// USGS API for historical water level backfill
	usgsAPIURL   = "https://waterservices.usgs.gov/nwis/dv/"
	usgsSiteID   = "02098197" // B. Everett Jordan Lake at Dam
	usgsParamCD  = "62614"    // Lake surface elevation above NGVD 1929

	// Persistence
	defaultDataDir   = "/data"
	tempHistoryFile  = "temp_history.json"
	levelHistoryFile = "level_history.json"
	saveInterval     = 1 * time.Hour

	// NWS Weather API (no key required)
	nwsForecastURL = "https://api.weather.gov/gridpoints/RAH/60,52/forecast"
	nwsHourlyURL   = "https://api.weather.gov/gridpoints/RAH/60,52/forecast/hourly"
	weatherPollInt = 30 * time.Minute
)

// getDataDir returns the data directory, honoring the DATA_DIR env var.
func getDataDir() string {
	if d := os.Getenv("DATA_DIR"); d != "" {
		return d
	}
	return defaultDataDir
}

//go:embed static home poker
var content embed.FS

// DataPoint holds a single date-keyed reading (one per day).
type DataPoint struct {
	Date  string  `json:"date"`  // "2026-04-08"
	Value float64 `json:"value"`
}

// History is a thread-safe store of daily data points.
// Only the latest value for each date is kept.
type History struct {
	mu     sync.RWMutex
	points []DataPoint
}

func (h *History) Add(val float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	today := time.Now().Format("2006-01-02")
	// If the last entry is for today, update it in place
	if len(h.points) > 0 && h.points[len(h.points)-1].Date == today {
		h.points[len(h.points)-1].Value = val
	} else {
		h.points = append(h.points, DataPoint{Date: today, Value: val})
	}
}

func (h *History) Latest() (DataPoint, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.points) == 0 {
		return DataPoint{}, false
	}
	return h.points[len(h.points)-1], true
}

func (h *History) All() []DataPoint {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]DataPoint, len(h.points))
	copy(out, h.points)
	return out
}

// AddWithDate inserts a data point for a specific date.
// Used for historical backfill. Skips if the date already exists.
func (h *History) AddWithDate(date string, val float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range h.points {
		if p.Date == date {
			return
		}
	}
	h.points = append(h.points, DataPoint{Date: date, Value: val})
}

// SetForDate upserts a value for the given ISO date, returning true if
// anything actually changed (new entry, or existing entry's value was
// different). The boolean lets callers skip a disk write when the
// reading is unchanged, which matters for data sources that re-publish
// the same value across weekends and holidays (USACE Jordan Lake).
func (h *History) SetForDate(date string, val float64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.points {
		if h.points[i].Date == date {
			if h.points[i].Value == val {
				return false
			}
			h.points[i].Value = val
			return true
		}
	}
	h.points = append(h.points, DataPoint{Date: date, Value: val})
	return true
}

// RemoveConsecutiveDuplicates drops entries whose value exactly matches
// the preceding entry's value once history is sorted by date. Idempotent:
// safe to run on every startup. Used to scrub phantom Saturday/Sunday
// entries left over from before SetForDate was wired up. Returns the
// number of points dropped.
func (h *History) RemoveConsecutiveDuplicates() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.points) < 2 {
		return 0
	}
	out := h.points[:1]
	dropped := 0
	for i := 1; i < len(h.points); i++ {
		if h.points[i].Value == out[len(out)-1].Value {
			dropped++
			continue
		}
		out = append(out, h.points[i])
	}
	h.points = out
	return dropped
}

// Sort sorts the data points by date ascending.
func (h *History) Sort() {
	h.mu.Lock()
	defer h.mu.Unlock()
	sort.Slice(h.points, func(i, j int) bool {
		return h.points[i].Date < h.points[j].Date
	})
}

// Len returns the number of stored data points.
func (h *History) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.points)
}

// SaveToFile writes the history to a JSON file.
func (h *History) SaveToFile(path string) error {
	h.mu.RLock()
	data, err := json.Marshal(h.points)
	h.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshalling history: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}
	return nil
}

// LoadFromFile reads history from a JSON file.
func (h *History) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var points []DataPoint
	if err := json.Unmarshal(data, &points); err != nil {
		return fmt.Errorf("unmarshalling history: %w", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.points = points
	return nil
}

var (
	tempHistory  = &History{}
	levelHistory = &History{}
)

var (
	waterTemp = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "jordan_lake_water_temperature_fahrenheit",
		Help: "Current water temperature of Jordan Lake in degrees Fahrenheit.",
	})
	waterLevel = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "jordan_lake_water_level_feet",
		Help: "Current water level (midnight elevation) of Jordan Lake in feet.",
	})

	tempRegex  = regexp.MustCompile(`Lake temperature, degrees Fahrenheit=\s*([0-9.]+)`)
	levelRegex = regexp.MustCompile(`Midnight\s+Elevation\s+Today\s*=\s*([0-9.]+)`)
	// USACE reports headline each day as "MONDAY 9 JUN 2026" (or longer
	// month names — JUNE etc). The LAST occurrence in the report text is
	// the most recent measurement date; we tag readings with that so
	// weekend re-publishes of Friday's data don't manufacture phantom
	// Saturday/Sunday entries.
	reportDateRegex = regexp.MustCompile(`(?i)(MON|TUE|WED|THU|FRI|SAT|SUN)[A-Z]*\s+(\d{1,2})\s+(JAN|FEB|MAR|APR|MAY|JUN|JUL|AUG|SEP|OCT|NOV|DEC)[A-Z]*\s+(\d{4})`)
)

func init() {
	prometheus.MustRegister(waterTemp)
	prometheus.MustRegister(waterLevel)
}

// fetchAndParse downloads the report and returns the last matching
// temperature and water-level values found in the text.
func fetchAndParse() error {
	// The USACE site uses a government-issued certificate that may not be in
	// all default CA bundles (e.g. Alpine's). We skip verification for this
	// single, well-known endpoint.
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	resp, err := client.Get(dataURL)
	if err != nil {
		return fmt.Errorf("fetching data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading body: %w", err)
	}

	text := string(body)

	// Parse the report's most-recent measurement date so weekend
	// re-publishes of Friday's data don't get tagged as Sat/Sun "today".
	// If we can't parse a date for some reason, fall back to today.
	reportDate, dateOK := parseReportDate(text)
	if !dateOK {
		log.Println("Warning: no parseable report date in USACE text; falling back to today")
		reportDate = time.Now().Format("2006-01-02")
	}

	// Find ALL matches and use the LAST one to get the most recent valid data,
	// skipping any asterisk placeholders from partially-updated reports.
	if temp, ok := lastMatch(tempRegex, text); ok {
		waterTemp.Set(temp)
		if tempHistory.SetForDate(reportDate, temp) {
			saveTempHistory()
			log.Printf("Updated water temperature: %.1f °F (%s)", temp, reportDate)
		}
	} else {
		log.Println("Warning: no valid water temperature found in report")
	}

	if level, ok := lastMatch(levelRegex, text); ok {
		waterLevel.Set(level)
		if levelHistory.SetForDate(reportDate, level) {
			saveLevelHistory()
			log.Printf("Updated water level: %.2f ft (%s)", level, reportDate)
		}
	} else {
		log.Println("Warning: no valid water level found in report")
	}

	return nil
}

// parseReportDate finds the LAST date headline in the USACE report
// (format e.g. "FRIDAY 6 JUN 2026") and returns it as ISO YYYY-MM-DD.
// Returns ok=false when no recognisable headline is present.
func parseReportDate(text string) (string, bool) {
	matches := reportDateRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return "", false
	}
	last := matches[len(matches)-1]
	// last[2]=day, last[3]=month abbrev, last[4]=year
	monthByAbbrev := map[string]int{
		"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
		"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
	}
	m, ok := monthByAbbrev[strings.ToUpper(last[3])[:3]]
	if !ok {
		return "", false
	}
	day, err := strconv.Atoi(last[2])
	if err != nil || day < 1 || day > 31 {
		return "", false
	}
	year, err := strconv.Atoi(last[4])
	if err != nil || year < 2000 || year > 2100 {
		return "", false
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, m, day), true
}

// lastMatch finds all regex matches in text and returns the parsed float
// from the last match's first capture group.
func lastMatch(re *regexp.Regexp, text string) (float64, bool) {
	matches := re.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return 0, false
	}
	last := matches[len(matches)-1]
	val, err := strconv.ParseFloat(last[1], 64)
	if err != nil {
		return 0, false
	}
	return val, true
}

// --- USGS Historical Backfill ---

// usgsResponse models the USGS Water Services JSON response.
type usgsResponse struct {
	Value struct {
		TimeSeries []struct {
			Values []struct {
				Value []struct {
					DateTime string `json:"dateTime"`
					Value    string `json:"value"`
				} `json:"value"`
			} `json:"values"`
		} `json:"timeSeries"`
	} `json:"value"`
}

// backfillFromUSGS fetches historical daily water level data from the USGS
// API and pre-populates levelHistory. Fetches up to 5 years of data.
func backfillFromUSGS() {
	end := time.Now().Format("2006-01-02")
	start := time.Now().AddDate(-5, 0, 0).Format("2006-01-02")

	url := fmt.Sprintf("%s?format=json&sites=%s&startDT=%s&endDT=%s&parameterCd=%s&siteStatus=all",
		usgsAPIURL, usgsSiteID, start, end, usgsParamCD)

	log.Printf("Backfilling water level from USGS (%s to %s)...", start, end)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		TrackAPICall("USGS", err)
		log.Printf("Warning: USGS backfill failed (fetch): %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("status %d", resp.StatusCode)
		TrackAPICall("USGS", statusErr)
		log.Printf("Warning: USGS backfill failed (%v)", statusErr)
		return
	}

	var usgs usgsResponse
	if err := json.NewDecoder(resp.Body).Decode(&usgs); err != nil {
		TrackAPICall("USGS", err)
		log.Printf("Warning: USGS backfill failed (decode): %v", err)
		return
	}

	count := 0
	for _, ts := range usgs.Value.TimeSeries {
		for _, vals := range ts.Values {
			for _, v := range vals.Value {
				elevation, err := strconv.ParseFloat(v.Value, 64)
				if err != nil {
					continue
				}
				date := v.DateTime[:10] // "2026-04-08T00:00:00.000" → "2026-04-08"
				levelHistory.AddWithDate(date, elevation)
				count++
			}
		}
	}

	TrackAPICall("USGS", nil)
	log.Printf("Backfilled %d days of water level history from USGS", count)
}

// --- Temperature Persistence ---

func tempHistoryPath() string {
	return filepath.Join(getDataDir(), tempHistoryFile)
}

// snapshotPreScrub copies src to "<src-without-ext>.pre-scrub<ext>" if
// (a) src exists and (b) the backup doesn't already exist. Used to
// preserve the pre-dedup history exactly once. Idempotent on later
// boots — once the backup is in place we never touch it again.
func snapshotPreScrub(src string) error {
	if _, err := os.Stat(src); err != nil {
		// Nothing to back up (first boot before any persisted history)
		// — quietly skip.
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	ext := filepath.Ext(src)
	dst := src[:len(src)-len(ext)] + ".pre-scrub" + ext
	if _, err := os.Stat(dst); err == nil {
		// Backup already in place — nothing to do.
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	log.Printf("Snapshotted pre-scrub history to %s", dst)
	return nil
}

// loadTempHistory loads persisted temperature data from disk.
func loadTempHistory() {
	path := tempHistoryPath()
	if err := tempHistory.LoadFromFile(path); err != nil {
		if os.IsNotExist(err) {
			log.Println("No persisted temperature history found, starting fresh")
		} else {
			log.Printf("Warning: failed to load temperature history: %v", err)
		}
		return
	}
	log.Printf("Loaded %d days of temperature history from disk", tempHistory.Len())
}

// saveTempHistory saves temperature data to disk.
func saveTempHistory() {
	path := tempHistoryPath()
	if err := tempHistory.SaveToFile(path); err != nil {
		log.Printf("Warning: failed to save temperature history: %v", err)
		return
	}
	log.Printf("Saved %d days of temperature history to disk", tempHistory.Len())
}

// tempPersistLoop periodically saves temperature and level history to disk.
func tempPersistLoop() {
	ticker := time.NewTicker(saveInterval)
	defer ticker.Stop()
	for range ticker.C {
		saveTempHistory()
		saveLevelHistory()
	}
}

// --- Water Level Persistence ---

func levelHistoryPath() string {
	return filepath.Join(getDataDir(), levelHistoryFile)
}

func loadLevelHistory() {
	path := levelHistoryPath()
	if err := levelHistory.LoadFromFile(path); err != nil {
		if os.IsNotExist(err) {
			log.Println("No persisted water level history found, starting fresh")
		} else {
			log.Printf("Warning: failed to load water level history: %v", err)
		}
		return
	}
	log.Printf("Loaded %d days of water level history from disk", levelHistory.Len())
}

func saveLevelHistory() {
	path := levelHistoryPath()
	if err := levelHistory.SaveToFile(path); err != nil {
		log.Printf("Warning: failed to save water level history: %v", err)
		return
	}
	log.Printf("Saved %d days of water level history to disk", levelHistory.Len())
}

// --- NWS Weather ---

// WeatherPeriod is a single forecast period from the NWS API.
type WeatherPeriod struct {
	Name          string `json:"name"`
	StartTime     string `json:"startTime"`
	Temperature   int    `json:"temperature"`
	WindSpeed     string `json:"windSpeed"`
	WindDirection string `json:"windDirection"`
	ShortForecast string `json:"shortForecast"`
	IsDaytime     bool   `json:"isDaytime"`
}

// WeatherData holds the latest weather info.
type WeatherData struct {
	mu       sync.RWMutex
	Forecast []WeatherPeriod `json:"forecast"` // day/night periods (next 7 days)
	Hourly   []WeatherPeriod `json:"hourly"`   // hourly (next 24h)
	UV       *UVData         `json:"uv,omitempty"`
}

var weather = &WeatherData{}

// nwsAPIResponse models the NWS forecast JSON.
type nwsAPIResponse struct {
	Properties struct {
		Periods []struct {
			Name            string `json:"name"`
			StartTime       string `json:"startTime"`
			Temperature     int    `json:"temperature"`
			TemperatureUnit string `json:"temperatureUnit"`
			WindSpeed       string `json:"windSpeed"`
			WindDirection   string `json:"windDirection"`
			ShortForecast   string `json:"shortForecast"`
			IsDaytime       bool   `json:"isDaytime"`
		} `json:"periods"`
	} `json:"properties"`
}

func fetchNWSForecast(url string) ([]WeatherPeriod, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "(jordan-lake-dashboard, contact@hipposcottomus.com)")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching NWS data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("NWS returned status %d", resp.StatusCode)
	}

	var nws nwsAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&nws); err != nil {
		return nil, fmt.Errorf("decoding NWS response: %w", err)
	}

	periods := make([]WeatherPeriod, len(nws.Properties.Periods))
	for i, p := range nws.Properties.Periods {
		periods[i] = WeatherPeriod{
			Name:          p.Name,
			StartTime:     p.StartTime,
			Temperature:   p.Temperature,
			WindSpeed:     p.WindSpeed,
			WindDirection: p.WindDirection,
			ShortForecast: p.ShortForecast,
			IsDaytime:     p.IsDaytime,
		}
	}
	return periods, nil
}

func updateWeather() {
	forecast, err := fetchNWSForecast(nwsForecastURL)
	TrackAPICall("NWS-Raleigh", err)
	if err != nil {
		log.Printf("Warning: failed to fetch NWS forecast: %v", err)
	}

	hourly, err := fetchNWSForecast(nwsHourlyURL)
	if err != nil {
		log.Printf("Warning: failed to fetch NWS hourly: %v", err)
	}

	uv, uvErr := fetchUVIndex(jordanLat, jordanLon)
	TrackAPICall("OpenMeteo-UV", uvErr)
	if uvErr != nil {
		log.Printf("Warning: failed to fetch UV index: %v", uvErr)
	}

	weather.mu.Lock()
	defer weather.mu.Unlock()
	if forecast != nil {
		weather.Forecast = forecast
		log.Printf("Updated forecast: %d periods", len(forecast))
	}
	if hourly != nil {
		// Keep only next 24 hours
		if len(hourly) > 24 {
			hourly = hourly[:24]
		}
		weather.Hourly = hourly
		log.Printf("Updated hourly forecast: %d hours", len(hourly))
	}
	if uv != nil {
		weather.UV = uv
		log.Printf("Updated UV index: %.1f", uv.Current)
	}
}

func weatherLoop() {
	updateWeather()
	ticker := time.NewTicker(weatherPollInt)
	defer ticker.Stop()
	for range ticker.C {
		updateWeather()
	}
}

// scrapeLoop runs the scraper immediately, then every pollInterval.
func scrapeLoop() {
	log.Println("Running initial scrape...")
	err := fetchAndParse()
	TrackAPICall("USACE", err)
	if err != nil {
		log.Printf("Error during initial scrape: %v", err)
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		log.Println("Running scheduled scrape...")
		err := fetchAndParse()
		TrackAPICall("USACE", err)
		if err != nil {
			log.Printf("Error during scrape: %v", err)
		}
	}
}

func main() {
	// --- Load persisted data & backfill ---
	loadTempHistory()
	loadLevelHistory()
	backfillFromUSGS()
	levelHistory.Sort()
	tempHistory.Sort()
	// Scrub leftover phantom entries from before SetForDate was wired up:
	// USACE doesn't update on weekends/holidays, so the old scrapeLoop
	// would stamp Friday's value as Saturday's and Sunday's. The scrub
	// itself is idempotent. Before the FIRST scrub we copy each history
	// file to a `.pre-scrub.json` companion so we can recover if the
	// dedup decides to drop something we didn't expect. The backup is
	// created at most once — once it exists, subsequent boots skip it
	// (the scrub is a no-op anyway).
	if err := snapshotPreScrub(tempHistoryPath()); err != nil {
		log.Printf("[Jordan] could not snapshot temp history before scrub: %v", err)
	}
	if err := snapshotPreScrub(levelHistoryPath()); err != nil {
		log.Printf("[Jordan] could not snapshot level history before scrub: %v", err)
	}
	if dropped := tempHistory.RemoveConsecutiveDuplicates(); dropped > 0 {
		log.Printf("[Jordan] Dropped %d consecutive-duplicate temp points (weekend phantoms)", dropped)
	}
	if dropped := levelHistory.RemoveConsecutiveDuplicates(); dropped > 0 {
		log.Printf("[Jordan] Dropped %d consecutive-duplicate level points (weekend phantoms)", dropped)
	}
	saveLevelHistory()
	saveTempHistory()

	// --- Start background loops ---
	go scrapeLoop()
	go tempPersistLoop()
	go weatherLoop()

	mux := http.NewServeMux()

	// --- API endpoints (under basePath) ---
	mux.HandleFunc(basePath+"/api/current", handleCurrent)
	mux.HandleFunc(basePath+"/api/history", handleHistory)
	mux.HandleFunc(basePath+"/api/weather", handleWeather)

	// --- Ft Lauderdale dashboard ---
	InitFTL(mux)

	// --- Lake Gaston dashboard ---
	InitGaston(mux)

	// --- Lake Minneola dashboard ---
	InitMinneola(mux)

	// --- Lake Conway dashboard ---
	InitConway(mux)

	// --- Poker equity calculator ---
	InitPoker(mux)

	// --- Night Sky astronomy dashboard ---
	InitAstro(mux)

	// --- Analytics page ---
	InitAnalytics(mux)

	// --- AI-generated boating blurb for Jordan Lake ---
	InitBlurb(mux)

	// --- Generic Open Graph preview image at /og.png ---
	mux.HandleFunc("/og.png", handleSiteOGImage)

	// --- App icons rendered on demand ---
	mux.HandleFunc("/apple-touch-icon.png", handleAppleTouchIcon)
	mux.HandleFunc("/icon-512.png", handleIcon512)

	// --- Prometheus metrics (stays at root for scraping) ---
	mux.Handle("/metrics", promhttp.Handler())

	// --- Health check (for uptime monitoring & probes) ---
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	// --- Dashboard (embedded static files under basePath) ---
	staticSub, err := fs.Sub(content, "static")
	if err != nil {
		log.Fatalf("Failed to load embedded static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle(basePath+"/", http.StripPrefix(basePath, fileServer))

	// Serve homepage at root
	homeSub, _ := fs.Sub(content, "home")
	homeServer := http.FileServer(http.FS(homeSub))
	mux.Handle("/", homeServer)

	log.Printf("Serving dashboard on %s%s/", listenAddr, basePath)
	log.Printf("Prometheus metrics on %s/metrics", listenAddr)
	if err := http.ListenAndServe(listenAddr, analyticsMiddleware(mux)); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// --- API Handlers ---

type currentResponse struct {
	Temperature *DataPoint `json:"temperature"`
	WaterLevel  *DataPoint `json:"waterLevel"`
}

func handleCurrent(w http.ResponseWriter, r *http.Request) {
	resp := currentResponse{}
	if pt, ok := tempHistory.Latest(); ok {
		resp.Temperature = &pt
	}
	if pt, ok := levelHistory.Latest(); ok {
		resp.WaterLevel = &pt
	}
	writeJSON(w, resp)
}

type historyResponse struct {
	Temperature []DataPoint `json:"temperature"`
	WaterLevel  []DataPoint `json:"waterLevel"`
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	resp := historyResponse{
		Temperature: tempHistory.All(),
		WaterLevel:  levelHistory.All(),
	}
	writeJSON(w, resp)
}

func handleWeather(w http.ResponseWriter, r *http.Request) {
	weather.mu.RLock()
	resp := struct {
		Forecast []WeatherPeriod `json:"forecast"`
		Hourly   []WeatherPeriod `json:"hourly"`
		UV       *UVData         `json:"uv,omitempty"`
	}{
		Forecast: weather.Forecast,
		Hourly:   weather.Hourly,
		UV:       weather.UV,
	}
	weather.mu.RUnlock()
	writeJSON(w, resp)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
