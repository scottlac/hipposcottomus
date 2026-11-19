package main

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NWS coastal waters forecasts, as republished by NDBC. One HTML page per
// forecast office product (FZUS52.KMFL = Miami, FZUS52.KKEY = Key West), with
// one block per zone group:
//
//	<p>AMZ651-671-192000<br>
//	Coastal waters from Deerfield Beach to Ocean Reef FL out 20 NM<br>
//	241 AM EST Thu Nov 19 2026
//	</p>
//	<p class="warn-highlight">SMALL CRAFT ADVISORY IN EFFECT ...</p>
//	<p><span class="fcst-day">TODAY</span><br>
//	E winds 20 to 25 kt...</p>
//	<hr>
const ndbcForecastURLFmt = "https://www.ndbc.noaa.gov/data/Forecasts/%s.html"

// MarinePeriod is one named forecast period ("TODAY", "TONIGHT", "FRI"…).
type MarinePeriod struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// MarineForecast is one zone's parsed forecast.
type MarineForecast struct {
	Zone       string         `json:"zone"`       // "AMZ630"
	Issued     string         `json:"issued"`     // "241 AM EST Thu Nov 19 2026"
	Advisories []string       `json:"advisories"` // "SMALL CRAFT ADVISORY IN EFFECT…"
	Periods    []MarinePeriod `json:"periods"`
	Notes      []string       `json:"notes,omitempty"` // trailing remarks, e.g. "Winds and waves higher in and near tstms."
	Synopsis   string         `json:"synopsis,omitempty"`
}

var (
	// Zone-group header, e.g. "AMZ651-671-192000", "GMZ042>044-192130",
	// "GMZ052>055-072>075-192130". Always opens a <p>.
	marineHeaderRe = regexp.MustCompile(`<p>\s*((?:[A-Z]{2}Z\d{3})[A-Z0-9>\-]*?-\d{6})-?\s*<br\s*/?>`)
	marineParaRe   = regexp.MustCompile(`(?s)<p([^>]*)>(.*?)</p>`)
	marineDayRe    = regexp.MustCompile(`(?s)^\s*<span class="fcst-day">(.*?)</span>\s*(?:<br\s*/?>)?(.*)$`)
	marineBrRe     = regexp.MustCompile(`<br\s*/?>`)
	marineTagRe    = regexp.MustCompile(`<[^>]*>`)
	marineSpaceRe  = regexp.MustCompile(`\s+`)
	marineIssuedRe = regexp.MustCompile(`^\d{3,4} (?:AM|PM) [A-Z]{3,4} `)
)

// expandMarineZones turns a UGC zone header into the list of zone codes it
// covers: "AMZ651-671-192000" → [AMZ651 AMZ671]; "GMZ042>044-192130" →
// [GMZ042 GMZ043 GMZ044]. The trailing 6-digit token is the expiry time.
func expandMarineZones(header string) []string {
	var zones []string
	prefix := ""
	for _, tok := range strings.Split(header, "-") {
		tok = strings.TrimSpace(tok)
		if tok == "" || len(tok) == 6 && isDigits(tok) {
			continue
		}
		if len(tok) >= 6 && !isDigits(tok[:3]) {
			prefix = tok[:3]
			tok = tok[3:]
		}
		if prefix == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(tok, ">")
		if !isRange {
			if len(tok) == 3 && isDigits(tok) {
				zones = append(zones, prefix+tok)
			}
			continue
		}
		a, errA := strconv.Atoi(lo)
		b, errB := strconv.Atoi(hi)
		if errA != nil || errB != nil || b < a || b-a > 50 {
			continue
		}
		for n := a; n <= b; n++ {
			zones = append(zones, fmt.Sprintf("%s%03d", prefix, n))
		}
	}
	return zones
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// marineText flattens an HTML fragment to one line of plain text.
func marineText(fragment string) string {
	s := marineTagRe.ReplaceAllString(fragment, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(marineSpaceRe.ReplaceAllString(s, " "))
}

// marineBlock returns the raw HTML of the zone-group block covering zone, or
// "" if the page has no such block. A block runs from its header to the next
// header, the next <hr>, or the end of the forecast <div>.
func marineBlock(page, zone string) string {
	headers := marineHeaderRe.FindAllStringSubmatchIndex(page, -1)
	for i, h := range headers {
		covered := false
		for _, z := range expandMarineZones(page[h[2]:h[3]]) {
			if z == zone {
				covered = true
				break
			}
		}
		if !covered {
			continue
		}
		end := len(page)
		if i+1 < len(headers) {
			end = headers[i+1][0]
		}
		block := page[h[0]:end]
		for _, stop := range []string{"<hr", "</div>"} {
			if j := strings.Index(block, stop); j >= 0 {
				block = block[:j]
			}
		}
		return block
	}
	return ""
}

// parseMarineZone extracts one zone's forecast from an NDBC forecast page.
// Returns nil if the zone isn't on the page or has no forecast periods.
func parseMarineZone(page, zone string) *MarineForecast {
	block := marineBlock(page, zone)
	if block == "" {
		return nil
	}
	mf := &MarineForecast{Zone: zone, Advisories: []string{}}
	for i, m := range marineParaRe.FindAllStringSubmatch(block, -1) {
		attrs, body := m[1], m[2]
		if i == 0 {
			// Header paragraph: zone codes, zone names, then the issue time.
			for _, line := range marineBrRe.Split(body, -1) {
				for _, sub := range strings.Split(line, "\n") {
					if t := marineText(sub); marineIssuedRe.MatchString(t) {
						mf.Issued = t
					}
				}
			}
			continue
		}
		if strings.Contains(attrs, "warn-highlight") {
			if t := marineText(body); t != "" {
				mf.Advisories = append(mf.Advisories, t)
			}
			continue
		}
		if d := marineDayRe.FindStringSubmatch(body); d != nil {
			name, text := marineText(d[1]), marineText(d[2])
			if name != "" && text != "" {
				mf.Periods = append(mf.Periods, MarinePeriod{Name: name, Text: text})
			}
			continue
		}
		if t := marineText(body); t != "" {
			mf.Notes = append(mf.Notes, t)
		}
	}
	if len(mf.Periods) == 0 {
		return nil
	}
	return mf
}

// parseMarineSynopsis returns the forecaster's synopsis paragraph from the
// synopsis block (zone AMZ600 for Miami, GMZ005 for Key West), or "".
// Both offices follow the synopsis with Gulf Stream position paragraphs, so
// only the first real paragraph after the heading is kept.
func parseMarineSynopsis(page, zone string) string {
	block := marineBlock(page, zone)
	if block == "" {
		return ""
	}
	for i, m := range marineParaRe.FindAllStringSubmatch(block, -1) {
		if i == 0 {
			continue // header
		}
		t := marineText(m[2])
		upper := strings.ToUpper(t)
		if t == "" || strings.HasPrefix(upper, ".SYNOPSIS") || upper == "SYNOPSIS" {
			continue
		}
		return t
	}
	return ""
}

// fetchMarinePage downloads one forecast product page.
func fetchMarinePage(product string) (string, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf(ndbcForecastURLFmt, product), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", coastalUserAgent)
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("NDBC returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
