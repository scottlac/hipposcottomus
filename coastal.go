package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Coastal boating dashboards. Every location (Ft. Lauderdale, Key Biscayne,
// Key Largo…) is one coastalLocation value in coastalLocations below; they all
// share this backend and the single frontend in coastal/. Adding a location
// means adding one entry to that slice, plus the route in k8s/manifests.yaml,
// home/index.html, home/sw.js and analytics.go's trackedPaths.

const (
	// NOAA CO-OPS API – Tides & Currents
	noaaBaseURL = "https://api.tidesandcurrents.noaa.gov/api/prod/datagetter"

	// NDBC realtime observations (used where no CO-OPS sensor is nearby)
	ndbcRealtimeURLFmt = "https://www.ndbc.noaa.gov/data/realtime2/%s.txt"

	nwsPointsURLFmt = "https://api.weather.gov/points/%.4f,%.4f"

	coastalUserAgent    = "(hipposcottomus-boating-dashboard, contact@hipposcottomus.com)"
	coastalPollInterval = 30 * time.Minute

	// Sensor readings older than this are treated as missing rather than
	// shown as current.
	coastalObsMaxAge = 3 * time.Hour
)

//go:embed coastal
var coastalFiles embed.FS

// ── Data types ──────────────────────────────────────────────────

type TideEvent struct {
	Time   string  `json:"time"` // RFC 3339, in the location's time zone
	Height float64 `json:"height"`
	Type   string  `json:"type"` // "High" or "Low"
}

type TidePoint struct {
	Time   string  `json:"time"` // RFC 3339, in the location's time zone
	Height float64 `json:"height"`
}

// WindObs is a live anemometer reading from a CO-OPS station.
type WindObs struct {
	Knots     float64 `json:"knots"`
	GustKnots float64 `json:"gustKnots"`
	Direction string  `json:"direction"` // compass point, e.g. "ENE"
	Degrees   float64 `json:"degrees"`
	Time      string  `json:"time"`   // RFC 3339
	Source    string  `json:"source"` // station name
}

// LevelPoint is one observed-minus-predicted water level sample.
type LevelPoint struct {
	Time   string  `json:"time"` // RFC 3339
	Offset float64 `json:"offset"`
}

// WaterLevel compares a tide gauge's observed water level with its own
// prediction. Wind and pressure push real water levels off the astronomical
// prediction, and that offset is much the same across nearby water — so it can
// be applied to the predictions for a location that has no gauge of its own.
type WaterLevel struct {
	Offset    float64      `json:"offset"`    // latest observed − predicted, ft
	Observed  float64      `json:"observed"`  // at the gauge, ft MLLW
	Predicted float64      `json:"predicted"` // at the gauge, ft MLLW
	Time      string       `json:"time"`
	Source    string       `json:"source"` // gauge name
	Series    []LevelPoint `json:"series"` // last 12 hours
	Flood     *FloodLevels `json:"flood,omitempty"`
}

// FloodLevels are a gauge's flood thresholds in ft above its MLLW, so they
// compare directly with WaterLevel.Observed. A nil level is not defined for
// the gauge.
type FloodLevels struct {
	Minor    *float64 `json:"minor,omitempty"`
	Moderate *float64 `json:"moderate,omitempty"`
	Major    *float64 `json:"major,omitempty"`
}

// NWSAlert is an active NWS watch, warning or advisory for the location's
// point (Coastal Flood Advisory, High Surf Advisory, Rip Current Statement…).
type NWSAlert struct {
	Event       string `json:"event"`
	Severity    string `json:"severity"`
	Headline    string `json:"headline"`
	Description string `json:"description"`
	Instruction string `json:"instruction,omitempty"`
	Onset       string `json:"onset,omitempty"` // RFC 3339
	Ends        string `json:"ends,omitempty"`  // RFC 3339; "until further notice" when empty
}

// marineZoneConfig is one NWS marine zone shown on a dashboard.
type marineZoneConfig struct {
	Zone string // "AMZ630"
	Name string // heading shown to users
}

// marineZoneData is a zone's config plus its latest parsed forecast.
type marineZoneData struct {
	Name string `json:"name"`
	*MarineForecast
}

// coastalLocation is the static configuration and live state of one coastal
// dashboard.
type coastalLocation struct {
	// ── Identity ──
	Key      string // short id used in logs and analytics labels
	BasePath string // "/keybiscayne"
	Name     string // "Key Biscayne"
	Place    string // subtitle locality, e.g. "Biscayne Channel, FL"
	OGBlurb  string // link-preview description

	// Title and Icon head the page; empty means "<Name> Boating Dashboard"
	// and an anchor.
	Title string
	Icon  string

	// ── Data sources ──
	Lat, Lon float64 // NWS forecast grid point and UV lookup (must be on land)

	TideStation     string // CO-OPS tide prediction station id
	TideStationName string

	// CO-OPS station with live sensors (water temperature, wind, water level).
	// Empty if none is close enough to be representative.
	ObsStation     string
	ObsStationName string

	// NDBC station used for water temperature when ObsStation is empty.
	BuoyStation     string
	BuoyStationName string

	MarineProduct  string             // NDBC forecast page, e.g. "FZUS52.KMFL"
	MarineSynopsis string             // zone code of that page's synopsis block
	MarineZones    []marineZoneConfig // first is the primary (most local) zone

	// About is the location-specific section of the AI summary prompt.
	About string

	// trackLabels overrides analytics labels (Ft. Lauderdale keeps the names
	// it had before the other locations existed, so its history carries over).
	trackLabels map[string]string

	// ── Live state ──
	mu              sync.RWMutex
	tideHiLo        []TideEvent
	tideCurve       []TidePoint
	waterTemp       *float64
	waterTempAt     time.Time
	waterTempSource string
	wind            *WindObs
	level           *WaterLevel
	flood           *FloodLevels // thresholds at ObsStation; fetched once
	alerts          []NWSAlert
	forecast        []WeatherPeriod
	hourly          []WeatherPeriod
	uv              *UVData
	marine          []marineZoneData
	marineSynopsis  string

	// NWS grid URLs, discovered lazily from Lat/Lon.
	forecastURL string
	hourlyURL   string

	indexHTML []byte
	blurb     *lakeBlurbConfig
}

var coastalLocations = []*coastalLocation{
	{
		Key:      "ftl",
		BasePath: "/ftlauderdale",
		Name:     "Ft. Lauderdale",
		Place:    "Bahia Mar, FL",
		OGBlurb:  "Tide predictions, water temperature, live wind, and the marine forecast for boating off Fort Lauderdale, FL.",
		Lat:      26.1133, Lon: -80.1083,
		TideStation:     "8722939",
		TideStationName: "Bahia Mar Yacht Club",
		ObsStation:      "8722956",
		ObsStationName:  "South Port Everglades",
		MarineProduct:   "FZUS52.KMFL",
		MarineSynopsis:  "AMZ600",
		MarineZones: []marineZoneConfig{
			{Zone: "AMZ651", Name: "Atlantic — Deerfield Beach to Ocean Reef, out 20 NM"},
		},
		About: `Fort Lauderdale, FL. Tide predictions are for Bahia Mar Yacht Club on the Intracoastal Waterway just inside Port Everglades Inlet; water temperature and live wind come from the South Port Everglades station about two miles south. Boats here run the Intracoastal and New River, and go out Port Everglades Inlet or Hillsboro Inlet into the Atlantic, where the Gulf Stream is often only a few miles offshore. Tidal range is roughly 2–3 ft. Current runs hard through the inlets, and an outgoing (falling) tide against onshore easterly wind stacks up steep seas at the inlet mouth. The marine forecast zone is the Atlantic coastal waters from Deerfield Beach to Ocean Reef out 20 NM.`,
		trackLabels: map[string]string{
			"NOAA-Tides":     "NOAA-Tides",
			"NOAA-WaterTemp": "NOAA-WaterTemp",
			"NWS":            "NWS-Miami",
			"NDBC-Marine":    "NDBC-Marine",
		},
	},
	{
		Key:      "keybiscayne",
		BasePath: "/keybiscayne",
		Name:     "Key Biscayne",
		Place:    "Biscayne Channel, FL",
		OGBlurb:  "Tide predictions, water temperature, live wind, and the marine forecast for boating around Key Biscayne and Biscayne Bay, FL.",
		Lat:      25.6937, Lon: -80.1628,
		TideStation:     "8723251",
		TideStationName: "Coral Shoal, Biscayne Channel",
		ObsStation:      "8723214",
		ObsStationName:  "Virginia Key",
		MarineProduct:   "FZUS52.KMFL",
		MarineSynopsis:  "AMZ600",
		MarineZones: []marineZoneConfig{
			{Zone: "AMZ630", Name: "Biscayne Bay"},
			{Zone: "AMZ651", Name: "Atlantic — Deerfield Beach to Ocean Reef, out 20 NM"},
		},
		About: `Key Biscayne, FL. Tide predictions are for Coral Shoal in Biscayne Channel, the marked channel through the Safety Valve flats south of Cape Florida that connects Biscayne Bay to the ocean (it passes Stiltsville). Water temperature and live wind come from the Virginia Key station a few miles north. Boats here work the bay, the flats, and run outside to the reef line and the Gulf Stream. Tidal range is only about 1.5–2 ft, but the flats either side of the channels are very shallow, so low water matters for anyone leaving the marked channels, and the channels carry noticeable current mid-tide. Two marine zones are given: Biscayne Bay (protected; described as chop, not seas) and the Atlantic coastal waters outside (seas in feet). Say which one you mean when they differ — a breezy day can be fine in the bay and rough outside.`,
	},
	{
		Key:      "keylargo",
		BasePath: "/keylargo",
		Name:     "Key Largo",
		Place:    "Largo Sound, FL",
		OGBlurb:  "Tide predictions, water temperature, and the Hawk Channel marine forecast for boating out of Key Largo, FL.",
		Lat:      25.1244, Lon: -80.4116,
		TideStation:     "8723651",
		TideStationName: "Largo Sound, Key Largo",
		BuoyStation:     "BWSF1",
		BuoyStationName: "Blackwater Sound",
		MarineProduct:   "FZUS52.KKEY",
		MarineSynopsis:  "GMZ005",
		MarineZones: []marineZoneConfig{
			{Zone: "GMZ042", Name: "Hawk Channel — Ocean Reef to Craig Key, out to the reef"},
			{Zone: "GMZ052", Name: "Straits of Florida — Ocean Reef to Craig Key, out 20 NM"},
			{Zone: "GMZ031", Name: "Florida Bay — Barnes, Blackwater and Buttonwood Sounds"},
		},
		About: `Key Largo, FL (Upper Keys, ocean side). Tide predictions are for Largo Sound, the enclosed sound inside John Pennekamp Coral Reef State Park that reaches the ocean through the narrow South Sound Creek and North Sound Creek. Tidal range inside the sound is very small — well under 1 ft — so tide height rarely matters for depth; what matters is the current in the creeks and timing. Water temperature is from Blackwater Sound on the bay side of the island, a shallow sound that runs a little warmer in summer and cooler after winter cold fronts than the reef. There is no live wind sensor nearby, so wind is forecast only. Boats here run out through the creeks into Hawk Channel and on to the reef (Molasses, Grecian Rocks, the Christ of the Abyss at Key Largo Dry Rocks) for diving, snorkeling and fishing. Three marine zones are given: Hawk Channel (ocean side, inside the reef, somewhat protected), the Straits of Florida (outside the reef, open water), and Florida Bay (the bay side: Barnes, Blackwater and Buttonwood Sounds — shallow, protected, reached from the ocean side through cuts such as Adams Cut and Jewfish Creek). Lead with the ocean side, since that is where most trips from Largo Sound go; mention the bay when it is the better option for the day, such as when the ocean side is rough. The forecast text covers a long stretch of the Keys; where it says "off the Upper Keys", that clause is the one that applies here. Dive and snorkel trips care most about seas at the reef and thunderstorms.`,
	},
	{
		Key:      "northtopsail",
		BasePath: "/northtopsail",
		Name:     "North Topsail Beach",
		Title:    "North Topsail Beach Dashboard",
		Icon:     "🏖️",
		Place:    "Topsail Island, NC",
		OGBlurb:  "Tides, observed water level against flood levels, NWS coastal alerts, water temperature, wind, and the marine forecast for North Topsail Beach, NC.",
		Lat:      34.4896, Lon: -77.4325,
		TideStation:     "8657419",
		TideStationName: "Ocean City Beach fishing pier",
		ObsStation:      "8658163",
		ObsStationName:  "Wrightsville Beach",
		MarineProduct:   "FZUS52.KMHX",
		MarineSynopsis:  "AMZ100",
		MarineZones: []marineZoneConfig{
			{Zone: "AMZ158", Name: "Atlantic — Cape Lookout to Surf City, out 20 NM"},
		},
		About: `North Topsail Beach, NC: the town on the northern part of Topsail Island, a narrow barrier island in Onslow County. The ocean beach faces southeast onto Onslow Bay; New River Inlet is at the north end of the island, with Topsail Sound and the Intracoastal Waterway behind it. Most people reading this dashboard are staying on the island: walking the beach, surf fishing from the beach and the piers, surfing, and swimming in season; some run boats out of New River Inlet. Tide predictions are for the Ocean City Beach fishing pier on the ocean front at the southern end of town; at that pier the average high tide is about 4.3 ft and the average low about 0.2 ft above MLLW. There is no gauge on the island: observed water level, water temperature and live wind come from the Wrightsville Beach station on the ocean about 28 miles southwest, so treat them as indicative rather than exact here. Onshore northeast to east winds and coastal storms push water up on this coast; a high tide running well above predictions here means a narrow or vanished beach at high tide, waves reaching the dunes, ocean overwash, and flooding of low spots along the island's main road and the sound side. For this location, put any coastal flood, high surf, rip current or storm alert first, and say what it means for someone on the beach or driving the island, not just for boats.`,
	},
}

// title is the page heading.
func (loc *coastalLocation) title() string {
	if loc.Title != "" {
		return loc.Title
	}
	return loc.Name + " Boating Dashboard"
}

func (loc *coastalLocation) icon() string {
	if loc.Icon != "" {
		return loc.Icon
	}
	return "⚓"
}

// trackLabel returns the analytics source label for one kind of fetch.
func (loc *coastalLocation) trackLabel(kind string) string {
	if l, ok := loc.trackLabels[kind]; ok {
		return l
	}
	return kind + "-" + loc.Key
}

func (loc *coastalLocation) logf(format string, args ...any) {
	log.Printf("[Coastal-"+loc.Key+"] "+format, args...)
}

// ── Tides ───────────────────────────────────────────────────────

type noaaPredictionsResp struct {
	Predictions []struct {
		T    string `json:"t"`    // "2026-04-17 00:00"
		V    string `json:"v"`    // "1.234"
		Type string `json:"type"` // "H" or "L"
	} `json:"predictions"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// parseNOAATime parses CO-OPS's "2026-04-17 09:03" timestamps. All requests
// ask for time_zone=gmt, so the result is unambiguous regardless of the
// server's own zone or DST.
func parseNOAATime(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04", s, time.UTC)
}

// localStamp formats t as RFC 3339 in the dashboards' time zone (all current
// locations are in Florida's Eastern zone).
func localStamp(t time.Time) string {
	return t.In(etLocation).Format(time.RFC3339)
}

// fetchTideHiLo returns the high/low predictions for a station from 12 hours
// ago to three days out. These stations are "subordinate" stations: NOAA
// publishes only their high/low times and heights, not a continuous curve.
func fetchTideHiLo(station string) ([]TideEvent, error) {
	now := time.Now().UTC()
	url := fmt.Sprintf("%s?station=%s&product=predictions&datum=MLLW&time_zone=gmt&units=english&interval=hilo&format=json&begin_date=%s&end_date=%s",
		noaaBaseURL, station,
		now.Add(-12*time.Hour).Format("20060102"),
		now.Add(72*time.Hour).Format("20060102"))

	var data noaaPredictionsResp
	if err := coastalGetJSON(url, &data); err != nil {
		return nil, err
	}
	if data.Error != nil {
		return nil, fmt.Errorf("NOAA: %s", data.Error.Message)
	}
	return parseTideHiLo(data), nil
}

func parseTideHiLo(data noaaPredictionsResp) []TideEvent {
	events := make([]TideEvent, 0, len(data.Predictions))
	for _, p := range data.Predictions {
		v, err := strconv.ParseFloat(p.V, 64)
		if err != nil {
			continue
		}
		t, err := parseNOAATime(p.T)
		if err != nil {
			continue
		}
		typ := "High"
		if p.Type == "L" {
			typ = "Low"
		}
		events = append(events, TideEvent{Time: localStamp(t), Height: v, Type: typ})
	}
	return events
}

// tideCosine is the height at fraction frac (0–1) of the way from one tide
// extreme to the next: a half cosine, which is how a tide actually moves
// between slack waters (slow near high/low, fastest mid-tide).
func tideCosine(h0, h1, frac float64) float64 {
	return h0 + (h1-h0)*(1-math.Cos(frac*math.Pi))/2
}

// interpolateTideCurve generates a smooth curve between hi/lo events.
func interpolateTideCurve(events []TideEvent) []TidePoint {
	var points []TidePoint
	step := 10 * time.Minute

	for i := 0; i < len(events)-1; i++ {
		t0, err0 := time.Parse(time.RFC3339, events[i].Time)
		t1, err1 := time.Parse(time.RFC3339, events[i+1].Time)
		if err0 != nil || err1 != nil {
			continue
		}
		duration := t1.Sub(t0).Seconds()
		if duration <= 0 {
			continue
		}
		for t := t0; t.Before(t1); t = t.Add(step) {
			h := tideCosine(events[i].Height, events[i+1].Height, t.Sub(t0).Seconds()/duration)
			points = append(points, TidePoint{
				Time:   localStamp(t),
				Height: math.Round(h*1000) / 1000,
			})
		}
	}

	if len(events) > 0 {
		last := events[len(events)-1]
		points = append(points, TidePoint{Time: last.Time, Height: last.Height})
	}
	return points
}

// tideState describes the tide at one instant, derived from hi/lo events.
type tideState struct {
	Height float64
	Rising bool
	Next   TideEvent // the upcoming high or low
}

// tideAt returns the interpolated tide state at now, or ok=false when now is
// outside the span of the events.
func tideAt(events []TideEvent, now time.Time) (tideState, bool) {
	for i := 0; i < len(events)-1; i++ {
		t0, err0 := time.Parse(time.RFC3339, events[i].Time)
		t1, err1 := time.Parse(time.RFC3339, events[i+1].Time)
		if err0 != nil || err1 != nil || !t1.After(t0) {
			continue
		}
		if now.Before(t0) || !now.Before(t1) {
			continue
		}
		frac := now.Sub(t0).Seconds() / t1.Sub(t0).Seconds()
		return tideState{
			Height: tideCosine(events[i].Height, events[i+1].Height, frac),
			Rising: events[i+1].Height > events[i].Height,
			Next:   events[i+1],
		}, true
	}
	return tideState{}, false
}

func (loc *coastalLocation) updateTides() {
	events, err := fetchTideHiLo(loc.TideStation)
	TrackAPICall(loc.trackLabel("NOAA-Tides"), err)
	if err != nil {
		loc.logf("tide fetch error: %v", err)
		return
	}
	if len(events) < 2 {
		loc.logf("tide fetch returned only %d events; keeping previous data", len(events))
		return
	}
	curve := interpolateTideCurve(events)

	loc.mu.Lock()
	loc.tideHiLo = events
	loc.tideCurve = curve
	loc.mu.Unlock()
	loc.logf("updated %d hi/lo tide events (%d curve points)", len(events), len(curve))
}

// ── Live observations ───────────────────────────────────────────

type coopsObs struct {
	T  string `json:"t"`
	V  string `json:"v"`  // scalar products (water_temperature…)
	S  string `json:"s"`  // wind speed, knots with units=english
	D  string `json:"d"`  // wind direction, degrees true
	DR string `json:"dr"` // wind direction, compass point
	G  string `json:"g"`  // gust, knots
}

type coopsObsResp struct {
	Data  []coopsObs `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// fetchCOOPSLatest returns a station's most recent reading for one product,
// rejecting readings older than coastalObsMaxAge (sensors do go offline for
// weeks at a time and the API keeps returning the last value).
func fetchCOOPSLatest(station, product string) (coopsObs, time.Time, error) {
	url := fmt.Sprintf("%s?station=%s&product=%s&time_zone=gmt&units=english&format=json&date=latest",
		noaaBaseURL, station, product)
	var data coopsObsResp
	if err := coastalGetJSON(url, &data); err != nil {
		return coopsObs{}, time.Time{}, err
	}
	if data.Error != nil {
		return coopsObs{}, time.Time{}, fmt.Errorf("NOAA: %s", data.Error.Message)
	}
	if len(data.Data) == 0 {
		return coopsObs{}, time.Time{}, fmt.Errorf("no data returned")
	}
	obs := data.Data[len(data.Data)-1]
	at, err := parseNOAATime(obs.T)
	if err != nil {
		return coopsObs{}, time.Time{}, fmt.Errorf("bad timestamp %q", obs.T)
	}
	if age := time.Since(at); age > coastalObsMaxAge {
		return coopsObs{}, time.Time{}, fmt.Errorf("latest reading is %s old", age.Round(time.Minute))
	}
	return obs, at, nil
}

// parseNDBCWaterTemp reads the newest water temperature (°F) from an NDBC
// realtime2 text file. The file is newest-first, whitespace-delimited, with
// two "#" header lines and "MM" for missing values; times are UTC.
func parseNDBCWaterTemp(text string) (float64, time.Time, error) {
	col := -1
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.HasPrefix(fields[0], "#") {
			for i, f := range fields {
				if f == "WTMP" {
					col = i
				}
			}
			continue
		}
		if col < 0 {
			return 0, time.Time{}, fmt.Errorf("no WTMP column in header")
		}
		if len(fields) <= col || len(fields) < 5 || fields[col] == "MM" {
			continue
		}
		c, err := strconv.ParseFloat(fields[col], 64)
		if err != nil {
			continue
		}
		at, err := time.Parse("2006 01 02 15 04", strings.Join(fields[:5], " "))
		if err != nil {
			continue
		}
		return c*9/5 + 32, at, nil
	}
	return 0, time.Time{}, fmt.Errorf("no water temperature readings")
}

func fetchNDBCWaterTemp(station string) (float64, time.Time, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf(ndbcRealtimeURLFmt, station), nil)
	if err != nil {
		return 0, time.Time{}, err
	}
	req.Header.Set("User-Agent", coastalUserAgent)
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, time.Time{}, fmt.Errorf("NDBC returned status %d", resp.StatusCode)
	}
	// The file holds 45 days of readings, newest first; the head is plenty.
	head, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return 0, time.Time{}, err
	}
	f, at, err := parseNDBCWaterTemp(string(head))
	if err != nil {
		return 0, time.Time{}, err
	}
	if age := time.Since(at); age > coastalObsMaxAge {
		return 0, time.Time{}, fmt.Errorf("latest reading is %s old", age.Round(time.Minute))
	}
	return f, at, nil
}

