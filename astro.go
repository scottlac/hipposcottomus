package main

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	satellite "github.com/joshuaferrara/go-satellite"
)

const (
	astroBasePath = "/astronomy"

	// CelesTrak TLE feed for the ISS (catalog number 25544).
	issTLEURL = "https://celestrak.org/NORAD/elements/gp.php?CATNR=25544&FORMAT=TLE"

	// Default observer coords: Cary, NC.
	defaultLat = 35.7915
	defaultLon = -78.7811

	tleRefreshInterval = 12 * time.Hour
	tleRetryInterval   = 15 * time.Minute

	// Horizon altitudes (degrees).
	sunriseAlt      = -0.833
	civilTwiAlt     = -6.0
	nauticalTwiAlt  = -12.0
	astroTwiAlt     = -18.0
	goldenHourAlt   = 6.0
	goldenHourEndAlt = -4.0
	moonriseAlt     = -0.583

	// Synodic-month anchor: 2000-01-06 18:14 UT (known new moon).
	synodicAnchorJD = 2451549.5 + (18.0+14.0/60.0)/24.0
	synodicMonth    = 29.530588853

	// Mean obliquity of the ecliptic, J2000 (degrees).
	meanObliquity = 23.4393
)

//go:embed astro
var astroFiles embed.FS

type tleData struct {
	mu      sync.RWMutex
	sat     *satellite.Satellite
	fetched time.Time
}

var issTLE = &tleData{}

// ── TLE fetching ────────────────────────────────────────────────

