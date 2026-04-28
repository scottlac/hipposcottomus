package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ── Analytics.RecordPageView ────────────────────────────────────

func newTestAnalytics() *Analytics {
	return &Analytics{
		PageViews:   map[string]map[string]int64{},
		APIHealth:   map[string]*APIHealthEntry{},
		Countries:   map[string]int64{},
		CityHeatmap: map[string]int64{},
		ScreenSizes: map[string]int64{},
	}
}

func TestRecordPageView_TrackedPath(t *testing.T) {
	a := newTestAnalytics()
	a.RecordPageView("/lakedashboard/", geoLookup{})
	a.RecordPageView("/lakedashboard/", geoLookup{})
	a.RecordPageView("/poker/", geoLookup{})

	if len(a.PageViews["lake"]) != 1 {
		t.Errorf("expected lake to have 1 date bucket, got %+v", a.PageViews["lake"])
	}
	var lakeCount int64
	for _, c := range a.PageViews["lake"] {
		lakeCount += c
	}
	if lakeCount != 2 {
		t.Errorf("lake count = %d, want 2", lakeCount)
	}
	if a.PageViews["poker"] == nil {
		t.Errorf("expected poker entry, got none")
	}
}

func TestRecordPageView_IgnoresUntrackedPaths(t *testing.T) {
	a := newTestAnalytics()
	a.RecordPageView("/lakedashboard/app.js", geoLookup{})
	a.RecordPageView("/lakedashboard/api/history", geoLookup{})
	a.RecordPageView("/metrics", geoLookup{})
	a.RecordPageView("/healthz", geoLookup{})

	if len(a.PageViews) != 0 {
		t.Errorf("expected no tracked paths, got %+v", a.PageViews)
	}
}

func TestRecordPageView_HeatmapBucketsByWeekdayHour(t *testing.T) {
	a := newTestAnalytics()
	a.RecordPageView("/", geoLookup{})

	// Exactly one cell in the 7x24 grid should be 1, all others 0.
	var nonZero int
	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h++ {
			if a.Heatmap[d][h] == 1 {
				nonZero++
			} else if a.Heatmap[d][h] != 0 {
				t.Errorf("unexpected Heatmap[%d][%d] = %d", d, h, a.Heatmap[d][h])
			}
		}
	}
	if nonZero != 1 {
		t.Errorf("expected exactly one non-zero heatmap cell, got %d", nonZero)
	}
}

func TestRecordPageView_GeoRecordedOnlyWhenEnabled(t *testing.T) {
	// Save + restore the global so the test is isolated.
	orig := geoEnabled
	t.Cleanup(func() { geoEnabled = orig })

	a := newTestAnalytics()
	geoEnabled = false
	a.RecordPageView("/", geoLookup{country: "US", bucket: "35.8,-78.8"})
	if len(a.Countries) != 0 || len(a.CityHeatmap) != 0 {
		t.Errorf("geo disabled but was recorded: countries=%v heatmap=%v", a.Countries, a.CityHeatmap)
	}

	geoEnabled = true
	a.RecordPageView("/", geoLookup{country: "US", bucket: "35.8,-78.8"})
	if a.Countries["US"] != 1 {
		t.Errorf("expected US=1 after enabled, got %+v", a.Countries)
	}
	if a.CityHeatmap["35.8,-78.8"] != 1 {
		t.Errorf("expected bucket count 1, got %+v", a.CityHeatmap)
	}
}

func TestRecordPageView_UnknownCountryBucketsToQuestionMarks(t *testing.T) {
	orig := geoEnabled
	geoEnabled = true
	t.Cleanup(func() { geoEnabled = orig })

	a := newTestAnalytics()
	a.RecordPageView("/", geoLookup{}) // no country, no bucket
	if a.Countries["??"] != 1 {
		t.Errorf("expected ?? bucket for empty country, got %+v", a.Countries)
	}
	if len(a.CityHeatmap) != 0 {
		t.Errorf("expected no city bucket when bucket==\"\", got %+v", a.CityHeatmap)
	}
}

// ── clientIP ────────────────────────────────────────────────────

