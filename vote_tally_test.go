package main

import (
	"reflect"
	"sort"
	"testing"
)

// The exact poll from the DnD group's session on rankedchoices.com that
// prompted this tool ("Indoctrinate the child hostages?"). Five candidates,
// five ballots, partial rankings, a three-way tie for fewest votes in the
// first meaningful round, and — the kicker — a Condorcet winner that loses
// the instant runoff.
func screenshotInput() TallyInput {
	const (
		J = "just-ask"    // Just Ask them
		P = "persuasion"  // Persuasion Check
		C = "child-icide" // Child-icide
		I = "intimidate"  // Intimidation Check
		D = "idc"         // idc
	)
	return TallyInput{
		CandidateIDs: []string{J, P, C, I, D},
		Ballots: [][]string{
			{J, P, I},       // Vote 1
			{P, J, I, C, D}, // Vote 2
			{C, I, P},       // Caelynn
			{I, C, P, J, D}, // Vote 4
			{J, P, D, I, C}, // Vote 5
		},
	}
}

func TestScreenshotScenario(t *testing.T) {
	res := RunTally(screenshotInput())

	// Same official winner the old site produced.
	if res.WinnerID != "just-ask" {
		t.Fatalf("winner = %q, want just-ask", res.WinnerID)
	}
	if res.TotalBallots != 5 {
		t.Fatalf("totalBallots = %d, want 5", res.TotalBallots)
	}

	// Round 1: idc has zero first prefs and is batch-eliminated alone —
	// no tie-break theater required for a candidate nobody voted for.
	r1 := res.Rounds[0]
	if want := map[string]int{"just-ask": 2, "persuasion": 1, "child-icide": 1, "intimidate": 1, "idc": 0}; !reflect.DeepEqual(r1.Counts, want) {
		t.Fatalf("round 1 counts = %v, want %v", r1.Counts, want)
	}
	if !reflect.DeepEqual(r1.Eliminated, []string{"idc"}) {
		t.Fatalf("round 1 eliminated = %v, want [idc]", r1.Eliminated)
	}
	if r1.TieBreak != nil {
		t.Fatalf("round 1 should not need a tie-break, got %+v", r1.TieBreak)
	}
	if r1.Majority != 3 || r1.Continuing != 5 || r1.Exhausted != 0 {
		t.Fatalf("round 1 majority/continuing/exhausted = %d/%d/%d, want 3/5/0", r1.Majority, r1.Continuing, r1.Exhausted)
	}

	// Round 2: persuasion, child-icide, intimidate tied at 1. The old
	// site resolved this with an unexplained "weighted tiebreaker". Ours
	// must resolve it by the fewest-rankings rule: child-icide appears on
	// 4 of 5 ballots, the other two on all 5.
	r2 := res.Rounds[1]
	if r2.TieBreak == nil {
		t.Fatal("round 2 should have a tie-break event")
	}
	wantTied := []string{"persuasion", "child-icide", "intimidate"}
	if !reflect.DeepEqual(r2.TieBreak.TiedIDs, wantTied) {
		t.Fatalf("round 2 tied = %v, want %v", r2.TieBreak.TiedIDs, wantTied)
	}
	if r2.TieBreak.Rule != RuleFewestRankings {
		t.Fatalf("round 2 tie-break rule = %q, want %q", r2.TieBreak.Rule, RuleFewestRankings)
	}
	if r2.TieBreak.EliminatedID != "child-icide" {
		t.Fatalf("round 2 eliminated %q, want child-icide", r2.TieBreak.EliminatedID)
	}

	// The tie-break was NOT decisive: forcing either alternative
	// elimination still elects just-ask. The honest answer here is that
	// this particular winner was robust — the flag must stay quiet.
	if res.TieBreakSensitive {
		t.Fatalf("tie-break sensitivity should be false, alternates = %v", res.AlternateWinners)
	}

	// The real scandal the old site never surfaced: Persuasion Check
	// beats every other candidate head-to-head yet loses the runoff.
	if res.Condorcet.WinnerID != "persuasion" {
		t.Fatalf("condorcet winner = %q, want persuasion", res.Condorcet.WinnerID)
	}
	if res.Condorcet.WinnerID == res.WinnerID {
		t.Fatal("test expects IRV and Condorcet to disagree in this scenario")
	}

	// No ballot exhausts in this poll; every round should say so.
	for _, rd := range res.Rounds {
		if rd.Exhausted != 0 {
			t.Fatalf("round %d exhausted = %d, want 0", rd.Number, rd.Exhausted)
		}
	}
}