func (loc *coastalLocation) updateObservations() {
	var (
		temp   float64
		tempAt time.Time
		source string
		err    error
	)
	switch {
	case loc.ObsStation != "":
		var obs coopsObs
		obs, tempAt, err = fetchCOOPSLatest(loc.ObsStation, "water_temperature")
		if err == nil {
			temp, err = strconv.ParseFloat(obs.V, 64)
		}
		source = loc.ObsStationName
	case loc.BuoyStation != "":
		temp, tempAt, err = fetchNDBCWaterTemp(loc.BuoyStation)
		source = loc.BuoyStationName
	default:
		return
	}
	TrackAPICall(loc.trackLabel("NOAA-WaterTemp"), err)

	loc.mu.Lock()
	if err != nil {
		// Don't keep showing a reading once it has aged out.
		if loc.waterTemp != nil && time.Since(loc.waterTempAt) > coastalObsMaxAge {
			loc.waterTemp = nil
		}
	} else {
		loc.waterTemp = &temp
		loc.waterTempAt = tempAt
		loc.waterTempSource = source
	}
	loc.mu.Unlock()
	if err != nil {
		loc.logf("water temp error: %v", err)
	} else {
		loc.logf("updated water temp: %.1f°F (%s)", temp, source)
	}

	if loc.ObsStation == "" {
		return
	}
	wind, werr := loc.fetchWind()
	TrackAPICall(loc.trackLabel("NOAA-Wind"), werr)
	loc.mu.Lock()
	loc.wind = wind // nil on error: fall back to the forecast rather than show stale wind
	loc.mu.Unlock()
	if werr != nil {
		loc.logf("wind observation error: %v", werr)
	}

	loc.mu.RLock()
	flood := loc.flood
	loc.mu.RUnlock()
	if flood == nil {
		var ferr error
		flood, ferr = fetchFloodLevels(loc.ObsStation)
		if ferr != nil {
			loc.logf("flood levels error: %v", ferr) // retried next poll
		} else {
			loc.mu.Lock()
			loc.flood = flood
			loc.mu.Unlock()
		}
	}

	level, lerr := fetchWaterLevel(loc.ObsStation, loc.ObsStationName)
	TrackAPICall(loc.trackLabel("NOAA-WaterLevel"), lerr)
	if level != nil && flood != nil && (flood.Minor != nil || flood.Moderate != nil || flood.Major != nil) {
		level.Flood = flood
	}
	loc.mu.Lock()
	loc.level = level // nil on error, as with wind
	loc.mu.Unlock()
	if lerr != nil {
		loc.logf("water level error: %v", lerr)
	} else {
		loc.logf("water level is %+.2f ft vs predicted at %s", level.Offset, level.Source)
	}
}

