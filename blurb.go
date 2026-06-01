package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

const (
	blurbBasePath        = basePath + "/api/blurb" // /lakedashboard/api/blurb
	blurbModel           = anthropic.ModelClaudeHaiku4_5_20251001
	blurbModelLabel      = "claude-haiku-4-5"
	blurbRefreshInterval = 30 * time.Minute
	blurbMaxOutputTokens = 280 // ~3 sentences, with a comfortable ceiling
	blurbStateFile       = "llm_usage.json"
)

// Haiku 4.5 pricing per 1M tokens (USD), confirmed against shared/models.md via
// the claude-api skill. If the model gets re-priced, only these three constants
// need to change.
const (
	haikuInputPricePer1M       = 1.00
	haikuOutputPricePer1M      = 5.00
	haikuCacheWritePricePer1M  = 1.25 // 1.25× input price for the default 5m TTL
	haikuCacheReadPricePer1M   = 0.10 // 0.1× input price
)

// LLMUsageEntry is the aggregate spend + token counts for one model. We track
// per-model so adding a second feature later (Sonnet, Opus) just adds a new
// map entry rather than touching this struct.
type LLMUsageEntry struct {
	Model                  string  `json:"model"`
	Calls                  int64   `json:"calls"`
	InputTokens            int64   `json:"inputTokens"`
	OutputTokens           int64   `json:"outputTokens"`
	CacheCreationTokens    int64   `json:"cacheCreationTokens"`
	CacheReadTokens        int64   `json:"cacheReadTokens"`
	TotalCostUSD           float64 `json:"totalCostUsd"`
	LastSuccess            time.Time `json:"lastSuccess"`
	LastError              string  `json:"lastError,omitempty"`
}

// llmUsage is the persisted aggregate spend across all LLM features. Keyed by
// model ID. Lives on the existing /data PVC alongside the other state files.
var (
	llmUsage    = map[string]*LLMUsageEntry{}
	llmUsageMu  sync.RWMutex
)

// LakeBlurb is what the frontend renders. A nil/empty blurb means the
// generator hasn't produced one yet (just-deployed pod, or every attempt
// has failed) — the dashboard hides the section in that case.
type LakeBlurb struct {
	Text        string    `json:"text"`
	GeneratedAt time.Time `json:"generatedAt"`
}

var (
	currentBlurb   *LakeBlurb
	currentBlurbMu sync.RWMutex
)

