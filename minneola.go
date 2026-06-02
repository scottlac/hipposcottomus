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
	minneolaBasePath  = "/minneola"
	minneolaFullPool = 95.3 // ft NGVD-29 — SJRWMD Minimum Average (Ch 40C-8, F.A.C.), the lake's regulated target. 95.0 ft is the rounded topo-map elevation and was the original placeholder.

	// Lake Minneola itself has no real-time USGS gauge. We pull lake elevation
	// from canal-connected Lake Minnehaha (station 02236840, tracks within
	// ~0.1 ft) and estimate water temperature from a 7-day trailing mean of
	// NWS air temperature via Open-Meteo's archive API — no nearby USGS
	// station reports water temperature, and central FL inland lake-surface
	// temp tracks the rolling air-temp mean within ~2–3 °F year-round.
	// The footer attributes both data choices.
	minneolaUSGSSite = "02236840"
	// Lake-elevation codes USGS reports under for reservoir/lake sites; the
	// station uses whichever one it considers canonical and the rest come
	// back as empty time series.
	//   62614 — lake elevation NGVD-29
	//   62615 — lake elevation NAVD-88
	//   62616 — reservoir water surface elevation (newer code)
	//   00062 — elevation of reservoir water surface above datum
	minneolaUSGSParams = "62614,62615,62616,00062"

	minneolaUSGSIVURL = "https://waterservices.usgs.gov/nwis/iv/"
	minneolaUSGSDVURL = "https://waterservices.usgs.gov/nwis/dv/"

	minneolaPollInterval   = 15 * time.Minute
	minneolaWeatherPollInt = 30 * time.Minute

	// Persistence files (sit alongside Jordan Lake's history files on the
	// same /data/ PVC).
	minneolaTempHistoryFile  = "minneola_temp_history.json"
	minneolaLevelHistoryFile = "minneola_level_history.json"

	// Lake Minneola centroid (28.59°N, -81.78°W) for NWS grid discovery — picks up
	// whichever WFO covers it (likely MLB/Melbourne for central FL).
	minneolaPointsURL = "https://api.weather.gov/points/28.59,-81.78"
)

//go:embed minneola
var minneolaFiles embed.FS

// Histories and weather data live as globals, same shape as Jordan Lake.
var (
	minneolaTempHistory  = &History{}
	minneolaLevelHistory = &History{}
	minneolaWeather      = &WeatherData{}

	minneolaForecastURL string
	minneolaHourlyURL   string
)

// usgsMultiResp and celsiusToFahrenheit are shared with gaston.go.

// minneolaFetchIV pulls the most recent IV (instantaneous values) and updates
// the in-memory histories. The last good elevation reading wins regardless
// of which datum code it was reported under.
func minneolaFetchIV() error {
	url := fmt.Sprintf("%s?format=json&sites=%s&parameterCd=%s&period=PT6H",
		minneolaUSGSIVURL, minneolaUSGSSite, minneolaUSGSParams)
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
		minneolaLevelHistory.Add(level)
		log.Printf("[Minneola] Updated water level: %.2f ft", level)
	} else {
		log.Println("[Minneola] No water level reading in IV response")
	}
	return nil
}

// minneolaBackfillFromUSGS pulls up to 5 years of daily elevation values
// from the Minnehaha gauge. Water temperature is estimated separately via
// Open-Meteo (see minneola_temp_estimate.go).
func minneolaBackfillFromUSGS() {
	end := time.Now().Format("2006-01-02")
	start := time.Now().AddDate(-5, 0, 0).Format("2006-01-02")
	url := fmt.Sprintf("%s?format=json&sites=%s&startDT=%s&endDT=%s&parameterCd=%s&siteStatus=all",
		minneolaUSGSDVURL, minneolaUSGSSite, start, end, minneolaUSGSParams)
	log.Printf("[Minneola] Backfilling level history from USGS (%s to %s)...", start, end)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		TrackAPICall("USGS-Minneola-DV", err)
		log.Printf("[Minneola] Warning: USGS backfill failed (fetch): %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("status %d", resp.StatusCode)
		TrackAPICall("USGS-Minneola-DV", statusErr)
		log.Printf("[Minneola] Warning: USGS backfill failed (%v)", statusErr)
		return
	}

	var data usgsMultiResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		TrackAPICall("USGS-Minneola-DV", err)
		log.Printf("[Minneola] Warning: USGS backfill failed (decode): %v", err)
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
					minneolaLevelHistory.AddWithDate(date, x)
					levelCount++
				}
			}
		}
	}

	TrackAPICall("USGS-Minneola-DV", nil)
	log.Printf("[Minneola] Backfilled %d level points", levelCount)
}