func TestClientIP(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		remote  string
		want    string
	}{
		{
			name:    "X-Real-IP wins",
			headers: map[string]string{"X-Real-IP": "203.0.113.5", "X-Forwarded-For": "198.51.100.1"},
			remote:  "10.0.0.1:54321",
			want:    "203.0.113.5",
		},
		{
			name:    "X-Forwarded-For leftmost when X-Real-IP missing",
			headers: map[string]string{"X-Forwarded-For": "198.51.100.1, 10.0.0.2, 10.0.0.3"},
			remote:  "10.0.0.1:54321",
			want:    "198.51.100.1",
		},
		{
			name:    "Falls back to RemoteAddr",
			headers: nil,
			remote:  "192.0.2.10:443",
			want:    "192.0.2.10",
		},
		{
			name:    "IPv6 in X-Real-IP",
			headers: map[string]string{"X-Real-IP": "2001:db8::1"},
			remote:  "10.0.0.1:54321",
			want:    "2001:db8::1",
		},
		{
			name:    "Invalid X-Real-IP falls through to X-Forwarded-For",
			headers: map[string]string{"X-Real-IP": "not-an-ip", "X-Forwarded-For": "203.0.113.5"},
			remote:  "10.0.0.1:54321",
			want:    "203.0.113.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remote
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			got := clientIP(r)
			if got == nil {
				t.Fatalf("got nil IP, want %q", tt.want)
			}
			if got.String() != tt.want {
				t.Errorf("got %q, want %q", got.String(), tt.want)
			}
		})
	}
}

// ── TrackAPICall ────────────────────────────────────────────────

func TestTrackAPICall(t *testing.T) {
	// This mutates the package-level `analytics`. Reset it around the test.
	origPageViews := analytics.PageViews
	origAPIHealth := analytics.APIHealth
	analytics.PageViews = map[string]map[string]int64{}
	analytics.APIHealth = map[string]*APIHealthEntry{}
	t.Cleanup(func() {
		analytics.PageViews = origPageViews
		analytics.APIHealth = origAPIHealth
	})

	TrackAPICall("Test", nil)
	TrackAPICall("Test", nil)
	TrackAPICall("Test", errFake{"oops"})

	entry := analytics.APIHealth["Test"]
	if entry == nil {
		t.Fatal("expected entry for Test, got nil")
	}
	if entry.Success != 2 {
		t.Errorf("success = %d, want 2", entry.Success)
	}
	if entry.Failure != 1 {
		t.Errorf("failure = %d, want 1", entry.Failure)
	}
	if entry.LastSuccess.IsZero() {
		t.Error("LastSuccess should be non-zero after a success")
	}
	if entry.LastError != "oops" {
		t.Errorf("LastError = %q, want %q", entry.LastError, "oops")
	}
}

type errFake struct{ msg string }

func (e errFake) Error() string { return e.msg }

// ── RecordScreenSize ────────────────────────────────────────────

func TestRecordScreenSize(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantStore map[string]int64
	}{
		{"valid", "1920x1080", map[string]int64{"1920x1080": 1}},
		{"valid trim leading zeros", "01920x01080", map[string]int64{"1920x1080": 1}},
		{"empty", "", map[string]int64{}},
		{"missing x", "1920", map[string]int64{}},
		{"non-numeric", "WIDExHIGH", map[string]int64{}},
		{"too small", "10x10", map[string]int64{}},
		{"too large", "99999x99999", map[string]int64{}},
		{"too long", strings.Repeat("9", 50), map[string]int64{}},
		{"negative", "-100x-100", map[string]int64{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAnalytics()
			a.RecordScreenSize(tt.input)
			if !reflect.DeepEqual(a.ScreenSizes, tt.wantStore) {
				t.Errorf("ScreenSizes = %+v, want %+v", a.ScreenSizes, tt.wantStore)
			}
		})
	}
}

func TestRecordScreenSize_Increments(t *testing.T) {
	a := newTestAnalytics()
	for i := 0; i < 3; i++ {
		a.RecordScreenSize("1920x1080")
	}
	a.RecordScreenSize("1366x768")
	if a.ScreenSizes["1920x1080"] != 3 {
		t.Errorf("1920x1080 count = %d, want 3", a.ScreenSizes["1920x1080"])
	}
	if a.ScreenSizes["1366x768"] != 1 {
		t.Errorf("1366x768 count = %d, want 1", a.ScreenSizes["1366x768"])
	}
}

// ── handleAnalyticsScreen ───────────────────────────────────────

