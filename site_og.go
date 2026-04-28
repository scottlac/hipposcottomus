package main

import (
	"image"
	"image/png"
	"io"
	"log"
	"net/http"
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