// ── NWS weather ─────────────────────────────────────────────────

func minneolaDiscoverNWSGrid() {
	req, _ := http.NewRequest("GET", minneolaPointsURL, nil)
	req.Header.Set("User-Agent", "(lake-minneola-dashboard, contact@hipposcottomus.com)")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Minneola] NWS points lookup failed: %v", err)
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
		log.Printf("[Minneola] NWS points decode failed: %v", err)
		return
	}
	minneolaForecastURL = pts.Properties.Forecast
	minneolaHourlyURL = pts.Properties.ForecastHourly
	log.Printf("[Minneola] NWS grid discovered: forecast=%s hourly=%s", minneolaForecastURL, minneolaHourlyURL)
}

func updateMinneolaWeather() {
	if minneolaForecastURL == "" || minneolaHourlyURL == "" {
		minneolaDiscoverNWSGrid()
		if minneolaForecastURL == "" {
			log.Println("[Minneola] NWS grid not available, skipping weather update")
			return
		}
	}

	forecast, err := fetchNWSForecast(minneolaForecastURL)
	TrackAPICall("NWS-Minneola", err)
	if err != nil {
		log.Printf("[Minneola] forecast error: %v", err)
	}
	hourly, errH := fetchNWSForecast(minneolaHourlyURL)
	if errH != nil {
		log.Printf("[Minneola] hourly error: %v", errH)
	}

	uv, uvErr := fetchUVIndex(28.59, -81.78) // Lake Minneola centroid (Clermont, FL)
	TrackAPICall("OpenMeteo-UV", uvErr)
	if uvErr != nil {
		log.Printf("[Minneola] UV index error: %v", uvErr)
	}

	minneolaWeather.mu.Lock()
	defer minneolaWeather.mu.Unlock()
	if forecast != nil {
		minneolaWeather.Forecast = forecast
		log.Printf("[Minneola] Updated forecast: %d periods", len(forecast))
	}
	if hourly != nil {
		if len(hourly) > 24 {
			hourly = hourly[:24]
		}
		minneolaWeather.Hourly = hourly
		log.Printf("[Minneola] Updated hourly forecast: %d hours", len(hourly))
	}
	if uv != nil {
		minneolaWeather.UV = uv
		log.Printf("[Minneola] Updated UV index: %.1f", uv.Current)
	}
}

// ── Persistence ─────────────────────────────────────────────────

func minneolaTempPath() string  { return filepath.Join(getDataDir(), minneolaTempHistoryFile) }
func minneolaLevelPath() string { return filepath.Join(getDataDir(), minneolaLevelHistoryFile) }

func loadMinneolaHistories() {
	if err := minneolaTempHistory.LoadFromFile(minneolaTempPath()); err != nil {
		if os.IsNotExist(err) {
			log.Println("[Minneola] no persisted temp history, starting fresh")
		} else {
			log.Printf("[Minneola] failed to load temp history: %v", err)
		}
	} else {
		log.Printf("[Minneola] loaded %d days of temp history", minneolaTempHistory.Len())
	}
	if err := minneolaLevelHistory.LoadFromFile(minneolaLevelPath()); err != nil {
		if os.IsNotExist(err) {
			log.Println("[Minneola] no persisted level history, starting fresh")
		} else {
			log.Printf("[Minneola] failed to load level history: %v", err)
		}
	} else {
		log.Printf("[Minneola] loaded %d days of level history", minneolaLevelHistory.Len())
	}
}

