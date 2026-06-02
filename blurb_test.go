package main

import (
	"math"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

func resetLLMUsage() {
	llmUsageMu.Lock()
	llmUsage = map[string]*LLMUsageEntry{}
	llmUsageMu.Unlock()
}

func almostEqual(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// TestRecordLLMUsage_CostMath verifies the per-call cost formula against the
// constants documented in shared/models.md for Haiku 4.5: $1/M input,
// $5/M output, $1.25/M cache write (5m TTL), $0.10/M cache read.
func TestRecordLLMUsage_CostMath(t *testing.T) {
	resetLLMUsage()
	t.Cleanup(resetLLMUsage)

	recordLLMUsage(blurbModelLabel, anthropic.Usage{
		InputTokens:              200,
		OutputTokens:             150,
		CacheCreationInputTokens: 0,
		CacheReadInputTokens:     5000,
	})

	// Expected:
	//   200    * $1.00 / 1e6 = $0.000200
	//   150    * $5.00 / 1e6 = $0.000750
	//   5000   * $0.10 / 1e6 = $0.000500
	// total                 = $0.001450
	want := 0.001450

	llmUsageMu.RLock()
	got := llmUsage[blurbModelLabel].TotalCostUSD
	llmUsageMu.RUnlock()
	if !almostEqual(got, want, 1e-9) {
		t.Errorf("cost = %.9f, want %.9f", got, want)
	}
}

// TestRecordLLMUsage_Accumulates ensures repeated calls keep adding up rather
// than overwriting, and that all four token types accumulate independently.
func TestRecordLLMUsage_Accumulates(t *testing.T) {
	resetLLMUsage()
	t.Cleanup(resetLLMUsage)

	// First call: cache miss / write.
	recordLLMUsage(blurbModelLabel, anthropic.Usage{
		InputTokens:              200,
		OutputTokens:             150,
		CacheCreationInputTokens: 5000,
		CacheReadInputTokens:     0,
	})
	// Second call: cache hit.
	recordLLMUsage(blurbModelLabel, anthropic.Usage{
		InputTokens:              200,
		OutputTokens:             150,
		CacheCreationInputTokens: 0,
		CacheReadInputTokens:     5000,
	})

	llmUsageMu.RLock()
	e := llmUsage[blurbModelLabel]
	llmUsageMu.RUnlock()

	if e.Calls != 2 {
		t.Errorf("calls = %d, want 2", e.Calls)
	}
	if e.InputTokens != 400 {
		t.Errorf("input = %d, want 400", e.InputTokens)
	}
	if e.OutputTokens != 300 {
		t.Errorf("output = %d, want 300", e.OutputTokens)
	}
	if e.CacheCreationTokens != 5000 {
		t.Errorf("cache write = %d, want 5000", e.CacheCreationTokens)
	}
	if e.CacheReadTokens != 5000 {
		t.Errorf("cache read = %d, want 5000", e.CacheReadTokens)
	}

	// Cumulative cost = sum of both calls
	wantWrite := 200*1.00/1e6 + 150*5.00/1e6 + 5000*1.25/1e6
	wantRead := 200*1.00/1e6 + 150*5.00/1e6 + 5000*0.10/1e6
	want := wantWrite + wantRead
	if !almostEqual(e.TotalCostUSD, want, 1e-9) {
		t.Errorf("total cost = %.9f, want %.9f", e.TotalCostUSD, want)
	}
}

// TestRecordLLMError_StoresMessage ensures errors land on the same entry and
// truncate at the documented limit (errors propagate to the analytics page).
func TestRecordLLMError_StoresMessage(t *testing.T) {
	resetLLMUsage()
	t.Cleanup(resetLLMUsage)

	recordLLMError(blurbModelLabel, errFake{"network down"})
	llmUsageMu.RLock()
	e := llmUsage[blurbModelLabel]
	llmUsageMu.RUnlock()

	if e.LastError != "network down" {
		t.Errorf("lastError = %q, want %q", e.LastError, "network down")
	}
}

// TestInBlackout checks the overnight 8pm–6am ET no-generation window at
// several hours, building the times in Eastern so the test is unambiguous
// regardless of where it runs.
func TestInBlackout(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load ET: %v", err)
	}
	cases := []struct {
		hour int
		want bool
	}{
		{0, true},   // midnight — blackout
		{3, true},   // 3am — blackout
		{5, true},   // 5am — blackout
		{6, false},  // 6am — generation resumes
		{9, false},  // 9am — active
		{14, false}, // 2pm — active
		{19, false}, // 7pm — active
		{20, true},  // 8pm — blackout begins
		{23, true},  // 11pm — blackout
	}
	for _, c := range cases {
		ts := time.Date(2026, 6, 1, c.hour, 30, 0, 0, loc)
		if got := inBlackout(ts); got != c.want {
			t.Errorf("inBlackout(%02d:30 ET) = %v, want %v", c.hour, got, c.want)
		}
	}
}

// TestBlackoutStartFor checks the start-of-blackout calculation for both
// halves of the blackout window — pre-midnight (8pm–11:59pm ET starts today
// at 8pm) and post-midnight (midnight–6am ET starts yesterday at 8pm).
func TestBlackoutStartFor(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load ET: %v", err)
	}

	// 22:55 ET on Jun 1: blackout started 20:00 ET on Jun 1.
	t1 := time.Date(2026, 6, 1, 22, 55, 0, 0, loc)
	want1 := time.Date(2026, 6, 1, 20, 0, 0, 0, loc)
	if got := blackoutStartFor(t1); !got.Equal(want1) {
		t.Errorf("blackoutStartFor(22:55 Jun 1) = %v, want %v", got, want1)
	}

	// 03:30 ET on Jun 2: blackout started 20:00 ET on Jun 1 (the day before).
	t2 := time.Date(2026, 6, 2, 3, 30, 0, 0, loc)
	want2 := time.Date(2026, 6, 1, 20, 0, 0, 0, loc)
	if got := blackoutStartFor(t2); !got.Equal(want2) {
		t.Errorf("blackoutStartFor(03:30 Jun 2) = %v, want %v", got, want2)
	}
}

