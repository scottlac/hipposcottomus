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
	_ "time/tzdata" // embed the IANA tz database so America/New_York loads on alpine

	"github.com/anthropics/anthropic-sdk-go"
)

const (
	blurbModel           = anthropic.ModelClaudeHaiku4_5_20251001
	blurbModelLabel      = "claude-haiku-4-5"
	blurbMinInterval     = 30 * time.Minute // min time between on-demand generations
	blurbMaxOutputTokens = 280              // ~3 sentences, with a comfortable ceiling
	blurbStateFile       = "llm_usage.json"
)

// Haiku 4.5 pricing per 1M tokens (USD), confirmed against shared/models.md via
// the claude-api skill. If the model gets re-priced, only these constants
// need to change.
const (
	haikuInputPricePer1M      = 1.00
	haikuOutputPricePer1M     = 5.00
	haikuCacheWritePricePer1M = 1.25 // 1.25× input price for the default 5m TTL
	haikuCacheReadPricePer1M  = 0.10 // 0.1× input price
)

// LLMUsageEntry is the aggregate spend + token counts for one model. Keyed by
// model in the llmUsage map below — multiple lakes using the same model share
// one entry, which is the right thing for cost tracking.
type LLMUsageEntry struct {
	Model               string    `json:"model"`
	Calls               int64     `json:"calls"`
	InputTokens         int64     `json:"inputTokens"`
	OutputTokens        int64     `json:"outputTokens"`
	CacheCreationTokens int64     `json:"cacheCreationTokens"`
	CacheReadTokens     int64     `json:"cacheReadTokens"`
	TotalCostUSD        float64   `json:"totalCostUsd"`
	LastSuccess         time.Time `json:"lastSuccess"`
	LastError           string    `json:"lastError,omitempty"`
}

var (
	llmUsage   = map[string]*LLMUsageEntry{}
	llmUsageMu sync.RWMutex
)

