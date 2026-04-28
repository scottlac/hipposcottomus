package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"sync"

	"github.com/paulhankin/poker/v2/poker"
)

const (
	pokerBasePath = "/poker"

	// Number of Monte Carlo iterations for equity calculation.
	// Each iteration samples a random opponent configuration and a random
	// completed board, then calls HoldemEquities for a single evaluation.
	// At 50k, the standard error on a 50/50 race is ~0.22% — visually stable
	// across reloads. Reduce if request latency becomes a problem.
	pokerIterations = 50000
)

// pokerRNGPool provides per-request random sources so concurrent requests
// don't contend on a single mutex.
var pokerRNGPool = sync.Pool{
	New: func() any {
		return rand.New(rand.NewSource(rand.Int63()))
	},
}

// ── Request / Response types ────────────────────────────────────

type equityRequest struct {
	Hand       []string `json:"hand"`       // e.g. ["As", "Kh"]
	Board      []string `json:"board"`      // 0–5 cards, e.g. ["7d", "8d", "9d"]
	NumPlayers int      `json:"numPlayers"` // 2–10, total players including hero
}

type equityResponse struct {
	WinProbability  float64  `json:"winProbability"`
	TieProbability  float64  `json:"tieProbability"`
	HandDescription string   `json:"handDescription"`
	Threats         []threat `json:"threats,omitempty"`
}

// threat is one poker hand category that a single random opponent could hold
// to beat the hero, given the current board.
type threat struct {
	Category    string `json:"category"`
	Combos      int    `json:"combos"`      // number of 2-card opponent hands in this category that beat hero
	TotalCombos int    `json:"totalCombos"` // total 2-card opponent hands considered (denominator)
}

// ── Card parsing ────────────────────────────────────────────────

// parseCard accepts rank-first strings ("As", "Kh", "Td", "2c") and
// returns a paulhankin poker.Card. The library's NameToCard uses suit-first
// notation ("SA", "HK"), so we convert here.
func parseCard(s string) (poker.Card, error) {
	if len(s) != 2 {
		return 0, fmt.Errorf("invalid card %q: must be 2 chars", s)
	}
	rank := strings.ToUpper(s[:1])
	suit := strings.ToUpper(s[1:])
	name := suit + rank
	c, ok := poker.NameToCard[name]
	if !ok {
		return 0, fmt.Errorf("invalid card %q", s)
	}
	return c, nil
}

func parseCards(ss []string) ([]poker.Card, error) {
	out := make([]poker.Card, len(ss))
	for i, s := range ss {
		c, err := parseCard(s)
		if err != nil {
			return nil, err
		}
		out[i] = c
	}
	return out, nil
}

// ── Equity calculation ──────────────────────────────────────────

// calcEquity runs a Monte Carlo simulation over random opponent hole cards
// and random board completions, returning average win and tie probabilities
// for the hero hand.
func calcEquity(hero [2]poker.Card, board []poker.Card, numPlayers int) (winProb, tieProb float64, err error) {
	if numPlayers < 2 || numPlayers > 10 {
		return 0, 0, fmt.Errorf("numPlayers must be between 2 and 10")
	}

	// Build the remaining deck (cards not yet used by hero or board).
	used := map[poker.Card]bool{hero[0]: true, hero[1]: true}
	for _, c := range board {
		if used[c] {
			return 0, 0, fmt.Errorf("duplicate card in input")
		}
		used[c] = true
	}
	deck := make([]poker.Card, 0, 52-len(used))
	for _, c := range poker.Cards {
		if !used[c] {
			deck = append(deck, c)
		}
	}

	// Cards we need to draw each iteration: 2 per opponent + cards to
	// complete the board to 5.
	opponentCount := numPlayers - 1
	boardMissing := 5 - len(board)
	needed := 2*opponentCount + boardMissing
	if needed > len(deck) {
		return 0, 0, fmt.Errorf("not enough cards left in deck")
	}

	rng := pokerRNGPool.Get().(*rand.Rand)
	defer pokerRNGPool.Put(rng)

	var totalWin, totalTie float64
	shuffled := make([]poker.Card, len(deck))
	copy(shuffled, deck)
	hands := make([][2]poker.Card, numPlayers)
	hands[0] = hero
	fullBoard := make([]poker.Card, 5)
	copy(fullBoard, board)

	for iter := 0; iter < pokerIterations; iter++ {
		// Partial Fisher-Yates: only shuffle the prefix we need.
		for i := 0; i < needed; i++ {
			j := i + rng.Intn(len(shuffled)-i)
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		}

		// Deal opponent hands.
		idx := 0
		for p := 1; p < numPlayers; p++ {
			hands[p] = [2]poker.Card{shuffled[idx], shuffled[idx+1]}
			idx += 2
		}
		// Complete the board.
		for i := 0; i < boardMissing; i++ {
			fullBoard[len(board)+i] = shuffled[idx]
			idx++
		}

		// With a full 5-card board, HoldemEquities does a single evaluation
		// per hand and returns exact equity for this configuration.
		eqs, err := poker.HoldemEquities(hands, fullBoard)
		if err != nil {
			return 0, 0, err
		}
		totalWin += eqs[0].Win
		totalTie += eqs[0].Tie
	}

	return totalWin / float64(pokerIterations), totalTie / float64(pokerIterations), nil
}

