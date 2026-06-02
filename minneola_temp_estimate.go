package main

// Lake Minneola's water-temperature estimator. The smoothing model and
// Open-Meteo plumbing live in temp_estimate.go.

var minneolaTempEstimateConfig = LakeTempEstimateConfig{
	Key:            "Minneola",
	Lat:            28.59,
	Lon:            -81.78,
	History:        minneolaTempHistory,
	WindowDays:     7,
	HealthSource:   "OpenMeteo-Minneola-Air",
	PollInterval:   minneolaWeatherPollInt,
	RecentPastDays: 92,
}

func minneolaBackfillTempEstimate() { backfillLakeTempEstimate(minneolaTempEstimateConfig) }
func minneolaTempEstimateLoop()     { lakeTempEstimateLoop(minneolaTempEstimateConfig) }
