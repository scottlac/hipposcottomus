package main

// Lake Conway's water-temperature estimator. The smoothing model and
// Open-Meteo plumbing live in temp_estimate.go.

var conwayTempEstimateConfig = LakeTempEstimateConfig{
	Key:            "Conway",
	Lat:            28.47,
	Lon:            -81.35,
	History:        conwayTempHistory,
	WindowDays:     7,
	HealthSource:   "OpenMeteo-Conway-Air",
	PollInterval:   conwayWeatherPollInt,
	RecentPastDays: 92,
}

func conwayBackfillTempEstimate() { backfillLakeTempEstimate(conwayTempEstimateConfig) }
func conwayTempEstimateLoop()     { lakeTempEstimateLoop(conwayTempEstimateConfig) }
