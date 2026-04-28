package main

import (
	"testing"

	"github.com/paulhankin/poker/v2/poker"
)

func mustCard(t *testing.T, s string) poker.Card {
	t.Helper()
	c, err := parseCard(s)
	if err != nil {
		t.Fatalf("bad card %q: %v", s, err)
	}
	return c
}

// ── classify5 ───────────────────────────────────────────────────

func TestClassify5(t *testing.T) {
	tests := []struct {
		name  string
		cards []string
		want  HandCategory
	}{
		{"high card",        []string{"As", "Kd", "Qh", "Jc", "9s"}, CatHighCard},
		{"one pair",         []string{"As", "Ad", "Qh", "Jc", "9s"}, CatPair},
		{"two pair",         []string{"As", "Ad", "Qh", "Qc", "9s"}, CatTwoPair},
		{"three of a kind",  []string{"As", "Ad", "Ah", "Jc", "9s"}, CatTrips},
		{"straight",         []string{"5s", "6d", "7h", "8c", "9s"}, CatStraight},
		{"wheel straight",   []string{"As", "2d", "3h", "4c", "5s"}, CatStraight},
		{"flush",            []string{"As", "Ks", "9s", "5s", "2s"}, CatFlush},
		{"full house",       []string{"As", "Ad", "Ah", "Qc", "Qs"}, CatFullHouse},
		{"four of a kind",   []string{"As", "Ad", "Ah", "Ac", "9s"}, CatQuads},
		{"straight flush",   []string{"5s", "6s", "7s", "8s", "9s"}, CatStraightFlush},
		{"royal-as-sf",      []string{"Ts", "Js", "Qs", "Ks", "As"}, CatStraightFlush},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cards [5]poker.Card
			for i, s := range tt.cards {
				cards[i] = mustCard(t, s)
			}
			if got := classify5(cards); got != tt.want {
				t.Errorf("classify5(%v) = %v, want %v", tt.cards, got, tt.want)
			}
		})
	}
}

// ── bestCategory across 6/7 cards ───────────────────────────────

func TestBestCategory_7Cards(t *testing.T) {
	// Hero AsAh, board 8d 9c 8h 7h Ad → AAA88 (full house).
	cards := []poker.Card{
		mustCard(t, "As"), mustCard(t, "Ah"),
		mustCard(t, "8d"), mustCard(t, "9c"),
		mustCard(t, "8h"), mustCard(t, "7h"),
		mustCard(t, "Ad"),
	}
	if got := bestCategory(cards); got != CatFullHouse {
		t.Errorf("expected Full House, got %v", got)
	}
}

func TestBestCategory_6Cards(t *testing.T) {
	// Hero AsAh, board Kd Qc Jh Ts → AKQJT broadway straight is best.
	cards := []poker.Card{
		mustCard(t, "As"), mustCard(t, "Ah"),
		mustCard(t, "Kd"), mustCard(t, "Qc"),
		mustCard(t, "Jh"), mustCard(t, "Ts"),
	}
	if got := bestCategory(cards); got != CatStraight {
		t.Errorf("expected Straight, got %v", got)
	}
}

// ── computeThreats ──────────────────────────────────────────────

func TestComputeThreats_NilForShortBoard(t *testing.T) {
	hero := [2]poker.Card{mustCard(t, "As"), mustCard(t, "Ah")}
	if got := computeThreats(hero, nil); got != nil {
		t.Errorf("expected nil for empty board, got %+v", got)
	}
	if got := computeThreats(hero, []poker.Card{mustCard(t, "Kd"), mustCard(t, "Qc")}); got != nil {
		t.Errorf("expected nil for 2-card board, got %+v", got)
	}
}

func TestComputeThreats_AcesFullOnRiver(t *testing.T) {
	// Hero AsAh, board 8d 9c 8h 7h Ad → AAA88. Only thing that beats this:
	// quad eights, which requires opponent to hold both remaining 8s
	// (exactly 1 combo: 8s+8c). No straight flush is reachable here — every
	// hearts run that includes the board's 7h+8h needs 3+ more hearts, and
	// opponent only holds 2 hole cards.
	hero := [2]poker.Card{mustCard(t, "As"), mustCard(t, "Ah")}
	board := []poker.Card{
		mustCard(t, "8d"), mustCard(t, "9c"),
		mustCard(t, "8h"), mustCard(t, "7h"),
		mustCard(t, "Ad"),
	}
	threats := computeThreats(hero, board)

	if len(threats) != 1 {
		t.Fatalf("expected exactly 1 threat category (Four of a Kind), got %+v", threats)
	}
	if threats[0].Category != "Four of a Kind" {
		t.Errorf("category = %q, want Four of a Kind", threats[0].Category)
	}
	if threats[0].Combos != 1 {
		t.Errorf("combos = %d, want 1 (only 8s+8c → quad eights)", threats[0].Combos)
	}
	// Denominator: 52 - 7 (hero+board) = 45 unseen cards. C(45, 2) = 990.
	if threats[0].TotalCombos != 990 {
		t.Errorf("totalCombos = %d, want 990", threats[0].TotalCombos)
	}
}

func TestComputeThreats_OrderedStrongestFirst(t *testing.T) {
	// Hero has a weak two pair on a board with lots of stronger possibilities:
	// trips, straight, flush, full house, quads all reachable.
	hero := [2]poker.Card{mustCard(t, "2s"), mustCard(t, "2h")}
	board := []poker.Card{
		mustCard(t, "7d"), mustCard(t, "7c"),
		mustCard(t, "5h"), mustCard(t, "Kd"),
		mustCard(t, "3s"),
	}
	threats := computeThreats(hero, board)
	if len(threats) < 2 {
		t.Fatalf("expected at least 2 threat categories, got %+v", threats)
	}
	// Categories should appear strongest-first.
	ord := map[string]int{
		"High Card": 0, "Pair": 1, "Two Pair": 2, "Three of a Kind": 3,
		"Straight": 4, "Flush": 5, "Full House": 6, "Four of a Kind": 7,
		"Straight Flush": 8,
	}
	for i := 1; i < len(threats); i++ {
		if ord[threats[i-1].Category] <= ord[threats[i].Category] {
			t.Errorf("threats not ordered strongest-first: %s before %s",
				threats[i-1].Category, threats[i].Category)
		}
	}
}