// levelOffsets pairs each observed water level with the gauge's prediction for
// the same instant and returns observed − predicted for every match.
func levelOffsets(observed, predicted []coopsObs) []LevelPoint {
	pred := make(map[string]float64, len(predicted))
	for _, p := range predicted {
		if v, err := strconv.ParseFloat(p.V, 64); err == nil {
			pred[p.T] = v
		}
	}
	var out []LevelPoint
	for _, o := range observed {
		v, err := strconv.ParseFloat(o.V, 64) // gaps in the record have an empty value
		if err != nil {
			continue
		}
		p, ok := pred[o.T]
		if !ok {
			continue
		}
		t, err := parseNOAATime(o.T)
		if err != nil {
			continue
		}
		out = append(out, LevelPoint{Time: localStamp(t), Offset: math.Round((v-p)*100) / 100})
	}
	return out
}

// fetchWaterLevel reads the last 12 hours of observed and predicted water
// level at a gauge.
func fetchWaterLevel(station, name string) (*WaterLevel, error) {
	base := fmt.Sprintf("%s?station=%s&datum=MLLW&time_zone=gmt&units=english&format=json&range=12", noaaBaseURL, station)

	var obs coopsObsResp
	if err := coastalGetJSON(base+"&product=water_level", &obs); err != nil {
		return nil, err
	}
	if obs.Error != nil {
		return nil, fmt.Errorf("NOAA: %s", obs.Error.Message)
	}
	var pred struct {
		Predictions []coopsObs `json:"predictions"`
		Error       *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := coastalGetJSON(base+"&product=predictions", &pred); err != nil {
		return nil, err
	}
	if pred.Error != nil {
		return nil, fmt.Errorf("NOAA: %s", pred.Error.Message)
	}

	series := levelOffsets(obs.Data, pred.Predictions)
	if len(series) == 0 {
		return nil, fmt.Errorf("no overlapping observed and predicted water levels")
	}
	last := series[len(series)-1]
	at, _ := time.Parse(time.RFC3339, last.Time)
	if age := time.Since(at); age > coastalObsMaxAge {
		return nil, fmt.Errorf("latest water level is %s old", age.Round(time.Minute))
	}
	// Recover the latest observed/predicted pair for display.
	wl := &WaterLevel{Offset: last.Offset, Time: last.Time, Source: name, Series: series}
	for i := len(obs.Data) - 1; i >= 0; i-- {
		if t, err := parseNOAATime(obs.Data[i].T); err == nil && t.Equal(at) {
			wl.Observed, _ = strconv.ParseFloat(obs.Data[i].V, 64)
			wl.Predicted = math.Round((wl.Observed-last.Offset)*100) / 100
			break
		}
	}
	return wl, nil
}

// fetchFloodLevels returns a gauge's NWS flood thresholds (falling back to
// NOAA's where NWS defines none), converted from the station datum the API
// reports them in to ft above MLLW.
func fetchFloodLevels(station string) (*FloodLevels, error) {
	// The response also carries non-numeric fields ("self"), so decode the
	// thresholds by name.
	var levels struct {
		NWSMinor    *float64 `json:"nws_minor"`
		NWSModerate *float64 `json:"nws_moderate"`
		NWSMajor    *float64 `json:"nws_major"`
		NOSMinor    *float64 `json:"nos_minor"`
		NOSModerate *float64 `json:"nos_moderate"`
		NOSMajor    *float64 `json:"nos_major"`
	}
	url := fmt.Sprintf("https://api.tidesandcurrents.noaa.gov/mdapi/prod/webapi/stations/%s/floodlevels.json?units=english", station)
	if err := coastalGetJSON(url, &levels); err != nil {
		return nil, err
	}
	raw := map[string]*float64{
		"nws_minor": levels.NWSMinor, "nws_moderate": levels.NWSModerate, "nws_major": levels.NWSMajor,
		"nos_minor": levels.NOSMinor, "nos_moderate": levels.NOSModerate, "nos_major": levels.NOSMajor,
	}
	var datums struct {
		Datums []stationDatum `json:"datums"`
	}
	url = fmt.Sprintf("https://api.tidesandcurrents.noaa.gov/mdapi/prod/webapi/stations/%s/datums.json?units=english", station)
	if err := coastalGetJSON(url, &datums); err != nil {
		return nil, err
	}
	return floodLevelsMLLW(raw, datums.Datums)
}

// stationDatum is one entry of a CO-OPS station's datums list.
type stationDatum struct {
	Name  string   `json:"name"`
	Value *float64 `json:"value"`
}

// floodLevelsMLLW converts floodlevels.json values (station datum) to ft
// above MLLW.
func floodLevelsMLLW(raw map[string]*float64, datums []stationDatum) (*FloodLevels, error) {
	var mllw *float64
	for _, d := range datums {
		if d.Name == "MLLW" {
			mllw = d.Value
		}
	}
	if mllw == nil {
		return nil, fmt.Errorf("station has no MLLW datum")
	}
	pick := func(level string) *float64 {
		for _, src := range []string{"nws_", "nos_"} {
			if v := raw[src+level]; v != nil {
				x := math.Round((*v-*mllw)*100) / 100
				return &x
			}
		}
		return nil
	}
	fl := &FloodLevels{Minor: pick("minor"), Moderate: pick("moderate"), Major: pick("major")}
	if fl.Minor == nil && fl.Moderate == nil && fl.Major == nil {
		return &FloodLevels{}, nil // defined for no level: remember that, don't refetch
	}
	return fl, nil
}

// alertsResponse is the part of api.weather.gov/alerts/active we read.
type alertsResponse struct {
	Features []struct {
		Properties struct {
			Event       string `json:"event"`
			Severity    string `json:"severity"`
			Status      string `json:"status"`
			Headline    string `json:"headline"`
			Description string `json:"description"`
			Instruction string `json:"instruction"`
			Onset       string `json:"onset"`
			Ends        string `json:"ends"`
			Expires     string `json:"expires"`
		} `json:"properties"`
	} `json:"features"`
}

// fetchAlerts returns the NWS alerts in effect at a point.
func fetchAlerts(lat, lon float64) ([]NWSAlert, error) {
	var resp alertsResponse
	url := fmt.Sprintf("https://api.weather.gov/alerts/active?point=%.4f,%.4f", lat, lon)
	if err := coastalGetJSON(url, &resp); err != nil {
		return nil, err
	}
	return resp.alerts(), nil
}

// alerts keeps the real (not test or exercise) alerts, one per event: an
// update reissues the same event, and the API lists the newest first.
func (resp alertsResponse) alerts() []NWSAlert {
	alerts := []NWSAlert{}
	seen := map[string]bool{}
	defer func() {
		// Alerts about the water's edge first; the rest (wind, rain, heat…)
		// after, each group in the API's order.
		sort.SliceStable(alerts, func(i, j int) bool {
			return isCoastalAlert(alerts[i].Event) && !isCoastalAlert(alerts[j].Event)
		})
	}()
	for _, f := range resp.Features {
		p := f.Properties
		if p.Status != "Actual" || p.Event == "" || seen[p.Event] {
			continue
		}
		seen[p.Event] = true
		ends := p.Ends
		if ends == "" {
			ends = p.Expires
		}
		alerts = append(alerts, NWSAlert{
			Event:       p.Event,
			Severity:    p.Severity,
			Headline:    p.Headline,
			Description: tidyAlertText(p.Description),
			Instruction: tidyAlertText(p.Instruction),
			Onset:       p.Onset,
			Ends:        ends,
		})
	}
	return alerts
}

// isCoastalAlert reports whether an NWS event is about the coast or the
// water: coastal flooding, surf, rip currents, storm surge, tropical systems,
// marine and beach hazards.
func isCoastalAlert(event string) bool {
	e := strings.ToLower(event)
	for _, k := range []string{"coastal", "surf", "rip current", "storm surge", "hurricane", "tropical", "marine", "beach", "tsunami", "lakeshore"} {
		if strings.Contains(e, k) {
			return true
		}
	}
	return false
}

// tidyAlertText unwraps the NWS's fixed-width lines while keeping paragraph
// breaks.
func tidyAlertText(s string) string {
	var paras []string
	for _, p := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n\n") {
		if t := strings.TrimSpace(marineSpaceRe.ReplaceAllString(p, " ")); t != "" {
			paras = append(paras, t)
		}
	}
	return strings.Join(paras, "\n\n")
}