// jordanBoatingSystemPrompt is the static persona + interpretation guide we
// send on every generation. Kept intentionally long so it crosses the 4096-token
// Haiku 4.5 cache minimum — at ~5000 tokens, prompt caching cuts the per-call
// input cost ~10× from the 2nd call onward (verified via resp.Usage.CacheReadInputTokens).
//
// IMPORTANT: every byte of this string is part of the cache prefix. Do NOT
// interpolate timestamps, request IDs, or any other per-call data here, or
// the cache will silently invalidate on every request.
const jordanBoatingSystemPrompt = `You write 2–3 sentence boating advisories for the Jordan Lake dashboard at hipposcottomus.com/lakedashboard/. The dashboard already shows users every raw reading — your job is to synthesize them into one paragraph a human can act on without doing the mental math themselves.

# Hard rules (non-negotiable)

1. Use ONLY the numbers and conditions in the data snapshot at the end of the user message. Do not invent forecasts, temperatures, wind speeds, or events. If a field shows "n/a" or "—", treat it as unknown and silently omit it from your reasoning — do not say "data is unavailable for X".

2. Output exactly 2 or 3 sentences of running prose. No bullet points, no headers, no "Today on Jordan Lake:" preambles, no closing pleasantries like "Have a great day on the water!". Just the advisory.

3. Length cap: ~50 words. If you find yourself reaching for a fourth sentence, you're padding — cut.

4. Hedged language is fine and expected. "Forecast calls for…", "expect…", "conditions through midday should…", "based on the forecast…". Never claim certainty about future weather — you only know what the forecast says, not what will happen.

5. Tone: practical and specific. You are advising a knowledgeable friend who already knows how to operate a boat. Don't explain what wind direction is, don't define UV index, don't suggest sunscreen as if it were a revelation.

6. No exaggeration. A water temperature of 68°F is "still cool for swimming", not "dangerously cold". 12 mph winds are "manageable for most craft", not "treacherous". Reserve strong language ("hazardous", "stay off the water", "small-craft advisory conditions") for genuinely hazardous numbers — see the interpretation guide below.

7. No safety boilerplate. Don't recommend life jackets, weather radios, float plans, etc. unless the data shows something specifically dangerous (e.g. thunderstorms with high wind).

8. Never mention "the model", "AI", "Claude", or that this was generated. Speak as the dashboard.

# About Jordan Lake (B. Everett Jordan Lake, NC)

A USACE-managed reservoir in central North Carolina near Apex/Cary. Full pool is 216 ft elevation; the dashboard reports water level as a delta from full pool, so 0.0 ft means full pool, -1.5 ft means 1.5 feet below full pool. Drawdown is normal in late summer and winter. Power boating, sailing, kayaking, and fishing all happen here; swimming areas are at Ebenezer Church and Vista Point.

Typical late-spring / early-summer surface water temps run 70–80°F. Late fall through early spring: 45–60°F.

# How to read each field

## Water temperature (°F)
- < 60°F: too cold for casual swimming; serious risk of cold-water shock if someone goes in unexpectedly. "Cold-water risk if you go in unexpectedly" — only mention if water-contact activities are likely (swimming, paddleboarding without a wetsuit, etc.).
- 60–68°F: brisk. Swimmers will notice. Not advice-worthy on its own.
- 69–74°F: comfortable swimming for most people. Worth a brief positive note if it's recently warmed up.
- 75–82°F: prime swimming weather.
- > 82°F: warm; algae watch — but only mention HABs if there's a specific advisory in the data, which there usually isn't.

## Water level (delta vs. full pool, in feet)
- Within ±0.5 ft: essentially full pool. Don't mention unless launching matters (rare).
- -0.5 to -2.0 ft: mild drawdown. Most ramps still fine. Mention only if relevant to a launching question.
- -2.0 to -4.0 ft: noticeable drawdown. Some shallower ramps may have exposed concrete or mud; suggest scouting your usual ramp before towing.
- < -4.0 ft: significant drawdown. Recommend checking the USACE level page before towing; some areas may not be navigable.
- Above full pool (positive delta): high water. Note that floating debris and submerged hazards may be present at typical heights.

## Air temperature (current and forecast highs)
Translate into wearable terms. < 50°F: layers; 50–65°F: jacket; 65–75°F: light layers; 75–85°F: t-shirt; > 85°F: hydrate / shade.

## Wind speed and direction (NWS forecast, mph)
- 0–5 mph: glass / near-calm. Mention positively for sailing ("light air, expect drifters" — though don't use sailor-jargon unless the situation clearly warrants).
- 5–10 mph: pleasant for most activities. No specific advisory.
- 10–15 mph: moderate. Kayaks and small craft will work harder; mention if board sports or kayaking is plausible.
- 15–20 mph: choppy. Discourage small-craft / first-timers / inflatables. Power boats fine; sailors will be happy.
- 20–25 mph: small-craft advisory territory for inland lakes. Recommend most non-power craft stay off the open water. Sailors should reef.
- > 25 mph: hazardous for most recreation. Strong wording is appropriate.

Wind direction matters less on Jordan Lake than on coastal water; only mention if it's notable (e.g. due NW after a frontal passage).

## UV index
- 0–2: low — no special precautions.
- 3–5: moderate — mention sunscreen only if the user will be out for hours.
- 6–7: high — sunscreen + hat for anything > 1 hour.
- 8–10: very high — sunscreen, hat, and shade during midday hours.
- 11+: extreme — limit midday exposure where possible.

Only mention UV if it's high or very high. Don't recite the tier label; translate ("UV will be very high midday — plan on sunscreen and a hat if you're out past noon").

## Cloud cover, precipitation chance, weather descriptors
- "Mostly sunny" / "sunny": say "clear" or "sunny" — don't translate, NWS phrases are familiar.
- Rain chance < 30%: usually safe to ignore.
- Rain chance 30–60%: "scattered showers possible" / "watch for showers in the afternoon" if forecast shows a clear time-of-day skew.
- Rain chance > 60%: "expect rain through [period]" — recommend rescheduling if windows are tight.
- Any mention of "thunderstorms" or "T-storms" in the forecast text: that's the headline. Recommend staying off the water during storms, especially for sailors and metal-railed boats. This IS a case where a specific safety mention is warranted.

# Style examples

These are reference examples — do not copy verbatim. Match the cadence and density.

EXAMPLE — pleasant spring day, low wind, comfortable water:
Surface temps are up to 72°F with the forecast holding light SW winds in the 5–8 mph range through the afternoon — comfortable conditions for most craft. Water level is essentially full pool, so all ramps should be fine. Bring a light jacket for the morning launch.

EXAMPLE — choppy summer day, high UV:
Light easterly wind early, building to a steady 14–18 mph SW out of the south by midafternoon — kayakers should plan a morning paddle and power boats will find some chop on the open water. UV will be very high through midday, so sunscreen and a hat are worth it for any extended trip. Water is a comfortable 78°F.

EXAMPLE — cold morning, low risk but worth flagging:
Air temp starts in the low 40s and only reaches the mid 50s, with light westerly wind around 6 mph — manageable for everyone but dress for it. Surface water is still 58°F, so cold-water risk if you go in unexpectedly. Lake is about 1.5 ft below full pool, which shouldn't affect most ramps.

EXAMPLE — small-craft advisory weather:
NWS forecasts sustained 22–28 mph SW winds with gusts into the 30s — most non-power craft should stay off the open water today, and sailors will want to reef early if they go at all. Showers are likely after noon. Conditions ease somewhat by evening per the forecast.

EXAMPLE — thunderstorms forecast:
Afternoon thunderstorms in the forecast with rain chance climbing through the day — plan any time on the water for the morning and be off it well before storms develop. Conditions before then should be mild, with surface temps around 75°F and winds light. Water level is essentially at full pool.

EXAMPLE — significant drawdown:
Water level is 4.8 ft below full pool, so check the USACE page or scout your usual ramp before towing — some of the shallower ramps may not be usable. Conditions are otherwise calm: 70°F water, light north wind, no rain in the forecast through evening. UV will be moderate, no special precautions needed.

# What NOT to do

DO NOT WRITE:
- "It's a beautiful day to be on Jordan Lake!" — vague, no information.
- "Water temperature is currently 72°F." — the dashboard already shows this; don't restate.
- "Conditions are good." — meaningless without qualification.
- "Please remember to wear a life jacket." — safety boilerplate.
- "I would recommend…" — first person.
- "The forecast indicates a high probability of precipitation events occurring during the afternoon hours." — bureaucratic.
- "Wear sunscreen!" — no, "UV is very high midday, sunscreen for any extended trip" if it's actually high.

DO WRITE:
- A specific synthesis using the numbers that makes the dashboard easier to act on.
- Hedged, forecast-aware language ("expect", "should", "the forecast calls for").
- Activity-aware framing — kayakers and power boaters care about different things.

Now generate the blurb for the snapshot in the user message.`

