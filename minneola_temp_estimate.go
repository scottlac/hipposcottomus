package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Lake Minneola has no real-time USGS water-temperature gauge, and no nearby
// USGS station reports parameter 00010/00011 either. We estimate the surface
// temperature from a 7-day trailing mean of NWS air temperature for the lake
// centroid — a well-established proxy for shallow subtropical lakes that
// tracks the truth within ~2–3 °F year-round. Air-temp history comes from
// Open-Meteo's free archive + forecast endpoints (no key required).

const (
	openMeteoArchiveURL = "https://archive-api.open-meteo.com/v1/archive"

	// 7-day window matches Lake Minneola's ~10 ft average depth — shallow
	// enough to follow short-term swings, deep enough to damp daily noise.
	minneolaTempEstimateWindow = 7

	// The forecast endpoint exposes up to 92 past days. We use this both to
	// cover the archive's ~5-day publication lag and to refresh today's
	// estimate each weather poll.
	minneolaTempRecentPastDays = 92
)

// dailyAirTemp is one calendar day of Tmax/Tmin (°F), keyed by ISO date.
type dailyAirTemp struct {
	Date string
	Hi   float64
	Lo   float64
}

// fetchOpenMeteoDaily pulls daily Tmax/Tmin from one of Open-Meteo's
// endpoints. The archive endpoint requires explicit start/end dates; the
// forecast endpoint takes a past_days count instead. We use the same response
// shape for both.
func fetchOpenMeteoDaily(baseURL string, qs string) ([]dailyAirTemp, error) {
	url := fmt.Sprintf("%s?%s&daily=temperature_2m_max,temperature_2m_min&temperature_unit=fahrenheit&timezone=America/New_York&latitude=28.59&longitude=-81.78",
		baseURL, qs)
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

// minneolaBackfillTempEstimate populates ~5 years of estimated water-temp
// history at startup. Combines the archive endpoint (long history, 5-day
// lag) with a forecast call (covers the lag plus today).
func minneolaBackfillTempEstimate() {
	end := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	start := time.Now().AddDate(-5, 0, 0).Format("2006-01-02")
	log.Printf("[Minneola] Backfilling water-temp estimate from Open-Meteo (%s to %s)...", start, end)

	archive, err := fetchOpenMeteoDaily(openMeteoArchiveURL,
		fmt.Sprintf("start_date=%s&end_date=%s", start, end))
	if err != nil {
		TrackAPICall("OpenMeteo-Minneola-Air", err)
		log.Printf("[Minneola] archive backfill failed: %v", err)
		// Don't return — the forecast pull below still covers the recent ~92 days.
	} else {
		TrackAPICall("OpenMeteo-Minneola-Air", nil)
	}

	recent, err := fetchOpenMeteoDaily(openMeteoUVURL,
		fmt.Sprintf("past_days=%d&forecast_days=1", minneolaTempRecentPastDays))
	if err != nil {
		TrackAPICall("OpenMeteo-Minneola-Air", err)
		log.Printf("[Minneola] recent-air backfill failed: %v", err)
	}

	daily := mergeDailyByDate(archive, recent)
	if len(daily) == 0 {
		log.Println("[Minneola] no air-temp data returned; skipping temp-estimate backfill")
		return
	}

	var added int
	for i := minneolaTempEstimateWindow - 1; i < len(daily); i++ {
		mean, ok := trailingMeanDailyMean(daily, i, minneolaTempEstimateWindow)
		if !ok {
			continue
		}
		minneolaTempHistory.AddWithDate(daily[i].Date, mean)
		added++
	}
	log.Printf("[Minneola] Backfilled %d estimated water-temp points (%d-day trailing mean of NWS air temp)",
		added, minneolaTempEstimateWindow)
}

// updateMinneolaTempEstimate refreshes today's estimate using the last 7
// days of air temperatures. Called every weather poll so the value tracks
// the most recent forecast revisions.
func updateMinneolaTempEstimate() {
	daily, err := fetchOpenMeteoDaily(openMeteoUVURL,
		fmt.Sprintf("past_days=%d&forecast_days=1", minneolaTempEstimateWindow))
	TrackAPICall("OpenMeteo-Minneola-Air", err)
	if err != nil {
		log.Printf("[Minneola] temp-estimate refresh failed: %v", err)
		return
	}
	if len(daily) < minneolaTempEstimateWindow {
		log.Printf("[Minneola] only %d days of air temp available, need %d for estimate",
			len(daily), minneolaTempEstimateWindow)
		return
	}
	mean, ok := trailingMeanDailyMean(daily, len(daily)-1, minneolaTempEstimateWindow)
	if !ok {
		return
	}
	minneolaTempHistory.Add(mean)
	log.Printf("[Minneola] Updated water-temp estimate: %.1f °F (7-day mean of air temp)", mean)
}

// minneolaTempEstimateLoop refreshes the estimate on the weather cadence.
// Runs forever; the initial refresh happens immediately so the dashboard
// has a number to render after a cold start.
func minneolaTempEstimateLoop() {
	updateMinneolaTempEstimate()
	ticker := time.NewTicker(minneolaWeatherPollInt)
	defer ticker.Stop()
	for range ticker.C {
		updateMinneolaTempEstimate()
	}
}