func (loc *coastalLocation) updateAlerts() {
	alerts, err := fetchAlerts(loc.Lat, loc.Lon)
	TrackAPICall(loc.trackLabel("NWS-Alerts"), err)
	if err != nil {
		loc.logf("alerts error: %v", err) // keep the last list rather than blank it
		return
	}
	loc.mu.Lock()
	loc.alerts = alerts
	loc.mu.Unlock()
	if len(alerts) > 0 {
		names := make([]string, len(alerts))
		for i, a := range alerts {
			names[i] = a.Event
		}
		loc.logf("active alerts: %s", strings.Join(names, ", "))
	}
}

func (loc *coastalLocation) fetchWind() (*WindObs, error) {
	obs, at, err := fetchCOOPSLatest(loc.ObsStation, "wind")
	if err != nil {
		return nil, err
	}
	speed, err := strconv.ParseFloat(obs.S, 64)
	if err != nil {
		return nil, fmt.Errorf("bad wind speed %q", obs.S)
	}
	gust, _ := strconv.ParseFloat(obs.G, 64)
	deg, _ := strconv.ParseFloat(obs.D, 64)
	return &WindObs{
		Knots:     speed,
		GustKnots: gust,
		Direction: obs.DR,
		Degrees:   deg,
		Time:      localStamp(at),
		Source:    loc.ObsStationName,
	}, nil
}