func fetchISSTLE() error {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(issTLEURL)
	if err != nil {
		return fmt.Errorf("fetching TLE: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TLE returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading TLE: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	var line1, line2 string
	for _, ln := range lines {
		t := strings.TrimRight(ln, " \t")
		if strings.HasPrefix(t, "1 ") && len(t) >= 69 {
			line1 = t
		} else if strings.HasPrefix(t, "2 ") && len(t) >= 69 {
			line2 = t
		}
	}
	if line1 == "" || line2 == "" {
		return fmt.Errorf("TLE parse: missing line1/line2 in response")
	}
	sat := satellite.TLEToSat(line1, line2, satellite.GravityWGS84)
	if sat.Error != 0 {
		return fmt.Errorf("TLE init error %d: %s", sat.Error, sat.ErrorStr)
	}
	issTLE.mu.Lock()
	issTLE.sat = &sat
	issTLE.fetched = time.Now()
	issTLE.mu.Unlock()
	log.Printf("[Astro] Loaded ISS TLE")
	return nil
}

func tleLoop() {
	for {
		err := fetchISSTLE()
		TrackAPICall("CelesTrak-TLE", err)
		if err != nil {
			log.Printf("[Astro] TLE fetch failed: %v — retry in %s", err, tleRetryInterval)
			time.Sleep(tleRetryInterval)
			continue
		}
		time.Sleep(tleRefreshInterval)
	}
}

// ── Julian date helpers ─────────────────────────────────────────

func julianDate(t time.Time) float64 {
	t = t.UTC()
	y, mo, d := t.Year(), int(t.Month()), t.Day()
	h := float64(t.Hour()) + float64(t.Minute())/60.0 + float64(t.Second())/3600.0
	if mo <= 2 {
		y -= 1
		mo += 12
	}
	A := y / 100
	B := 2 - A + A/4
	jd := math.Floor(365.25*float64(y+4716)) +
		math.Floor(30.6001*float64(mo+1)) +
		float64(d) + float64(B) - 1524.5 + h/24.0
	return jd
}

func deg2rad(d float64) float64 { return d * math.Pi / 180.0 }
func rad2deg(r float64) float64 { return r * 180.0 / math.Pi }

// ── Sun position ────────────────────────────────────────────────

// sunPosition returns sun RA, Dec (radians) and equation of time (minutes)
// for a given Julian date. NOAA Solar Position algorithm.
func sunPosition(jd float64) (ra, dec, eqTime float64) {
	T := (jd - 2451545.0) / 36525.0

	L0 := math.Mod(280.46646+T*(36000.76983+T*0.0003032), 360.0)
	if L0 < 0 {
		L0 += 360
	}
	M := 357.52911 + T*(35999.05029-T*0.0001537)
	e := 0.016708634 - T*(0.000042037+T*0.0000001267)

	Mrad := deg2rad(M)
	C := math.Sin(Mrad)*(1.914602-T*(0.004817+T*0.000014)) +
		math.Sin(2*Mrad)*(0.019993-T*0.000101) +
		math.Sin(3*Mrad)*0.000289

	trueLong := L0 + C
	omega := 125.04 - 1934.136*T
	lambda := trueLong - 0.00569 - 0.00478*math.Sin(deg2rad(omega))
	epsilon := 23.0 + (26.0+(21.448-T*(46.815+T*(0.00059-T*0.001813)))/60.0)/60.0
	epsilon += 0.00256 * math.Cos(deg2rad(omega))

	lambdaRad := deg2rad(lambda)
	epsRad := deg2rad(epsilon)
	ra = math.Atan2(math.Cos(epsRad)*math.Sin(lambdaRad), math.Cos(lambdaRad))
	dec = math.Asin(math.Sin(epsRad) * math.Sin(lambdaRad))

	y := math.Tan(epsRad/2) * math.Tan(epsRad/2)
	L0rad := deg2rad(L0)
	Etime := y*math.Sin(2*L0rad) - 2*e*math.Sin(Mrad) +
		4*e*y*math.Sin(Mrad)*math.Cos(2*L0rad) -
		0.5*y*y*math.Sin(4*L0rad) -
		1.25*e*e*math.Sin(2*Mrad)
	eqTime = rad2deg(Etime) * 4.0
	return
}

// sunAltitude returns the sun's altitude in degrees for an observer at
// (lat, lon in degrees) at the given UTC time.
func sunAltitude(t time.Time, lat, lon float64) float64 {
	jd := julianDate(t)
	ra, dec, _ := sunPosition(jd)
	// Greenwich mean sidereal time (degrees)
	gmstDeg := greenwichSiderealDeg(jd)
	lstDeg := gmstDeg + lon
	ha := deg2rad(lstDeg) - ra
	latRad := deg2rad(lat)
	sinAlt := math.Sin(latRad)*math.Sin(dec) + math.Cos(latRad)*math.Cos(dec)*math.Cos(ha)
	return rad2deg(math.Asin(sinAlt))
}

// greenwichSiderealDeg returns GMST in degrees (0..360) from a Julian date.
func greenwichSiderealDeg(jd float64) float64 {
	T := (jd - 2451545.0) / 36525.0
	gmst := 280.46061837 + 360.98564736629*(jd-2451545.0) +
		T*T*(0.000387933-T/38710000.0)
	gmst = math.Mod(gmst, 360.0)
	if gmst < 0 {
		gmst += 360
	}
	return gmst
}

// ── Sun events for a given day ──────────────────────────────────

type SunEvents struct {
	GoldenHourAMStart *time.Time `json:"goldenHourAMStart"`
	Sunrise           *time.Time `json:"sunrise"`
	GoldenHourAMEnd   *time.Time `json:"goldenHourAMEnd"`
	SolarNoon         *time.Time `json:"solarNoon"`
	GoldenHourPMStart *time.Time `json:"goldenHourPMStart"`
	Sunset            *time.Time `json:"sunset"`
	GoldenHourPMEnd   *time.Time `json:"goldenHourPMEnd"`
	CivilDawn         *time.Time `json:"civilDawn"`
	CivilDusk         *time.Time `json:"civilDusk"`
	NauticalDawn      *time.Time `json:"nauticalDawn"`
	NauticalDusk      *time.Time `json:"nauticalDusk"`
	AstroDawn         *time.Time `json:"astroDawn"`
	AstroDusk         *time.Time `json:"astroDusk"`
}

// sunEventAt returns the UTC time at which the sun is at the given altitude
// (degrees) on the specified day (local date), for the requested event side:
// "rise" for morning crossing, "set" for evening. Returns nil if no crossing.
func sunEventAt(date time.Time, lat, lon, targetAltDeg float64, rising bool) *time.Time {
	// Scan the 24-hour span of this local date at 5-minute steps.
	loc := date.Location()
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	step := 5 * time.Minute
	prevAlt := sunAltitude(start, lat, lon)
	for i := 1; i <= 24*12; i++ {
		t := start.Add(time.Duration(i) * step)
		alt := sunAltitude(t, lat, lon)
		crossedUp := prevAlt < targetAltDeg && alt >= targetAltDeg
		crossedDown := prevAlt > targetAltDeg && alt <= targetAltDeg
		if (rising && crossedUp) || (!rising && crossedDown) {
			// Linear interp between prev and current sample.
			frac := (targetAltDeg - prevAlt) / (alt - prevAlt)
			crossing := t.Add(-step + time.Duration(frac*float64(step)))
			return &crossing
		}
		prevAlt = alt
	}
	return nil
}

// solarNoon returns the time of solar noon on the given local date.
func solarNoon(date time.Time, lat, lon float64) *time.Time {
	loc := date.Location()
	noonUTC := time.Date(date.Year(), date.Month(), date.Day(), 12, 0, 0, 0, time.UTC)
	jd := julianDate(noonUTC)
	_, _, eq := sunPosition(jd)
	// solar noon (UTC, minutes) = 720 - 4*lon - eq
	mins := 720.0 - 4.0*lon - eq
	noon := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC).
		Add(time.Duration(mins * float64(time.Minute)))
	local := noon.In(loc)
	return &local
}

func computeSunEvents(date time.Time, lat, lon float64) SunEvents {
	var ev SunEvents
	ev.Sunrise = sunEventAt(date, lat, lon, sunriseAlt, true)
	ev.Sunset = sunEventAt(date, lat, lon, sunriseAlt, false)
	ev.CivilDawn = sunEventAt(date, lat, lon, civilTwiAlt, true)
	ev.CivilDusk = sunEventAt(date, lat, lon, civilTwiAlt, false)
	ev.NauticalDawn = sunEventAt(date, lat, lon, nauticalTwiAlt, true)
	ev.NauticalDusk = sunEventAt(date, lat, lon, nauticalTwiAlt, false)
	ev.AstroDawn = sunEventAt(date, lat, lon, astroTwiAlt, true)
	ev.AstroDusk = sunEventAt(date, lat, lon, astroTwiAlt, false)
	ev.GoldenHourAMStart = sunEventAt(date, lat, lon, goldenHourEndAlt, true)
	ev.GoldenHourAMEnd = sunEventAt(date, lat, lon, goldenHourAlt, true)
	ev.GoldenHourPMStart = sunEventAt(date, lat, lon, goldenHourAlt, false)
	ev.GoldenHourPMEnd = sunEventAt(date, lat, lon, goldenHourEndAlt, false)
	ev.SolarNoon = solarNoon(date, lat, lon)
	return ev
}

// ── Moon calculations ───────────────────────────────────────────

// MoonInfo holds the moon phase/illumination and rise/set times.
type MoonInfo struct {
	PhaseName    string     `json:"phaseName"`
	Illumination float64    `json:"illumination"` // 0..1
	AgeDays      float64    `json:"ageDays"`
	NextNewMoon  time.Time  `json:"nextNewMoon"`
	NextFullMoon time.Time  `json:"nextFullMoon"`
	Moonrise     *time.Time `json:"moonrise"`
	Moonset      *time.Time `json:"moonset"`
}

// moonAge returns the age (days since last new moon) from a Julian date.
func moonAge(jd float64) float64 {
	age := math.Mod(jd-synodicAnchorJD, synodicMonth)
	if age < 0 {
		age += synodicMonth
	}
	return age
}

func moonIllumination(age float64) float64 {
	return (1 - math.Cos(2*math.Pi*age/synodicMonth)) / 2
}

func moonPhaseName(age float64) string {
	frac := age / synodicMonth
	switch {
	case frac < 0.03 || frac >= 0.97:
		return "New Moon"
	case frac < 0.22:
		return "Waxing Crescent"
	case frac < 0.28:
		return "First Quarter"
	case frac < 0.47:
		return "Waxing Gibbous"
	case frac < 0.53:
		return "Full Moon"
	case frac < 0.72:
		return "Waning Gibbous"
	case frac < 0.78:
		return "Last Quarter"
	default:
		return "Waning Crescent"
	}
}

// nextMoonEvent finds the next time the moon age crosses `target` (days
// into the cycle) starting from `from`.
func nextMoonEvent(from time.Time, target float64) time.Time {
	age := moonAge(julianDate(from))
	daysAway := target - age
	if daysAway <= 0 {
		daysAway += synodicMonth
	}
	return from.Add(time.Duration(daysAway * 24 * float64(time.Hour)))
}

// moonPosition computes moon RA, Dec (radians) from a Julian date using
// Meeus' simplified algorithm (ecliptic lon/lat → equatorial coords).
func moonPosition(jd float64) (ra, dec float64) {
	T := (jd - 2451545.0) / 36525.0
	L := 218.3164477 + 481267.88123421*T
	Mp := 134.9633964 + 477198.8675055*T
	F := 93.2720950 + 483202.0175233*T

	lon := L + 6.289*math.Sin(deg2rad(Mp))
	lat := 5.128 * math.Sin(deg2rad(F))

	lonRad := deg2rad(lon)
	latRad := deg2rad(lat)
	epsRad := deg2rad(meanObliquity)
	sinLon := math.Sin(lonRad)
	ra = math.Atan2(sinLon*math.Cos(epsRad)-math.Tan(latRad)*math.Sin(epsRad), math.Cos(lonRad))
	dec = math.Asin(math.Sin(latRad)*math.Cos(epsRad) + math.Cos(latRad)*math.Sin(epsRad)*sinLon)
	return
}

// moonAltitude returns moon altitude (degrees) at UTC time t.
func moonAltitude(t time.Time, lat, lon float64) float64 {
	jd := julianDate(t)
	ra, dec := moonPosition(jd)
	gmstDeg := greenwichSiderealDeg(jd)
	lstDeg := gmstDeg + lon
	ha := deg2rad(lstDeg) - ra
	latRad := deg2rad(lat)
	sinAlt := math.Sin(latRad)*math.Sin(dec) + math.Cos(latRad)*math.Cos(dec)*math.Cos(ha)
	return rad2deg(math.Asin(sinAlt))
}

// computeMoonRiseSet finds today's moonrise/moonset by sampling moon altitude
// every 10 min across a 24h window centered on local noon.
func computeMoonRiseSet(date time.Time, lat, lon float64) (rise, set *time.Time) {
	loc := date.Location()
	noon := time.Date(date.Year(), date.Month(), date.Day(), 12, 0, 0, 0, loc)
	start := noon.Add(-12 * time.Hour)
	step := 10 * time.Minute
	prevAlt := moonAltitude(start, lat, lon)
	for i := 1; i <= 24*6; i++ {
		t := start.Add(time.Duration(i) * step)
		alt := moonAltitude(t, lat, lon)
		if prevAlt < moonriseAlt && alt >= moonriseAlt && rise == nil {
			frac := (moonriseAlt - prevAlt) / (alt - prevAlt)
			cross := t.Add(-step + time.Duration(frac*float64(step)))
			// Only record if it falls on `date` (local).
			if cross.In(loc).Day() == date.Day() {
				rise = &cross
			}
		}
		if prevAlt > moonriseAlt && alt <= moonriseAlt && set == nil {
			frac := (moonriseAlt - prevAlt) / (alt - prevAlt)
			cross := t.Add(-step + time.Duration(frac*float64(step)))
			if cross.In(loc).Day() == date.Day() {
				set = &cross
			}
		}
		prevAlt = alt
		if rise != nil && set != nil {
			break
		}
	}
	return
}

func computeMoonInfo(now time.Time, lat, lon float64) MoonInfo {
	jd := julianDate(now)
	age := moonAge(jd)
	rise, set := computeMoonRiseSet(now, lat, lon)
	return MoonInfo{
		PhaseName:    moonPhaseName(age),
		Illumination: moonIllumination(age),
		AgeDays:      age,
		NextNewMoon:  nextMoonEvent(now, 0),
		NextFullMoon: nextMoonEvent(now, synodicMonth/2),
		Moonrise:     rise,
		Moonset:      set,
	}
}

// ── ISS passes ──────────────────────────────────────────────────

type ISSPass struct {
	Start      time.Time `json:"start"`
	Peak       time.Time `json:"peak"`
	End        time.Time `json:"end"`
	PeakAltDeg float64   `json:"peakAltDeg"`
	StartAzDeg float64   `json:"startAzDeg"`
	EndAzDeg   float64   `json:"endAzDeg"`
	DurationS  int       `json:"durationSec"`
}

func issLookAngles(sat satellite.Satellite, t time.Time, obs satellite.LatLong) satellite.LookAngles {
	u := t.UTC()
	pos, _ := satellite.Propagate(sat, u.Year(), int(u.Month()), u.Day(), u.Hour(), u.Minute(), u.Second())
	jd := satellite.JDay(u.Year(), int(u.Month()), u.Day(), u.Hour(), u.Minute(), u.Second())
	return satellite.ECIToLookAngles(pos, obs, 0.0, jd)
}

func computeISSPasses(now time.Time, lat, lon float64, horizonHours int, maxPasses int) []ISSPass {
	issTLE.mu.RLock()
	sat := issTLE.sat
	issTLE.mu.RUnlock()
	if sat == nil {
		return nil
	}

	obs := satellite.LatLong{Latitude: deg2rad(lat), Longitude: deg2rad(lon)}
	step := 60 * time.Second
	var passes []ISSPass

	inPass := false
	var startT, peakT time.Time
	var startAz, peakAlt float64
	end := now.Add(time.Duration(horizonHours) * time.Hour)
	for t := now; t.Before(end); t = t.Add(step) {
		la := issLookAngles(*sat, t, obs)
		elDeg := rad2deg(la.El)
		azDeg := rad2deg(la.Az)

		if !inPass && elDeg > 0 {
			inPass = true
			startT = t
			startAz = azDeg
			peakT = t
			peakAlt = elDeg
		} else if inPass {
			if elDeg > peakAlt {
				peakAlt = elDeg
				peakT = t
			}
			if elDeg <= 0 {
				inPass = false
				endT := t
				endAz := azDeg
				sunEl := sunAltitude(peakT, lat, lon)
				visible := sunEl < civilTwiAlt && peakAlt > 10.0
				if visible {
					passes = append(passes, ISSPass{
						Start:      startT,
						Peak:       peakT,
						End:        endT,
						PeakAltDeg: peakAlt,
						StartAzDeg: startAz,
						EndAzDeg:   endAz,
						DurationS:  int(endT.Sub(startT).Seconds()),
					})
					if len(passes) >= maxPasses {
						return passes
					}
				}
			}
		}
	}
	return passes
}

// ── Ephemeris of the day ────────────────────────────────────────

type Highlight struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Icon   string `json:"icon"`
}

