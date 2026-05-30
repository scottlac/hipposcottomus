package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Open-Meteo is a free, no-auth aggregator that exposes UV index forecasts
// per lat/lon. We use it for all three water dashboards. ~10k calls/day
// free; 3 dashboards × 48 polls/day = 144, well under.
const openMeteoUVURL = "https://api.open-meteo.com/v1/forecast"

// UVHour is a single hour's forecasted UV index.
type UVHour struct {
	Time     string  `json:"time"`     // ISO 8601, local TZ per location
	UVIndex  float64 `json:"uvIndex"`
}

// UVData is the shape stored alongside each dashboard's WeatherData and
// returned from /api/weather as a "uv" sub-object.
type UVData struct {
	Current   float64  `json:"current"`
	UpdatedAt string   `json:"updatedAt"` // ISO timestamp of the "current" reading
	Hourly    []UVHour `json:"hourly"`    // next 24h, local time per location
}

// openMeteoResponse is the subset of the Open-Meteo forecast API we read.
// All blocks are optional — the API only returns what we ask for.
type openMeteoResponse struct {
	Current struct {
		Time    string  `json:"time"`
		UVIndex float64 `json:"uv_index"`
	} `json:"current"`
	Hourly struct {
		Time    []string  `json:"time"`
		UVIndex []float64 `json:"uv_index"`
	} `json:"hourly"`
}

// fetchUVIndex queries Open-Meteo for the current UV and the next 24h of
// hourly UV. timezone=auto so the hourly timestamps reflect the location's
// civil time (sunrise/sunset align with the user's intuition).
func fetchUVIndex(lat, lon float64) (*UVData, error) {
	url := fmt.Sprintf("%s?latitude=%.4f&longitude=%.4f&current=uv_index&hourly=uv_index&timezone=auto&forecast_days=2",
		openMeteoUVURL, lat, lon)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching UV index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Open-Meteo returned status %d", resp.StatusCode)
	}

	var data openMeteoResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decoding Open-Meteo response: %w", err)
	}

	// Build the next-24h hourly slice starting from the first time that's
	// >= now (Open-Meteo includes earlier hours since midnight by default).
	now := time.Now()
	hourly := make([]UVHour, 0, 24)
	for i, t := range data.Hourly.Time {
		if i >= len(data.Hourly.UVIndex) {
			break
		}
		parsed, err := time.ParseInLocation("2006-01-02T15:04", t, time.Local)
		if err != nil {
			// Best-effort: include it anyway with the raw string.
			hourly = append(hourly, UVHour{Time: t, UVIndex: data.Hourly.UVIndex[i]})
			continue
		}
		if parsed.Before(now.Add(-1 * time.Hour)) {
			continue
		}
		hourly = append(hourly, UVHour{Time: t, UVIndex: data.Hourly.UVIndex[i]})
		if len(hourly) >= 24 {
			break
		}
	}

	return &UVData{
		Current:   data.Current.UVIndex,
		UpdatedAt: data.Current.Time,
		Hourly:    hourly,
	}, nil
}
