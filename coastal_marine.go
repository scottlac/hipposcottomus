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

// Marine zone forecasts come from the NWS Coastal Waters Forecast (CWF) text
// product of the local forecast office, read from two places:
//
//  1. api.weather.gov — the raw product text, the primary source:
//
//     AMZ630-192000-
//     Biscayne Bay-
//     241 AM EST Thu Nov 19 2026
//
//     ...SMALL CRAFT ADVISORY IN EFFECT THROUGH THIS AFTERNOON...
//
//     .TODAY...E winds 10 to 15 kt. A moderate chop.
//     .TONIGHT...N winds 5 to 10 kt. Light chop.
//
//     $$
//
//  2. NDBC's HTML rendering of the same product, the fallback, one page per
//     product (FZUS52.KMFL = Miami, FZUS52.KKEY = Key West):
//
//     <p>AMZ630-192000<br>
//     Biscayne Bay<br>
//     241 AM EST Thu Nov 19 2026
//     </p>
//     <p class="warn-highlight">SMALL CRAFT ADVISORY IN EFFECT ...</p>
//     <p><span class="fcst-day">TODAY</span><br>
//     E winds 10 to 15 kt...</p>
//     <hr>
const (
	ndbcForecastURLFmt   = "https://www.ndbc.noaa.gov/data/Forecasts/%s.html"
	nwsProductListURLFmt = "https://api.weather.gov/products/types/CWF/locations/%s"
	nwsProductURLFmt     = "https://api.weather.gov/products/%s"
)

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

// ── Raw text product ────────────────────────────────────────────

var (
	// "AMZ651-671-192000-" on a line of its own.
	marineTextHeaderRe = regexp.MustCompile(`^[A-Z]{2}Z\d{3}[A-Z0-9>\-]*-\d{6}-$`)
	// ".TODAY...E winds 10 to 15 kt." — the period name is upper-case, which
	// tells it apart from prose headings like ".Synopsis for Jupiter Inlet…".
	marineTextPeriodRe = regexp.MustCompile(`^\.([A-Z][A-Z0-9 ]*?)\.\.\.(.*)$`)
)

// marineTextSegment returns the lines of the "$$"-delimited segment of a CWF
// product that covers zone, starting just after its zone-code header line.
func marineTextSegment(product, zone string) []string {
	lines := strings.Split(strings.ReplaceAll(product, "\r", ""), "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if !marineTextHeaderRe.MatchString(line) {
			continue
		}
		covered := false
		for _, z := range expandMarineZones(line) {
			if z == zone {
				covered = true
				break
			}
		}
		if !covered {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "$$" || marineTextHeaderRe.MatchString(t) {
				end = j
				break
			}
		}
		return lines[i+1 : end]
	}
	return nil
}