// ── Hand description ────────────────────────────────────────────

// describeHand returns a human-readable description of the best poker hand
// given the hero's hole cards plus any known board cards.
func describeHand(hero [2]poker.Card, board []poker.Card) string {
	total := len(board) + 2
	cards := append([]poker.Card{hero[0], hero[1]}, board...)

	switch total {
	case 2:
		return preflopDescription(hero)
	case 3, 5, 7:
		d, err := poker.Describe(cards)
		if err != nil {
			return ""
		}
		return d
	case 4, 6:
		// Describe only supports 3, 5, or 7 cards. Find the best 5-card
		// subset (for 6 cards) or best 3-card subset (for 4 cards).
		return bestSubsetDescription(cards)
	}
	return ""
}

// preflopDescription returns a label for two hole cards when no board is set.
func preflopDescription(hero [2]poker.Card) string {
	r0 := hero[0].Rank()
	r1 := hero[1].Rank()
	rn0 := rankName(r0)
	rn1 := rankName(r1)
	if r0 == r1 {
		return "pocket " + rn0 + "s"
	}
	// Order high first.
	hi, lo := rn0, rn1
	if hero[0].RawRank() < hero[1].RawRank() {
		hi, lo = rn1, rn0
	}
	suited := hero[0].Suit() == hero[1].Suit()
	if suited {
		return hi + "-" + lo + " suited"
	}
	return hi + "-" + lo + " offsuit"
}

func rankName(r poker.Rank) string {
	switch r {
	case 1:
		return "Ace"
	case 11:
		return "Jack"
	case 12:
		return "Queen"
	case 13:
		return "King"
	default:
		return r.String()
	}
}

// bestSubsetDescription finds the best 3- or 5-card subset of the given cards
// and returns its description.
func bestSubsetDescription(cards []poker.Card) string {
	n := len(cards)
	var k int
	if n >= 5 {
		k = 5
	} else {
		k = 3
	}

	var bestRank int16 = -1
	bestSubset := make([]poker.Card, k)
	subset := make([]poker.Card, k)

	idx := make([]int, k)
	for i := range idx {
		idx[i] = i
	}

	for {
		for i, j := range idx {
			subset[i] = cards[j]
		}
		var ev int16
		if k == 5 {
			var h [5]poker.Card
			copy(h[:], subset)
			ev = poker.Eval5(&h)
		} else {
			var h [3]poker.Card
			copy(h[:], subset)
			ev = poker.Eval3(&h)
		}
		if ev > bestRank {
			bestRank = ev
			copy(bestSubset, subset)
		}
		if !nextCombination(idx, n) {
			break
		}
	}

	d, err := poker.Describe(bestSubset)
	if err != nil {
		return ""
	}
	return d
}

// ── Hand category classification ────────────────────────────────

// HandCategory enumerates the nine standard 5-card poker hand categories,
// ordered from weakest to strongest.
type HandCategory int

const (
	CatHighCard HandCategory = iota
	CatPair
	CatTwoPair
	CatTrips
	CatStraight
	CatFlush
	CatFullHouse
	CatQuads
	CatStraightFlush
)

func (c HandCategory) String() string {
	switch c {
	case CatHighCard:
		return "High Card"
	case CatPair:
		return "Pair"
	case CatTwoPair:
		return "Two Pair"
	case CatTrips:
		return "Three of a Kind"
	case CatStraight:
		return "Straight"
	case CatFlush:
		return "Flush"
	case CatFullHouse:
		return "Full House"
	case CatQuads:
		return "Four of a Kind"
	case CatStraightFlush:
		return "Straight Flush"
	}
	return ""
}

