package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ftlBasePath = "/ftlauderdale"

	// NOAA CO-OPS API – Tides & Currents
	noaaStation = "8722939" // Fort Lauderdale – Bahia Mar YC
	noaaBaseURL = "https://api.tidesandcurrents.noaa.gov/api/prod/datagetter"

	// NWS API – grid point will be looked up at startup
	ftlPointsURL = "https://api.weather.gov/points/26.1133,-80.1083"

	// Marine forecast – Deerfield Beach to Ocean Reef out 20 NM & 20-60 NM
	marineForecastURL = "https://www.ndbc.noaa.gov/data/Forecasts/FZUS52.KMFL.html"

	ftlPollInterval = 30 * time.Minute
)

//go:embed ftl
var ftlFiles embed.FS

// ── NOAA CO-OPS response types ──────────────────────────────────

type noaaPrediction struct {
	T    string `json:"t"`    // "2026-04-17 00:00"
	V    string `json:"v"`    // "1.234"
	Type string `json:"type"` // "H" or "L" (hi-lo only)
}

type noaaPredictionsResp struct {
	Predictions []noaaPrediction `json:"predictions"`
}

type noaaDataResp struct {
	Data []struct {
		T string `json:"t"`
		V string `json:"v"`
	} `json:"data"`
}

// ── FTL data types ──────────────────────────────────────────────

type TideEvent struct {
	Time   string  `json:"time"`
	Height float64 `json:"height"`
	Type   string  `json:"type"` // "High" or "Low"
}

type TidePoint struct {
	Time   string  `json:"time"`
	Height float64 `json:"height"`
}

type FTLStore struct {
	mu             sync.RWMutex
	TideHiLo       []TideEvent     `json:"tideHiLo"`
	TideCurve      []TidePoint     `json:"tideCurve"`
	WaterTemp      *float64        `json:"waterTemp"`
	Forecast       []WeatherPeriod `json:"forecast"`
	Hourly         []WeatherPeriod `json:"hourly"`
	MarineForecast string          `json:"marineForecast"`
}

var ftlStore = &FTLStore{}

// NWS grid URLs discovered at startup
var (
	ftlForecastURL string
	ftlHourlyURL   string
)

// ── NOAA fetchers ───────────────────────────────────────────────

func fetchTidePredictions() {
	// Use begin_date/end_date — this station only supports hi-lo predictions.
	now := time.Now()
	begin := now.Add(-6 * time.Hour).Format("20060102")
	end := now.Add(72 * time.Hour).Format("20060102")

	hiloURL := fmt.Sprintf("%s?station=%s&product=predictions&datum=MLLW&time_zone=lst_ldt&units=english&interval=hilo&format=json&begin_date=%s&end_date=%s",
		noaaBaseURL, noaaStation, begin, end)
	hiLoEvents, err := fetchNOAAHiLo(hiloURL)
	TrackAPICall("NOAA-Tides", err)
	if err != nil {
		log.Printf("[FTL] tide hi-lo fetch error: %v", err)
	}

	// Synthesize a smooth tide curve from hi-lo points using cosine interpolation
	var curvePoints []TidePoint
	if len(hiLoEvents) >= 2 {
		curvePoints = interpolateTideCurve(hiLoEvents)
		log.Printf("[FTL] Synthesized %d tide curve points from %d hi/lo events", len(curvePoints), len(hiLoEvents))
	}

	ftlStore.mu.Lock()
	if hiLoEvents != nil {
		ftlStore.TideHiLo = hiLoEvents
		log.Printf("[FTL] Updated %d hi/lo tide events", len(hiLoEvents))
	}
	if curvePoints != nil {
		ftlStore.TideCurve = curvePoints
	}
	ftlStore.mu.Unlock()
}

// parseNOAALocalTime parses NOAA's local time format "2026-04-17 09:03".
func parseNOAALocalTime(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04", s, time.Now().Location())
}

// interpolateTideCurve generates a smooth cosine-interpolated curve between hi/lo events.
func interpolateTideCurve(events []TideEvent) []TidePoint {
	var points []TidePoint
	step := 10 * time.Minute

	for i := 0; i < len(events)-1; i++ {
		t0, err0 := parseNOAALocalTime(events[i].Time)
		t1, err1 := parseNOAALocalTime(events[i+1].Time)
		if err0 != nil || err1 != nil {
			continue
		}
		h0 := events[i].Height
		h1 := events[i+1].Height
		duration := t1.Sub(t0).Seconds()
		if duration <= 0 {
			continue
		}

		for t := t0; t.Before(t1); t = t.Add(step) {
			frac := t.Sub(t0).Seconds() / duration
			// Cosine interpolation: smooth transition from h0 to h1
			h := h0 + (h1-h0)*(1-math.Cos(frac*math.Pi))/2
			points = append(points, TidePoint{
				Time:   t.Format("2006-01-02 15:04"),
				Height: math.Round(h*1000) / 1000,
			})
		}
	}

	// Add the final point
	if len(events) > 0 {
		last := events[len(events)-1]
		points = append(points, TidePoint{Time: last.Time, Height: last.Height})
	}

	return points
}