type meteorShower struct {
	name      string
	peakMonth time.Month
	peakDay   int
}

var meteorShowers = []meteorShower{
	{"Quadrantids", time.January, 3},
	{"Quadrantids", time.January, 4},
	{"Lyrids", time.April, 22},
	{"Eta Aquariids", time.May, 6},
	{"Perseids", time.August, 12},
	{"Perseids", time.August, 13},
	{"Orionids", time.October, 21},
	{"Orionids", time.October, 22},
	{"Leonids", time.November, 17},
	{"Leonids", time.November, 18},
	{"Geminids", time.December, 13},
	{"Geminids", time.December, 14},
	{"Ursids", time.December, 22},
}

func nearestShower(now time.Time) (meteorShower, int, bool) {
	year := now.Year()
	for _, s := range meteorShowers {
		peak := time.Date(year, s.peakMonth, s.peakDay, 0, 0, 0, 0, now.Location())
		diff := int(peak.Sub(now).Hours() / 24.0)
		if diff >= -2 && diff <= 2 {
			return s, diff, true
		}
	}
	return meteorShower{}, 0, false
}

func computeHighlight(now time.Time, moon MoonInfo, passes []ISSPass) Highlight {
	if s, diff, ok := nearestShower(now); ok {
		label := "peaks today"
		if diff > 0 {
			label = fmt.Sprintf("peaks in %d day(s)", diff)
		} else if diff < 0 {
			label = fmt.Sprintf("peaked %d day(s) ago", -diff)
		}
		return Highlight{
			Title:  s.name + " meteor shower",
			Detail: "Radiant shower " + label + ". Watch after midnight for best rates.",
			Icon:   "☄️",
		}
	}

	// Full / new moon ±1 day — measured against moon age in the cycle.
	age := moon.AgeDays
	if math.Abs(age-synodicMonth/2) <= 1.0 {
		return Highlight{
			Title:  "Full Moon",
			Detail: fmt.Sprintf("The Moon is near full %s — washes out faint stars but makes for great lunar viewing.", humanWhen(moon.NextFullMoon, now)),
			Icon:   "🌕",
		}
	}
	if age <= 1.0 || age >= synodicMonth-1.0 {
		return Highlight{
			Title:  "New Moon",
			Detail: fmt.Sprintf("New Moon %s — dark skies, ideal for deep-sky and Milky Way observing.", humanWhen(moon.NextNewMoon, now)),
			Icon:   "🌑",
		}
	}

	// Tonight's first ISS pass with peak >40°.
	for _, p := range passes {
		if p.PeakAltDeg > 40 {
			return Highlight{
				Title:  "Bright ISS Pass",
				Detail: fmt.Sprintf("Peak altitude %.0f° at %s — look %s.", p.PeakAltDeg, p.Peak.In(now.Location()).Format("3:04 PM"), azimuthCompass(p.StartAzDeg)),
				Icon:   "🛰️",
			}
		}
	}

	return Highlight{
		Title:  moon.PhaseName,
		Detail: fmt.Sprintf("%.0f%% illuminated · Moon age %.1f days.", moon.Illumination*100, moon.AgeDays),
		Icon:   moonEmoji(moon.AgeDays),
	}
}

