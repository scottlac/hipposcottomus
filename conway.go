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
	"strconv"
	"time"
)

const (
	conwayBasePath = "/conway"
	// Full pool per Orange County (NAVD-88 since 2015): the south-lake
	// weir crest is 85.46 ft NAVD-88 (was 86.4 ft NGVD-29 before the
	// county's 2015 datum conversion). USGS station 02262800 reports
	// against this same datum, so deltas line up.
	conwayFullPool = 85.46

	// USGS 02262800 — Lake Conway at Pine Castle, FL. Directly on Lake
	// Conway (no proxy needed for water level), located on Big Lake Conway
	// in the four-lake chain (Big, Little, Lake Mary, Lake Gatlin). Like
	// most central FL inland-lake stations, it appears to report
	// elevation only — water temperature is estimated from air-temp; see
	// conway_temp_estimate.go.
	conwayUSGSSite = "02262800"
	// Cover every lake-elevation code USGS reports under so we work
	// regardless of which datum the station's canonical series uses.
	//   62614 — lake elevation NGVD-29
	//   62615 — lake elevation NAVD-88
	//   62616 — reservoir water surface elevation (newer code)
	//   00062 — elevation of reservoir water surface above datum
	conwayUSGSParams = "62614,62615,62616,00062"

	conwayUSGSIVURL = "https://waterservices.usgs.gov/nwis/iv/"
	conwayUSGSDVURL = "https://waterservices.usgs.gov/nwis/dv/"

	conwayPollInterval   = 15 * time.Minute
	conwayWeatherPollInt = 30 * time.Minute

	// Persistence files (sit alongside the other lakes' history files
	// on the same /data/ PVC).
	conwayTempHistoryFile  = "conway_temp_history.json"
	conwayLevelHistoryFile = "conway_level_history.json"

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

// conwayFetchIV pulls the most recent IV (instantaneous values) and updates
// the in-memory histories. The last good elevation reading wins regardless
// of which datum code it was reported under.
func conwayFetchIV() error {
	url := fmt.Sprintf("%s?format=json&sites=%s&parameterCd=%s&period=PT6H",
		conwayUSGSIVURL, conwayUSGSSite, conwayUSGSParams)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("fetching USGS IV: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("USGS IV returned status %d", resp.StatusCode)
	}

	var data usgsMultiResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("decoding USGS IV: %w", err)
	}

	var (
		gotLevel bool
		level    float64
	)
	for _, ts := range data.Value.TimeSeries {
		if len(ts.Variable.VariableCode) == 0 {
			continue
		}
		code := ts.Variable.VariableCode[0].Value
		var latest string
		for _, vals := range ts.Values {
			for _, v := range vals.Value {
				if v.Value == "" || v.Value == "-999999" {
					continue
				}
				latest = v.Value
			}
		}
		if latest == "" {
			continue
		}
		x, err := strconv.ParseFloat(latest, 64)
		if err != nil {
			continue
		}
		switch code {
		case "62614", "62615", "62616", "00062":
			level = x
			gotLevel = true
		}
	}

	if gotLevel {
		conwayLevelHistory.Add(level)
		log.Printf("[Conway] Updated water level: %.2f ft", level)
	} else {
		log.Println("[Conway] No water level reading in IV response")
	}
	return nil
}

// conwayBackfillFromUSGS pulls up to 5 years of daily elevation values
// from the Minnehaha gauge. Water temperature is estimated separately via
// Open-Meteo (see conway_temp_estimate.go).
func conwayBackfillFromUSGS() {
	end := time.Now().Format("2006-01-02")
	start := time.Now().AddDate(-5, 0, 0).Format("2006-01-02")
	url := fmt.Sprintf("%s?format=json&sites=%s&startDT=%s&endDT=%s&parameterCd=%s&siteStatus=all",
		conwayUSGSDVURL, conwayUSGSSite, start, end, conwayUSGSParams)
	log.Printf("[Conway] Backfilling level history from USGS (%s to %s)...", start, end)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		TrackAPICall("USGS-Conway-DV", err)
		log.Printf("[Conway] Warning: USGS backfill failed (fetch): %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("status %d", resp.StatusCode)
		TrackAPICall("USGS-Conway-DV", statusErr)
		log.Printf("[Conway] Warning: USGS backfill failed (%v)", statusErr)
		return
	}

	var data usgsMultiResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		TrackAPICall("USGS-Conway-DV", err)
		log.Printf("[Conway] Warning: USGS backfill failed (decode): %v", err)
		return
	}

	var levelCount int
	for _, ts := range data.Value.TimeSeries {
		if len(ts.Variable.VariableCode) == 0 {
			continue
		}
		code := ts.Variable.VariableCode[0].Value
		for _, vals := range ts.Values {
			for _, v := range vals.Value {
				if len(v.DateTime) < 10 || v.Value == "" || v.Value == "-999999" {
					continue
				}
				date := v.DateTime[:10]
				x, err := strconv.ParseFloat(v.Value, 64)
				if err != nil {
					continue
				}
				switch code {
				case "62614", "62615", "62616", "00062":
					conwayLevelHistory.AddWithDate(date, x)
					levelCount++
				}
			}
		}
	}

	TrackAPICall("USGS-Conway-DV", nil)
	log.Printf("[Conway] Backfilled %d level points", levelCount)
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

func conwayScrapeLoop() {
	log.Println("[Conway] Running initial USGS IV scrape...")
	err := conwayFetchIV()
	TrackAPICall("USGS-Conway-IV", err)
	if err != nil {
		log.Printf("[Conway] initial IV scrape error: %v", err)
	} else {
		saveConwayHistories()
	}

	ticker := time.NewTicker(conwayPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		err := conwayFetchIV()
		TrackAPICall("USGS-Conway-IV", err)
		if err != nil {
			log.Printf("[Conway] scheduled IV scrape error: %v", err)
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
	conwayBackfillFromUSGS()
	conwayBackfillTempEstimate()
	conwayLevelHistory.Sort()
	conwayTempHistory.Sort()
	saveConwayHistories()

	go conwayScrapeLoop()
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