// ── NWS weather ─────────────────────────────────────────────────

func (loc *coastalLocation) discoverNWSGrid() error {
	var pts struct {
		Properties struct {
			Forecast       string `json:"forecast"`
			ForecastHourly string `json:"forecastHourly"`
		} `json:"properties"`
	}
	if err := coastalGetJSON(fmt.Sprintf(nwsPointsURLFmt, loc.Lat, loc.Lon), &pts); err != nil {
		return err
	}
	if pts.Properties.Forecast == "" || pts.Properties.ForecastHourly == "" {
		return fmt.Errorf("NWS points response had no forecast URLs")
	}
	loc.forecastURL = pts.Properties.Forecast
	loc.hourlyURL = pts.Properties.ForecastHourly
	loc.logf("NWS grid discovered: %s", loc.forecastURL)
	return nil
}

func (loc *coastalLocation) updateWeather() {
	if loc.forecastURL == "" {
		if err := loc.discoverNWSGrid(); err != nil {
			TrackAPICall(loc.trackLabel("NWS"), err)
			loc.logf("NWS grid lookup failed: %v", err)
		}
	}

	var forecast, hourly []WeatherPeriod
	if loc.forecastURL != "" {
		var err error
		forecast, err = fetchNWSForecast(loc.forecastURL)
		TrackAPICall(loc.trackLabel("NWS"), err)
		if err != nil {
			loc.logf("forecast error: %v", err)
		}
		hourly, err = fetchNWSForecast(loc.hourlyURL)
		if err != nil {
			loc.logf("hourly error: %v", err)
		}
	}

	uv, uvErr := fetchUVIndex(loc.Lat, loc.Lon)
	TrackAPICall("OpenMeteo-UV", uvErr)
	if uvErr != nil {
		loc.logf("UV index error: %v", uvErr)
	}

	loc.mu.Lock()
	if forecast != nil {
		loc.forecast = forecast
	}
	if hourly != nil {
		if len(hourly) > 24 {
			hourly = hourly[:24]
		}
		loc.hourly = hourly
	}
	if uv != nil {
		loc.uv = uv
	}
	loc.mu.Unlock()
}

