package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	conwayBasePath = "/conway"
	// Full pool per Orange County (NAVD-88 since 2015): the south-lake
	// weir crest is 85.46 ft NAVD-88. The Water Atlas station reports
	// against the same datum.
	conwayFullPool = 85.46

	// Orange County operates its own data-logger station (StationID
	// BCWP, "Conway Station") at 28.461746°N, -81.342009°W — directly
	// on Lake Conway, ~hourly cadence, NAVD-88. Replaces the earlier
	// USGS 02262800 approach: that station is manually-read monthly by
	// the Lake Conway Water and Navigation Control District and didn't
	// publish anything useful through either USGS IV or DV.
	conwayWADataSource         = "ORANGECO_DATA_LOGGERS"
	conwayWAStation            = "BCWP"
	conwayLevelRefreshPeriod   = 6 * time.Hour
	conwayWeatherPollInt       = 30 * time.Minute

	// Persistence files (sit alongside the other lakes' history files
	// on the same /data/ PVC). Level file renamed at the Water-Atlas
	// switchover so old (empty/USGS) state doesn't survive.
	conwayTempHistoryFile  = "conway_temp_history.json"
	conwayLevelHistoryFile = "conway_level_history_wa.json"

	// Lake Conway centroid (28.47°N, -81.35°W, south Orlando — Pine
	// Castle / Belle Isle / Edgewood) for NWS grid discovery. NWS
	// Melbourne covers this area.
	conwayPointsURL = "https://api.weather.gov/points/28.47,-81.35"
)

//go:embed conway
var conwayFiles embed.FS

// Histories and weather data live as globals, same shape as Jordan Lake.
var (
	conwayTempHistory  = &History{}
	conwayLevelHistory = &History{}
	conwayWeather      = &WeatherData{}

	conwayForecastURL string
	conwayHourlyURL   string
)

// usgsMultiResp and celsiusToFahrenheit are shared with gaston.go.

// conwayRefreshLevel pulls daysBack days of water-level data from the
// Water Atlas API (Orange County data loggers, Conway Station BCWP) and
// folds new dates into history. AddWithDate skips duplicates so calling
// this repeatedly is safe. Outliers are dropped — sensor noise on this
// station occasionally publishes physically-implausible values.
func conwayRefreshLevel(daysBack int) error {
	url := fmt.Sprintf("%s/DataMapper/Agency/%s/Station/%s/Hydrology/Levels/GraphData?numberOfDays=%d",
		waterAtlasBaseURL, conwayWADataSource, conwayWAStation, daysBack)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("fetching Water Atlas levels: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Water Atlas returned status %d", resp.StatusCode)
	}

	var points []waterAtlasLevelPoint
	if err := json.NewDecoder(resp.Body).Decode(&points); err != nil {
		return fmt.Errorf("decoding Water Atlas response: %w", err)
	}

	var added, dropped int
	for _, p := range points {
		if p.ResultValue < 70 || p.ResultValue > 95 {
			dropped++
			continue
		}
		date := p.SampleDate
		if i := strings.Index(date, "T"); i > 0 {
			date = date[:i]
		}
		if len(date) < 10 {
			continue
		}
		conwayLevelHistory.AddWithDate(date, p.ResultValue)
		added++
	}
	conwayLevelHistory.Sort()
	log.Printf("[Conway] Water Atlas level refresh: %d points added, %d dropped as outliers (window=%d days)",
		added, dropped, daysBack)
	return nil
}

// conwayBackfillLevel pulls 5 years at startup (will return whatever's
// available — the Orange County data logger only started publishing in
// April 2026, so for now this realistically returns a few weeks).
func conwayBackfillLevel() {
	log.Println("[Conway] Backfilling level history from Water Atlas (5 years)...")
	err := conwayRefreshLevel(5 * 365)
	TrackAPICall("WaterAtlas-Conway", err)
	if err != nil {
		log.Printf("[Conway] Warning: backfill failed: %v", err)
	}
}

// ── NWS weather ─────────────────────────────────────────────────

