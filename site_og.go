package main

import (
	"image"
	"image/png"
	"io"
	"log"
	"net/http"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
)

// renderSiteOG draws a generic site preview image used by every dashboard
// except poker. Re-uses the palette and fonts from poker_og.go.
func renderSiteOG(w io.Writer) error {
	ensureOGFonts()

	img := image.NewRGBA(image.Rect(0, 0, ogWidth, ogHeight))
	fillRect(img, img.Bounds(), ogBG)

	// Big centered hippo wordmark.
	drawTextCentered(img, "🦛  hipposcottomus", ogWidth/2, 280, titleFace, ogText)
	drawTextCentered(img, "Dashboards & projects by Scott", ogWidth/2, 340, subtitleFace, ogMuted)

	// Dashboard list along the bottom.
	dashboards := []string{
		"🌊 Jordan Lake",
		"⚓ Ft. Lauderdale",
		"🂡 Hold'em Equity",
		"🔭 Night Sky",
		"📊 Analytics",
	}
	y := 470
	x := 140
	step := (ogWidth - 2*x) / (len(dashboards) - 1)
	for i, label := range dashboards {
		drawTextCentered(img, label, x+i*step, y, subtitleFace, ogAccent)
	}

	drawText(img, "hipposcottomus.com", 60, ogHeight-30, subtitleFace, ogMuted)
	return png.Encode(w, img)
}

// handleSiteOGImage serves /og.png — the generic site preview image.
func handleSiteOGImage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if err := renderSiteOG(w); err != nil {
		log.Printf("[Site] OG render failed: %v", err)
	}
}

// ── App icon (PNG) ───────────────────────────────────────────────

// iconFontCache holds per-pixel-size faces for the wordmark, derived from
// gobold.TTF. Each canvas size needs its own face since opentype.NewFace
// takes a fixed point size.
var (
	iconFont     *opentype.Font
	iconFontOnce sync.Once
)

func iconFace(pxSize float64) font.Face {
	iconFontOnce.Do(func() {
		f, err := opentype.Parse(gobold.TTF)
		if err != nil {
			log.Fatalf("parse gobold: %v", err)
		}
		iconFont = f
	})
	face, err := opentype.NewFace(iconFont, &opentype.FaceOptions{
		Size:    pxSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		log.Fatalf("opentype.NewFace: %v", err)
	}
	return face
}

// renderAppIcon draws a square dark-navy app icon with "hippo" in bold
// white centered. Designed to look intentional at all common iOS sizes
// (60, 76, 120, 152, 167, 180) and Android adaptive masking.
func renderAppIcon(w io.Writer, size int) error {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fillRect(img, img.Bounds(), ogBG)

	// Wordmark face: ~28% of the canvas height. The Go bold "hippo"
	// glyphs at this size fit comfortably with margin on either side
	// at any of the standard icon dimensions.
	face := iconFace(float64(size) * 0.28)
	const word = "hippo"
	wordWidth := font.MeasureString(face, word).Round()
	metrics := face.Metrics()
	// Vertical center: shift the baseline so the cap-height-ish band
	// sits in the middle of the canvas.
	baseline := size/2 + metrics.Ascent.Round()/2 - metrics.Descent.Round()/2
	x := (size - wordWidth) / 2

	drawText(img, word, x, baseline, face, ogText)

	// Subtle accent stripe at the bottom — tiny, matches the dashboard
	// gradient bars on the home page so the icon visually links to the
	// site.
	stripeH := size / 28
	if stripeH < 2 {
		stripeH = 2
	}
	stripe := image.Rect(size/4, size-stripeH*2-size/16, 3*size/4, size-stripeH-size/16)
	fillRect(img, stripe, ogAccent)

	return png.Encode(w, img)
}

func handleAppleTouchIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if err := renderAppIcon(w, 180); err != nil {
		log.Printf("[Site] apple-touch-icon render failed: %v", err)
	}
}

func handleIcon512(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if err := renderAppIcon(w, 512); err != nil {
		log.Printf("[Site] icon-512 render failed: %v", err)
	}
}
