package main

// vote_tally.go — the counting engine for the ranked choice voting tool.
//
// Pure functions, no I/O, no clock, no store access: given candidate IDs
// (in poll-creation order) and a set of ballots (rankings of candidate IDs,
// best first, possibly partial), produce a full instant-runoff result plus
// a Condorcet cross-check and a tie-break sensitivity analysis.
//
// Design goals, born from watching a bad RCV site decide an election with
// the words "Weighted tiebreaker loser is X" and no further explanation:
//
//   - Exhausted ballots are a visible bucket in every round, never
//     silently dropped.
//   - The majority threshold is reported against continuing ballots each
//     round, alongside the original ballot count.
//   - Tie-breaks are deterministic, use a stated rule chain, and whenever
//     one fires the count is re-run once per alternative choice; if any
//     alternative produces a different winner the result is flagged.
//   - The pairwise (Condorcet) winner is always computed so the UI can
//     call out the case where instant-runoff disagrees with the
//     head-to-head favorite.

import "sort"

// TallyInput is everything the engine needs.
type TallyInput struct {
	// CandidateIDs in poll-creation order. Order matters: it is the
	// final tie-break rule ("listed last is eliminated").
	CandidateIDs []string
	// Ballots are rankings of candidate IDs, best first. May be partial.
	// Callers must have validated that IDs are known and unduplicated.
	Ballots [][]string
}

// TieBreakRule identifies which rule in the chain resolved a tie.
type TieBreakRule string

const (
	// RulePrevRound: among the tied, fewest votes in the previous round.
	RulePrevRound TieBreakRule = "prevRound"
	// RuleFewestRankings: among the tied, ranked on the fewest ballots
	// overall (at any position).
	RuleFewestRankings TieBreakRule = "fewestRankings"
	// RuleListedLast: still tied — the option listed last at poll
	// creation is eliminated.
	RuleListedLast TieBreakRule = "listedLast"
)

// TieBreakEvent records one tie-break during the count.
type TieBreakEvent struct {
	Round        int          `json:"round"`
	TiedIDs      []string     `json:"tiedIds"`      // tied for fewest votes
	Rule         TieBreakRule `json:"rule"`         // rule that resolved it
	EliminatedID string       `json:"eliminatedId"` // the chosen loser
}

// TallyRound is one instant-runoff round.
type TallyRound struct {
	Number     int            `json:"number"`
	Counts     map[string]int `json:"counts"`     // first prefs among standing candidates
	Exhausted  int            `json:"exhausted"`  // ballots with no standing candidate ranked
	Continuing int            `json:"continuing"` // ballots still counting this round
	Majority   int            `json:"majority"`   // continuing/2 + 1

	// Eliminated lists candidates removed after this round's count:
	// either the whole zero-vote batch, or exactly one via (tie-broken)
	// fewest votes. Empty on the deciding round.
	Eliminated []string       `json:"eliminated"`
	TieBreak   *TieBreakEvent `json:"tieBreak,omitempty"`

	// WinnerID is set when this round decides the election (majority
	// reached, or only one candidate left standing).
	WinnerID string `json:"winnerId,omitempty"`
}

// CondorcetResult is the pairwise cross-check.
type CondorcetResult struct {
	// WinnerID is the candidate who beats every other head-to-head, or
	// "" if none exists.
	WinnerID string `json:"winnerId,omitempty"`
	// HasCycle is true when there is no Condorcet winner despite there
	// being candidates with support (a preference cycle).
	HasCycle bool `json:"hasCycle"`
	// Matrix[i][j] = number of ballots preferring CandidateIDs[i] over
	// CandidateIDs[j]. A candidate ranked anywhere beats one unranked;
	// two unranked candidates express no preference.
	Matrix [][]int `json:"matrix"`
}

// TallyResult is the complete output of a count.
type TallyResult struct {
	// WinnerID of the official instant-runoff count, or "" when there is
	// no winner (no ballots, or every ballot exhausted).
	WinnerID string       `json:"winnerId,omitempty"`
	Rounds   []TallyRound `json:"rounds"`

	TotalBallots int `json:"totalBallots"`

	// TieBreakSensitive is true when re-running the count with a
	// different (but equally tied) elimination choice at any tie-break
	// point produces a different winner.
	TieBreakSensitive bool `json:"tieBreakSensitive"`
	// AlternateWinners lists the other winners those re-runs produced.
	AlternateWinners []string `json:"alternateWinners,omitempty"`

	Condorcet CondorcetResult `json:"condorcet"`
}

