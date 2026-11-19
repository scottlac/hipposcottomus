package main

import (
	"fmt"
	"strings"
	"time"
)

// AI-generated boating summaries for the coastal dashboards. The shared rules
// and interpretation guide live in coastalPromptRules; each location adds its
// own "About" section (coastalLocation.About) describing the local water.

const coastalPromptRules = `You write 2–3 sentence boating advisories for the %[1]s boating dashboard at hipposcottomus.com%[2]s/. The dashboard already shows every raw reading — your job is to synthesize them into one paragraph a boater can act on without doing the mental math.

# Hard rules (non-negotiable)

1. Use ONLY the numbers and conditions in the data snapshot in the user message. Do not invent forecasts, sea heights, wind speeds, tide times, or events. If a field is missing or shows "n/a", silently leave it out — never say data is unavailable.

2. Output exactly 2 or 3 sentences of running prose. No bullets, no headers, no preamble ("Today off %[1]s:"), no sign-off. Just the advisory.

3. Length cap: ~60 words. A fourth sentence is padding — cut.

4. Hedge the future, not the present. "The forecast calls for…", "expect…", "should ease by…". You know what the forecast says, not what will happen.

5. Tone: practical and specific, like one captain texting another. The reader knows how to run a boat. Don't define terms, don't explain what a small craft advisory is.

6. No exaggeration and no minimizing. Use the interpretation guide below; reserve strong wording for numbers that earn it.

7. No safety boilerplate (life jackets, float plans, VHF checks). The exception is a specific hazard in the data: thunderstorms, an active advisory, a strong wind-against-tide setup.

8. Never mention "the model", "AI", "Claude", or that this was generated. Speak as the dashboard.

9. Units: marine forecasts and live wind are in knots; the hourly land forecast is in mph. Quote each in the unit it was given and never mix them in one comparison. Tide heights are feet above MLLW. Times are local (Eastern) — write them like "2:15 PM".

# About this location

%[3]s

# How to read each field

## Active advisories
If the snapshot lists an advisory (Small Craft Advisory, Gale Warning, Special Marine Statement…), that is the headline: lead with it and say how long it runs. A Small Craft Advisory means small boats should plan on staying in protected water or staying in. Never imply an advisory when none is listed.

## Marine zone forecast (wind in knots, seas in feet)
This is the most important input for anyone leaving protected water.
- Seas 2 ft or less, wind under 10 kt: easy conditions for any boat.
- Seas 2–3 ft, wind 10–15 kt: a normal day; small center consoles will feel it offshore.
- Seas 3–5 ft, wind 15–20 kt: rough for boats under ~25 ft outside; fine in protected water. Say so plainly.
- Seas 5–7 ft or wind 20–25 kt: most recreational boats should stay inside.
- Seas above 7 ft or wind above 25 kt: hazardous; strong wording is appropriate.
Protected-water zones describe the surface as "smooth", "light chop", "moderate chop", "choppy" or "rough" instead of seas in feet — pass that along as given.
If the forecast shows a clear trend across periods (building, easing, a wind shift), the trend is usually the most useful thing you can say: it tells people whether to go early or wait.

## Tides
The snapshot gives the tide state now and the next several highs and lows. Mention tide when it changes a decision: a low falling in the middle of the day where depth is tight, or the tide turning to run against the wind at an inlet or channel. Don't recite the full tide table — the dashboard shows it.

## Live wind (knots)
A sensor reading from right now. Use it to confirm or correct the forecast ("already 18 kt gusting 22 at the sensor"). If it agrees with the forecast, there's no need to quote both.

## Water temperature (°F)
- Below 70: cool for South Florida; worth a note for swimmers, snorkelers and divers.
- 70–76: comfortable with a thin wetsuit or a short swim.
- 77–86: typical and pleasant; mention only in passing, if at all.
- Above 86: very warm; no action needed.

## Hourly and day/night land forecast
Use for rain, thunderstorms and air temperature. Any mention of thunderstorms is worth a clause, with the timing if the hourly data shows it — lightning and sudden squall winds are the main summer hazard here. Rain chances under 30%% can be ignored.

## UV index
Mention only when the peak is 8 or higher, and only briefly.

# Style examples

Reference only — match the cadence and density, don't copy.

EXAMPLE — easy day:
East wind around 10 kt with seas near 2 ft through the afternoon, so it's a comfortable day outside for most boats. High tide is at 1:40 PM and water is 81°F. The forecast keeps it quiet through tonight.

EXAMPLE — advisory in effect:
A Small Craft Advisory runs through Friday afternoon, with east wind 20–25 kt and seas 6–8 ft outside — a day for protected water only. The sensor is already showing 18 kt gusting 22. Conditions should ease to 10–15 kt by Saturday per the forecast.

EXAMPLE — go early:
Light northwest wind and smooth water this morning, but the forecast builds it to 15–20 kt with a moderate chop by late afternoon, so the early window is the good one. Low tide is at 12:31 PM. A slight chance of showers later.

EXAMPLE — thunderstorms:
Seas are only 1–2 ft, but thunderstorms are in the forecast from about 2 PM, so plan to be back in before then. Wind is southeast near 10 kt until the storms arrive. Water is 84°F.

# What NOT to write

- "It's a beautiful day to be on the water!" — no information.
- "Water temperature is currently 79°F." — restating a card.
- "Conditions are good." — meaningless without the numbers behind it.
- "Mariners are advised to exercise caution." — bureaucratic boilerplate.
- "I would recommend…" — first person.

Now write the advisory for the snapshot in the user message.`

// systemPrompt assembles the full prompt for one location.
func (loc *coastalLocation) systemPrompt() string {
	return fmt.Sprintf(coastalPromptRules, loc.Name, loc.BasePath, loc.About)
}