func saveMinneolaHistories() {
	if err := minneolaTempHistory.SaveToFile(minneolaTempPath()); err != nil {
		log.Printf("[Minneola] save temp history failed: %v", err)
	}
	if err := minneolaLevelHistory.SaveToFile(minneolaLevelPath()); err != nil {
		log.Printf("[Minneola] save level history failed: %v", err)
	}
}

// ── Loops ───────────────────────────────────────────────────────

func minneolaScrapeLoop() {
	log.Println("[Minneola] Running initial USGS IV scrape...")
	err := minneolaFetchIV()
	TrackAPICall("USGS-Minneola-IV", err)
	if err != nil {
		log.Printf("[Minneola] initial IV scrape error: %v", err)
	} else {
		saveMinneolaHistories()
	}

	ticker := time.NewTicker(minneolaPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		err := minneolaFetchIV()
		TrackAPICall("USGS-Minneola-IV", err)
		if err != nil {
			log.Printf("[Minneola] scheduled IV scrape error: %v", err)
			continue
		}
		saveMinneolaHistories()
	}
}

func minneolaWeatherLoop() {
	updateMinneolaWeather()
	ticker := time.NewTicker(minneolaWeatherPollInt)
	defer ticker.Stop()
	for range ticker.C {
		updateMinneolaWeather()
	}
}

// ── HTTP handlers ───────────────────────────────────────────────

type minneolaCurrentResponse struct {
	Temperature *DataPoint `json:"temperature"`
	WaterLevel  *DataPoint `json:"waterLevel"`
}

func handleMinneolaCurrent(w http.ResponseWriter, r *http.Request) {
	resp := minneolaCurrentResponse{}
	if pt, ok := minneolaTempHistory.Latest(); ok {
		resp.Temperature = &pt
	}
	if pt, ok := minneolaLevelHistory.Latest(); ok {
		resp.WaterLevel = &pt
	}
	writeJSON(w, resp)
}

type minneolaHistoryResponse struct {
	Temperature []DataPoint `json:"temperature"`
	WaterLevel  []DataPoint `json:"waterLevel"`
}

func handleMinneolaHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, minneolaHistoryResponse{
		Temperature: minneolaTempHistory.All(),
		WaterLevel:  minneolaLevelHistory.All(),
	})
}

func handleMinneolaWeather(w http.ResponseWriter, r *http.Request) {
	minneolaWeather.mu.RLock()
	defer minneolaWeather.mu.RUnlock()
	writeJSON(w, struct {
		Forecast []WeatherPeriod `json:"forecast"`
		Hourly   []WeatherPeriod `json:"hourly"`
		UV       *UVData         `json:"uv,omitempty"`
	}{
		Forecast: minneolaWeather.Forecast,
		Hourly:   minneolaWeather.Hourly,
		UV:       minneolaWeather.UV,
	})
}

// ── Init ────────────────────────────────────────────────────────

// InitMinneola wires up the Lake Minneola dashboard: persistence, backfill,
// background scrapers, and HTTP routes.
func InitMinneola(mux *http.ServeMux) {
	loadMinneolaHistories()
	minneolaBackfillFromUSGS()
	minneolaBackfillTempEstimate()
	minneolaLevelHistory.Sort()
	minneolaTempHistory.Sort()
	saveMinneolaHistories()

	go minneolaScrapeLoop()
	go minneolaWeatherLoop()
	go minneolaTempEstimateLoop()
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			saveMinneolaHistories()
		}
	}()

	mux.HandleFunc(minneolaBasePath+"/api/current", handleMinneolaCurrent)
	mux.HandleFunc(minneolaBasePath+"/api/history", handleMinneolaHistory)
	mux.HandleFunc(minneolaBasePath+"/api/weather", handleMinneolaWeather)

	staticSub, err := fs.Sub(minneolaFiles, "minneola")
	if err != nil {
		log.Fatalf("[Minneola] failed to load embedded static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle(minneolaBasePath+"/", http.StripPrefix(minneolaBasePath, fileServer))

	log.Printf("[Minneola] Lake Minneola dashboard registered at %s/", minneolaBasePath)
}