// TestShouldGenerate covers the decision matrix:
//   - daytime, fresh blurb → no
//   - daytime, stale blurb → yes
//   - daytime, no blurb → yes
//   - blackout, no blurb → yes (cold pod)
//   - blackout, blurb from BEFORE this blackout window → yes (the user's case:
//     a stale daytime blurb when someone visits at night)
//   - blackout, blurb from DURING this blackout window → no (already used the
//     one allowed refresh)
//   - post-midnight portion of blackout: still treats yesterday-evening start
//     as "this blackout window".
func TestShouldGenerate(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load ET: %v", err)
	}

	day := time.Date(2026, 6, 1, 14, 0, 0, 0, loc)        // 2pm ET
	evening := time.Date(2026, 6, 1, 22, 55, 0, 0, loc)   // 10:55pm ET (user's report)
	earlyAm := time.Date(2026, 6, 2, 3, 30, 0, 0, loc)    // 3:30am ET next day

	cases := []struct {
		name      string
		now       time.Time
		lastGenAt time.Time
		want      bool
	}{
		{"daytime, no blurb", day, time.Time{}, true},
		{"daytime, fresh blurb (5m ago)", day, day.Add(-5 * time.Minute), false},
		{"daytime, stale blurb (45m ago)", day, day.Add(-45 * time.Minute), true},

		{"blackout, no blurb (cold pod at 10:55pm)", evening, time.Time{}, true},
		{"blackout, daytime blurb from 3:40pm — should regenerate", evening,
			time.Date(2026, 6, 1, 15, 40, 0, 0, loc), true},
		{"blackout, blurb already generated at 8:30pm — no further regen", evening,
			time.Date(2026, 6, 1, 20, 30, 0, 0, loc), false},

		{"post-midnight blackout, blurb from 9pm — no regen (same window)", earlyAm,
			time.Date(2026, 6, 1, 21, 0, 0, 0, loc), false},
		{"post-midnight blackout, blurb from 5pm — regen (window already used? no, predates 8pm)", earlyAm,
			time.Date(2026, 6, 1, 17, 0, 0, 0, loc), true},
	}
	for _, c := range cases {
		if got := shouldGenerate(c.now, c.lastGenAt); got != c.want {
			t.Errorf("%s: shouldGenerate = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSnapshotLLMUsage_IsCopy verifies snapshotLLMUsage returns a value copy
// so the analytics handler can release the lock cleanly.
func TestSnapshotLLMUsage_IsCopy(t *testing.T) {
	resetLLMUsage()
	t.Cleanup(resetLLMUsage)

	recordLLMUsage(blurbModelLabel, anthropic.Usage{InputTokens: 100, OutputTokens: 50})
	snap := snapshotLLMUsage()
	if len(snap) != 1 {
		t.Fatalf("snap length = %d, want 1", len(snap))
	}
	// Mutate the snapshot. The internal store should be unchanged.
	snap[0].Calls = 99999
	llmUsageMu.RLock()
	defer llmUsageMu.RUnlock()
	if llmUsage[blurbModelLabel].Calls != 1 {
		t.Errorf("snapshot mutation leaked into internal store: calls = %d, want 1",
			llmUsage[blurbModelLabel].Calls)
	}
}
