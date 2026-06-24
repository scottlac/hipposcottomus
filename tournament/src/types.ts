// ---------------------------------------------------------------------------
// Domain types
//
// The app keeps a small, serializable "input" object as the single source of
// truth (persisted to localStorage, synced across windows, and undoable). The
// full bracket is *derived* from those inputs by a pure function (see
// bracket.ts), which is what makes editing past results re-resolve cleanly.
// ---------------------------------------------------------------------------

export type PlayerId = string

/** Self-reported skill bucket, used for spread-seeding Round 1. */
export type SkillLevel = 'Beginner' | 'Intermediate' | 'Expert'

export interface Player {
  id: PlayerId
  name: string
  skill?: SkillLevel
}

export type StationId = 'A' | 'B'

/** A single race grouping as authored for Round 1 (the only manually-structured round). */
export interface GroupStruct {
  /** Stable id, used for drag/drop and station references on pending matches. */
  id: string
  playerIds: PlayerId[]
  /** A bye is a 1-player "race" that auto-advances. */
  isBye: boolean
}

/** A recorded race result: the human finishing order, 1st → last. */
export interface ResultEntry {
  /** Finishing order of human players (playerIds). */
  order: PlayerId[]
  /** Optional track name (nice-to-have). */
  track?: string
  /** Epoch ms the result was recorded (for display only; supplied by caller). */
  at?: number
}

/**
 * Everything that is persisted / synced / undone. The derived bracket is a
 * pure function of this object plus nothing else.
 */
export interface TournamentInput {
  version: number
  title: string
  /** Default number of players who advance from each race. */
  defaultAdvance: number
  /** Roster, in seed order. */
  players: Player[]
  /** Round 1 groupings (manually editable before lock). */
  round0: GroupStruct[]
  /** Per-round advance overrides, keyed by round index. */
  roundAdvance: Record<number, number>
  /**
   * Recorded results keyed by player-set signature (sorted player ids). Keying
   * by player set — not by match position — is the trick that makes downstream
   * re-resolution automatic: change who advances upstream and the downstream
   * races get new player sets (new signatures) and therefore become un-entered.
   */
  results: Record<string, ResultEntry>
  /** Which match (by signature) is live at each station. */
  stations: Record<StationId, string | null>
  /** Whether the tournament has started (Round 1 + roster frozen). */
  locked: boolean
}

export type MatchStatus = 'pending' | 'in_progress' | 'done'

/** A fully-resolved race, ready to render. */
export interface DerivedMatch {
  /** Player-set signature; stable identity across re-resolution. */
  sig: string
  round: number
  indexInRound: number
  /** Human label, e.g. "R1 · Race 3". */
  label: string
  players: Player[]
  isBye: boolean
  advanceCount: number
  status: MatchStatus
  station: StationId | null
  /** Finishing order (humans), present when status === 'done'. */
  result?: Player[]
  /** Players who advance from this race. */
  advancers: Player[]
  /** Players knocked out here. */
  eliminated: Player[]
  track?: string
}

export interface DerivedRound {
  index: number
  /** Human label, e.g. "Round 1", "Semifinals", "Final". */
  label: string
  advanceCount: number
  matches: DerivedMatch[]
  complete: boolean
}

export type Phase = 'setup' | 'running' | 'complete'

export interface DerivedState {
  phase: Phase
  rounds: DerivedRound[]
  champion?: Player
  /** Convenience: matches currently live, by station. */
  live: Record<StationId, DerivedMatch | null>
  /** Pending matches not yet assigned to a station, earliest first. */
  onDeck: DerivedMatch[]
  /** Total humans still alive in the bracket. */
  remaining: number
}