// blurbClock formats a time the way the prompt asks the model to quote it.
func blurbClock(t time.Time) string {
	return t.In(etLocation).Format("Mon 3:04 PM")
}

// snapshot formats the location's live data for the model. Returns "" when
// there's too little to say anything useful (no tides and no marine forecast).
func (loc *coastalLocation) snapshot() string {
	return loc.snapshotAt(time.Now())
}

func (loc *coastalLocation) snapshotAt(now time.Time) string {
	loc.mu.RLock()
	defer loc.mu.RUnlock()

	if len(loc.tideHiLo) < 2 && len(loc.marine) == 0 {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Current %s snapshot. Local time now: %s.\n\n", loc.Name, blurbClock(now))

	// Advisories first — deduplicated across zones.
	seen := map[string]bool{}
	for _, z := range loc.marine {
		for _, a := range z.Advisories {
			if !seen[a] {
				seen[a] = true
				fmt.Fprintf(&sb, "ACTIVE ADVISORY (%s): %s\n", z.Name, a)
			}
		}
	}
	if len(seen) == 0 {
		sb.WriteString("Active advisories: none\n")
	}

	// Tides
	fmt.Fprintf(&sb, "\nTides at %s (ft above MLLW):\n", loc.TideStationName)
	if st, ok := tideAt(loc.tideHiLo, now); ok {
		dir := "falling"
		if st.Rising {
			dir = "rising"
		}
		fmt.Fprintf(&sb, "  Now: about %.1f ft and %s\n", st.Height, dir)
	}
	shown := 0
	for _, e := range loc.tideHiLo {
		t, err := time.Parse(time.RFC3339, e.Time)
		if err != nil || !t.After(now) {
			continue
		}
		fmt.Fprintf(&sb, "  %s: %s, %.1f ft\n", blurbClock(t), e.Type, e.Height)
		if shown++; shown >= 4 {
			break
		}
	}
	if shown == 0 {
		sb.WriteString("  n/a\n")
	}

	// Sensors
	if loc.waterTemp != nil {
		fmt.Fprintf(&sb, "\nWater temperature: %.1f °F (%s sensor, %s)\n",
			*loc.waterTemp, loc.waterTempSource, blurbClock(loc.waterTempAt))
	} else {
		sb.WriteString("\nWater temperature: n/a\n")
	}
	if w := loc.wind; w != nil {
		fmt.Fprintf(&sb, "Live wind (%s sensor): %s %.0f kt, gusting %.0f kt\n",
			w.Source, w.Direction, w.Knots, w.GustKnots)
	}

	// Marine zones: the next three periods of each.
	for _, z := range loc.marine {
		fmt.Fprintf(&sb, "\nMarine forecast — %s", z.Name)
		if z.Issued != "" {
			fmt.Fprintf(&sb, " (issued %s)", z.Issued)
		}
		sb.WriteString(":\n")
		n := len(z.Periods)
		if n > 3 {
			n = 3
		}
		for _, p := range z.Periods[:n] {
			fmt.Fprintf(&sb, "  %s: %s\n", p.Name, p.Text)
		}
	}
	if loc.marineSynopsis != "" {
		fmt.Fprintf(&sb, "\nForecaster synopsis: %s\n", loc.marineSynopsis)
	}

	// Land forecast
	if len(loc.hourly) > 0 {
		sb.WriteString("\nHourly land forecast (next ~12 hours, wind in mph):\n")
		n := len(loc.hourly)
		if n > 12 {
			n = 12
		}
		for _, p := range loc.hourly[:n] {
			when := p.StartTime
			if t, err := time.Parse(time.RFC3339, p.StartTime); err == nil {
				when = blurbClock(t)
			}
			fmt.Fprintf(&sb, "  %s: %d °F, %s, wind %s %s\n",
				when, p.Temperature, p.ShortForecast, p.WindSpeed, p.WindDirection)
		}
	}
	if len(loc.forecast) > 0 {
		sb.WriteString("\nDay/night land forecast:\n")
		n := len(loc.forecast)
		if n > 3 {
			n = 3
		}
		for _, p := range loc.forecast[:n] {
			fmt.Fprintf(&sb, "  %s: %d °F, %s, wind %s %s\n",
				p.Name, p.Temperature, p.ShortForecast, p.WindSpeed, p.WindDirection)
		}
	}

	if loc.uv != nil {
		if peak, ok := uvPeakToday(loc.uv); ok {
			fmt.Fprintf(&sb, "\nUV index: current %.1f, peak for the rest of today %.1f\n", loc.uv.Current, peak)
		} else {
			fmt.Fprintf(&sb, "\nUV index: current %.1f\n", loc.uv.Current)
		}
	}

	return sb.String()
}

// uvPeakToday is the highest forecast UV index among today's remaining hours.
func uvPeakToday(uv *UVData) (float64, bool) {
	today := uv.UpdatedAt
	if len(today) < 10 {
		return 0, false
	}
	today = today[:10]
	peak, found := 0.0, false
	for _, h := range uv.Hourly {
		if strings.HasPrefix(h.Time, today) && (!found || h.UVIndex > peak) {
			peak, found = h.UVIndex, true
		}
	}
	return peak, found
}

// blurbConfig returns (creating on first use) the summary endpoint config
// for this location.
func (loc *coastalLocation) blurbConfig() *lakeBlurbConfig {
	if loc.blurb == nil {
		loc.blurb = &lakeBlurbConfig{
			Key:          loc.Key,
			BasePath:     loc.BasePath + "/api/blurb",
			SystemPrompt: loc.systemPrompt(),
			SnapshotFn:   loc.snapshot,
		}
	}
	return loc.blurb
}