func fetchNOAAHiLo(url string) ([]TideEvent, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("NOAA returned %d", resp.StatusCode)
	}
	var data noaaPredictionsResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	events := make([]TideEvent, 0, len(data.Predictions))
	for _, p := range data.Predictions {
		v, err := strconv.ParseFloat(p.V, 64)
		if err != nil {
			continue
		}
		typ := "High"
		if p.Type == "L" {
			typ = "Low"
		}
		events = append(events, TideEvent{Time: p.T, Height: v, Type: typ})
	}
	return events, nil
}

// fetchNOAAWaterTemp gets water temp from Virginia Key (8723214), the nearest
// station with a water temperature sensor (~25 mi south of Bahia Mar).
func fetchNOAAWaterTemp() {
	url := fmt.Sprintf("%s?station=8723214&product=water_temperature&time_zone=lst_ldt&units=english&format=json&range=1",
		noaaBaseURL)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		TrackAPICall("NOAA-WaterTemp", err)
		log.Printf("[FTL] water temp fetch error: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("status %d", resp.StatusCode)
		TrackAPICall("NOAA-WaterTemp", statusErr)
		log.Printf("[FTL] water temp: NOAA returned %d (station may not have sensor)", resp.StatusCode)
		return
	}
	var data noaaDataResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		TrackAPICall("NOAA-WaterTemp", err)
		log.Printf("[FTL] water temp decode error: %v", err)
		return
	}
	if len(data.Data) == 0 {
		TrackAPICall("NOAA-WaterTemp", fmt.Errorf("no data returned"))
		log.Println("[FTL] water temp: no data returned")
		return
	}
	// Use the latest reading
	last := data.Data[len(data.Data)-1]
	v, err := strconv.ParseFloat(last.V, 64)
	if err != nil {
		TrackAPICall("NOAA-WaterTemp", err)
		return
	}
	TrackAPICall("NOAA-WaterTemp", nil)
	ftlStore.mu.Lock()
	ftlStore.WaterTemp = &v
	ftlStore.mu.Unlock()
	log.Printf("[FTL] Updated water temp: %.1f°F", v)
}

// ── NWS weather ─────────────────────────────────────────────────

// discoverNWSGrid looks up the NWS grid point for Ft Lauderdale.
func discoverNWSGrid() {
	req, _ := http.NewRequest("GET", ftlPointsURL, nil)
	req.Header.Set("User-Agent", "(ftl-boating-dashboard, contact@hipposcottomus.com)")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[FTL] NWS points lookup failed: %v", err)
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
		log.Printf("[FTL] NWS points decode failed: %v", err)
		return
	}
	ftlForecastURL = pts.Properties.Forecast
	ftlHourlyURL = pts.Properties.ForecastHourly
	log.Printf("[FTL] NWS grid discovered: forecast=%s hourly=%s", ftlForecastURL, ftlHourlyURL)
}

func updateFTLWeather() {
	if ftlForecastURL == "" || ftlHourlyURL == "" {
		discoverNWSGrid()
		if ftlForecastURL == "" {
			log.Println("[FTL] NWS grid not available, skipping weather update")
			return
		}
	}

	forecast, err := fetchNWSForecast(ftlForecastURL)
	TrackAPICall("NWS-Miami", err)
	if err != nil {
		log.Printf("[FTL] forecast error: %v", err)
	}
	hourly, err2 := fetchNWSForecast(ftlHourlyURL)
	if err2 != nil {
		log.Printf("[FTL] hourly error: %v", err2)
	}

	ftlStore.mu.Lock()
	if forecast != nil {
		ftlStore.Forecast = forecast
	}
	if hourly != nil {
		if len(hourly) > 24 {
			hourly = hourly[:24]
		}
		ftlStore.Hourly = hourly
	}
	ftlStore.mu.Unlock()
}

// ── Marine Forecast ─────────────────────────────────────────────

