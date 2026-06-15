package main

import (
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

// ── History ─────────────────────────────────────────────────────

func TestHistory_AddOverwritesSameDay(t *testing.T) {
	h := &History{}
	h.Add(10.0)
	h.Add(11.0) // same day — should update in place, not append
	h.Add(12.5)

	got := h.All()
	if len(got) != 1 {
		t.Fatalf("expected 1 point after same-day adds, got %d: %+v", len(got), got)
	}
	if got[0].Value != 12.5 {
		t.Errorf("expected latest value 12.5, got %v", got[0].Value)
	}
}

func TestHistory_AddWithDateSkipsDuplicates(t *testing.T) {
	h := &History{}
	h.AddWithDate("2026-01-01", 1.0)
	h.AddWithDate("2026-01-01", 99.0) // duplicate — should be ignored
	h.AddWithDate("2026-01-02", 2.0)

	got := h.All()
	if len(got) != 2 {
		t.Fatalf("expected 2 points, got %d: %+v", len(got), got)
	}
	if got[0].Value != 1.0 {
		t.Errorf("original value should be preserved on duplicate date; got %v", got[0].Value)
	}
}

func TestHistory_Sort(t *testing.T) {
	h := &History{}
	h.AddWithDate("2026-03-01", 3.0)
	h.AddWithDate("2026-01-01", 1.0)
	h.AddWithDate("2026-02-01", 2.0)
	h.Sort()

	got := h.All()
	want := []DataPoint{
		{Date: "2026-01-01", Value: 1.0},
		{Date: "2026-02-01", Value: 2.0},
		{Date: "2026-03-01", Value: 3.0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sort() = %+v, want %+v", got, want)
	}
}

func TestHistory_LatestEmpty(t *testing.T) {
	h := &History{}
	if _, ok := h.Latest(); ok {
		t.Error("Latest() on empty history should return ok=false")
	}
}

func TestHistory_SaveLoadRoundTrip(t *testing.T) {
	// Populate one history, save to a temp file, load into a fresh one, and
	// verify the data survived.
	h := &History{}
	h.AddWithDate("2026-04-01", 210.5)
	h.AddWithDate("2026-04-02", 211.0)
	h.AddWithDate("2026-04-03", 212.7)

	path := filepath.Join(t.TempDir(), "history.json")
	if err := h.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	loaded := &History{}
	if err := loaded.LoadFromFile(path); err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}

	if !reflect.DeepEqual(h.All(), loaded.All()) {
		t.Errorf("round trip mismatch:\nsaved:  %+v\nloaded: %+v", h.All(), loaded.All())
	}
}

// ── lastMatch ───────────────────────────────────────────────────

func TestLastMatch(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		text    string
		wantOK  bool
		wantVal float64
	}{
		{
			name:    "single match",
			pattern: `value=([0-9.]+)`,
			text:    `value=42.5`,
			wantOK:  true,
			wantVal: 42.5,
		},
		{
			name:    "multiple matches returns last",
			pattern: `value=([0-9.]+)`,
			text:    `value=1.0 and value=2.0 and value=3.0`,
			wantOK:  true,
			wantVal: 3.0,
		},
		{
			name:    "no match",
			pattern: `value=([0-9.]+)`,
			text:    `nothing here`,
			wantOK:  false,
		},
		{
			name:    "unparseable capture",
			pattern: `value=(\S+)`,
			text:    `value=abc`,
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re := regexp.MustCompile(tt.pattern)
			got, ok := lastMatch(re, tt.text)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.wantVal {
				t.Errorf("val = %v, want %v", got, tt.wantVal)
			}
		})
	}
}

// ── New history helpers + USACE date parsing ────────────────────

func TestHistory_SetForDate(t *testing.T) {
	h := &History{}

	if !h.SetForDate("2026-06-12", 10.0) {
		t.Error("SetForDate on new date should report change")
	}
	if h.SetForDate("2026-06-12", 10.0) {
		t.Error("SetForDate with same value should NOT report change (lets caller skip disk write)")
	}
	if !h.SetForDate("2026-06-12", 11.0) {
		t.Error("SetForDate with new value for same date should report change")
	}
	if !h.SetForDate("2026-06-13", 11.0) {
		t.Error("SetForDate on new date should report change even if value matches a previous date")
	}

	got := h.All()
	want := []DataPoint{
		{Date: "2026-06-12", Value: 11.0},
		{Date: "2026-06-13", Value: 11.0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after SetForDate sequence: got %+v, want %+v", got, want)
	}
}

func TestHistory_RemoveConsecutiveDuplicates(t *testing.T) {
	// Mirrors the real Jordan Lake weekend-phantom pattern: Friday's value
	// got stamped onto Saturday and Sunday by the old scrapeLoop. Monday
	// has a genuinely different reading.
	h := &History{}
	h.AddWithDate("2026-06-11", 81.5) // Thu
	h.AddWithDate("2026-06-12", 81.0) // Fri (real)
	h.AddWithDate("2026-06-13", 81.0) // Sat (phantom)
	h.AddWithDate("2026-06-14", 81.0) // Sun (phantom)
	h.AddWithDate("2026-06-15", 85.0) // Mon (real)
	h.AddWithDate("2026-06-16", 85.0) // Tue (real but happens to match Mon — drops as collateral)

	dropped := h.RemoveConsecutiveDuplicates()
	if dropped != 3 {
		t.Errorf("dropped = %d, want 3 (2 phantoms + 1 genuine collateral)", dropped)
	}

	got := h.All()
	want := []DataPoint{
		{Date: "2026-06-11", Value: 81.5},
		{Date: "2026-06-12", Value: 81.0},
		{Date: "2026-06-15", Value: 85.0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after scrub: got %+v, want %+v", got, want)
	}

	// Idempotent on a clean series.
	if d := h.RemoveConsecutiveDuplicates(); d != 0 {
		t.Errorf("second scrub dropped %d, want 0 (should be idempotent)", d)
	}
}

func TestParseReportDate(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantOK   bool
		wantDate string
	}{
		{
			name:     "abbreviated month",
			text:     "FRIDAY 6 JUN 2026 report follows...",
			wantOK:   true,
			wantDate: "2026-06-06",
		},
		{
			name:     "full month name",
			text:     "MONDAY 9 JUNE 2026 report follows...",
			wantOK:   true,
			wantDate: "2026-06-09",
		},
		{
			name:     "multiple dates returns LAST",
			text:     "THURSDAY 5 JUN 2026\nFRIDAY 6 JUN 2026\nMONDAY 9 JUN 2026 most recent",
			wantOK:   true,
			wantDate: "2026-06-09",
		},
		{
			name:     "no date present",
			text:     "no headline here, just data",
			wantOK:   false,
			wantDate: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseReportDate(tt.text)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (got date=%q)", ok, tt.wantOK, got)
			}
			if got != tt.wantDate {
				t.Errorf("date = %q, want %q", got, tt.wantDate)
			}
		})
	}
}
