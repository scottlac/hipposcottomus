package main

// vote_og.go — page serving and Open Graph previews for the voting tool.
//
// Same pattern as poker.go: the embedded index.html gets an {{.OGMeta}}
// slot injected before </head> once at startup, and each page request
// renders meta tags for the specific poll so a link dropped in Discord
// previews as "Vote: <poll title> — 3 of 5 have voted" with a rendered
// card image, instead of a bare URL. Reuses pokerOGFragment/pokerOGData
// and the drawing helpers from poker_og.go.

import (
	"html/template"
	"image"
	"image/png"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	voteHTMLOnce sync.Once
	voteHTMLTpl  *template.Template
)

func fsSubVote() (fs.FS, error) {
	return fs.Sub(content, "vote")
}

func loadVoteHTMLTemplate(staticSub fs.FS) {
	voteHTMLOnce.Do(func() {
		raw, err := fs.ReadFile(staticSub, "index.html")
		if err != nil {
			log.Fatalf("[Vote] failed to read embedded index.html: %v", err)
		}
		const headClose = `</head>`
		injected := strings.Replace(string(raw), headClose, `{{.OGMeta}}`+"\n"+headClose, 1)
		voteHTMLTpl = template.Must(template.New("vote").Parse(injected))
	})
}

// votePollFromPath extracts the poll (if any) referenced by a page path
// like /vote/p/{id} or /vote/r/{id}, plus which view it is.
func votePollFromPath(path string) (p *VotePoll, view string) {
	var id string
	switch {
	case strings.HasPrefix(path, voteBasePath+"/p/"):
		view, id = "p", strings.TrimPrefix(path, voteBasePath+"/p/")
	case strings.HasPrefix(path, voteBasePath+"/r/"):
		view, id = "r", strings.TrimPrefix(path, voteBasePath+"/r/")
	default:
		return nil, ""
	}
	id = strings.TrimSuffix(id, "/")
	polls.mu.RLock()
	p = polls.Polls[id]
	polls.mu.RUnlock()
	if p != nil && time.Now().After(p.ExpiresAt) {
		p = nil
	}
	return p, view
}

// voteOGCopy builds the OG title/description for a page. It never leaks
// the outcome: results can be hidden until close, so previews only ever
// show the poll title and participation.
func voteOGCopy(p *VotePoll, view string) (title, desc string) {
	if p == nil {
		return "Ranked Choice Voting", "Create a poll, share one link, rank the options — instant-runoff results with honest tie-breaks and a head-to-head cross-check."
	}
	polls.mu.RLock()
	voted := len(p.Ballots)
	total := len(p.Roster)
	n := len(p.Candidates)
	closed := p.Closed
	pollTitle := p.Title
	polls.mu.RUnlock()

	switch view {
	case "r":
		title = "Results: " + pollTitle
	default:
		title = "Vote: " + pollTitle
	}
	state := "voting open"
	if closed {
		state = "poll closed"
	}
	desc = plural(n, "option") + " · " + pluralOf(voted, total, "voted") + " · " + state
	return title, desc
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func pluralOf(x, y int, verb string) string {
	return strconv.Itoa(x) + " of " + strconv.Itoa(y) + " " + verb
}

// serveVoteHTML renders index.html with per-poll OG meta.
func serveVoteHTML(w http.ResponseWriter, r *http.Request) {
	p, view := votePollFromPath(r.URL.Path)
	title, desc := voteOGCopy(p, view)

	imageURL := absoluteURL(r, voteBasePath+"/og.png")
	if p != nil {
		imageURL = absoluteURL(r, voteBasePath+"/og.png?p="+p.ID)
	}

	var ogBuf strings.Builder
	if err := pokerOGFragment.Execute(&ogBuf, pokerOGData{
		Title: title, Description: desc,
		URL: absoluteURL(r, r.URL.Path), ImageURL: imageURL,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if err := voteHTMLTpl.Execute(w, struct{ OGMeta template.HTML }{OGMeta: template.HTML(ogBuf.String())}); err != nil {
		log.Printf("[Vote] HTML render failed: %v", err)
	}
}

// handleVoteOGImage renders the 1200×630 preview card. Titles come from
// the store (already cleaned on input), never from the query string.
func handleVoteOGImage(w http.ResponseWriter, r *http.Request) {
	ensureOGFonts()

	var poll *VotePoll
	if id := r.URL.Query().Get("p"); id != "" {
		polls.mu.RLock()
		poll = polls.Polls[id]
		polls.mu.RUnlock()
	}

	img := image.NewRGBA(image.Rect(0, 0, ogWidth, ogHeight))
	fillRect(img, img.Bounds(), ogBG)

	drawText(img, "🦛 hipposcottomus", 60, 90, titleFace, ogText)
	drawText(img, "Ranked Choice Voting", 60, 138, subtitleFace, ogMuted)

	if poll == nil {
		drawTextCentered(img, "Rank your options. Count them honestly.", ogWidth/2, 330, titleFace, ogText)
		drawTextCentered(img, "Instant runoff · visible tie-breaks · head-to-head cross-check", ogWidth/2, 390, subtitleFace, ogAccent)
	} else {
		polls.mu.RLock()
		title := poll.Title
		sub := plural(len(poll.Candidates), "option") + "  ·  " + pluralOf(len(poll.Ballots), len(poll.Roster), "voted")
		if poll.Closed {
			sub += "  ·  closed"
		}
		polls.mu.RUnlock()
		if r := []rune(title); len(r) > 60 {
			title = string(r[:57]) + "…"
		}
		drawTextCentered(img, title, ogWidth/2, 330, titleFace, ogText)
		drawTextCentered(img, sub, ogWidth/2, 390, subtitleFace, ogAccent)
	}

	drawText(img, "hipposcottomus.com/vote", 60, ogHeight-30, subtitleFace, ogMuted)

	w.Header().Set("Content-Type", "image/png")
	// Vote counts change; keep previews fresh-ish but cacheable.
	w.Header().Set("Cache-Control", "public, max-age=300")
	if err := png.Encode(w, img); err != nil {
		log.Printf("[Vote] OG render failed: %v", err)
	}
}