func conwayDiscoverNWSGrid() {
	req, _ := http.NewRequest("GET", conwayPointsURL, nil)
	req.Header.Set("User-Agent", "(lake-conway-dashboard, contact@hipposcottomus.com)")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Conway] NWS points lookup failed: %v", err)
		return
	}
	defer resp.Body.Close()
	var pts struct {
		Properties struct {
			Forecast       string `json:"forecast"`
			ForecastHourly string `json:"forecastHourly"`
		} `json:"properties"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pts); err != nil {
		log.Printf("[Conway] NWS points decode failed: %v", err)
		return
	}
	conwayForecastURL = pts.Properties.Forecast
	conwayHourlyURL = pts.Properties.ForecastHourly
	log.Printf("[Conway] NWS grid discovered: forecast=%s hourly=%s", conwayForecastURL, conwayHourlyURL)
}

func updateConwayWeather() {
	if conwayForecastURL == "" || conwayHourlyURL == "" {
		conwayDiscoverNWSGrid()
		if conwayForecastURL == "" {
			log.Println("[Conway] NWS grid not available, skipping weather update")
			return
		}
	}

	forecast, err := fetchNWSForecast(conwayForecastURL)
	TrackAPICall("NWS-Conway", err)
	if err != nil {
		log.Printf("[Conway] forecast error: %v", err)
	}
	hourly, errH := fetchNWSForecast(conwayHourlyURL)
	if errH != nil {
		log.Printf("[Conway] hourly error: %v", errH)
	}

	uv, uvErr := fetchUVIndex(28.47, -81.35) // Lake Conway centroid (south Orlando, FL)
	TrackAPICall("OpenMeteo-UV", uvErr)
	if uvErr != nil {
		log.Printf("[Conway] UV index error: %v", uvErr)
	}

	conwayWeather.mu.Lock()
	defer conwayWeather.mu.Unlock()
	if forecast != nil {
		conwayWeather.Forecast = forecast
		log.Printf("[Conway] Updated forecast: %d periods", len(forecast))
	}
	if hourly != nil {
		if len(hourly) > 24 {
			hourly = hourly[:24]
		}
		conwayWeather.Hourly = hourly
		log.Printf("[Conway] Updated hourly forecast: %d hours", len(hourly))
	}
	if uv != nil {
		conwayWeather.UV = uv
		log.Printf("[Conway] Updated UV index: %.1f", uv.Current)
	}
}

// ── Persistence ─────────────────────────────────────────────────

func conwayTempPath() string  { return filepath.Join(getDataDir(), conwayTempHistoryFile) }
func conwayLevelPath() string { return filepath.Join(getDataDir(), conwayLevelHistoryFile) }

func loadConwayHistories() {
	if err := conwayTempHistory.LoadFromFile(conwayTempPath()); err != nil {
		if os.IsNotExist(err) {
			log.Println("[Conway] no persisted temp history, starting fresh")
		} else {
			log.Printf("[Conway] failed to load temp history: %v", err)
		}
	} else {
		log.Printf("[Conway] loaded %d days of temp history", conwayTempHistory.Len())
	}
	if err := conwayLevelHistory.LoadFromFile(conwayLevelPath()); err != nil {
		if os.IsNotExist(err) {
			log.Println("[Conway] no persisted level history, starting fresh")
		} else {
			log.Printf("[Conway] failed to load level history: %v", err)
		}
	} else {
		log.Printf("[Conway] loaded %d days of level history", conwayLevelHistory.Len())
	}
}

func saveConwayHistories() {
	if err := conwayTempHistory.SaveToFile(conwayTempPath()); err != nil {
		log.Printf("[Conway] save temp history failed: %v", err)
	}
	if err := conwayLevelHistory.SaveToFile(conwayLevelPath()); err != nil {
		log.Printf("[Conway] save level history failed: %v", err)
	}
}

// ── Loops ───────────────────────────────────────────────────────

// conwayLevelLoop polls the Water Atlas every 6 hours with a 120-day
// look-back. The Orange County data logger updates ~hourly but our
// dashboard refresh cadence is much coarser, and a 120-day window is
// plenty to cover any retroactive corrections.
func conwayLevelLoop() {
	ticker := time.NewTicker(conwayLevelRefreshPeriod)
	defer ticker.Stop()
	for range ticker.C {
		err := conwayRefreshLevel(120)
		TrackAPICall("WaterAtlas-Conway", err)
		if err != nil {
			log.Printf("[Conway] level refresh error: %v", err)
			continue
		}
		saveConwayHistories()
	}
}

func conwayWeatherLoop() {
	updateConwayWeather()
	ticker := time.NewTicker(conwayWeatherPollInt)
	defer ticker.Stop()
	for range ticker.C {
		updateConwayWeather()
	}
}

// ── HTTP handlers ───────────────────────────────────────────────

type conwayCurrentResponse struct {
	Temperature *DataPoint `json:"temperature"`
	WaterLevel  *DataPoint `json:"waterLevel"`
}

func handleConwayCurrent(w http.ResponseWriter, r *http.Request) {
	resp := conwayCurrentResponse{}
	if pt, ok := conwayTempHistory.Latest(); ok {
		resp.Temperature = &pt
	}
	if pt, ok := conwayLevelHistory.Latest(); ok {
		resp.WaterLevel = &pt
	}
	writeJSON(w, resp)
}

type conwayHistoryResponse struct {
	Temperature []DataPoint `json:"temperature"`
	WaterLevel  []DataPoint `json:"waterLevel"`
}

func handleConwayHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, conwayHistoryResponse{
		Temperature: conwayTempHistory.All(),
		WaterLevel:  conwayLevelHistory.All(),
	})
}

func handleConwayWeather(w http.ResponseWriter, r *http.Request) {
	conwayWeather.mu.RLock()
	defer conwayWeather.mu.RUnlock()
	writeJSON(w, struct {
		Forecast []WeatherPeriod `json:"forecast"`
		Hourly   []WeatherPeriod `json:"hourly"`
		UV       *UVData         `json:"uv,omitempty"`
	}{
		Forecast: conwayWeather.Forecast,
		Hourly:   conwayWeather.Hourly,
		UV:       conwayWeather.UV,
	})
}

// ── Init ────────────────────────────────────────────────────────

// InitConway wires up the Lake Conway dashboard: persistence, backfill,
// background scrapers, and HTTP routes.
func InitConway(mux *http.ServeMux) {
	loadConwayHistories()
	conwayBackfillLevel()
	conwayBackfillTempEstimate()
	conwayLevelHistory.Sort()
	conwayTempHistory.Sort()
	saveConwayHistories()

	go conwayLevelLoop()
	go conwayWeatherLoop()
	go conwayTempEstimateLoop()
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			saveConwayHistories()
		}
	}()

	mux.HandleFunc(conwayBasePath+"/api/current", handleConwayCurrent)
	mux.HandleFunc(conwayBasePath+"/api/history", handleConwayHistory)
	mux.HandleFunc(conwayBasePath+"/api/weather", handleConwayWeather)

	staticSub, err := fs.Sub(conwayFiles, "conway")
	if err != nil {
		log.Fatalf("[Conway] failed to load embedded static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle(conwayBasePath+"/", http.StripPrefix(conwayBasePath, fileServer))

	log.Printf("[Conway] Lake Conway dashboard registered at %s/", conwayBasePath)
}