// classify5 returns the poker category for an exact 5-card hand. It is
// independent of paulhankin/poker's score packing — small and easy to test.
func classify5(cards [5]poker.Card) HandCategory {
	rankCount := [13]int{}
	suitCount := [4]int{}
	for _, c := range cards {
		rankCount[c.RawRank()]++
		suitCount[int(c.Suit())]++
	}

	flush := false
	for _, n := range suitCount {
		if n == 5 {
			flush = true
			break
		}
	}

	straight := false
	// Standard run of 5 consecutive ranks.
	for i := 0; i <= 12-4; i++ {
		if rankCount[i] == 1 && rankCount[i+1] == 1 && rankCount[i+2] == 1 &&
			rankCount[i+3] == 1 && rankCount[i+4] == 1 {
			straight = true
			break
		}
	}
	// The wheel: A-2-3-4-5. RawRank: 2=0, 3=1, 4=2, 5=3, A=12.
	if !straight && rankCount[0] == 1 && rankCount[1] == 1 &&
		rankCount[2] == 1 && rankCount[3] == 1 && rankCount[12] == 1 {
		straight = true
	}

	if straight && flush {
		return CatStraightFlush
	}
	if flush {
		return CatFlush
	}
	if straight {
		return CatStraight
	}

	// Pair / trips / quads via rank-multiplicity histogram.
	var pairs, trips, quads int
	for _, n := range rankCount {
		switch n {
		case 2:
			pairs++
		case 3:
			trips++
		case 4:
			quads++
		}
	}
	switch {
	case quads == 1:
		return CatQuads
	case trips == 1 && pairs >= 1:
		return CatFullHouse
	case trips == 1:
		return CatTrips
	case pairs >= 2:
		return CatTwoPair
	case pairs == 1:
		return CatPair
	}
	return CatHighCard
}

// bestCategory finds the strongest 5-card subset of the given cards (5–7
// cards) and returns its category. For 5 cards it just classifies directly;
// for 6 or 7 it iterates 5-card subsets, picks the highest by Eval5, and
// classifies that.
func bestCategory(cards []poker.Card) HandCategory {
	n := len(cards)
	if n < 5 {
		return CatHighCard
	}
	if n == 5 {
		var h [5]poker.Card
		copy(h[:], cards)
		return classify5(h)
	}

	var bestRank int16 = -1
	var bestSubset [5]poker.Card
	var subset [5]poker.Card

	idx := []int{0, 1, 2, 3, 4}
	for {
		for i, j := range idx {
			subset[i] = cards[j]
		}
		ev := poker.Eval5(&subset)
		if ev > bestRank {
			bestRank = ev
			bestSubset = subset
		}
		if !nextCombination(idx, n) {
			break
		}
	}
	return classify5(bestSubset)
}

// computeThreats enumerates every possible opponent 2-card hand from the
// remaining deck and groups those that beat the hero (with the current
// board) by hand category. Returns nil when the board has fewer than 3
// cards, since hero doesn't yet have a 5-card hand to compare against.
func computeThreats(hero [2]poker.Card, board []poker.Card) []threat {
	if len(board) < 3 {
		return nil
	}

	used := map[poker.Card]bool{hero[0]: true, hero[1]: true}
	for _, c := range board {
		used[c] = true
	}
	deck := make([]poker.Card, 0, 52)
	for _, c := range poker.Cards {
		if !used[c] {
			deck = append(deck, c)
		}
	}

	heroAll := make([]poker.Card, 0, 2+len(board))
	heroAll = append(heroAll, hero[0], hero[1])
	heroAll = append(heroAll, board...)
	heroEval := evalAll(heroAll)

	totals := [9]int{}
	losing := [9]int{}
	oppAll := make([]poker.Card, 0, 2+len(board))
	for i := 0; i < len(deck); i++ {
		for j := i + 1; j < len(deck); j++ {
			oppAll = append(oppAll[:0], deck[i], deck[j])
			oppAll = append(oppAll, board...)
			oppEval := evalAll(oppAll)
			cat := bestCategory(oppAll)
			totals[int(cat)]++
			if oppEval > heroEval {
				losing[int(cat)]++
			}
		}
	}

	totalCombos := 0
	for _, n := range totals {
		totalCombos += n
	}

	out := make([]threat, 0, 9)
	for cat := CatStraightFlush; cat >= CatHighCard; cat-- {
		if losing[int(cat)] == 0 {
			continue
		}
		out = append(out, threat{
			Category:    cat.String(),
			Combos:      losing[int(cat)],
			TotalCombos: totalCombos,
		})
	}
	return out
}