func humanWhen(t, ref time.Time) string {
	delta := t.Sub(ref)
	if delta < 0 {
		return "just passed"
	}
	hrs := int(delta.Hours())
	if hrs < 36 {
		return fmt.Sprintf("in %d hour(s)", hrs)
	}
	return "on " + t.Format("Jan 2")
}

func moonEmoji(age float64) string {
	frac := age / synodicMonth
	switch {
	case frac < 0.03 || frac >= 0.97:
		return "🌑"
	case frac < 0.22:
		return "🌒"
	case frac < 0.28:
		return "🌓"
	case frac < 0.47:
		return "🌔"
	case frac < 0.53:
		return "🌕"
	case frac < 0.72:
		return "🌖"
	case frac < 0.78:
		return "🌗"
	default:
		return "🌘"
	}
}

func azimuthCompass(az float64) string {
	dirs := []string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}
	idx := int(math.Mod(az/22.5+0.5, 16))
	if idx < 0 {
		idx += 16
	}
	return dirs[idx]
}

// ── API handler ─────────────────────────────────────────────────

type astroResponse struct {
	Location  struct {
		Lat      float64 `json:"lat"`
		Lon      float64 `json:"lon"`
		Timezone string  `json:"timezone"`
	} `json:"location"`
	Today     string    `json:"today"`
	Sun       SunEvents `json:"sun"`
	Moon      MoonInfo  `json:"moon"`
	ISSPasses []ISSPass `json:"issPasses"`
	Highlight Highlight `json:"highlight"`
}

