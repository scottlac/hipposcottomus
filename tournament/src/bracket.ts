// ---------------------------------------------------------------------------
// Pure bracket logic.
//
// resolve(input) turns the small persisted input object into the full derived
// bracket. It is deliberately pure and deterministic so the same inputs always
// produce the same bracket — which is what lets us edit/undo past results and
// have every downstream round re-resolve correctly with no bespoke patching.
// ---------------------------------------------------------------------------

import type {
  DerivedMatch,
  DerivedRound,
  DerivedState,
  GroupStruct,
  Phase,
  Player,
  PlayerId,
  StationId,
  TournamentInput,
} from './types'

export const RACE_SIZE = 4
export const STORE_VERSION = 1

/** Canonical signature for a set of players (order-independent, stable). */
export function signature(playerIds: readonly PlayerId[]): string {
  return [...playerIds].sort().join('|')
}

/**
 * Split `n` players into races of at most `size`, distributed as evenly as
 * possible so leftover players form smaller races (3 or 2 humans) instead of
 * one lopsided race or a lonely racer. Returns the size of each race.
 *
 *   21 → [4,4,4,3,3,3]   22 → [4,4,4,4,3,3]   5 → [3,2]   2 → [2]   1 → [1]
 *
 * A returned size of 1 only ever happens when n === 1 (a true bye); every
 * other case yields races of 2–4, so nobody ever "races" alone.
 */
export function distribute(n: number, size = RACE_SIZE): number[] {
  if (n <= 0) return []
  if (n <= size) return [n]
  const groups = Math.ceil(n / size)
  const base = Math.floor(n / groups)
  let remainder = n - base * groups
  const sizes: number[] = []
  for (let i = 0; i < groups; i++) {
    sizes.push(base + (remainder > 0 ? 1 : 0))
    if (remainder > 0) remainder--
  }
  return sizes
}

/** Chunk an ordered list of player ids into groups using the even distribution. */
export function chunkIntoGroups(playerIds: PlayerId[], size = RACE_SIZE): PlayerId[][] {
  const sizes = distribute(playerIds.length, size)
  const out: PlayerId[][] = []
  let cursor = 0
  for (const s of sizes) {
    out.push(playerIds.slice(cursor, cursor + s))
    cursor += s
  }
  return out
}

let idCounter = 0
/** Best-effort unique id for authored Round 1 groups. */
export function newId(prefix = 'g'): string {
  const rand =
    typeof crypto !== 'undefined' && 'randomUUID' in crypto
      ? crypto.randomUUID().slice(0, 8)
      : Math.random().toString(36).slice(2, 10)
  idCounter += 1
  return `${prefix}_${idCounter}_${rand}`
}

/** Build editable Round 1 groups from an ordered roster. */
export function buildRound1Groups(playerIds: PlayerId[], size = RACE_SIZE): GroupStruct[] {
  return chunkIntoGroups(playerIds, size).map((ids) => ({
    id: newId(),
    playerIds: ids,
    isBye: ids.length === 1,
  }))
}

/** Effective number that advance from a given round. */
export function effectiveAdvance(
  input: TournamentInput,
  round: number,
  numMatches: number,
): number {
  const override = input.roundAdvance[round]
  if (override != null && override > 0) return override
  // A round that is a single race is the Final: crown one champion.
  if (numMatches === 1) return 1
  return Math.max(1, input.defaultAdvance)
}

function roundLabelFor(index: number, totalRounds: number, numMatches: number): string {
  const isLast = index === totalRounds - 1
  if (isLast && numMatches === 1) return 'Final'
  if (index === totalRounds - 2 && numMatches <= 2) return 'Semifinals'
  if (index === totalRounds - 3 && numMatches <= 4) return 'Quarterfinals'
  return `Round ${index + 1}`
}

const SAFETY_MAX_ROUNDS = 64

/**
 * Resolve the full bracket from inputs.
 *
 * Round 0 comes straight from the authored groups. Each subsequent round is
 * generated from the advancers of the previous round once that round is
 * complete. Results are looked up by player-set signature, so re-entry /
 * editing / undo all "just work": changing an upstream result changes who
 * advances, which changes downstream player sets, which makes those downstream
 * races un-entered again automatically.
 */
