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
	gastonBasePath  = "/gaston"
	gastonFullPool  = 200.0 // ft — Dominion Energy normal operating elevation

	// USGS station: Lake Gaston (Roanoke River) Near Elams, NC
	gastonUSGSSite = "02079785"
	// Cover every reservoir-style code USGS uses so we don't have to know
	// the canonical one for this station up front. Whichever the station
	// actually reports gets used; the rest come back as empty time series.
	//   62614 — lake elevation NGVD-29
	//   62615 — lake elevation NAVD-88
	//   62616 — reservoir water surface elevation (newer code)
	//   00062 — elevation of reservoir water surface above datum
	//   00010 — water temperature, °C
	//   00011 — water temperature, °F
	gastonUSGSParams = "62614,62615,62616,00062,00010,00011"

	gastonUSGSIVURL = "https://waterservices.usgs.gov/nwis/iv/"
	gastonUSGSDVURL = "https://waterservices.usgs.gov/nwis/dv/"

	gastonPollInterval   = 15 * time.Minute
	gastonWeatherPollInt = 30 * time.Minute

	// Persistence files (sit alongside Jordan Lake's history files on the
	// same /data/ PVC).
	gastonTempHistoryFile  = "gaston_temp_history.json"
	gastonLevelHistoryFile = "gaston_level_history.json"

	// Lake Gaston centroid for NWS grid discovery — picks up whichever
	// WFO covers it (likely AKQ/Wakefield).
	gastonPointsURL = "https://api.weather.gov/points/36.50,-77.90"
)

//go:embed gaston
var gastonFiles embed.FS

// Histories and weather data live as globals, same shape as Jordan Lake.
var (
	gastonTempHistory  = &History{}
	gastonLevelHistory = &History{}
	gastonWeather      = &WeatherData{}

	gastonForecastURL string
	gastonHourlyURL   string
)

// ── USGS responses ──────────────────────────────────────────────

// usgsMultiResp matches the subset of /nwis/iv and /nwis/dv we need, with
// each time series tagged by parameter code so we can sort the values into
// the right history.
type usgsMultiResp struct {
	Value struct {
		TimeSeries []struct {
			Variable struct {
				VariableCode []struct {
					Value string `json:"value"`
				} `json:"variableCode"`
				VariableName string `json:"variableName"`
			} `json:"variable"`
			Values []struct {
				Value []struct {
					DateTime string `json:"dateTime"`
					Value    string `json:"value"`
				} `json:"value"`
			} `json:"values"`
		} `json:"timeSeries"`
	} `json:"value"`
}

// celsiusToFahrenheit converts a temperature reading; USGS reports water
// temperature in °C, the rest of the site shows °F.
func celsiusToFahrenheit(c float64) float64 {
	return c*9.0/5.0 + 32.0
}

// gastonFetchIV pulls the most recent IV (instantaneous values) and updates
// the in-memory histories. The last good elevation reading wins regardless
// of which datum code it was reported under.
func gastonFetchIV() error {
	url := fmt.Sprintf("%s?format=json&sites=%s&parameterCd=%s&period=PT6H",
		gastonUSGSIVURL, gastonUSGSSite, gastonUSGSParams)
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
		gotLevel, gotTemp bool
		level, temp       float64
		observed          []string
	)
	for _, ts := range data.Value.TimeSeries {
		if len(ts.Variable.VariableCode) == 0 {
			continue
		}
		code := ts.Variable.VariableCode[0].Value
		// Walk to the most recent non-empty reading.
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
			// Code was requested but the station has no data for it.
			continue
		}
		observed = append(observed, code+" "+ts.Variable.VariableName)
		x, err := strconv.ParseFloat(latest, 64)
		if err != nil {
			continue
		}
		switch code {
		case "62614", "62615", "62616", "00062":
			level = x
			gotLevel = true
		case "00010":
			temp = celsiusToFahrenheit(x)
			gotTemp = true
		case "00011":
			temp = x
			gotTemp = true
		}
	}

	// Self-diagnostic: surface every parameter the station actually reports
	// so we can extend the recognized list without guessing further.
	if len(observed) > 0 {
		log.Printf("[Gaston] USGS IV reported params: %v", observed)
	} else {
		log.Println("[Gaston] USGS IV returned 200 but no values for any requested param")
	}

	if gotLevel {
		gastonLevelHistory.Add(level)
		log.Printf("[Gaston] Updated water level: %.2f ft", level)
	} else {
		log.Println("[Gaston] No water level reading in IV response")
	}
	if gotTemp {
		gastonTempHistory.Add(temp)
		log.Printf("[Gaston] Updated water temp: %.1f °F", temp)
	}
	return nil
}

