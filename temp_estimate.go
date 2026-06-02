package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Helpers for estimating lake surface temperature from a trailing mean of
// NWS air temperature. Used for Florida lakes (Minneola, Conway) where no
// nearby USGS station reports water temperature.
//
// Data source: Open-Meteo's free, no-auth APIs.
//   - archive endpoint: ERA5 reanalysis, ~5-day publication lag, goes back to 1940
//   - forecast endpoint: covers recent ~92 days through today via past_days=N
//
// Methodology: For a shallow subtropical lake (~10 ft avg depth), surface
// temp tracks the 7-day rolling mean of (daily Tmax + Tmin) / 2 within
// ~±3 °F. The estimate is presented to users with an "EST" badge and the
// blurb prompt is instructed never to quote it verbatim.

const openMeteoArchiveURL = "https://archive-api.open-meteo.com/v1/archive"

// dailyAirTemp is one calendar day of Tmax/Tmin (°F), keyed by ISO date.
type dailyAirTemp struct {
	Date string
	Hi   float64
	Lo   float64
}

// fetchOpenMeteoDaily pulls daily Tmax/Tmin from one of Open-Meteo's
// endpoints. The archive endpoint requires explicit start_date/end_date in
// qs; the forecast endpoint takes past_days+forecast_days. The response
// shape is identical for both.
func fetchOpenMeteoDaily(baseURL string, qs string, lat, lon float64) ([]dailyAirTemp, error) {
	url := fmt.Sprintf("%s?%s&daily=temperature_2m_max,temperature_2m_min&temperature_unit=fahrenheit&timezone=America/New_York&latitude=%.4f&longitude=%.4f",
		baseURL, qs, lat, lon)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching Open-Meteo daily: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Open-Meteo daily returned status %d", resp.StatusCode)
	}

	var body struct {
		Daily struct {
			Time []string  `json:"time"`
			Hi   []float64 `json:"temperature_2m_max"`
			Lo   []float64 `json:"temperature_2m_min"`
		} `json:"daily"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding Open-Meteo daily: %w", err)
	}

	n := len(body.Daily.Time)
	if n > len(body.Daily.Hi) {
		n = len(body.Daily.Hi)
	}
	if n > len(body.Daily.Lo) {
		n = len(body.Daily.Lo)
	}
	out := make([]dailyAirTemp, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, dailyAirTemp{Date: body.Daily.Time[i], Hi: body.Daily.Hi[i], Lo: body.Daily.Lo[i]})
	}
	return out, nil
}

// mergeDailyByDate combines two daily series, preferring the second source
// where dates overlap. Used to splice the recent-forecast slice on top of
// the archive backfill (the archive has a ~5-day publication lag).
func mergeDailyByDate(base, override []dailyAirTemp) []dailyAirTemp {
	idx := make(map[string]dailyAirTemp, len(base)+len(override))
	for _, d := range base {
		idx[d.Date] = d
	}
	for _, d := range override {
		idx[d.Date] = d
	}
	out := make([]dailyAirTemp, 0, len(idx))
	for _, d := range idx {
		out = append(out, d)
	}
	// Sort ascending by date so the rolling-mean walk is straightforward.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Date > out[j].Date; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// trailingMeanDailyMean returns the mean of (Hi+Lo)/2 across the windowDays
// ending at endIdx (inclusive). Returns ok=false if the window can't be
// filled — caller skips that day.
func trailingMeanDailyMean(daily []dailyAirTemp, endIdx, windowDays int) (float64, bool) {
	if endIdx < windowDays-1 || endIdx >= len(daily) {
		return 0, false
	}
	var sum float64
	for i := endIdx - windowDays + 1; i <= endIdx; i++ {
		sum += (daily[i].Hi + daily[i].Lo) / 2
	}
	return sum / float64(windowDays), true
}

// LakeTempEstimateConfig wires one lake's estimator: where to fetch from
// (lat/lon), where to write (history), how wide to smooth (window), and
// what to call it in logs / API-health.
type LakeTempEstimateConfig struct {
	// Key is the short lake identifier used in log prefixes ("Minneola").
	Key string
	// Latitude / Longitude — lake centroid.
	Lat, Lon float64
	// History receives the rolling-mean values. AddWithDate for backfill,
	// Add for live updates.
	History *History
	// WindowDays defines the smoothing window. 7 is the well-tested
	// default for ~10 ft-deep subtropical lakes.
	WindowDays int
	// HealthSource is the label used in TrackAPICall (analytics page).
	HealthSource string
	// PollInterval controls live-update cadence; mirror the lake's
	// weather poll so the estimate refreshes alongside the forecast.
	PollInterval time.Duration
	// RecentPastDays is how far back to pull from the forecast
	// endpoint to cover the archive's ~5-day publication lag. 92 is the
	// API max and is more than sufficient.
	RecentPastDays int
}

// backfillLakeTempEstimate populates ~5 years of estimated water-temp
// history at startup. Combines the archive endpoint (long history,
// 5-day lag) with a forecast call (covers the lag plus today).
func backfillLakeTempEstimate(cfg LakeTempEstimateConfig) {
	end := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	start := time.Now().AddDate(-5, 0, 0).Format("2006-01-02")
	log.Printf("[%s] Backfilling water-temp estimate from Open-Meteo (%s to %s)...", cfg.Key, start, end)

	archive, err := fetchOpenMeteoDaily(openMeteoArchiveURL,
		fmt.Sprintf("start_date=%s&end_date=%s", start, end), cfg.Lat, cfg.Lon)
	if err != nil {
		TrackAPICall(cfg.HealthSource, err)
		log.Printf("[%s] archive backfill failed: %v", cfg.Key, err)
		// Don't return — the forecast pull below still covers the recent ~92 days.
	} else {
		TrackAPICall(cfg.HealthSource, nil)
	}

	recent, err := fetchOpenMeteoDaily(openMeteoUVURL,
		fmt.Sprintf("past_days=%d&forecast_days=1", cfg.RecentPastDays), cfg.Lat, cfg.Lon)
	if err != nil {
		TrackAPICall(cfg.HealthSource, err)
		log.Printf("[%s] recent-air backfill failed: %v", cfg.Key, err)
	}

	daily := mergeDailyByDate(archive, recent)
	if len(daily) == 0 {
		log.Printf("[%s] no air-temp data returned; skipping temp-estimate backfill", cfg.Key)
		return
	}

	var added int
	for i := cfg.WindowDays - 1; i < len(daily); i++ {
		mean, ok := trailingMeanDailyMean(daily, i, cfg.WindowDays)
		if !ok {
			continue
		}
		cfg.History.AddWithDate(daily[i].Date, mean)
		added++
	}
	log.Printf("[%s] Backfilled %d estimated water-temp points (%d-day trailing mean of NWS air temp)",
		cfg.Key, added, cfg.WindowDays)
}

// updateLakeTempEstimate refreshes today's estimate using the last
// WindowDays days of air temperatures.
func updateLakeTempEstimate(cfg LakeTempEstimateConfig) {
	daily, err := fetchOpenMeteoDaily(openMeteoUVURL,
		fmt.Sprintf("past_days=%d&forecast_days=1", cfg.WindowDays), cfg.Lat, cfg.Lon)
	TrackAPICall(cfg.HealthSource, err)
	if err != nil {
		log.Printf("[%s] temp-estimate refresh failed: %v", cfg.Key, err)
		return
	}
	if len(daily) < cfg.WindowDays {
		log.Printf("[%s] only %d days of air temp available, need %d for estimate",
			cfg.Key, len(daily), cfg.WindowDays)
		return
	}
	mean, ok := trailingMeanDailyMean(daily, len(daily)-1, cfg.WindowDays)
	if !ok {
		return
	}
	cfg.History.Add(mean)
	log.Printf("[%s] Updated water-temp estimate: %.1f °F (%d-day mean of air temp)",
		cfg.Key, mean, cfg.WindowDays)
}

// lakeTempEstimateLoop refreshes the estimate on PollInterval. Runs
// forever; the initial refresh happens immediately so the dashboard
// has a number to render after a cold start.
func lakeTempEstimateLoop(cfg LakeTempEstimateConfig) {
	updateLakeTempEstimate(cfg)
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for range ticker.C {
		updateLakeTempEstimate(cfg)
	}
}