// parseMarineTextZone extracts one zone's forecast from a raw CWF product.
// Returns nil if the zone isn't in the product or has no forecast periods.
func parseMarineTextZone(product, zone string) *MarineForecast {
	seg := marineTextSegment(product, zone)
	if seg == nil {
		return nil
	}
	mf := &MarineForecast{Zone: zone, Advisories: []string{}}

	// Everything up to the first blank line is zone names and the issue time.
	i := 0
	for ; i < len(seg) && strings.TrimSpace(seg[i]) != ""; i++ {
		if t := strings.TrimSpace(seg[i]); marineIssuedRe.MatchString(t) {
			mf.Issued = t
		}
	}

	var (
		period   *MarinePeriod
		advisory []string // lines of a "...HEADLINE..." being collected
		note     []string
	)
	join := func(parts []string) string {
		return strings.TrimSpace(marineSpaceRe.ReplaceAllString(strings.Join(parts, " "), " "))
	}
	flushPeriod := func() {
		if period != nil {
			period.Text = join([]string{period.Text})
			if period.Text != "" {
				mf.Periods = append(mf.Periods, *period)
			}
			period = nil
		}
	}
	flushNote := func() {
		if t := join(note); t != "" {
			mf.Notes = append(mf.Notes, t)
		}
		note = nil
	}
	for ; i < len(seg); i++ {
		line := strings.TrimSpace(seg[i])
		switch {
		case advisory != nil:
			advisory = append(advisory, line)
			if strings.HasSuffix(line, "...") || line == "" {
				if t := strings.Trim(join(advisory), ". "); t != "" {
					mf.Advisories = append(mf.Advisories, t)
				}
				advisory = nil
			}
		case line == "":
			flushPeriod()
			flushNote()
		case strings.HasPrefix(line, "..."):
			flushPeriod()
			flushNote()
			if strings.HasSuffix(line, "...") && len(line) > 6 {
				mf.Advisories = append(mf.Advisories, strings.Trim(line, ". "))
			} else {
				advisory = []string{line}
			}
		case marineTextPeriodRe.MatchString(line):
			flushPeriod()
			flushNote()
			m := marineTextPeriodRe.FindStringSubmatch(line)
			period = &MarinePeriod{Name: m[1], Text: m[2]}
		case period != nil:
			period.Text += " " + line
		default:
			note = append(note, line)
		}
	}
	flushPeriod()
	flushNote()

	if len(mf.Periods) == 0 {
		return nil
	}
	return mf
}

// parseMarineTextSynopsis returns the forecaster's synopsis paragraph from the
// synopsis segment of a raw CWF product, or "". The heading is one or more
// lines ending in "...", and offices differ in what follows it:
//
//	Key West:  .SYNOPSIS...                          text on the next line
//	Newport:   .Synopsis for Eastern NC waters...    text on the next line
//	Miami:     .Synopsis for Jupiter Inlet to…       heading wraps, then a
//	           …Bonita Beach FL out to 60 nm...      blank line, then text
//
// All of them follow with Gulf Stream position paragraphs, which are left out.
func parseMarineTextSynopsis(product, zone string) string {
	var paras [][]string
	var cur []string
	for _, line := range append(marineTextSegment(product, zone), "") {
		if t := strings.TrimSpace(line); t != "" {
			cur = append(cur, t)
		} else if cur != nil {
			paras = append(paras, cur)
			cur = nil
		}
	}
	join := func(lines []string) string {
		return marineSpaceRe.ReplaceAllString(strings.Join(lines, " "), " ")
	}
	for i, p := range paras {
		if !strings.HasPrefix(strings.ToUpper(p[0]), ".SYNOPSIS") {
			continue
		}
		for k, line := range p {
			if strings.HasSuffix(line, "...") {
				if rest := p[k+1:]; len(rest) > 0 {
					return join(rest) // text under the heading
				}
				break
			}
		}
		if i+1 < len(paras) {
			return join(paras[i+1]) // text in the next paragraph
		}
	}
	return ""
}

// fetchMarineProductText returns the text of a forecast office's latest
// Coastal Waters Forecast from the NWS API. office is the three-letter
// office id ("MFL", "KEY").
func fetchMarineProductText(office string) (string, error) {
	var list struct {
		Graph []struct {
			ID string `json:"id"`
		} `json:"@graph"`
	}
	if err := coastalGetJSON(fmt.Sprintf(nwsProductListURLFmt, office), &list); err != nil {
		return "", err
	}
	if len(list.Graph) == 0 || list.Graph[0].ID == "" {
		return "", fmt.Errorf("NWS lists no CWF products for %s", office)
	}
	var product struct {
		ProductText string `json:"productText"`
	}
	if err := coastalGetJSON(fmt.Sprintf(nwsProductURLFmt, list.Graph[0].ID), &product); err != nil {
		return "", err
	}
	if product.ProductText == "" {
		return "", fmt.Errorf("NWS product %s has no text", list.Graph[0].ID)
	}
	return product.ProductText, nil
}

// ── NDBC HTML page ──────────────────────────────────────────────

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
