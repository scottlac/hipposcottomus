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