// evalAll returns the highest-ranking poker eval int16 across all 5-card
// subsets of the given cards. Cards must be 5, 6, or 7 (preconditions
// guaranteed by the call sites).
func evalAll(cards []poker.Card) int16 {
	switch len(cards) {
	case 5:
		var h [5]poker.Card
		copy(h[:], cards)
		return poker.Eval5(&h)
	case 7:
		var h [7]poker.Card
		copy(h[:], cards)
		return poker.Eval7(&h)
	case 6:
		var subset [5]poker.Card
		var best int16 = -1
		for skip := 0; skip < 6; skip++ {
			i := 0
			for j := 0; j < 6; j++ {
				if j == skip {
					continue
				}
				subset[i] = cards[j]
				i++
			}
			ev := poker.Eval5(&subset)
			if ev > best {
				best = ev
			}
		}
		return best
	}
	return -1
}

// nextCombination advances idx to the next k-combination of {0..n-1}
// in lexicographic order. Returns false when exhausted.
func nextCombination(idx []int, n int) bool {
	k := len(idx)
	for i := k - 1; i >= 0; i-- {
		if idx[i] < n-(k-i) {
			idx[i]++
			for j := i + 1; j < k; j++ {
				idx[j] = idx[j-1] + 1
			}
			return true
		}
	}
	return false
}

// ── API handlers ────────────────────────────────────────────────

func handlePokerEquity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req equityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.Hand) != 2 {
		http.Error(w, "hand must contain exactly 2 cards", http.StatusBadRequest)
		return
	}
	if len(req.Board) > 5 {
		http.Error(w, "board must contain at most 5 cards", http.StatusBadRequest)
		return
	}
	if req.NumPlayers < 2 || req.NumPlayers > 10 {
		http.Error(w, "numPlayers must be between 2 and 10", http.StatusBadRequest)
		return
	}

	hand, err := parseCards(req.Hand)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	board, err := parseCards(req.Board)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	hero := [2]poker.Card{hand[0], hand[1]}
	win, tie, err := calcEquity(hero, board, req.NumPlayers)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp := equityResponse{
		WinProbability:  win,
		TieProbability:  tie,
		HandDescription: describeHand(hero, board),
		Threats:         computeThreats(hero, board),
	}
	writeJSON(w, resp)
}

// ── OG image + HTML templating ──────────────────────────────────

// parseShareCards mirrors the frontend's parseCardList helper: each card is
// exactly 2 chars (rank + suit), so the param is just the cards
// concatenated. Invalid pieces are silently skipped.
func parseShareCards(s string) []poker.Card {
	out := make([]poker.Card, 0, len(s)/2)
	used := map[poker.Card]bool{}
	for i := 0; i+2 <= len(s); i += 2 {
		c, err := parseCard(s[i : i+2])
		if err != nil || used[c] {
			continue
		}
		used[c] = true
		out = append(out, c)
	}
	return out
}

// handlePokerOGImage renders a 1200×630 PNG showing the hand and board
// described in the query string. Empty params produce a placeholder image.
func handlePokerOGImage(w http.ResponseWriter, r *http.Request) {
	hand := parseShareCards(r.URL.Query().Get("hand"))
	if len(hand) > 2 {
		hand = hand[:2]
	}
	board := parseShareCards(r.URL.Query().Get("board"))
	if len(board) > 5 {
		board = board[:5]
	}

	w.Header().Set("Content-Type", "image/png")
	// Per-URL caching is fine for a long time — same query → same image.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if err := renderPokerOG(w, hand, board); err != nil {
		log.Printf("[Poker] OG render failed: %v", err)
	}
}

// pokerHTMLTemplate is the index.html content with placeholder OG meta tags
// replaced at request time. Initialized lazily on first request.
var (
	pokerHTMLOnce sync.Once
	pokerHTMLTpl  *template.Template
)

// pokerOGData is the data passed to pokerHTMLTpl.
type pokerOGData struct {
	Title       string
	Description string
	URL         string
	ImageURL    string
}

func loadPokerHTMLTemplate(staticSub fs.FS) {
	pokerHTMLOnce.Do(func() {
		raw, err := fs.ReadFile(staticSub, "index.html")
		if err != nil {
			log.Fatalf("[Poker] failed to read embedded index.html: %v", err)
		}
		// Inject the OG action right before </head>. The base HTML stays
		// untouched on disk; the template is purely additive.
		const headClose = `</head>`
		injected := strings.Replace(string(raw), headClose, `{{.OGMeta}}`+"\n"+headClose, 1)
		pokerHTMLTpl = template.Must(template.New("poker").Parse(injected))
	})
}