// ── Marine forecast ─────────────────────────────────────────────

// marineSource is one way of obtaining and reading the forecast product.
type marineSource struct {
	label    string // analytics label kind
	fetch    func() (string, error)
	zone     func(doc, zone string) *MarineForecast
	synopsis func(doc, zone string) string
}

// marineOffice is the NWS office id for the location's product:
// "FZUS52.KMFL" → "MFL".
func (loc *coastalLocation) marineOffice() string {
	if n := len(loc.MarineProduct); n >= 3 {
		return loc.MarineProduct[n-3:]
	}
	return ""
}

// readMarine fetches from one source and parses every configured zone.
func (loc *coastalLocation) readMarine(src marineSource) ([]marineZoneData, string, error) {
	doc, err := src.fetch()
	if err != nil {
		return nil, "", err
	}
	var zones []marineZoneData
	for _, z := range loc.MarineZones {
		if mf := src.zone(doc, z.Zone); mf != nil {
			zones = append(zones, marineZoneData{Name: z.Name, MarineForecast: mf})
		} else {
			loc.logf("marine zone %s not found via %s", z.Zone, src.label)
		}
	}
	if len(zones) == 0 {
		// Say what did come back: a block page and a changed layout need
		// different fixes.
		sample := marineText(doc)
		if len(sample) > 80 {
			sample = sample[:80]
		}
		return nil, "", fmt.Errorf("none of the configured zones found (%d bytes, starts %q)", len(doc), sample)
	}
	return zones, src.synopsis(doc, loc.MarineSynopsis), nil
}

