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

	// Find ALL matches and use the LAST one to get the most recent valid data,
	// skipping any asterisk placeholders from partially-updated reports.
	if temp, ok := lastMatch(tempRegex, text); ok {
		waterTemp.Set(temp)
		tempHistory.Add(temp)
		saveTempHistory()
		log.Printf("Updated water temperature: %.1f °F", temp)
	} else {
		log.Println("Warning: no valid water temperature found in report")
	}

	if level, ok := lastMatch(levelRegex, text); ok {
		waterLevel.Set(level)
		levelHistory.Add(level)
		saveLevelHistory()
		log.Printf("Updated water level: %.2f ft", level)
	} else {
		log.Println("Warning: no valid water level found in report")
	}

	return nil
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
		log.Printf("Warning: USGS backfill failed (fetch): %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Warning: USGS backfill failed (status %d)", resp.StatusCode)
		return
	}

	var usgs usgsResponse
	if err := json.NewDecoder(resp.Body).Decode(&usgs); err != nil {
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

	log.Printf("Backfilled %d days of water level history from USGS", count)
}

// --- Temperature Persistence ---

func tempHistoryPath() string {
	return filepath.Join(getDataDir(), tempHistoryFile)
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
	if err != nil {
		log.Printf("Warning: failed to fetch NWS forecast: %v", err)
	}

	hourly, err := fetchNWSForecast(nwsHourlyURL)
	if err != nil {
		log.Printf("Warning: failed to fetch NWS hourly: %v", err)
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
	if err := fetchAndParse(); err != nil {
		log.Printf("Error during initial scrape: %v", err)
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		log.Println("Running scheduled scrape...")
		if err := fetchAndParse(); err != nil {
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
	saveLevelHistory()

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

	// --- Poker equity calculator ---
	InitPoker(mux)

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
	if err := http.ListenAndServe(listenAddr, mux); err != nil {
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
	}{
		Forecast: weather.Forecast,
		Hourly:   weather.Hourly,
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