// LakeBlurb is what the frontend renders. A nil/empty blurb means the
// generator hasn't produced one yet (just-deployed pod, or every attempt
// has failed) — the dashboard hides the section in that case.
type LakeBlurb struct {
	Text        string    `json:"text"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// lakeBlurbConfig bundles all the per-lake state for one blurb endpoint:
// where it's served, what system prompt drives it, how to read the current
// snapshot, and the in-memory state holding the last successful generation.
// New lakes wanting a blurb just add a new instance and call RegisterBlurb.
type lakeBlurbConfig struct {
	// Key is the short lake identifier used in log prefixes and the
	// TrackAPICall source label (e.g. "jordan", "minneola"). Lowercase
	// by convention.
	Key string

	// BasePath is the URL path the handler is mounted at.
	BasePath string

	// SystemPrompt is the static lake-specific persona + interpretation
	// guide. Sent on every generation — see the per-lake constants below.
	SystemPrompt string

	// SnapshotFn returns a structured snapshot string for the user message,
	// or "" if there isn't enough data yet to be worth a call.
	SnapshotFn func() string

	// In-memory state — initialized lazily by maybeTrigger / generate.
	mu      sync.RWMutex
	current *LakeBlurb
	genMu   sync.Mutex
}

// RegisterBlurb mounts cfg's HTTP handler and logs the registration. Call
// loadLLMUsage() once before any registration if you want the persisted
// running cost meter; InitBlurb below does that for you.
func RegisterBlurb(mux *http.ServeMux, cfg *lakeBlurbConfig) {
	mux.HandleFunc(cfg.BasePath, cfg.handle)
	log.Printf("[Blurb-%s] registered at %s (on-demand, min %s between calls 6am–8pm ET, one refresh allowed per 8pm–6am ET blackout)",
		cfg.Key, cfg.BasePath, blurbMinInterval)
}

// handle serves the cached blurb and opportunistically triggers a background
// regeneration when shouldGenerate says so. 204 (no content) if we've never
// generated one yet; the frontend hides the section in that case.
func (cfg *lakeBlurbConfig) handle(w http.ResponseWriter, r *http.Request) {
	cfg.maybeTrigger()

	cfg.mu.RLock()
	b := cfg.current
	cfg.mu.RUnlock()
	if b == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, b)
}

// maybeTrigger kicks off a background regeneration when shouldGenerate says so
// and no other generation for THIS lake is already running. Different lakes
// don't block each other.
func (cfg *lakeBlurbConfig) maybeTrigger() {
	cfg.mu.RLock()
	b := cfg.current
	cfg.mu.RUnlock()

	var lastGenAt time.Time
	if b != nil {
		lastGenAt = b.GeneratedAt
	}
	if !shouldGenerate(time.Now(), lastGenAt) {
		return
	}
	if !cfg.genMu.TryLock() {
		return // a generation for this lake is already in flight
	}

	go func() {
		defer cfg.genMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := cfg.generate(ctx)
		TrackAPICall("Anthropic-Blurb-"+cfg.Key, err)
		if err != nil {
			log.Printf("[Blurb-%s] on-demand generation failed: %v", cfg.Key, err)
			return
		}
		if e := saveLLMUsage(); e != nil {
			log.Printf("[Blurb-%s] failed to save LLM usage: %v", cfg.Key, e)
		}
	}()
}

// generate makes one Anthropic call and stores the result.
//
// NOTE on caching: we deliberately do NOT set cache_control on the system
// prompt. Generations are at least 30 minutes apart, but the default
// ephemeral cache TTL is 5 minutes — a cached prefix would always expire
// between calls, so every call would pay the 1.25× cache-WRITE premium and
// never get a READ. Plain uncached input ($1/M) is the cheapest option at
// this frequency.
func (cfg *lakeBlurbConfig) generate(ctx context.Context) error {
	snapshot := cfg.SnapshotFn()
	if snapshot == "" {
		return fmt.Errorf("no usable snapshot data yet")
	}

	client := anthropic.NewClient() // reads ANTHROPIC_API_KEY from env

	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     blurbModel,
		MaxTokens: blurbMaxOutputTokens,
		System: []anthropic.TextBlockParam{{
			Text: cfg.SystemPrompt,
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

	cfg.mu.Lock()
	cfg.current = &LakeBlurb{Text: final, GeneratedAt: time.Now()}
	cfg.mu.Unlock()

	recordLLMUsage(blurbModelLabel, resp.Usage)
	return nil
}

// recordLLMUsage updates the persisted aggregate using one call's reported
// token counts. Multiple lakes using the same model accumulate on one entry —
// which is what we want for cost tracking.
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

// ── Generation-gate decision functions ──────────────────────────

// etLocation is America/New_York, used for the overnight blackout window.
// The time/tzdata blank import guarantees this resolves even on alpine.
var etLocation *time.Location

func init() {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		log.Printf("[Blurb] could not load America/New_York (%v); blackout will use UTC", err)
		loc = time.UTC
	}
	etLocation = loc
}

// inBlackout reports whether t falls in the overnight no-generation window
// (8pm–6am Eastern). Nobody's checking lake conditions to go boating at 3am.
func inBlackout(t time.Time) bool {
	h := t.In(etLocation).Hour()
	return h >= 20 || h < 6
}

// blackoutStartFor returns the start (8pm ET) of the blackout window
// containing t. Pre-midnight → 8pm today; post-midnight → 8pm yesterday.
func blackoutStartFor(t time.Time) time.Time {
	et := t.In(etLocation)
	day := et
	if et.Hour() < 6 {
		day = et.AddDate(0, 0, -1)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 20, 0, 0, 0, etLocation)
}

// shouldGenerate is the pure decision function behind maybeTrigger.
//   - Daytime (6am–8pm ET): regen if last blurb older than blurbMinInterval (30m),
//     or there's no blurb yet.
//   - Blackout (8pm–6am ET): allow exactly one regen per blackout period —
//     when the last blurb predates the start of THIS blackout window. After
//     that one refresh, returns false until 6am.
func shouldGenerate(now, lastGenAt time.Time) bool {
	if !inBlackout(now) {
		return lastGenAt.IsZero() || now.Sub(lastGenAt) >= blurbMinInterval
	}
	if lastGenAt.IsZero() {
		return true
	}
	return lastGenAt.Before(blackoutStartFor(now))
}

// ── Init ────────────────────────────────────────────────────────

// InitBlurb is wired from main.go. Loads the persisted LLM-usage meter once,
// then registers every per-lake blurb endpoint. Adding a new lake = one more
// RegisterBlurb call here.
func InitBlurb(mux *http.ServeMux) {
	loadLLMUsage()
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		log.Println("[Blurb] ANTHROPIC_API_KEY not set — on-demand generation will fail until it's configured")
	}
	RegisterBlurb(mux, jordanBlurbConfig)
	RegisterBlurb(mux, minneolaBlurbConfig)
}