// gastonBackfillFromUSGS pulls up to 5 years of daily values for elevation
// AND temperature, populating both histories at startup. Missing parameters
// are silently ignored (the USGS response just contains no time series for
// a code the station doesn't track).
func gastonBackfillFromUSGS() {
	end := time.Now().Format("2006-01-02")
	start := time.Now().AddDate(-5, 0, 0).Format("2006-01-02")
	url := fmt.Sprintf("%s?format=json&sites=%s&startDT=%s&endDT=%s&parameterCd=%s&siteStatus=all",
		gastonUSGSDVURL, gastonUSGSSite, start, end, gastonUSGSParams)
	log.Printf("[Gaston] Backfilling history from USGS (%s to %s)...", start, end)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		TrackAPICall("USGS-Gaston-DV", err)
		log.Printf("[Gaston] Warning: USGS backfill failed (fetch): %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("status %d", resp.StatusCode)
		TrackAPICall("USGS-Gaston-DV", statusErr)
		log.Printf("[Gaston] Warning: USGS backfill failed (%v)", statusErr)
		return
	}

	var data usgsMultiResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		TrackAPICall("USGS-Gaston-DV", err)
		log.Printf("[Gaston] Warning: USGS backfill failed (decode): %v", err)
		return
	}

	var levelCount, tempCount int
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
					gastonLevelHistory.AddWithDate(date, x)
					levelCount++
				case "00010":
					gastonTempHistory.AddWithDate(date, celsiusToFahrenheit(x))
					tempCount++
				case "00011":
					gastonTempHistory.AddWithDate(date, x)
					tempCount++
				}
			}
		}
	}

	TrackAPICall("USGS-Gaston-DV", nil)
	log.Printf("[Gaston] Backfilled %d level points, %d temp points", levelCount, tempCount)
}

// ── NWS weather ─────────────────────────────────────────────────

func gastonDiscoverNWSGrid() {
	req, _ := http.NewRequest("GET", gastonPointsURL, nil)
	req.Header.Set("User-Agent", "(lake-gaston-dashboard, contact@hipposcottomus.com)")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Gaston] NWS points lookup failed: %v", err)
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
		log.Printf("[Gaston] NWS points decode failed: %v", err)
		return
	}
	gastonForecastURL = pts.Properties.Forecast
	gastonHourlyURL = pts.Properties.ForecastHourly
	log.Printf("[Gaston] NWS grid discovered: forecast=%s hourly=%s", gastonForecastURL, gastonHourlyURL)
}

func updateGastonWeather() {
	if gastonForecastURL == "" || gastonHourlyURL == "" {
		gastonDiscoverNWSGrid()
		if gastonForecastURL == "" {
			log.Println("[Gaston] NWS grid not available, skipping weather update")
			return
		}
	}

	forecast, err := fetchNWSForecast(gastonForecastURL)
	TrackAPICall("NWS-Gaston", err)
	if err != nil {
		log.Printf("[Gaston] forecast error: %v", err)
	}
	hourly, errH := fetchNWSForecast(gastonHourlyURL)
	if errH != nil {
		log.Printf("[Gaston] hourly error: %v", errH)
	}

	uv, uvErr := fetchUVIndex(36.50, -77.90) // Lake Gaston centroid
	TrackAPICall("OpenMeteo-UV", uvErr)
	if uvErr != nil {
		log.Printf("[Gaston] UV index error: %v", uvErr)
	}

	gastonWeather.mu.Lock()
	defer gastonWeather.mu.Unlock()
	if forecast != nil {
		gastonWeather.Forecast = forecast
		log.Printf("[Gaston] Updated forecast: %d periods", len(forecast))
	}
	if hourly != nil {
		if len(hourly) > 24 {
			hourly = hourly[:24]
		}
		gastonWeather.Hourly = hourly
		log.Printf("[Gaston] Updated hourly forecast: %d hours", len(hourly))
	}
	if uv != nil {
		gastonWeather.UV = uv
		log.Printf("[Gaston] Updated UV index: %.1f", uv.Current)
	}
}

// ── Persistence ─────────────────────────────────────────────────

func gastonTempPath() string  { return filepath.Join(getDataDir(), gastonTempHistoryFile) }
func gastonLevelPath() string { return filepath.Join(getDataDir(), gastonLevelHistoryFile) }

