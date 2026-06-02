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
	// Cover every lake-elevation code USGS reports under, plus 00065
	// (gage height, feet) which some lake gauges use instead. Whichever
	// the station's canonical series is gets used; the rest come back
	// as empty time series and are silently ignored.
	//   62614 — lake elevation NGVD-29
	//   62615 — lake elevation NAVD-88
	//   62616 — reservoir water surface elevation (newer code)
	//   00062 — elevation of reservoir water surface above datum
	//   00065 — gage height, feet (relative)
	conwayUSGSParams = "62614,62615,62616,00062,00065"

	conwayUSGSIVURL = "https://waterservices.usgs.gov/nwis/iv/"
	conwayUSGSDVURL = "https://waterservices.usgs.gov/nwis/dv/"

	// Lake Conway is updated monthly by the Orange County Lake Conway
	// Water and Navigation Control District (manual readings, not a
	// real-time sensor). The IV endpoint returns nothing; we poll DV
	// on a slow cadence and use the latest dated value as "current".
	conwayDVRefreshInterval = 6 * time.Hour
	conwayWeatherPollInt    = 30 * time.Minute

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

// conwayRefreshDV pulls daily-value water-level readings over [now-daysBack,
// now] and folds new dates into the history. AddWithDate skips dates that
// already exist, so calling this repeatedly is safe. Lake Conway 02262800
// is a manual-read station updated monthly; we still need to walk back a
// couple of months on each refresh because new monthly snapshots can show
// up dated to days that were previously empty.
func conwayRefreshDV(daysBack int) error {
	end := time.Now().Format("2006-01-02")
	start := time.Now().AddDate(0, 0, -daysBack).Format("2006-01-02")
	url := fmt.Sprintf("%s?format=json&sites=%s&startDT=%s&endDT=%s&parameterCd=%s&siteStatus=all",
		conwayUSGSDVURL, conwayUSGSSite, start, end, conwayUSGSParams)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("fetching USGS DV: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("USGS DV returned status %d", resp.StatusCode)
	}

	var data usgsMultiResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("decoding USGS DV: %w", err)
	}

	var (
		levelCount int
		observed   []string
	)
	for _, ts := range data.Value.TimeSeries {
		if len(ts.Variable.VariableCode) == 0 {
			continue
		}
		code := ts.Variable.VariableCode[0].Value
		observed = append(observed, code+" "+ts.Variable.VariableName)
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
				case "62614", "62615", "62616", "00062", "00065":
					conwayLevelHistory.AddWithDate(date, x)
					levelCount++
				}
			}
		}
	}

	conwayLevelHistory.Sort()
	if len(observed) > 0 {
		log.Printf("[Conway] USGS DV reported params: %v", observed)
	}
	log.Printf("[Conway] DV refresh added %d level points (last %d days)", levelCount, daysBack)
	return nil
}

// conwayBackfillFromUSGS pulls 5 years of DV at startup.
func conwayBackfillFromUSGS() {
	log.Println("[Conway] Backfilling level history from USGS (5 years)...")
	err := conwayRefreshDV(5 * 365)
	TrackAPICall("USGS-Conway-DV", err)
	if err != nil {
		log.Printf("[Conway] Warning: USGS backfill failed: %v", err)
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

// conwayDVRefreshLoop polls the DV endpoint for recent readings every
// conwayDVRefreshInterval. The Orange County monthly updates only need a
// slow cadence; we look back 90 days so a late-arriving snapshot from
// last month still gets picked up.
func conwayDVRefreshLoop() {
	ticker := time.NewTicker(conwayDVRefreshInterval)
	defer ticker.Stop()
	for range ticker.C {
		err := conwayRefreshDV(90)
		TrackAPICall("USGS-Conway-DV", err)
		if err != nil {
			log.Printf("[Conway] DV refresh error: %v", err)
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

	go conwayDVRefreshLoop()
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
