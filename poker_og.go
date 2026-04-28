package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"sync"

	"github.com/paulhankin/poker/v2/poker"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Open Graph image dimensions. 1200×630 is the de-facto standard accepted
// by Facebook, Twitter, WhatsApp, iMessage, Slack, etc.
const (
	ogWidth  = 1200
	ogHeight = 630
)

// Palette mirrors the site's CSS custom properties.
var (
	ogBG       = color.RGBA{0x0f, 0x17, 0x2a, 0xff} // --bg
	ogSurface  = color.RGBA{0x1e, 0x29, 0x3b, 0xff} // --surface
	ogBorder   = color.RGBA{0x33, 0x41, 0x55, 0xff} // --border
	ogText     = color.RGBA{0xe2, 0xe8, 0xf0, 0xff} // --text
	ogMuted    = color.RGBA{0x94, 0xa3, 0xb8, 0xff} // --text-muted
	ogAccent   = color.RGBA{0x38, 0xbd, 0xf8, 0xff} // --accent-blue
	ogGreen    = color.RGBA{0x4a, 0xde, 0x80, 0xff}
	ogRed      = color.RGBA{0xdc, 0x26, 0x26, 0xff}
	ogCardFace = color.RGBA{0xf8, 0xfa, 0xfc, 0xff}
	ogCardInk  = color.RGBA{0x0f, 0x17, 0x2a, 0xff}
)

// Lazy-initialized font faces. The TTF bytes ship with x/image; no font
// files are committed to this repo.
var (
	ogFontInit  sync.Once
	titleFace   font.Face
	subtitleFace font.Face
	labelFace   font.Face
	rankBigFace font.Face
	rankSmFace  font.Face
	suitFace    font.Face
)

func ensureOGFonts() {
	ogFontInit.Do(func() {
		bold, _ := opentype.Parse(gobold.TTF)
		regular, _ := opentype.Parse(goregular.TTF)
		titleFace, _ = opentype.NewFace(bold, &opentype.FaceOptions{Size: 56, DPI: 72, Hinting: font.HintingFull})
		subtitleFace, _ = opentype.NewFace(regular, &opentype.FaceOptions{Size: 28, DPI: 72, Hinting: font.HintingFull})
		labelFace, _ = opentype.NewFace(bold, &opentype.FaceOptions{Size: 22, DPI: 72, Hinting: font.HintingFull})
		rankBigFace, _ = opentype.NewFace(bold, &opentype.FaceOptions{Size: 76, DPI: 72, Hinting: font.HintingFull})
		rankSmFace, _ = opentype.NewFace(bold, &opentype.FaceOptions{Size: 32, DPI: 72, Hinting: font.HintingFull})
		suitFace, _ = opentype.NewFace(regular, &opentype.FaceOptions{Size: 64, DPI: 72, Hinting: font.HintingFull})
	})
}

// fillRect fills a solid rectangle.
func fillRect(img draw.Image, r image.Rectangle, c color.Color) {
	draw.Draw(img, r, &image.Uniform{C: c}, image.Point{}, draw.Src)
}

// strokeRect draws a 1-pixel border.
func strokeRect(img draw.Image, r image.Rectangle, c color.Color) {
	fillRect(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+1), c)
	fillRect(img, image.Rect(r.Min.X, r.Max.Y-1, r.Max.X, r.Max.Y), c)
	fillRect(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+1, r.Max.Y), c)
	fillRect(img, image.Rect(r.Max.X-1, r.Min.Y, r.Max.X, r.Max.Y), c)
}