func loadGastonHistories() {
	if err := gastonTempHistory.LoadFromFile(gastonTempPath()); err != nil {
		if os.IsNotExist(err) {
			log.Println("[Gaston] no persisted temp history, starting fresh")
		} else {
			log.Printf("[Gaston] failed to load temp history: %v", err)
		}
	} else {
		log.Printf("[Gaston] loaded %d days of temp history", gastonTempHistory.Len())
	}
	if err := gastonLevelHistory.LoadFromFile(gastonLevelPath()); err != nil {
		if os.IsNotExist(err) {
			log.Println("[Gaston] no persisted level history, starting fresh")
		} else {
			log.Printf("[Gaston] failed to load level history: %v", err)
		}
	} else {
		log.Printf("[Gaston] loaded %d days of level history", gastonLevelHistory.Len())
	}
}

func saveGastonHistories() {
	if err := gastonTempHistory.SaveToFile(gastonTempPath()); err != nil {
		log.Printf("[Gaston] save temp history failed: %v", err)
	}
	if err := gastonLevelHistory.SaveToFile(gastonLevelPath()); err != nil {
		log.Printf("[Gaston] save level history failed: %v", err)
	}
}

// ── Loops ───────────────────────────────────────────────────────

func gastonScrapeLoop() {
	log.Println("[Gaston] Running initial USGS IV scrape...")
	err := gastonFetchIV()
	TrackAPICall("USGS-Gaston-IV", err)
	if err != nil {
		log.Printf("[Gaston] initial IV scrape error: %v", err)
	} else {
		saveGastonHistories()
	}

	ticker := time.NewTicker(gastonPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		err := gastonFetchIV()
		TrackAPICall("USGS-Gaston-IV", err)
		if err != nil {
			log.Printf("[Gaston] scheduled IV scrape error: %v", err)
			continue
		}
		saveGastonHistories()
	}
}

func gastonWeatherLoop() {
	updateGastonWeather()
	ticker := time.NewTicker(gastonWeatherPollInt)
	defer ticker.Stop()
	for range ticker.C {
		updateGastonWeather()
	}
}

// ── HTTP handlers ───────────────────────────────────────────────

type gastonCurrentResponse struct {
	Temperature *DataPoint `json:"temperature"`
	WaterLevel  *DataPoint `json:"waterLevel"`
}

func handleGastonCurrent(w http.ResponseWriter, r *http.Request) {
	resp := gastonCurrentResponse{}
	if pt, ok := gastonTempHistory.Latest(); ok {
		resp.Temperature = &pt
	}
	if pt, ok := gastonLevelHistory.Latest(); ok {
		resp.WaterLevel = &pt
	}
	writeJSON(w, resp)
}

type gastonHistoryResponse struct {
	Temperature []DataPoint `json:"temperature"`
	WaterLevel  []DataPoint `json:"waterLevel"`
}

func handleGastonHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, gastonHistoryResponse{
		Temperature: gastonTempHistory.All(),
		WaterLevel:  gastonLevelHistory.All(),
	})
}

func handleGastonWeather(w http.ResponseWriter, r *http.Request) {
	gastonWeather.mu.RLock()
	defer gastonWeather.mu.RUnlock()
	writeJSON(w, struct {
		Forecast []WeatherPeriod `json:"forecast"`
		Hourly   []WeatherPeriod `json:"hourly"`
		UV       *UVData         `json:"uv,omitempty"`
	}{
		Forecast: gastonWeather.Forecast,
		Hourly:   gastonWeather.Hourly,
		UV:       gastonWeather.UV,
	})
}

// ── Init ────────────────────────────────────────────────────────

// InitGaston wires up the Lake Gaston dashboard: persistence, backfill,
// background scrapers, and HTTP routes.
func InitGaston(mux *http.ServeMux) {
	loadGastonHistories()
	gastonBackfillFromUSGS()
	gastonLevelHistory.Sort()
	gastonTempHistory.Sort()
	saveGastonHistories()

	go gastonScrapeLoop()
	go gastonWeatherLoop()
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			saveGastonHistories()
		}
	}()

	mux.HandleFunc(gastonBasePath+"/api/current", handleGastonCurrent)
	mux.HandleFunc(gastonBasePath+"/api/history", handleGastonHistory)
	mux.HandleFunc(gastonBasePath+"/api/weather", handleGastonWeather)

	staticSub, err := fs.Sub(gastonFiles, "gaston")
	if err != nil {
		log.Fatalf("[Gaston] failed to load embedded static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle(gastonBasePath+"/", http.StripPrefix(gastonBasePath, fileServer))

	log.Printf("[Gaston] Lake Gaston dashboard registered at %s/", gastonBasePath)
}
