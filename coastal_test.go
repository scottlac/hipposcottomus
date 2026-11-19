package main

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// ── Tides ───────────────────────────────────────────────────────

func TestParseNOAATime(t *testing.T) {
	got, err := parseNOAATime("2026-04-17 09:03")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, time.April, 17, 9, 3, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if _, err := parseNOAATime("not a time"); err == nil {
		t.Error("expected error on garbage input, got nil")
	}
}

func TestParseTideHiLo_ConvertsToEastern(t *testing.T) {
	var data noaaPredictionsResp
	data.Predictions = append(data.Predictions,
		struct {
			T    string `json:"t"`
			V    string `json:"v"`
			Type string `json:"type"`
		}{"2026-07-01 16:00", "2.5", "H"},
		struct {
			T    string `json:"t"`
			V    string `json:"v"`
			Type string `json:"type"`
		}{"2026-12-01 16:00", "0.4", "L"},
		struct {
			T    string `json:"t"`
			V    string `json:"v"`
			Type string `json:"type"`
		}{"2026-12-01 22:00", "bogus", "H"},
	)
	events := parseTideHiLo(data)
	if len(events) != 2 {
		t.Fatalf("expected 2 events (bad height skipped), got %d", len(events))
	}
	// 16:00 UTC is noon EDT in July and 11 AM EST in December.
	if events[0].Time != "2026-07-01T12:00:00-04:00" || events[0].Type != "High" {
		t.Errorf("summer event = %+v", events[0])
	}
	if events[1].Time != "2026-12-01T11:00:00-05:00" || events[1].Type != "Low" {
		t.Errorf("winter event = %+v", events[1])
	}
}

func TestInterpolateTideCurve_Empty(t *testing.T) {
	if got := interpolateTideCurve(nil); got != nil {
		t.Errorf("expected nil for nil input, got %+v", got)
	}
}

func TestInterpolateTideCurve_CosineBetweenHiLo(t *testing.T) {
	events := []TideEvent{
		{Time: "2026-04-17T00:00:00-04:00", Type: "Low", Height: 0.0},
		{Time: "2026-04-17T06:00:00-04:00", Type: "High", Height: 2.0},
	}
	points := interpolateTideCurve(events)
	if len(points) != 37 { // every 10 min for 6 h, plus the final event
		t.Fatalf("expected 37 points, got %d", len(points))
	}
	for _, p := range points {
		if p.Height < 0.0 || p.Height > 2.0 {
			t.Errorf("height %v outside [0, 2] at %s", p.Height, p.Time)
		}
	}
	mid := points[18]
	if mid.Time != "2026-04-17T03:00:00-04:00" || mid.Height < 0.99 || mid.Height > 1.01 {
		t.Errorf("midpoint = %+v, want 1.0 ft at 03:00", mid)
	}
	// A cosine is slow near the extremes: 1 h in is well under a linear 1/6.
	if points[6].Height > 0.2 {
		t.Errorf("1 h after low should be ~0.13 ft, got %v", points[6].Height)
	}
}

func TestTideAt(t *testing.T) {
	events := []TideEvent{
		{Time: "2026-04-17T00:00:00-04:00", Type: "Low", Height: 0.0},
		{Time: "2026-04-17T06:00:00-04:00", Type: "High", Height: 2.0},
		{Time: "2026-04-17T12:00:00-04:00", Type: "Low", Height: 0.4},
	}
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	st, ok := tideAt(events, at("2026-04-17T03:00:00-04:00"))
	if !ok || !st.Rising || st.Next.Type != "High" || st.Height < 0.99 || st.Height > 1.01 {
		t.Errorf("mid-flood state = %+v ok=%v", st, ok)
	}
	st, ok = tideAt(events, at("2026-04-17T09:00:00-04:00"))
	if !ok || st.Rising || st.Next.Type != "Low" || st.Height < 1.19 || st.Height > 1.21 {
		t.Errorf("mid-ebb state = %+v ok=%v", st, ok)
	}
	if _, ok := tideAt(events, at("2026-04-18T09:00:00-04:00")); ok {
		t.Error("expected ok=false beyond the last event")
	}
}