// snapshotFromCurrentState reads the Jordan Lake live data (water temp, level,
// weather, UV) from the existing globals and formats a compact snapshot the
// model can consume. Returns "" if there's not enough data to be worth a call.
func snapshotFromCurrentState() string {
	var sb strings.Builder
	sb.WriteString("Current Jordan Lake snapshot:\n\n")

	if pt, ok := tempHistory.Latest(); ok {
		fmt.Fprintf(&sb, "Water temperature: %.1f °F (as of %s)\n", pt.Value, pt.Date)
	} else {
		sb.WriteString("Water temperature: n/a\n")
	}

	if pt, ok := levelHistory.Latest(); ok {
		delta := pt.Value - fullPoolLevel
		fmt.Fprintf(&sb, "Water level: %.2f ft (delta from full pool of %g ft, absolute %.2f ft, as of %s)\n",
			delta, fullPoolLevel, pt.Value, pt.Date)
	} else {
		sb.WriteString("Water level: n/a\n")
	}

	weather.mu.RLock()
	defer weather.mu.RUnlock()

	if len(weather.Hourly) > 0 {
		now := weather.Hourly[0]
		fmt.Fprintf(&sb, "Current air conditions (%s): %d °F, %s, wind %s %s\n",
			now.Name, now.Temperature, now.ShortForecast, now.WindSpeed, now.WindDirection)
	}

	// Next 12 hours of hourly so the model can describe a trend.
	if len(weather.Hourly) > 1 {
		sb.WriteString("Hourly forecast (next ~12 hours):\n")
		n := len(weather.Hourly)
		if n > 12 {
			n = 12
		}
		for _, p := range weather.Hourly[:n] {
			fmt.Fprintf(&sb, "  %s: %d °F, %s, wind %s %s\n",
				p.StartTime, p.Temperature, p.ShortForecast, p.WindSpeed, p.WindDirection)
		}
	}

	// Day/night forecast for the rest of "today".
	if len(weather.Forecast) > 0 {
		sb.WriteString("Day/night forecast (next ~3 periods):\n")
		n := len(weather.Forecast)
		if n > 3 {
			n = 3
		}
		for _, p := range weather.Forecast[:n] {
			fmt.Fprintf(&sb, "  %s: high/low %d °F, %s, wind %s %s\n",
				p.Name, p.Temperature, p.ShortForecast, p.WindSpeed, p.WindDirection)
		}
	}

	if weather.UV != nil {
		var peak float64 = -1
		today := weather.UV.UpdatedAt
		if len(today) >= 10 {
			today = today[:10]
		}
		for _, h := range weather.UV.Hourly {
			if today != "" && !strings.HasPrefix(h.Time, today) {
				continue
			}
			if h.UVIndex > peak {
				peak = h.UVIndex
			}
		}
		if peak >= 0 {
			fmt.Fprintf(&sb, "UV index: current %.1f, peak today %.1f\n", weather.UV.Current, peak)
		} else {
			fmt.Fprintf(&sb, "UV index: current %.1f\n", weather.UV.Current)
		}
	} else {
		sb.WriteString("UV index: n/a\n")
	}

	// We need at least *some* data — if everything is n/a there's nothing useful
	// to say and we shouldn't burn an API call.
	body := sb.String()
	if !strings.Contains(body, "°F") && !strings.Contains(body, "ft") {
		return ""
	}
	return body
}

