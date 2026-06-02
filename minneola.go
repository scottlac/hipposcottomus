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
	minneolaBasePath = "/minneola"
	// Full pool reference is SJRWMD's adopted Minimum Average (Ch 40C-8,
	// F.A.C.) — the lake's regulated target — converted to NAVD-88 to
	// match the datum the Water Atlas reports under.
	//   NGVD-29: MFH 96.0 / Min Avg 95.3 / MFL 93.9
	//   NAVD-88: MFH 95.1 / Min Avg 94.4 / MFL 93.0
	minneolaFullPool = 94.4

	// Water Atlas API exposes SJRWMD's direct Lake Minneola station
	// (StationID 70400984, sitting in the lake itself at 28.558°N,
	// -81.769°W). Daily readings since 1986 (n=13,638). Replaces the
	// previous Lake Minnehaha proxy, which understated the deficit by
	// ~0.5–1.1 ft when the chain was drying.
	waterAtlasBaseURL          = "https://api.wateratlas.usf.edu"
	minneolaSJRWMDDataSource   = "SJRWMD_HYDRO"
	minneolaSJRWMDStation      = "70400984"
	minneolaLevelRefreshPeriod = 6 * time.Hour
	minneolaWeatherPollInt     = 30 * time.Minute

	// Persistence files (sit alongside the other lakes' history files
	// on the same /data/ PVC). The level file got a "_navd88" suffix at
	// the SJRWMD switchover so the old USGS-proxy data (NGVD-29) doesn't
	// mix into the new chart — the orphaned `minneola_level_history.json`
	// on the PVC is safe to delete.
	minneolaTempHistoryFile  = "minneola_temp_history.json"
	minneolaLevelHistoryFile = "minneola_level_history_navd88.json"

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

// waterAtlasLevelPoint matches the JSON shape returned by the Water Atlas
// DataMapper "Levels/GraphData" endpoint. The full LatestData endpoint also
// reports parameterID/parameter/units alongside this, but GraphData is
// already pre-filtered to the single levels parameter for the station.
type waterAtlasLevelPoint struct {
	SampleDate  string  `json:"sampleDate"`
	ResultValue float64 `json:"resultValue"`
}

// minneolaRefreshLevel pulls daysBack days of water-level data from the
// Water Atlas API and folds new dates into history via AddWithDate (which
// skips duplicates). Safe to call repeatedly; today's reading and any
// late-published past dates get picked up. Out-of-range values are dropped
// — the SJRWMD raw stream occasionally publishes garbage like 513 ft.
func minneolaRefreshLevel(daysBack int) error {
	url := fmt.Sprintf("%s/DataMapper/Agency/%s/Station/%s/Hydrology/Levels/GraphData?numberOfDays=%d",
		waterAtlasBaseURL, minneolaSJRWMDDataSource, minneolaSJRWMDStation, daysBack)
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
		if p.ResultValue < 80 || p.ResultValue > 100 {
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
		minneolaLevelHistory.AddWithDate(date, p.ResultValue)
		added++
	}
	minneolaLevelHistory.Sort()
	log.Printf("[Minneola] Water Atlas level refresh: %d points added, %d dropped as outliers (window=%d days)",
		added, dropped, daysBack)
	return nil
}

// minneolaBackfillLevel pulls 5 years at startup. The history files persist
// across restarts; AddWithDate skips dates already loaded.
func minneolaBackfillLevel() {
	log.Println("[Minneola] Backfilling level history from SJRWMD via Water Atlas (5 years)...")
	err := minneolaRefreshLevel(5 * 365)
	TrackAPICall("WaterAtlas-Minneola", err)
	if err != nil {
		log.Printf("[Minneola] backfill failed: %v", err)
	}
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

// minneolaLevelLoop polls the Water Atlas every 6 hours with a 120-day
// look-back. SJRWMD publishes its hydrology data daily but with a multi-
// week pipeline lag, so polling more often than that is wasted. The
// 120-day window comfortably covers the publication lag plus any
// retroactively-corrected past dates.
func minneolaLevelLoop() {
	ticker := time.NewTicker(minneolaLevelRefreshPeriod)
	defer ticker.Stop()
	for range ticker.C {
		err := minneolaRefreshLevel(120)
		TrackAPICall("WaterAtlas-Minneola", err)
		if err != nil {
			log.Printf("[Minneola] level refresh error: %v", err)
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
	minneolaBackfillLevel()
	minneolaBackfillTempEstimate()
	minneolaLevelHistory.Sort()
	minneolaTempHistory.Sort()
	saveMinneolaHistories()

	go minneolaLevelLoop()
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