// ── Observed water level ────────────────────────────────────────

func TestLevelOffsets(t *testing.T) {
	observed := []coopsObs{
		{T: "2026-11-19 15:30", V: "2.00"},
		{T: "2026-11-19 15:36", V: ""}, // gap in the sensor record
		{T: "2026-11-19 15:42", V: "2.054"},
		{T: "2026-11-19 15:48", V: "2.10"}, // no prediction for this instant
	}
	predicted := []coopsObs{
		{T: "2026-11-19 15:30", V: "1.25"},
		{T: "2026-11-19 15:36", V: "1.22"},
		{T: "2026-11-19 15:42", V: "1.204"},
	}
	got := levelOffsets(observed, predicted)
	if len(got) != 2 {
		t.Fatalf("expected 2 matched samples, got %+v", got)
	}
	if got[0].Offset != 0.75 || got[0].Time != "2026-11-19T10:30:00-05:00" {
		t.Errorf("first sample = %+v", got[0])
	}
	if got[1].Offset != 0.85 {
		t.Errorf("second sample = %+v, want offset 0.85", got[1])
	}
	if levelOffsets(nil, predicted) != nil {
		t.Error("expected nil with no observations")
	}
}

// ── NDBC water temperature ──────────────────────────────────────

func TestParseNDBCWaterTemp(t *testing.T) {
	text := `#YY  MM DD hh mm WDIR WSPD GST  WVHT   DPD   APD MWD   PRES  ATMP  WTMP  DEWP  VIS PTDY  TIDE
#yr  mo dy hr mn degT m/s  m/s     m   sec   sec degT   hPa  degC  degC  degC  nmi  hPa    ft
2026 11 19 15 00  MM   MM   MM    MM    MM    MM  MM     MM    MM    MM    MM   MM   MM  0.70
2026 11 19 14 00  MM   MM   MM    MM    MM    MM  MM     MM    MM  23.5    MM   MM   MM  0.70
2026 11 19 13 00  MM   MM   MM    MM    MM    MM  MM     MM    MM  23.4    MM   MM   MM  0.70
`
	f, at, err := parseNDBCWaterTemp(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f < 74.29 || f > 74.31 { // 23.5 °C
		t.Errorf("temp = %v °F, want 74.3", f)
	}
	// The newest row has no reading, so the 14:00 UTC row is used.
	if want := time.Date(2026, 11, 19, 14, 0, 0, 0, time.UTC); !at.Equal(want) {
		t.Errorf("time = %v, want %v", at, want)
	}

	if _, _, err := parseNDBCWaterTemp("<html>404 Not Found</html>"); err == nil {
		t.Error("expected error for a page with no header")
	}
	if _, _, err := parseNDBCWaterTemp(strings.Join(strings.Split(text, "\n")[:3], "\n")); err == nil {
		t.Error("expected error when every reading is missing")
	}
}

// ── Marine forecast parsing ─────────────────────────────────────

func TestExpandMarineZones(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"AMZ630-192000", "AMZ630"},
		{"AMZ651-671-192000", "AMZ651 AMZ671"},
		{"AMZ600-GMZ606-192000", "AMZ600 GMZ606"},
		{"GMZ042>044-192130", "GMZ042 GMZ043 GMZ044"},
		{"GMZ052>055-072>075-192130", "GMZ052 GMZ053 GMZ054 GMZ055 GMZ072 GMZ073 GMZ074 GMZ075"},
		{"GMZ656-657-676-192000", "GMZ656 GMZ657 GMZ676"},
		{"garbage", ""},
	}
	for _, tt := range tests {
		if got := strings.Join(expandMarineZones(tt.header), " "); got != tt.want {
			t.Errorf("expandMarineZones(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The fixtures are the real NDBC pages from 19 Nov 2026 (a Small Craft
// Advisory day), trimmed to the forecast content.
func TestParseMarineZone_Miami(t *testing.T) {
	page := readFixture(t, "marine_KMFL.html")

	bay := parseMarineZone(page, "AMZ630")
	if bay == nil {
		t.Fatal("AMZ630 (Biscayne Bay) not found")
	}
	if bay.Issued != "241 AM EST Thu Nov 19 2026" {
		t.Errorf("issued = %q", bay.Issued)
	}
	if len(bay.Advisories) != 1 || bay.Advisories[0] != "SMALL CRAFT ADVISORY IN EFFECT THROUGH THIS AFTERNOON" {
		t.Errorf("advisories = %q", bay.Advisories)
	}
	if len(bay.Periods) != 10 || bay.Periods[0].Name != "TODAY" || bay.Periods[1].Name != "TONIGHT" {
		t.Fatalf("periods = %+v", bay.Periods)
	}
	if !strings.HasPrefix(bay.Periods[0].Text, "E winds 10 to 15 kt. A moderate chop. Showers likely") {
		t.Errorf("TODAY text = %q", bay.Periods[0].Text)
	}
	if strings.ContainsAny(bay.Periods[0].Text, "<>\n") {
		t.Errorf("period text should be one clean line: %q", bay.Periods[0].Text)
	}
	if len(bay.Notes) != 1 || bay.Notes[0] != "Winds and waves higher in and near tstms." {
		t.Errorf("notes = %q", bay.Notes)
	}

	// Biscayne Bay is the last block on the page: nothing from the site
	// footer may leak into it.
	for _, p := range bay.Periods {
		if strings.Contains(p.Text, "Commerce") {
			t.Errorf("footer leaked into period %s", p.Name)
		}
	}

	atl := parseMarineZone(page, "AMZ651")
	if atl == nil {
		t.Fatal("AMZ651 not found")
	}
	if !strings.HasPrefix(atl.Periods[0].Text, "E winds 20 to 25 kt") {
		t.Errorf("AMZ651 TODAY = %q", atl.Periods[0].Text)
	}
	// Each zone must stop at its own block: AMZ651's text must not run on
	// into the Gulf, Lake Okeechobee or Biscayne Bay blocks that follow it.
	if len(atl.Periods) != 10 {
		t.Errorf("AMZ651 has %d periods, want 10 (leaking into later zones?)", len(atl.Periods))
	}
	// A zone listed second in a group header resolves to the same block.
	if off := parseMarineZone(page, "AMZ671"); off == nil || off.Periods[0].Text != atl.Periods[0].Text {
		t.Error("AMZ671 should share AMZ651's block")
	}

	if parseMarineZone(page, "AMZ999") != nil {
		t.Error("expected nil for a zone that isn't on the page")
	}

	syn := parseMarineSynopsis(page, "AMZ600")
	if !strings.HasPrefix(syn, "Gusty east-northeasterly winds will continue") {
		t.Errorf("synopsis = %q", syn)
	}
	if strings.Contains(syn, "Gulf Stream Hazards") {
		t.Errorf("synopsis ran on into the Gulf Stream paragraphs: %q", syn)
	}
}

func TestParseMarineZone_KeyWest(t *testing.T) {
	page := readFixture(t, "marine_KKEY.html")

	hawk := parseMarineZone(page, "GMZ042") // first zone of the "GMZ042>044" range
	if hawk == nil {
		t.Fatal("GMZ042 (Hawk Channel) not found")
	}
	if hawk.Issued != "424 AM EST Thu Nov 19 2026" {
		t.Errorf("issued = %q", hawk.Issued)
	}
	if len(hawk.Advisories) != 0 {
		t.Errorf("expected no advisories, got %q", hawk.Advisories)
	}
	if len(hawk.Periods) != 10 || hawk.Periods[2].Name != "FRIDAY" {
		t.Fatalf("periods = %+v", hawk.Periods)
	}
	if !strings.Contains(hawk.Periods[2].Text, "15 to 20 knots off the Upper Keys") {
		t.Errorf("FRIDAY text = %q", hawk.Periods[2].Text)
	}

	straits := parseMarineZone(page, "GMZ052")
	if straits == nil || straits.Periods[0].Text == hawk.Periods[0].Text {
		t.Error("GMZ052 should resolve to its own block, distinct from Hawk Channel")
	}

	syn := parseMarineSynopsis(page, "GMZ005")
	if !strings.HasPrefix(syn, "An expansive high pressure system") {
		t.Errorf("synopsis = %q", syn)
	}
}

// The raw-text fixtures are the same day's products from api.weather.gov
// (Key West's is the later 10:15 AM issuance).
func TestParseMarineTextZone_Miami(t *testing.T) {
	product := readFixture(t, "marine_cwf_MFL.txt")

	bay := parseMarineTextZone(product, "AMZ630")
	if bay == nil {
		t.Fatal("AMZ630 (Biscayne Bay) not found")
	}
	if bay.Issued != "241 AM EST Thu Nov 19 2026" {
		t.Errorf("issued = %q", bay.Issued)
	}
	if len(bay.Advisories) != 1 || bay.Advisories[0] != "SMALL CRAFT ADVISORY IN EFFECT THROUGH THIS AFTERNOON" {
		t.Errorf("advisories = %q", bay.Advisories)
	}
	if len(bay.Notes) != 1 || bay.Notes[0] != "Winds and waves higher in and near tstms." {
		t.Errorf("notes = %q", bay.Notes)
	}

	// The text product and NDBC's HTML rendering of it must parse to the
	// same forecast, whichever source ends up being used.
	html := parseMarineZone(readFixture(t, "marine_KMFL.html"), "AMZ630")
	if len(bay.Periods) != len(html.Periods) {
		t.Fatalf("text has %d periods, html has %d", len(bay.Periods), len(html.Periods))
	}
	for i := range bay.Periods {
		if bay.Periods[i] != html.Periods[i] {
			t.Errorf("period %d differs:\n text: %+v\n html: %+v", i, bay.Periods[i], html.Periods[i])
		}
	}

	atl := parseMarineTextZone(product, "AMZ651")
	if atl == nil || len(atl.Periods) != 10 || !strings.HasPrefix(atl.Periods[0].Text, "E winds 20 to 25 kt") {
		t.Errorf("AMZ651 = %+v", atl)
	}
	if parseMarineTextZone(product, "AMZ999") != nil {
		t.Error("expected nil for a zone that isn't in the product")
	}

	syn := parseMarineTextSynopsis(product, "AMZ600")
	if !strings.HasPrefix(syn, "Gusty east-northeasterly winds will continue") || strings.Contains(syn, "Gulf Stream") {
		t.Errorf("synopsis = %q", syn)
	}
	if syn != parseMarineSynopsis(readFixture(t, "marine_KMFL.html"), "AMZ600") {
		t.Error("text and html synopsis differ")
	}
}

func TestParseMarineTextZone_KeyWest(t *testing.T) {
	product := readFixture(t, "marine_cwf_KEY.txt")

	hawk := parseMarineTextZone(product, "GMZ042")
	if hawk == nil {
		t.Fatal("GMZ042 (Hawk Channel) not found")
	}
	if hawk.Issued != "1015 AM EST Thu Nov 19 2026" {
		t.Errorf("issued = %q", hawk.Issued)
	}
	if len(hawk.Periods) == 0 || hawk.Periods[0].Name != "THIS AFTERNOON" {
		t.Fatalf("periods = %+v", hawk.Periods)
	}
	if !strings.HasPrefix(hawk.Periods[0].Text, "Northeast to east winds 10 to 15 knots, becoming northeast") {
		t.Errorf("first period text = %q", hawk.Periods[0].Text)
	}
	for _, p := range hawk.Periods {
		if strings.Contains(p.Text, "  ") || strings.Contains(p.Text, "\n") {
			t.Errorf("period %s should be one clean line: %q", p.Name, p.Text)
		}
	}
	for _, zone := range []string{"GMZ052", "GMZ031"} {
		z := parseMarineTextZone(product, zone)
		if z == nil || z.Periods[0].Text == hawk.Periods[0].Text {
			t.Errorf("%s should resolve to its own block", zone)
		}
	}

	syn := parseMarineTextSynopsis(product, "GMZ005")
	if !strings.HasPrefix(syn, "A developing area of low pressure") || strings.Contains(syn, "Gulf Stream") {
		t.Errorf("synopsis = %q", syn)
	}
}

// ── Configuration sanity ────────────────────────────────────────

func TestCoastalLocations_Config(t *testing.T) {
	keys, paths := map[string]bool{}, map[string]bool{}
	for _, loc := range coastalLocations {
		if keys[loc.Key] || paths[loc.BasePath] {
			t.Errorf("%s: duplicate key or base path", loc.Key)
		}
		keys[loc.Key], paths[loc.BasePath] = true, true

		if !strings.HasPrefix(loc.BasePath, "/") || strings.HasSuffix(loc.BasePath, "/") {
			t.Errorf("%s: base path %q must start with / and not end with one", loc.Key, loc.BasePath)
		}
		if loc.TideStation == "" || len(loc.MarineZones) == 0 || loc.About == "" {
			t.Errorf("%s: missing tide station, marine zones or About text", loc.Key)
		}
		if (loc.ObsStation == "") == (loc.BuoyStation == "") {
			t.Errorf("%s: set exactly one of ObsStation / BuoyStation for water temperature", loc.Key)
		}
		if _, ok := trackedPaths[loc.BasePath+"/"]; !ok {
			t.Errorf("%s: %s/ is not in analytics trackedPaths", loc.Key, loc.BasePath)
		}
		prompt := loc.systemPrompt()
		if strings.Contains(prompt, "%!") {
			t.Errorf("%s: system prompt has a bad format verb", loc.Key)
		}
		if !strings.Contains(prompt, loc.Name) || !strings.Contains(prompt, loc.About) {
			t.Errorf("%s: system prompt is missing the location name or About text", loc.Key)
		}
	}
}

// ── Page and API routes ─────────────────────────────────────────

func TestCoastalRoutes(t *testing.T) {
	// Parse the template and mount routes exactly as InitCoastal does, but
	// without starting the polling goroutines.
	mux := http.NewServeMux()
	tmpl, assets := mustCoastalAssets(t)
	for _, loc := range coastalLocations {
		html, err := loc.renderIndex(tmpl)
		if err != nil {
			t.Fatalf("%s: render: %v", loc.Key, err)
		}
		loc.indexHTML = html
		registerCoastalRoutes(mux, loc, assets)
	}

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}

	for _, loc := range coastalLocations {
		page := get(loc.BasePath + "/")
		body := page.Body.String()
		if page.Code != 200 || !strings.Contains(body, "<title>"+loc.Name+" Boating Dashboard</title>") {
			t.Errorf("%s: index status %d, title missing", loc.Key, page.Code)
		}
		if !strings.Contains(body, `<base href="`+loc.BasePath+`/" />`) {
			t.Errorf("%s: wrong <base>", loc.Key)
		}
		if strings.Contains(body, "{{") {
			t.Errorf("%s: unrendered template action in page", loc.Key)
		}
		// Links to the other locations, never to itself.
		for _, other := range coastalLocations {
			has := strings.Contains(body, `<a href="`+other.BasePath+`/">`)
			if has == (other == loc) {
				t.Errorf("%s: link to %s present=%v", loc.Key, other.Key, has)
			}
		}
		if rec := get(loc.BasePath + "/app.js"); rec.Code != 200 {
			t.Errorf("%s: app.js status %d", loc.Key, rec.Code)
		}
		for _, api := range []string{"/api/tides", "/api/weather", "/api/marine"} {
			rec := get(loc.BasePath + api)
			if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
				t.Errorf("%s: %s status %d type %q", loc.Key, api, rec.Code, rec.Header().Get("Content-Type"))
			}
		}
	}
}

func mustCoastalAssets(t *testing.T) (*template.Template, http.Handler) {
	t.Helper()
	tmpl, assets, err := coastalAssets()
	if err != nil {
		t.Fatal(err)
	}
	return tmpl, assets
}

// ── Summary snapshot ────────────────────────────────────────────

func TestCoastalSnapshot(t *testing.T) {
	temp := 74.8
	loc := &coastalLocation{
		Key: "test", Name: "Test Key", TideStationName: "Test Channel",
		tideHiLo: []TideEvent{
			{Time: "2026-11-19T10:05:00-05:00", Type: "Low", Height: 0.7},
			{Time: "2026-11-19T16:00:00-05:00", Type: "High", Height: 2.2},
			{Time: "2026-11-19T22:31:00-05:00", Type: "Low", Height: 0.5},
		},
		waterTemp:       &temp,
		waterTempAt:     time.Date(2026, 11, 19, 15, 12, 0, 0, time.UTC),
		waterTempSource: "Virginia Key",
		wind:            &WindObs{Knots: 12.4, GustKnots: 15.8, Direction: "E", Source: "Virginia Key"},
		level:           &WaterLevel{Offset: 0.87, Source: "Virginia Key", Time: "2026-11-19T12:54:00-05:00"},
		marine: []marineZoneData{
			{Name: "Biscayne Bay", MarineForecast: &MarineForecast{
				Issued:     "241 AM EST Thu Nov 19 2026",
				Advisories: []string{"SMALL CRAFT ADVISORY IN EFFECT THROUGH THIS AFTERNOON"},
				Periods: []MarinePeriod{
					{"TODAY", "E winds 10 to 15 kt."}, {"TONIGHT", "N winds 5 to 10 kt."},
					{"FRI", "NW winds 5 to 10 kt."}, {"FRI NIGHT", "Should not appear."},
				},
			}},
			{Name: "Atlantic", MarineForecast: &MarineForecast{
				Advisories: []string{"SMALL CRAFT ADVISORY IN EFFECT THROUGH THIS AFTERNOON"},
				Periods:    []MarinePeriod{{"TODAY", "E winds 20 to 25 kt. Seas 6 to 8 ft."}},
			}},
		},
		hourly: []WeatherPeriod{{StartTime: "2026-11-19T13:00:00-05:00", Temperature: 79, ShortForecast: "Showers", WindSpeed: "14 mph", WindDirection: "E"}},
		uv:     &UVData{Current: 5.2, UpdatedAt: "2026-11-19T13:00", Hourly: []UVHour{{"2026-11-19T13:00", 5.2}, {"2026-11-19T14:00", 4.1}, {"2026-11-20T12:00", 9.9}}},
	}
	now := time.Date(2026, 11, 19, 18, 2, 0, 0, time.UTC) // 1:02 PM EST

	got := loc.snapshotAt(now)
	for _, want := range []string{
		"Local time now: Thu 1:02 PM",
		"ACTIVE ADVISORY (Biscayne Bay): SMALL CRAFT ADVISORY",
		"Now: about 1.4 ft and rising",
		"Thu 4:00 PM: High, 2.2 ft",
		"Observed water level: running 0.9 ft above the predictions (Virginia Key gauge, Thu 12:54 PM)",
		"Thu 10:31 PM: Low, 0.5 ft",
		"Water temperature: 74.8 °F (Virginia Key sensor, Thu 10:12 AM)",
		"Live wind (Virginia Key sensor): E 12 kt, gusting 16 kt",
		"FRI: NW winds 5 to 10 kt.",
		"E winds 20 to 25 kt. Seas 6 to 8 ft.",
		"Thu 1:00 PM: 79 °F, Showers, wind 14 mph E",
		"UV index: current 5.2, peak for the rest of today 5.2", // tomorrow's 9.9 excluded
	} {
		if !strings.Contains(got, want) {
			t.Errorf("snapshot missing %q\n---\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"Should not appear", "Thu 10:05 AM: Low"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("snapshot should not contain %q", unwanted)
		}
	}
	if n := strings.Count(got, "ACTIVE ADVISORY"); n != 1 {
		t.Errorf("identical advisory on two zones should be listed once, got %d", n)
	}

	if s := (&coastalLocation{Name: "Empty"}).snapshotAt(now); s != "" {
		t.Errorf("expected empty snapshot with no data, got %q", s)
	}
}