// updateMarine reads the marine forecast from the NWS API, falling back to
// NDBC's copy of the same product if that fails.
func (loc *coastalLocation) updateMarine() {
	sources := []marineSource{
		{
			label:    "NWS-Marine",
			fetch:    func() (string, error) { return fetchMarineProductText(loc.marineOffice()) },
			zone:     parseMarineTextZone,
			synopsis: parseMarineTextSynopsis,
		},
		{
			label:    "NDBC-Marine",
			fetch:    func() (string, error) { return fetchMarinePage(loc.MarineProduct) },
			zone:     parseMarineZone,
			synopsis: parseMarineSynopsis,
		},
	}
	for _, src := range sources {
		zones, synopsis, err := loc.readMarine(src)
		TrackAPICall(loc.trackLabel(src.label), err)
		if err != nil {
			loc.logf("marine forecast via %s failed: %v", src.label, err)
			continue
		}
		loc.mu.Lock()
		loc.marine = zones
		loc.marineSynopsis = synopsis
		loc.mu.Unlock()
		loc.logf("updated marine forecast via %s (%d zone(s))", src.label, len(zones))
		return
	}
}

// ── Shared HTTP helper ──────────────────────────────────────────

func coastalGetJSON(url string, out any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", coastalUserAgent)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// CO-OPS reports some errors as a JSON body on a 400, so decode before
	// giving up on a non-200.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if decErr := json.Unmarshal(body, out); decErr != nil {
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return decErr
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// ── Background loop ─────────────────────────────────────────────

func (loc *coastalLocation) updateAll() {
	loc.updateTides()
	loc.updateAlerts()
	loc.updateObservations()
	loc.updateWeather()
	loc.updateMarine()
}

func (loc *coastalLocation) loop() {
	loc.updateAll()
	ticker := time.NewTicker(coastalPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		loc.updateAll()
	}
}

// ── API handlers ────────────────────────────────────────────────

func (loc *coastalLocation) handleTides(w http.ResponseWriter, r *http.Request) {
	loc.mu.RLock()
	resp := struct {
		HiLo    []TideEvent `json:"hiLo"`
		Curve   []TidePoint `json:"curve"`
		Station string      `json:"station"`
		Level   *WaterLevel `json:"level"`
	}{loc.tideHiLo, loc.tideCurve, loc.TideStationName, loc.level}
	loc.mu.RUnlock()
	writeJSON(w, resp)
}

func (loc *coastalLocation) handleWeather(w http.ResponseWriter, r *http.Request) {
	loc.mu.RLock()
	resp := struct {
		Forecast        []WeatherPeriod `json:"forecast"`
		Hourly          []WeatherPeriod `json:"hourly"`
		WaterTemp       *float64        `json:"waterTemp"`
		WaterTempTime   string          `json:"waterTempTime,omitempty"`
		WaterTempSource string          `json:"waterTempSource,omitempty"`
		Wind            *WindObs        `json:"wind"`
		UV              *UVData         `json:"uv,omitempty"`
	}{
		Forecast:  loc.forecast,
		Hourly:    loc.hourly,
		WaterTemp: loc.waterTemp,
		Wind:      loc.wind,
		UV:        loc.uv,
	}
	if loc.waterTemp != nil {
		resp.WaterTempTime = localStamp(loc.waterTempAt)
		resp.WaterTempSource = loc.waterTempSource
	}
	loc.mu.RUnlock()
	writeJSON(w, resp)
}

func (loc *coastalLocation) handleMarine(w http.ResponseWriter, r *http.Request) {
	loc.mu.RLock()
	resp := struct {
		Zones    []marineZoneData `json:"zones"`
		Synopsis string           `json:"synopsis,omitempty"`
		Alerts   []NWSAlert       `json:"alerts"`
	}{loc.marine, loc.marineSynopsis, loc.alerts}
	loc.mu.RUnlock()
	if resp.Zones == nil {
		resp.Zones = []marineZoneData{}
	}
	if resp.Alerts == nil {
		resp.Alerts = []NWSAlert{}
	}
	writeJSON(w, resp)
}

// ── Page rendering ──────────────────────────────────────────────

// renderIndex fills the shared coastal/index.html template for this location.
// Titles and link-preview tags have to be in the served HTML (crawlers don't
// run JS), which is why this is a server-side template rather than a config
// endpoint.
func (loc *coastalLocation) renderIndex(tmpl *template.Template) ([]byte, error) {
	type link struct{ Name, Path string }
	var others []link
	for _, o := range coastalLocations {
		if o != loc {
			others = append(others, link{o.Name, o.BasePath + "/"})
		}
	}
	marineURL := fmt.Sprintf(ndbcForecastURLFmt, loc.MarineProduct)
	data := struct {
		Title, Icon                            string
		Name, Place, BasePath, OGBlurb         string
		TideStation, TideStationName           string
		MarineProduct, MarineURL               string
		ObsStation, ObsStationName             string
		BuoyStation, BuoyStationName, BlurbTag string
		Others                                 []link
	}{
		Title: loc.title(), Icon: loc.icon(),
		Name: loc.Name, Place: loc.Place, BasePath: loc.BasePath, OGBlurb: loc.OGBlurb,
		TideStation: loc.TideStation, TideStationName: loc.TideStationName,
		MarineProduct: loc.MarineProduct, MarineURL: marineURL,
		ObsStation: loc.ObsStation, ObsStationName: loc.ObsStationName,
		BuoyStation: loc.BuoyStation, BuoyStationName: loc.BuoyStationName,
		BlurbTag: blurbModelDisplay,
		Others:   others,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ── Init ────────────────────────────────────────────────────────

// InitCoastal registers every coastal dashboard's routes and starts their
// background polling.
func InitCoastal(mux *http.ServeMux) {
	tmpl, fileServer, err := coastalAssets()
	if err != nil {
		log.Fatalf("[Coastal] Failed to load embedded frontend: %v", err)
	}

	for i, loc := range coastalLocations {
		loc.indexHTML, err = loc.renderIndex(tmpl)
		if err != nil {
			log.Fatalf("[Coastal] Failed to render index for %s: %v", loc.Key, err)
		}
		registerCoastalRoutes(mux, loc, fileServer)

		// Stagger startup so the locations don't hit NOAA/NWS in one burst.
		go func(loc *coastalLocation, delay time.Duration) {
			time.Sleep(delay)
			loc.loop()
		}(loc, time.Duration(i)*5*time.Second)

		log.Printf("[Coastal] %s dashboard registered at %s/", loc.Name, loc.BasePath)
	}
}

// coastalAssets returns the parsed index template and a handler for the
// other embedded frontend files.
func coastalAssets() (*template.Template, http.Handler, error) {
	staticSub, err := fs.Sub(coastalFiles, "coastal")
	if err != nil {
		return nil, nil, err
	}
	tmpl, err := template.ParseFS(staticSub, "index.html")
	if err != nil {
		return nil, nil, err
	}
	return tmpl, http.FileServer(http.FS(staticSub)), nil
}

func registerCoastalRoutes(mux *http.ServeMux, loc *coastalLocation, assets http.Handler) {
	mux.HandleFunc(loc.BasePath+"/api/tides", loc.handleTides)
	mux.HandleFunc(loc.BasePath+"/api/weather", loc.handleWeather)
	mux.HandleFunc(loc.BasePath+"/api/marine", loc.handleMarine)

	stripped := http.StripPrefix(loc.BasePath, assets)
	mux.HandleFunc(loc.BasePath+"/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == loc.BasePath+"/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(loc.indexHTML)
			return
		}
		stripped.ServeHTTP(w, r)
	})
}