const pokerOGTagsTpl = `
  <meta property="og:type" content="website">
  <meta property="og:site_name" content="hipposcottomus">
  <meta property="og:title" content="{{.Title}}">
  <meta property="og:description" content="{{.Description}}">
  <meta property="og:url" content="{{.URL}}">
  <meta property="og:image" content="{{.ImageURL}}">
  <meta property="og:image:width" content="1200">
  <meta property="og:image:height" content="630">
  <meta name="twitter:card" content="summary_large_image">
  <meta name="twitter:title" content="{{.Title}}">
  <meta name="twitter:description" content="{{.Description}}">
  <meta name="twitter:image" content="{{.ImageURL}}">
`

var pokerOGFragment = template.Must(template.New("og").Parse(pokerOGTagsTpl))

// servePokerHTML renders index.html with OG meta tags reflecting the
// current ?hand=&board= query string, so link previews on
// WhatsApp/iMessage/Slack/etc. show the actual hand.
func servePokerHTML(w http.ResponseWriter, r *http.Request) {
	hand := parseShareCards(r.URL.Query().Get("hand"))
	board := parseShareCards(r.URL.Query().Get("board"))

	title, desc := pokerOGCopy(hand, board)
	imageURL := absoluteURL(r, pokerBasePath+"/og.png")
	if r.URL.RawQuery != "" {
		imageURL = absoluteURL(r, pokerBasePath+"/og.png?"+r.URL.RawQuery)
	}
	pageURL := absoluteURL(r, r.URL.Path)
	if r.URL.RawQuery != "" {
		pageURL = absoluteURL(r, r.URL.Path+"?"+r.URL.RawQuery)
	}

	var ogBuf strings.Builder
	if err := pokerOGFragment.Execute(&ogBuf, pokerOGData{
		Title: title, Description: desc, URL: pageURL, ImageURL: imageURL,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if err := pokerHTMLTpl.Execute(w, struct{ OGMeta template.HTML }{OGMeta: template.HTML(ogBuf.String())}); err != nil {
		log.Printf("[Poker] HTML render failed: %v", err)
	}
}

// pokerOGCopy returns the OG title and description for a hand/board.
func pokerOGCopy(hand, board []poker.Card) (title, desc string) {
	if len(hand) == 0 {
		return "Hold'em Equity Calculator",
			"Pick your hole cards and the board, then see your win probability against random opponents."
	}
	handStr := cardsString(hand)
	if len(board) == 0 {
		return "Hold'em Equity · " + handStr,
			"Win probability for " + handStr + " preflop against random opponents."
	}
	return "Hold'em Equity · " + handStr + " / " + cardsString(board),
		"Win probability for " + handStr + " on board " + cardsString(board) + "."
}

func cardsString(cards []poker.Card) string {
	parts := make([]string, len(cards))
	for i, c := range cards {
		parts[i] = rankLabel(c.Rank()) + suitGlyph(c.Suit())
	}
	return strings.Join(parts, " ")
}

// absoluteURL builds an absolute URL from the request, honoring
// X-Forwarded-Proto / X-Forwarded-Host (set by the nginx ingress).
func absoluteURL(r *http.Request, path string) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host + path
}

// ── Init ────────────────────────────────────────────────────────

// InitPoker registers the poker equity calculator routes.
func InitPoker(mux *http.ServeMux) {
	mux.HandleFunc(pokerBasePath+"/api/equity", handlePokerEquity)
	mux.HandleFunc(pokerBasePath+"/og.png", handlePokerOGImage)

	staticSub, err := fs.Sub(content, "poker")
	if err != nil {
		log.Fatalf("[Poker] failed to load embedded static files: %v", err)
	}
	loadPokerHTMLTemplate(staticSub)
	fileServer := http.FileServer(http.FS(staticSub))

	// Intercept the bare /poker/ and /poker/index.html requests so we can
	// inject OG meta. Everything else (assets) flows through the static
	// FileServer untouched.
	mux.HandleFunc(pokerBasePath+"/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pokerBasePath+"/" || r.URL.Path == pokerBasePath+"/index.html" {
			servePokerHTML(w, r)
			return
		}
		http.StripPrefix(pokerBasePath, fileServer).ServeHTTP(w, r)
	})

	log.Printf("[Poker] Poker equity calculator registered at %s/", pokerBasePath)
}
