package main

import (
	"strings"
	"testing"
	"time"
)

// ── parseNOAALocalTime ──────────────────────────────────────────

func TestParseNOAALocalTime(t *testing.T) {
	got, err := parseNOAALocalTime("2026-04-17 09:03")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Year() != 2026 || got.Month() != time.April || got.Day() != 17 {
		t.Errorf("date mismatch: got %v", got)
	}
	if got.Hour() != 9 || got.Minute() != 3 {
		t.Errorf("time-of-day mismatch: got %v", got)
	}
}

func TestParseNOAALocalTime_Invalid(t *testing.T) {
	if _, err := parseNOAALocalTime("not a time"); err == nil {
		t.Error("expected error on garbage input, got nil")
	}
}

// ── interpolateTideCurve ────────────────────────────────────────

func TestInterpolateTideCurve_Empty(t *testing.T) {
	if got := interpolateTideCurve(nil); got != nil {
		t.Errorf("expected nil for nil input, got %+v", got)
	}
}

func TestInterpolateTideCurve_MonotonicBetweenHiLo(t *testing.T) {
	// Between a low and a high, every interpolated sample should be within
	// the endpoints' range, and the peak/trough should be at the endpoints.
	events := []TideEvent{
		{Time: "2026-04-17 00:00", Type: "L", Height: 0.0},
		{Time: "2026-04-17 06:00", Type: "H", Height: 2.0},
	}
	points := interpolateTideCurve(events)

	if len(points) < 10 {
		t.Fatalf("expected many interpolated points, got %d", len(points))
	}

	// All interpolated heights must be in [0, 2].
	for _, p := range points {
		if p.Height < 0.0 || p.Height > 2.0 {
			t.Errorf("interpolated height %v outside [0, 2] for %s", p.Height, p.Time)
		}
	}

	// Cosine interpolation should cross the midpoint (1.0) near t=3h.
	var midpoint *TidePoint
	for i := range points {
		if points[i].Time == "2026-04-17 03:00" {
			midpoint = &points[i]
			break
		}
	}
	if midpoint == nil {
		t.Fatal("no point at midpoint time")
	}
	if midpoint.Height < 0.95 || midpoint.Height > 1.05 {
		t.Errorf("cosine midpoint should be ~1.0, got %v", midpoint.Height)
	}
}

// ── stripHTMLTags ───────────────────────────────────────────────

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no tags", "hello world", "hello world"},
		{"simple", "<p>hello</p>", "hello"},
		{"nested", "<b><i>bold italic</i></b>", "bold italic"},
		{"self-closing", "line one<br/>line two", "line oneline two"},
		{"preserves newlines", "a\n<b>b</b>\nc", "a\nb\nc"},
		{"unmatched lt looks like tag", "<incomplete", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripHTMLTags(tt.in); got != tt.want {
				t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ── extractMarineZone ───────────────────────────────────────────

func TestExtractMarineZone(t *testing.T) {
	text := `AMZ650-171200-
Jupiter Inlet to Deerfield Beach out 20 NM-
600 AM EDT Fri Apr 17 2026

.TODAY...East wind 10 to 15 knots. Seas 2 to 3 feet.

$$

AMZ651-171200-
Deerfield Beach to Ocean Reef out 20 NM-
600 AM EDT Fri Apr 17 2026

.TODAY...Southeast wind 5 to 10 knots. Seas 2 feet.
.TONIGHT...Light wind. Seas 1 to 2 feet.

$$`

	section := extractMarineZone(text, "Deerfield Beach to Ocean Reef")
	if section == "" {
		t.Fatal("expected non-empty section for AMZ651")
	}
	if !strings.Contains(section, "Southeast wind 5 to 10 knots") {
		t.Errorf("AMZ651 section missing expected body: %q", section)
	}
	// Must not leak into the AMZ650 section above.
	if strings.Contains(section, "East wind 10 to 15 knots") {
		t.Errorf("AMZ651 section leaked content from AMZ650: %q", section)
	}
}

func TestExtractMarineZone_Missing(t *testing.T) {
	if got := extractMarineZone("no matching zone here", "Deerfield"); got != "" {
		t.Errorf("expected empty string for missing zone, got %q", got)
	}
}
