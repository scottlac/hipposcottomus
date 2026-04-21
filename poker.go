package main

import (
	"encoding/json"
	"fmt"
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
	pokerIterations = 1500
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
	WinProbability  float64 `json:"winProbability"`
	TieProbability  float64 `json:"tieProbability"`
	HandDescription string  `json:"handDescription"`
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
	}
	writeJSON(w, resp)
}

// ── Init ────────────────────────────────────────────────────────

// InitPoker registers the poker equity calculator routes.
func InitPoker(mux *http.ServeMux) {
	mux.HandleFunc(pokerBasePath+"/api/equity", handlePokerEquity)

	staticSub, err := fs.Sub(content, "poker")
	if err != nil {
		log.Fatalf("[Poker] failed to load embedded static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle(pokerBasePath+"/", http.StripPrefix(pokerBasePath, fileServer))

	log.Printf("[Poker] Poker equity calculator registered at %s/", pokerBasePath)
}