func parseCoordsFromQuery(q url.Values) (lat, lon float64) {
	lat = defaultLat
	lon = defaultLon
	if v := q.Get("lat"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= -90 && f <= 90 {
			lat = f
		}
	}
	if v := q.Get("lon"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= -180 && f <= 180 {
			lon = f
		}
	}
	return
}

func handleAstroData(w http.ResponseWriter, r *http.Request) {
	lat, lon := parseCoordsFromQuery(r.URL.Query())
	now := time.Now()
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	sun := computeSunEvents(today, lat, lon)
	moon := computeMoonInfo(now, lat, lon)
	passes := computeISSPasses(now, lat, lon, 48, 5)

	// Sort passes by start time (should already be, but ensure).
	sort.Slice(passes, func(i, j int) bool { return passes[i].Start.Before(passes[j].Start) })

	highlight := computeHighlight(now, moon, passes)

	resp := astroResponse{}
	resp.Location.Lat = lat
	resp.Location.Lon = lon
	resp.Location.Timezone = loc.String()
	resp.Today = today.Format("2006-01-02")
	resp.Sun = sun
	resp.Moon = moon
	resp.ISSPasses = passes
	resp.Highlight = highlight

	writeJSON(w, resp)
}

// ── Init ────────────────────────────────────────────────────────

// InitAstro registers the Night Sky routes and starts the TLE fetch loop.
func InitAstro(mux *http.ServeMux) {
	mux.HandleFunc(astroBasePath+"/api/data", handleAstroData)

	staticSub, err := fs.Sub(astroFiles, "astro")
	if err != nil {
		log.Fatalf("[Astro] failed to load embedded static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle(astroBasePath+"/", http.StripPrefix(astroBasePath, fileServer))

	go tleLoop()

	log.Printf("[Astro] Night Sky dashboard registered at %s/", astroBasePath)
}