// drawText renders s at baseline (x, y) using the given face.
func drawText(img draw.Image, s string, x, y int, face font.Face, c color.Color) {
	d := &font.Drawer{
		Dst:  img,
		Src:  &image.Uniform{C: c},
		Face: face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(s)
}

// drawTextCentered horizontally centers s around centerX at baseline y.
func drawTextCentered(img draw.Image, s string, centerX, y int, face font.Face, c color.Color) {
	w := font.MeasureString(face, s).Round()
	drawText(img, s, centerX-w/2, y, face, c)
}

// suitGlyph returns the unicode pip for a card suit. The Go fonts include
// these in the basic-Latin-supplement block, so they render correctly.
func suitGlyph(s poker.Suit) string {
	switch s {
	case poker.Spade:
		return "♠"
	case poker.Heart:
		return "♥"
	case poker.Diamond:
		return "♦"
	case poker.Club:
		return "♣"
	}
	return "?"
}

// rankLabel returns the human-readable label for a card rank ("10" instead
// of "T", which fits the OG-image aesthetic better than the in-app picker
// display).
func rankLabel(r poker.Rank) string {
	switch r {
	case 1:
		return "A"
	case 11:
		return "J"
	case 12:
		return "Q"
	case 13:
		return "K"
	case 10:
		return "10"
	default:
		return r.String()
	}
}

func suitInk(s poker.Suit) color.Color {
	if s == poker.Heart || s == poker.Diamond {
		return ogRed
	}
	return ogCardInk
}

// drawCard renders a single playing card centered on its top-left corner.
func drawCard(img draw.Image, c poker.Card, x, y, w, h int) {
	rect := image.Rect(x, y, x+w, y+h)
	fillRect(img, rect, ogCardFace)
	strokeRect(img, image.Rect(x-1, y-1, x+w+1, y+h+1), ogBorder)

	rank := rankLabel(c.Rank())
	suit := suitGlyph(c.Suit())
	ink := suitInk(c.Suit())

	// Top-left rank.
	drawText(img, rank, x+12, y+44, rankSmFace, ink)
	// Center pip.
	drawTextCentered(img, suit, x+w/2, y+h/2+24, suitFace, ink)
	// Bottom-right rank (rotated would need a transform; we omit for
	// simplicity — the top-left + center is enough to read).
	rw := font.MeasureString(rankSmFace, rank).Round()
	drawText(img, rank, x+w-12-rw, y+h-12, rankSmFace, ink)
}

// drawPlaceholderCard renders an empty slot.
func drawPlaceholderCard(img draw.Image, x, y, w, h int) {
	rect := image.Rect(x, y, x+w, y+h)
	fillRect(img, rect, ogSurface)
	strokeRect(img, image.Rect(x-1, y-1, x+w+1, y+h+1), ogBorder)
	// Dotted-style center dot.
	dot := image.Rect(x+w/2-4, y+h/2-4, x+w/2+4, y+h/2+4)
	fillRect(img, dot, ogMuted)
}

// renderPokerOG draws the OG preview for a hand+board into a PNG and writes
// it to w. Either of hand or board may be empty; the layout adjusts.
func renderPokerOG(w io.Writer, hand, board []poker.Card) error {
	ensureOGFonts()

	img := image.NewRGBA(image.Rect(0, 0, ogWidth, ogHeight))
	fillRect(img, img.Bounds(), ogBG)

	// Header band.
	drawText(img, "🦛 hipposcottomus", 60, 90, titleFace, ogText)
	drawText(img, "Hold'em Equity Calculator", 60, 138, subtitleFace, ogMuted)

	// Layout constants.
	const (
		cardW = 130
		cardH = 184
		gap   = 18
	)

	// "Your Hand" row (left).
	handX := 60
	handY := 220
	drawText(img, "YOUR HAND", handX, handY-12, labelFace, ogGreen)
	for i := 0; i < 2; i++ {
		x := handX + i*(cardW+gap)
		if i < len(hand) {
			drawCard(img, hand[i], x, handY, cardW, cardH)
		} else {
			drawPlaceholderCard(img, x, handY, cardW, cardH)
		}
	}

	// "Board" row (centered below).
	boardWidthPx := 5*cardW + 4*gap
	boardX := (ogWidth - boardWidthPx) / 2
	boardY := handY + cardH + 60
	drawText(img, "BOARD", boardX, boardY-12, labelFace, ogAccent)
	for i := 0; i < 5; i++ {
		x := boardX + i*(cardW+gap)
		if i < len(board) {
			drawCard(img, board[i], x, boardY, cardW, cardH)
		} else {
			drawPlaceholderCard(img, x, boardY, cardW, cardH)
		}
	}

	// Footer URL hint.
	drawText(img, "hipposcottomus.com/poker", 60, ogHeight-30, subtitleFace, ogMuted)

	return png.Encode(w, img)
}
