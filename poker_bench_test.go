package main

import (
	"testing"

	"github.com/paulhankin/poker/v2/poker"
)

// Benchmark the equity calculation at a representative scenario:
// AA in the hole, full board 8d-9c-8h-7h-Ad, 6 players.
// Run with `go test -run none -bench BenchmarkCalcEquity -benchtime=3x`
// to measure wall time at the configured pokerIterations.
func BenchmarkCalcEquity(b *testing.B) {
	hero := [2]poker.Card{
		poker.NameToCard["SA"],
		poker.NameToCard["HA"],
	}
	board := []poker.Card{
		poker.NameToCard["D8"],
		poker.NameToCard["C9"],
		poker.NameToCard["H8"],
		poker.NameToCard["H7"],
		poker.NameToCard["DA"],
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := calcEquity(hero, board, 6)
		if err != nil {
			b.Fatal(err)
		}
	}
}