export function resolve(input: TournamentInput): DerivedState {
  const byId = new Map<PlayerId, Player>(input.players.map((p) => [p.id, p]))
  const lookup = (ids: PlayerId[]): Player[] =>
    ids.map((id) => byId.get(id)).filter((p): p is Player => !!p)

  // First pass: build raw rounds (labels filled in afterward once we know the
  // total round count).
  type RawMatch = Omit<DerivedMatch, 'label'>
  const rawRounds: { advanceCount: number; matches: RawMatch[]; complete: boolean }[] = []

  let structs: GroupStruct[] = input.round0.map((g) => ({
    ...g,
    // Defend against stale references to removed players.
    playerIds: g.playerIds.filter((id) => byId.has(id)),
  }))

  let champion: Player | undefined
  let round = 0

  while (structs.length > 0 && round < SAFETY_MAX_ROUNDS) {
    const advanceCount = effectiveAdvance(input, round, structs.length)
    const matches: RawMatch[] = structs.map((g, idx) => {
      const players = lookup(g.playerIds)
      const isBye = g.isBye || players.length <= 1
      const sig = signature(g.playerIds)
      const entry = input.results[sig]
      const station: StationId | null =
        input.stations.A === sig ? 'A' : input.stations.B === sig ? 'B' : null

      let status: DerivedMatch['status'] = 'pending'
      let result: Player[] | undefined
      let advancers: Player[] = []
      let eliminated: Player[] = []

      if (isBye) {
        status = 'done'
        advancers = players
      } else if (entry) {
        // Only keep ordered ids still part of this race; append any missing.
        const ordered = lookup(entry.order).filter((p) => g.playerIds.includes(p.id))
        const present = new Set(ordered.map((p) => p.id))
        const tail = players.filter((p) => !present.has(p.id))
        const finished = [...ordered, ...tail]
        const n = Math.min(advanceCount, finished.length)
        result = finished
        advancers = finished.slice(0, n)
        eliminated = finished.slice(n)
        status = 'done'
      } else {
        status = station ? 'in_progress' : 'pending'
      }

      return {
        sig,
        round,
        indexInRound: idx,
        players,
        isBye,
        advanceCount,
        status,
        station,
        result,
        advancers,
        eliminated,
        track: entry?.track,
      }
    })

    const complete = matches.length > 0 && matches.every((m) => m.status === 'done')
    rawRounds.push({ advanceCount, matches, complete })

    if (!complete) break

    const advancers = matches.flatMap((m) => m.advancers)
    if (advancers.length <= 1) {
      champion = advancers[0]
      break
    }
    // Generate the next round from this round's advancers.
    structs = chunkIntoGroups(
      advancers.map((p) => p.id),
    ).map((ids) => ({ id: signature(ids), playerIds: ids, isBye: ids.length === 1 }))
    round += 1
  }

  const totalRounds = rawRounds.length
  const rounds: DerivedRound[] = rawRounds.map((r, i) => {
    const label = roundLabelFor(i, totalRounds, r.matches.length)
    const isFinalSingle = label === 'Final'
    return {
      index: i,
      label,
      advanceCount: r.advanceCount,
      complete: r.complete,
      matches: r.matches.map((m) => ({
        ...m,
        label: isFinalSingle
          ? 'Final'
          : `${label.startsWith('Round') ? 'R' + (i + 1) : label} · Race ${m.indexInRound + 1}`,
      })),
    }
  })

  // Live matches by station + on-deck queue.
  const allMatches = rounds.flatMap((r) => r.matches)
  const live: Record<StationId, DerivedMatch | null> = {
    A: allMatches.find((m) => m.station === 'A' && m.status === 'in_progress') ?? null,
    B: allMatches.find((m) => m.station === 'B' && m.status === 'in_progress') ?? null,
  }
  const onDeck = allMatches
    .filter((m) => m.status === 'pending')
    .sort((a, b) => a.round - b.round || a.indexInRound - b.indexInRound)

  const eliminatedTotal = allMatches.reduce((sum, m) => sum + m.eliminated.length, 0)
  const remaining = champion ? 1 : Math.max(0, input.players.length - eliminatedTotal)

  const phase: Phase = !input.locked ? 'setup' : champion ? 'complete' : 'running'

  return { phase, rounds, champion, live, onDeck, remaining }
}

/** Final standings for export: champion first, then by elimination round (latest first). */
export function standings(
  input: TournamentInput,
  derived: DerivedState,
): { rank: number; player: Player; status: string; roundReached: number; placed: string }[] {
  const eliminationRound = new Map<PlayerId, number>()
  for (const r of derived.rounds) {
    for (const m of r.matches) {
      for (const p of m.eliminated) eliminationRound.set(p.id, r.index)
    }
  }
  // Deepest round a player appeared in.
  const deepest = new Map<PlayerId, number>()
  for (const r of derived.rounds) {
    for (const m of r.matches) {
      for (const p of m.players) {
        deepest.set(p.id, Math.max(deepest.get(p.id) ?? 0, r.index))
      }
    }
  }

  const rows = input.players.map((player) => {
    const isChampion = derived.champion?.id === player.id
    const elim = eliminationRound.get(player.id)
    const reached = deepest.get(player.id) ?? 0
    const status = isChampion
      ? 'Champion'
      : elim != null
        ? `Eliminated R${elim + 1}`
        : derived.phase === 'setup'
          ? 'Seeded'
          : 'Active'
    return { player, status, roundReached: reached, isChampion, elim: elim ?? 999 }
  })

  rows.sort((a, b) => {
    if (a.isChampion !== b.isChampion) return a.isChampion ? -1 : 1
    if (b.roundReached !== a.roundReached) return b.roundReached - a.roundReached
    return b.elim - a.elim
  })

  return rows.map((row, i) => ({
    rank: i + 1,
    player: row.player,
    status: row.status,
    roundReached: row.roundReached + 1,
    placed: row.isChampion ? '🥇 Champion' : `${i + 1}`,
  }))
}