func fetchMarineForecast() {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(marineForecastURL)
	if err != nil {
		TrackAPICall("NDBC-Marine", err)
		log.Printf("[FTL] marine forecast fetch error: %v", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	TrackAPICall("NDBC-Marine", nil)

	// Extract the section for "Deerfield Beach to Ocean Reef" (AMZ651)
	section := extractMarineZone(text, "Deerfield Beach to Ocean Reef")
	if section == "" {
		// Fallback: try Jupiter Inlet to Deerfield Beach (AMZ650)
		section = extractMarineZone(text, "Jupiter Inlet to Deerfield Beach")
	}
	if section == "" {
		// Last resort: grab Synopsis
		section = extractMarineZone(text, "Synopsis")
	}

	ftlStore.mu.Lock()
	if section != "" {
		ftlStore.MarineForecast = section
		log.Printf("[FTL] Updated marine forecast (%d chars)", len(section))
	}
	ftlStore.mu.Unlock()
}

// extractMarineZone pulls out a zone section from the NDBC marine forecast text.
func extractMarineZone(text, zoneName string) string {
	lower := strings.ToLower(text)
	needle := strings.ToLower(zoneName)
	idx := strings.Index(lower, needle)
	if idx < 0 {
		return ""
	}

	// Walk back to find start of this block (look for the zone code line like "AMZ651-...")
	start := idx
	for start > 0 && text[start-1] != '\n' {
		start--
	}
	// Walk further back to include the zone code header (line above)
	if start > 2 {
		prevNewline := strings.LastIndex(text[:start-1], "\n")
		if prevNewline >= 0 {
			start = prevNewline + 1
		}
	}

	// Find the end: next "AMZ" or "GMZ" zone header, or end of text
	rest := text[idx:]
	endMarkers := []string{"\nAMZ", "\nGMZ", "\n$$"}
	endIdx := len(rest)
	for _, marker := range endMarkers {
		if i := strings.Index(rest[10:], marker); i >= 0 && i+10 < endIdx {
			endIdx = i + 10
		}
	}

	section := strings.TrimSpace(text[start : idx+endIdx])

	// Clean up HTML tags if present (since we're fetching the HTML version)
	section = stripHTMLTags(section)

	return section
}

func stripHTMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// ── Background loop ─────────────────────────────────────────────

func ftlUpdateAll() {
	fetchTidePredictions()
	fetchNOAAWaterTemp()
	updateFTLWeather()
	fetchMarineForecast()
}

func ftlLoop() {
	log.Println("[FTL] Running initial data fetch...")
	ftlUpdateAll()
	ticker := time.NewTicker(ftlPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		log.Println("[FTL] Running scheduled update...")
		ftlUpdateAll()
	}
}

// ── API handlers ────────────────────────────────────────────────

func handleFTLTides(w http.ResponseWriter, r *http.Request) {
	ftlStore.mu.RLock()
	resp := struct {
		HiLo  []TideEvent `json:"hiLo"`
		Curve []TidePoint `json:"curve"`
	}{
		HiLo:  ftlStore.TideHiLo,
		Curve: ftlStore.TideCurve,
	}
	ftlStore.mu.RUnlock()
	writeJSON(w, resp)
}

func handleFTLWeather(w http.ResponseWriter, r *http.Request) {
	ftlStore.mu.RLock()
	resp := struct {
		Forecast  []WeatherPeriod `json:"forecast"`
		Hourly    []WeatherPeriod `json:"hourly"`
		WaterTemp *float64        `json:"waterTemp"`
	}{
		Forecast:  ftlStore.Forecast,
		Hourly:    ftlStore.Hourly,
		WaterTemp: ftlStore.WaterTemp,
	}
	ftlStore.mu.RUnlock()
	writeJSON(w, resp)
}

func handleFTLMarine(w http.ResponseWriter, r *http.Request) {
	ftlStore.mu.RLock()
	resp := struct {
		Forecast string `json:"forecast"`
	}{
		Forecast: ftlStore.MarineForecast,
	}
	ftlStore.mu.RUnlock()
	writeJSON(w, resp)
}

// ── Init ────────────────────────────────────────────────────────

// InitFTL registers all Ft Lauderdale routes and starts background polling.
func InitFTL(mux *http.ServeMux) {
	// API endpoints
	mux.HandleFunc(ftlBasePath+"/api/tides", handleFTLTides)
	mux.HandleFunc(ftlBasePath+"/api/weather", handleFTLWeather)
	mux.HandleFunc(ftlBasePath+"/api/marine", handleFTLMarine)

	// Embedded static files
	staticSub, err := fs.Sub(ftlFiles, "ftl")
	if err != nil {
		log.Fatalf("[FTL] Failed to load embedded static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle(ftlBasePath+"/", http.StripPrefix(ftlBasePath, fileServer))

	// Start background data fetch loop
	go ftlLoop()

	log.Printf("[FTL] Ft Lauderdale dashboard registered at %s/", ftlBasePath)
}