// forcedElim overrides one elimination during a re-run: at round Round,
// if CandidateID is among those tied for fewest votes, eliminate it
// instead of applying the rule chain. Used by the sensitivity analysis.
type forcedElim struct {
	Round       int
	CandidateID string
}

// RunTally computes the full result: primary IRV count, tie-break
// sensitivity re-runs, and the Condorcet cross-check.
func RunTally(in TallyInput) TallyResult {
	res := runIRV(in, nil)
	res.TotalBallots = len(in.Ballots)

	// Sensitivity: for every tie-break that fired in the primary count,
	// re-run once per alternative tied candidate. Any re-run producing a
	// different winner flags the result. Bounded by the poll caps
	// (candidates × rounds), so this is at most a few hundred cheap runs.
	altSet := map[string]bool{}
	for _, rd := range res.Rounds {
		if rd.TieBreak == nil {
			continue
		}
		for _, altID := range rd.TieBreak.TiedIDs {
			if altID == rd.TieBreak.EliminatedID {
				continue
			}
			rerun := runIRV(in, &forcedElim{Round: rd.Number, CandidateID: altID})
			if rerun.WinnerID != res.WinnerID {
				altSet[rerun.WinnerID] = true
			}
		}
	}
	if len(altSet) > 0 {
		res.TieBreakSensitive = true
		for id := range altSet {
			res.AlternateWinners = append(res.AlternateWinners, id)
		}
		sort.Strings(res.AlternateWinners)
	}

	res.Condorcet = condorcet(in)
	return res
}

// runIRV performs one instant-runoff count. override, when non-nil,
// hijacks a single tie-break decision (see forcedElim).
func runIRV(in TallyInput, override *forcedElim) TallyResult {
	res := TallyResult{}
	if len(in.CandidateIDs) == 0 {
		return res
	}

	standing := map[string]bool{}
	for _, id := range in.CandidateIDs {
		standing[id] = true
	}
	// listedIndex implements the final tie-break rule.
	listedIndex := map[string]int{}
	for i, id := range in.CandidateIDs {
		listedIndex[id] = i
	}
	// rankedOn[c] = number of ballots ranking c at any position (rule 2).
	rankedOn := map[string]int{}
	for _, b := range in.Ballots {
		for _, id := range b {
			rankedOn[id]++
		}
	}

	var prevCounts map[string]int
	for round := 1; ; round++ {
		// Count first preferences among standing candidates.
		counts := map[string]int{}
		for id := range standing {
			counts[id] = 0
		}
		exhausted := 0
		for _, b := range in.Ballots {
			counted := false
			for _, id := range b {
				if standing[id] {
					counts[id]++
					counted = true
					break
				}
			}
			if !counted {
				exhausted++
			}
		}
		continuing := len(in.Ballots) - exhausted
		majority := continuing/2 + 1

		rd := TallyRound{
			Number:     round,
			Counts:     counts,
			Exhausted:  exhausted,
			Continuing: continuing,
			Majority:   majority,
		}

		// No live ballots at all: nobody can win.
		if continuing == 0 {
			res.Rounds = append(res.Rounds, rd)
			return res
		}

		// Majority, or last one standing, decides it.
		leader, leaderVotes := "", -1
		for id, n := range counts {
			if n > leaderVotes || (n == leaderVotes && listedIndex[id] < listedIndex[leader]) {
				leader, leaderVotes = id, n
			}
		}
		if leaderVotes >= majority || len(standing) == 1 {
			rd.WinnerID = leader
			res.Rounds = append(res.Rounds, rd)
			res.WinnerID = leader
			return res
		}

		// Find the fewest-votes count among standing candidates.
		min := -1
		for _, n := range counts {
			if min == -1 || n < min {
				min = n
			}
		}

		if min == 0 {
			// Batch-eliminate every zero-vote candidate. They hold no
			// ballots, so removing them together transfers nothing and
			// needs no tie-break.
			for id, n := range counts {
				if n == 0 {
					rd.Eliminated = append(rd.Eliminated, id)
					delete(standing, id)
				}
			}
			sortByListed(rd.Eliminated, listedIndex)
		} else {
			// Eliminate exactly one candidate with the fewest votes,
			// tie-breaking deterministically if several are tied.
			var tied []string
			for id, n := range counts {
				if n == min {
					tied = append(tied, id)
				}
			}
			sortByListed(tied, listedIndex)

			loser := tied[0]
			if len(tied) > 1 {
				var rule TieBreakRule
				if override != nil && override.Round == round && contains(tied, override.CandidateID) {
					loser, rule = override.CandidateID, RuleListedLast // rule label unused on re-runs
				} else {
					loser, rule = breakTie(tied, prevCounts, rankedOn, listedIndex)
				}
				rd.TieBreak = &TieBreakEvent{
					Round:        round,
					TiedIDs:      tied,
					Rule:         rule,
					EliminatedID: loser,
				}
			}
			rd.Eliminated = []string{loser}
			delete(standing, loser)
		}

		res.Rounds = append(res.Rounds, rd)
		prevCounts = counts
	}
}