func TestMajorityInRoundOne(t *testing.T) {
	res := RunTally(TallyInput{
		CandidateIDs: []string{"a", "b", "c"},
		Ballots: [][]string{
			{"a", "b"}, {"a", "c"}, {"a"}, {"b", "a"}, {"c"},
		},
	})
	if res.WinnerID != "a" {
		t.Fatalf("winner = %q, want a", res.WinnerID)
	}
	if len(res.Rounds) != 1 {
		t.Fatalf("rounds = %d, want 1", len(res.Rounds))
	}
	if res.Rounds[0].WinnerID != "a" {
		t.Fatalf("round 1 winner = %q, want a", res.Rounds[0].WinnerID)
	}
}

func TestExhaustedBallotsAreVisible(t *testing.T) {
	// c's lone ballot exhausts when c is eliminated, then b's two ballots
	// exhaust when b loses the tie-break. The exhausted bucket and the
	// shrinking majority threshold must both be reported every round.
	res := RunTally(TallyInput{
		CandidateIDs: []string{"a", "b", "c"},
		Ballots: [][]string{
			{"a"}, {"a"}, {"b"}, {"b"}, {"c"},
		},
	})
	if res.WinnerID != "a" {
		t.Fatalf("winner = %q, want a", res.WinnerID)
	}
	type snap struct{ exhausted, continuing, majority int }
	var got []snap
	for _, rd := range res.Rounds {
		got = append(got, snap{rd.Exhausted, rd.Continuing, rd.Majority})
	}
	want := []snap{{0, 5, 3}, {1, 4, 3}, {3, 2, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exhausted/continuing/majority per round = %v, want %v", got, want)
	}
}

func TestTieBreakSensitivityFlagFires(t *testing.T) {
	// A dead 1-1 heat between two bullet ballots: whichever candidate the
	// rule chain eliminates, the other wins. The whole election hinges on
	// the tie-break, and the result must say so.
	res := RunTally(TallyInput{
		CandidateIDs: []string{"a", "b"},
		Ballots:      [][]string{{"a"}, {"b"}},
	})
	if res.WinnerID != "a" {
		// Rule chain: no previous round, equal rankings, so "listed
		// last" eliminates b and a wins.
		t.Fatalf("winner = %q, want a", res.WinnerID)
	}
	if !res.TieBreakSensitive {
		t.Fatal("tie-break sensitivity flag should fire")
	}
	if !reflect.DeepEqual(res.AlternateWinners, []string{"b"}) {
		t.Fatalf("alternate winners = %v, want [b]", res.AlternateWinners)
	}
	if res.Rounds[0].TieBreak == nil || res.Rounds[0].TieBreak.Rule != RuleListedLast {
		t.Fatalf("expected a listedLast tie-break in round 1, got %+v", res.Rounds[0].TieBreak)
	}
}

func TestPrevRoundRuleBreaksTie(t *testing.T) {
	// Engineer a tie in round 2 between candidates that were NOT tied in
	// round 1, so the previous-round rule (rule 1) settles it.
	//
	// Round 1: a=3, b=2, c=1, d=3 (9 ballots, majority 5) → c eliminated.
	// Round 2: c's ballot transfers to b → a=3, b=3, d=3. All tied at 3;
	// previous round: a=3, b=2, d=3 → b had fewest before, b eliminated.
	res := RunTally(TallyInput{
		CandidateIDs: []string{"a", "b", "c", "d"},
		Ballots: [][]string{
			{"a"}, {"a"}, {"a"},
			{"b"}, {"b"},
			{"c", "b"},
			{"d"}, {"d"}, {"d"},
		},
	})
	r2 := res.Rounds[1]
	if r2.TieBreak == nil {
		t.Fatal("round 2 should have a tie-break event")
	}
	if r2.TieBreak.Rule != RulePrevRound {
		t.Fatalf("round 2 rule = %q, want %q", r2.TieBreak.Rule, RulePrevRound)
	}
	if r2.TieBreak.EliminatedID != "b" {
		t.Fatalf("round 2 eliminated %q, want b", r2.TieBreak.EliminatedID)
	}
}

func TestCondorcetCycle(t *testing.T) {
	// Rock-paper-scissors preferences: a>b>c, b>c>a, c>a>b.
	res := RunTally(TallyInput{
		CandidateIDs: []string{"a", "b", "c"},
		Ballots: [][]string{
			{"a", "b", "c"},
			{"b", "c", "a"},
			{"c", "a", "b"},
		},
	})
	if res.Condorcet.WinnerID != "" {
		t.Fatalf("condorcet winner = %q, want none", res.Condorcet.WinnerID)
	}
	if !res.Condorcet.HasCycle {
		t.Fatal("expected a Condorcet cycle")
	}
}

func TestCondorcetMatrixUnrankedSemantics(t *testing.T) {
	// A ranked candidate beats an unranked one; two unranked candidates
	// express no preference.
	res := RunTally(TallyInput{
		CandidateIDs: []string{"a", "b", "c"},
		Ballots:      [][]string{{"a"}}, // b and c unranked
	})
	m := res.Condorcet.Matrix
	if m[0][1] != 1 || m[0][2] != 1 {
		t.Fatalf("a should beat unranked b and c: %v", m)
	}
	if m[1][2] != 0 || m[2][1] != 0 {
		t.Fatalf("unranked b vs c should be 0-0: %v", m)
	}
}

func TestNoBallots(t *testing.T) {
	res := RunTally(TallyInput{CandidateIDs: []string{"a", "b"}})
	if res.WinnerID != "" {
		t.Fatalf("winner = %q, want none", res.WinnerID)
	}
	if len(res.Rounds) != 1 || res.Rounds[0].Continuing != 0 {
		t.Fatalf("expected a single round with 0 continuing ballots, got %+v", res.Rounds)
	}
	if res.Condorcet.WinnerID != "" || res.Condorcet.HasCycle {
		t.Fatalf("empty election should have no Condorcet verdict: %+v", res.Condorcet)
	}
}

func TestNoCandidates(t *testing.T) {
	res := RunTally(TallyInput{})
	if res.WinnerID != "" || len(res.Rounds) != 0 {
		t.Fatalf("empty input should produce empty result, got %+v", res)
	}
}

func TestSingleCandidate(t *testing.T) {
	res := RunTally(TallyInput{
		CandidateIDs: []string{"a"},
		Ballots:      [][]string{{"a"}, {"a"}},
	})
	if res.WinnerID != "a" || len(res.Rounds) != 1 {
		t.Fatalf("single candidate should win in round 1, got %+v", res)
	}
	if res.Condorcet.WinnerID != "a" {
		t.Fatalf("sole candidate is trivially the Condorcet winner, got %q", res.Condorcet.WinnerID)
	}
}

func TestDeterminism(t *testing.T) {
	// Same input must always produce the identical result — the entire
	// point of ditching "weighted tiebreakers".
	in := screenshotInput()
	first := RunTally(in)
	for i := 0; i < 25; i++ {
		again := RunTally(in)
		if again.WinnerID != first.WinnerID ||
			!reflect.DeepEqual(again.AlternateWinners, first.AlternateWinners) ||
			len(again.Rounds) != len(first.Rounds) {
			t.Fatalf("run %d differed: %+v vs %+v", i, again, first)
		}
		for r := range again.Rounds {
			if !reflect.DeepEqual(again.Rounds[r].Counts, first.Rounds[r].Counts) ||
				!reflect.DeepEqual(again.Rounds[r].Eliminated, first.Rounds[r].Eliminated) {
				t.Fatalf("run %d round %d differed", i, r+1)
			}
		}
	}
}

// sanity: sortByListed used on TiedIDs keeps deterministic creation order.
func TestTiedIDsInCreationOrder(t *testing.T) {
	res := RunTally(screenshotInput())
	tied := res.Rounds[1].TieBreak.TiedIDs
	if !sort.SliceIsSorted(tied, func(i, j int) bool {
		order := map[string]int{"just-ask": 0, "persuasion": 1, "child-icide": 2, "intimidate": 3, "idc": 4}
		return order[tied[i]] < order[tied[j]]
	}) {
		t.Fatalf("tied ids not in creation order: %v", tied)
	}
}