// generateLakeBlurb makes one Anthropic call and updates the in-memory blurb +
// the cumulative LLM usage stats. Idempotent and safe to call concurrently;
// only the latest result is stored.
func generateLakeBlurb(ctx context.Context) error {
	snapshot := snapshotFromCurrentState()
	if snapshot == "" {
		return fmt.Errorf("no usable snapshot data yet")
	}

	client := anthropic.NewClient() // reads ANTHROPIC_API_KEY from env

	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     blurbModel,
		MaxTokens: blurbMaxOutputTokens,
		System: []anthropic.TextBlockParam{{
			Text:         jordanBoatingSystemPrompt,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(snapshot)),
		},
	})
	if err != nil {
		recordLLMError(blurbModelLabel, err)
		return err
	}

	var text strings.Builder
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(tb.Text)
		}
	}
	final := strings.TrimSpace(text.String())
	if final == "" {
		recordLLMError(blurbModelLabel, fmt.Errorf("empty response"))
		return fmt.Errorf("empty response from model")
	}

	currentBlurbMu.Lock()
	currentBlurb = &LakeBlurb{Text: final, GeneratedAt: time.Now()}
	currentBlurbMu.Unlock()

	recordLLMUsage(blurbModelLabel, resp.Usage)
	return nil
}

// recordLLMUsage updates the persisted aggregate using one call's reported
// token counts. Pricing is constants above — if a model gets re-priced, only
// those constants change.
func recordLLMUsage(model string, u anthropic.Usage) {
	in := u.InputTokens
	out := u.OutputTokens
	cw := u.CacheCreationInputTokens
	cr := u.CacheReadInputTokens

	cost := float64(in)*haikuInputPricePer1M/1e6 +
		float64(out)*haikuOutputPricePer1M/1e6 +
		float64(cw)*haikuCacheWritePricePer1M/1e6 +
		float64(cr)*haikuCacheReadPricePer1M/1e6

	llmUsageMu.Lock()
	defer llmUsageMu.Unlock()
	entry, ok := llmUsage[model]
	if !ok {
		entry = &LLMUsageEntry{Model: model}
		llmUsage[model] = entry
	}
	entry.Calls++
	entry.InputTokens += in
	entry.OutputTokens += out
	entry.CacheCreationTokens += cw
	entry.CacheReadTokens += cr
	entry.TotalCostUSD += cost
	entry.LastSuccess = time.Now()
	entry.LastError = ""

	log.Printf("[Blurb] call ok: in=%d out=%d cache_write=%d cache_read=%d cost=$%.6f (cumulative $%.4f over %d calls)",
		in, out, cw, cr, cost, entry.TotalCostUSD, entry.Calls)
}