// breakTie applies the rule chain to candidates tied for fewest votes:
//  1. fewest votes in the previous round;
//  2. ranked on the fewest ballots overall;
//  3. listed last at poll creation.
//
// Each rule narrows the tied set; the first rule that leaves a single
// candidate names the loser and the rule reported to the user.
func breakTie(tied []string, prevCounts map[string]int, rankedOn, listedIndex map[string]int) (string, TieBreakRule) {
	if prevCounts != nil {
		if narrowed := keepMin(tied, func(id string) int { return prevCounts[id] }); len(narrowed) < len(tied) {
			if len(narrowed) == 1 {
				return narrowed[0], RulePrevRound
			}
			tied = narrowed
		}
	}
	if narrowed := keepMin(tied, func(id string) int { return rankedOn[id] }); len(narrowed) < len(tied) {
		if len(narrowed) == 1 {
			return narrowed[0], RuleFewestRankings
		}
		tied = narrowed
	}
	// Listed last loses.
	loser := tied[0]
	for _, id := range tied[1:] {
		if listedIndex[id] > listedIndex[loser] {
			loser = id
		}
	}
	return loser, RuleListedLast
}

// keepMin returns the subset of ids with the minimum score.
func keepMin(ids []string, score func(string) int) []string {
	min := score(ids[0])
	for _, id := range ids[1:] {
		if s := score(id); s < min {
			min = s
		}
	}
	var out []string
	for _, id := range ids {
		if score(id) == min {
			out = append(out, id)
		}
	}
	return out
}

// condorcet builds the pairwise preference matrix and finds the candidate
// (if any) who beats every other head-to-head.
func condorcet(in TallyInput) CondorcetResult {
	n := len(in.CandidateIDs)
	idx := map[string]int{}
	for i, id := range in.CandidateIDs {
		idx[id] = i
	}

	matrix := make([][]int, n)
	for i := range matrix {
		matrix[i] = make([]int, n)
	}

	for _, b := range in.Ballots {
		// rank[i] = position of candidate i on this ballot, or n (worse
		// than any ranked candidate) when unranked.
		rank := make([]int, n)
		for i := range rank {
			rank[i] = n
		}
		for pos, id := range b {
			rank[idx[id]] = pos
		}
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				switch {
				case rank[i] < rank[j]:
					matrix[i][j]++
				case rank[j] < rank[i]:
					matrix[j][i]++
					// both unranked: no preference expressed
				}
			}
		}
	}

	res := CondorcetResult{Matrix: matrix}
	if len(in.Ballots) == 0 || n == 0 {
		return res
	}

	for i := 0; i < n; i++ {
		beatsAll := true
		for j := 0; j < n; j++ {
			if i != j && matrix[i][j] <= matrix[j][i] {
				beatsAll = false
				break
			}
		}
		if beatsAll {
			res.WinnerID = in.CandidateIDs[i]
			return res
		}
	}
	if n > 1 {
		res.HasCycle = true
	}
	return res
}

// sortByListed orders ids by their poll-creation position, keeping every
// eliminated/tied list deterministic in the output.
func sortByListed(ids []string, listedIndex map[string]int) {
	sort.Slice(ids, func(a, b int) bool { return listedIndex[ids[a]] < listedIndex[ids[b]] })
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