func TestHandleAnalyticsScreen(t *testing.T) {
	origAnalytics := analytics
	analytics = newTestAnalytics()
	t.Cleanup(func() { analytics = origAnalytics })

	tests := []struct {
		name       string
		method     string
		body       string
		wantStatus int
		wantStored bool
	}{
		{"valid POST", "POST", `{"size":"1920x1080"}`, http.StatusNoContent, true},
		{"GET rejected", "GET", "", http.StatusMethodNotAllowed, false},
		{"bad JSON", "POST", `not json`, http.StatusBadRequest, false},
		{"empty size silently ignored", "POST", `{"size":""}`, http.StatusNoContent, false},
		{"junk size silently ignored", "POST", `{"size":"abc"}`, http.StatusNoContent, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analytics.ScreenSizes = map[string]int64{}
			req := httptest.NewRequest(tt.method, "/analytics/api/screen", bytes.NewBufferString(tt.body))
			rr := httptest.NewRecorder()
			handleAnalyticsScreen(rr, req)
			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if tt.wantStored && analytics.ScreenSizes["1920x1080"] != 1 {
				t.Errorf("expected 1920x1080 to be stored, got %+v", analytics.ScreenSizes)
			}
			if !tt.wantStored && len(analytics.ScreenSizes) != 0 {
				t.Errorf("expected empty store, got %+v", analytics.ScreenSizes)
			}
		})
	}
}

func TestHandleAnalyticsScreen_BodySizeCap(t *testing.T) {
	// MaxBytesReader caps at 256 bytes; a much larger body should be rejected
	// before parsing.
	origAnalytics := analytics
	analytics = newTestAnalytics()
	t.Cleanup(func() { analytics = origAnalytics })

	huge := `{"size":"` + strings.Repeat("A", 500) + `"}`
	req := httptest.NewRequest("POST", "/analytics/api/screen", strings.NewReader(huge))
	rr := httptest.NewRecorder()
	handleAnalyticsScreen(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for oversized body, got %d", rr.Code)
	}
}

// ── Persistence round trip ──────────────────────────────────────

func TestAnalytics_SaveLoadRoundTrip(t *testing.T) {
	a := newTestAnalytics()
	orig := geoEnabled
	geoEnabled = true
	t.Cleanup(func() { geoEnabled = orig })

	a.RecordPageView("/", geoLookup{country: "US", bucket: "35.8,-78.8"})
	a.RecordPageView("/poker/", geoLookup{country: "CA", bucket: "45.4,-75.7"})

	// Save using the real path via DATA_DIR override.
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)

	// Temporarily swap the global so the package-level SaveToFile writes ours.
	origAnalytics := analytics
	analytics = a
	t.Cleanup(func() { analytics = origAnalytics })

	if err := analytics.SaveToFile(); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	// Confirm the file lives where getDataDir + analyticsFile says it does.
	path := filepath.Join(getDataDir(), analyticsFile)

	// Load into a fresh Analytics.
	loaded := newTestAnalytics()
	tmp := analytics
	analytics = loaded
	if err := analytics.LoadFromFile(); err != nil {
		t.Fatalf("LoadFromFile %s: %v", path, err)
	}
	analytics = tmp

	if !reflect.DeepEqual(a.PageViews, loaded.PageViews) {
		t.Errorf("PageViews round trip mismatch:\nsaved:  %+v\nloaded: %+v", a.PageViews, loaded.PageViews)
	}
	if !reflect.DeepEqual(a.Countries, loaded.Countries) {
		t.Errorf("Countries round trip mismatch:\nsaved:  %+v\nloaded: %+v", a.Countries, loaded.Countries)
	}
	if !reflect.DeepEqual(a.CityHeatmap, loaded.CityHeatmap) {
		t.Errorf("CityHeatmap round trip mismatch:\nsaved:  %+v\nloaded: %+v", a.CityHeatmap, loaded.CityHeatmap)
	}
}

// ── Middleware via httptest ─────────────────────────────────────

func TestAnalyticsMiddleware_CountsTrackedPaths(t *testing.T) {
	// Swap in a fresh global analytics to avoid polluting other tests.
	origAnalytics := analytics
	analytics = newTestAnalytics()
	t.Cleanup(func() { analytics = origAnalytics })

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := analyticsMiddleware(next)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	for _, path := range []string{"/", "/analytics/", "/analytics/app.js", "/metrics"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
	}

	// Only "/" and "/analytics/" are in trackedPaths.
	if len(analytics.PageViews) != 2 {
		t.Errorf("expected 2 tracked kinds, got %+v", analytics.PageViews)
	}
	if _, ok := analytics.PageViews["home"]; !ok {
		t.Error("expected home view to be recorded")
	}
	if _, ok := analytics.PageViews["analytics"]; !ok {
		t.Error("expected analytics view to be recorded")
	}
}