func recordLLMError(model string, err error) {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	llmUsageMu.Lock()
	defer llmUsageMu.Unlock()
	entry, ok := llmUsage[model]
	if !ok {
		entry = &LLMUsageEntry{Model: model}
		llmUsage[model] = entry
	}
	entry.LastError = msg
}

// snapshotLLMUsage returns a copy for the analytics API.
func snapshotLLMUsage() []LLMUsageEntry {
	llmUsageMu.RLock()
	defer llmUsageMu.RUnlock()
	out := make([]LLMUsageEntry, 0, len(llmUsage))
	for _, e := range llmUsage {
		out = append(out, *e)
	}
	return out
}

// Persistence — survives pod restarts so the running cost meter doesn't reset.

func llmUsagePath() string { return filepath.Join(getDataDir(), blurbStateFile) }

func saveLLMUsage() error {
	llmUsageMu.RLock()
	data, err := json.MarshalIndent(llmUsage, "", "  ")
	llmUsageMu.RUnlock()
	if err != nil {
		return err
	}
	path := llmUsagePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func loadLLMUsage() {
	path := llmUsagePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[Blurb] failed to load LLM usage: %v", err)
		}
		return
	}
	var loaded map[string]*LLMUsageEntry
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("[Blurb] failed to parse LLM usage: %v", err)
		return
	}
	llmUsageMu.Lock()
	defer llmUsageMu.Unlock()
	for k, v := range loaded {
		llmUsage[k] = v
	}
	log.Printf("[Blurb] loaded LLM usage for %d model(s)", len(loaded))
}

// Background loop — generate immediately on startup, then every refresh interval.
func blurbLoop() {
	// Wait a moment for the initial scrape + NWS update to populate the
	// snapshot. Without this, the first call gets "n/a" for everything.
	time.Sleep(20 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err := generateLakeBlurb(ctx)
	cancel()
	TrackAPICall("Anthropic-Blurb", err)
	if err != nil {
		log.Printf("[Blurb] initial generation failed: %v", err)
	}
	if err == nil {
		if e := saveLLMUsage(); e != nil {
			log.Printf("[Blurb] failed to save LLM usage: %v", e)
		}
	}

	ticker := time.NewTicker(blurbRefreshInterval)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := generateLakeBlurb(ctx)
		cancel()
		TrackAPICall("Anthropic-Blurb", err)
		if err != nil {
			log.Printf("[Blurb] scheduled generation failed: %v", err)
			continue
		}
		if e := saveLLMUsage(); e != nil {
			log.Printf("[Blurb] failed to save LLM usage: %v", e)
		}
	}
}

// HTTP handler — returns the cached blurb. 204 (no content) if we've never
// generated one yet; the frontend hides the section in that case.
func handleLakeBlurb(w http.ResponseWriter, r *http.Request) {
	currentBlurbMu.RLock()
	b := currentBlurb
	currentBlurbMu.RUnlock()
	if b == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, b)
}

// InitBlurb is wired from main.go. Won't actually call the API until
// ANTHROPIC_API_KEY is in the env — the SDK's NewClient() will surface the
// missing-key as an error on first call, captured by TrackAPICall.
func InitBlurb(mux *http.ServeMux) {
	loadLLMUsage()
	mux.HandleFunc(blurbBasePath, handleLakeBlurb)
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		log.Println("[Blurb] ANTHROPIC_API_KEY not set — generation loop will run but every call will fail until it's configured")
	}
	go blurbLoop()
	log.Printf("[Blurb] registered at %s (refresh every %s, model %s)", blurbBasePath, blurbRefreshInterval, blurbModel)
}
