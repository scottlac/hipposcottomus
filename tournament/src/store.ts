// ---------------------------------------------------------------------------
// State store: a tiny external store (for useSyncExternalStore) that owns the
// single TournamentInput, persists it to localStorage, mirrors it across
// windows via BroadcastChannel (with a storage-event fallback), and keeps an
// undo stack. All mutations go through the action functions exported here.
// ---------------------------------------------------------------------------

import { buildRound1Groups, newId, signature, STORE_VERSION } from './bracket'
import type { GroupStruct, Player, PlayerId, StationId, TournamentInput } from './types'

const STORAGE_KEY = 'ci360-bracket-board'
const CHANNEL_NAME = 'ci360-bracket-board'
const UNDO_LIMIT = 100

export const DEFAULT_TITLE = '2026 CI360 Mario Kart Tournament 🏁 Facilitators of Fun'

function emptyInput(): TournamentInput {
  return {
    version: STORE_VERSION,
    title: DEFAULT_TITLE,
    defaultAdvance: 2,
    players: [],
    round0: [],
    roundAdvance: {},
    results: {},
    stations: { A: null, B: null },
    locked: false,
  }
}

function clone(input: TournamentInput): TournamentInput {
  return JSON.parse(JSON.stringify(input))
}

function load(): TournamentInput {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return emptyInput()
    const parsed = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object') return emptyInput()
    // Merge over defaults so older / partial payloads stay valid.
    return { ...emptyInput(), ...parsed, stations: { A: null, B: null, ...parsed.stations } }
  } catch {
    return emptyInput()
  }
}

let state: TournamentInput = load()
let undoStack: TournamentInput[] = []
const listeners = new Set<() => void>()

const channel: BroadcastChannel | null =
  typeof BroadcastChannel !== 'undefined' ? new BroadcastChannel(CHANNEL_NAME) : null

function emit() {
  for (const l of listeners) l()
}

function persist() {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(state))
  } catch {
    /* storage full / unavailable — in-memory state still works */
  }
}

function broadcast() {
  channel?.postMessage({ type: 'state', state })
}

/** Replace state from a remote window (no re-broadcast, no undo push). */
function adoptRemote(next: TournamentInput) {
  if (JSON.stringify(next) === JSON.stringify(state)) return
  state = next
  emit()
}

if (channel) {
  channel.onmessage = (e) => {
    if (e.data?.type === 'state') adoptRemote(e.data.state as TournamentInput)
  }
}
if (typeof window !== 'undefined') {
  window.addEventListener('storage', (e) => {
    if (e.key === STORAGE_KEY && e.newValue) {
      try {
        adoptRemote(JSON.parse(e.newValue))
      } catch {
        /* ignore malformed */
      }
    }
  })
}

// --- external-store plumbing -----------------------------------------------

export function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getSnapshot(): TournamentInput {
  return state
}

export function canUndo(): boolean {
  return undoStack.length > 0
}

/**
 * Apply a mutation. `mutate` receives a deep clone it may freely edit (or it
 * can return a fresh object). Records the prior state for undo, then persists,
 * broadcasts and notifies.
 */
function commit(mutate: (draft: TournamentInput) => TournamentInput | void, record = true) {
  const draft = clone(state)
  const result = mutate(draft)
  const next = result ?? draft
  if (record) {
    undoStack.push(state)
    if (undoStack.length > UNDO_LIMIT) undoStack.shift()
  }
  state = next
  persist()
  broadcast()
  emit()
}

// --- helpers ----------------------------------------------------------------

function parseNames(blob: string): string[] {
  return blob
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean)
}

function makePlayer(name: string): Player {
  return { id: newId('p'), name }
}

// --- actions: roster & setup -----------------------------------------------

export const actions = {
  setTitle(title: string) {
    commit((d) => {
      d.title = title
    })
  },

  setDefaultAdvance(n: number) {
    commit((d) => {
      d.defaultAdvance = Math.max(1, Math.floor(n))
    })
  },

  setRoundAdvance(round: number, n: number | null) {
    commit((d) => {
      if (n == null) delete d.roundAdvance[round]
      else d.roundAdvance[round] = Math.max(1, Math.floor(n))
    })
  },

  addPlayersFromText(blob: string) {
    const names = parseNames(blob)
    if (!names.length) return
    commit((d) => {
      for (const name of names) d.players.push(makePlayer(name))
    })
  },

  addPlayer(name: string) {
    const trimmed = name.trim()
    if (!trimmed) return
    commit((d) => {
      d.players.push(makePlayer(trimmed))
    })
  },

  renamePlayer(id: PlayerId, name: string) {
    commit((d) => {
      const p = d.players.find((x) => x.id === id)
      if (p) p.name = name
    })
  },

  removePlayer(id: PlayerId) {
    commit((d) => {
      d.players = d.players.filter((p) => p.id !== id)
      // Drop from any authored group too.
      for (const g of d.round0) g.playerIds = g.playerIds.filter((pid) => pid !== id)
      d.round0 = d.round0.filter((g) => g.playerIds.length > 0)
    })
  },

  movePlayer(id: PlayerId, dir: -1 | 1) {
    commit((d) => {
      const i = d.players.findIndex((p) => p.id === id)
      const j = i + dir
      if (i < 0 || j < 0 || j >= d.players.length) return
      const tmp = d.players[i]
      d.players[i] = d.players[j]
      d.players[j] = tmp
    })
  },

  reorderPlayers(orderedIds: PlayerId[]) {
    commit((d) => {
      const byId = new Map(d.players.map((p) => [p.id, p]))
      const next = orderedIds.map((id) => byId.get(id)).filter(Boolean) as Player[]
      // Append any not mentioned (safety).
      for (const p of d.players) if (!orderedIds.includes(p.id)) next.push(p)
      d.players = next
    })
  },

  shuffleSeeds() {
    commit((d) => {
      const a = d.players
      for (let i = a.length - 1; i > 0; i--) {
        const j = Math.floor(Math.random() * (i + 1))
        ;[a[i], a[j]] = [a[j], a[i]]
      }
    })
  },

  // --- Round 1 builder ------------------------------------------------------

  generateRound1() {
    commit((d) => {
      d.round0 = buildRound1Groups(d.players.map((p) => p.id))
    })
  },

  clearRound1() {
    commit((d) => {
      d.round0 = []
    })
  },

  addGroup() {
    commit((d) => {
      d.round0.push({ id: newId(), playerIds: [], isBye: false })
    })
  },

  removeGroup(groupId: string) {
    commit((d) => {
      const g = d.round0.find((x) => x.id === groupId)
      if (!g) return
      if (g.playerIds.length > 0 && d.round0.length > 1) {
        // Spill players into the first other group rather than losing them.
        const target = d.round0.find((x) => x.id !== groupId)
        if (target) target.playerIds.push(...g.playerIds)
      }
      d.round0 = d.round0.filter((x) => x.id !== groupId)
    })
  },

  toggleBye(groupId: string) {
    commit((d) => {
      const g = d.round0.find((x) => x.id === groupId)
      if (g) g.isBye = !g.isBye
    })
  },

  /** Pull a player out of every Round 1 group (back into the unassigned tray). */
  unassignPlayer(playerId: PlayerId) {
    commit((d) => {
      for (const g of d.round0) g.playerIds = g.playerIds.filter((id) => id !== playerId)
    })
  },

  /** Move a player into a target group (drag/drop). Removes from any current group. */
  movePlayerToGroup(playerId: PlayerId, targetGroupId: string, index?: number) {
    commit((d) => {
      for (const g of d.round0) g.playerIds = g.playerIds.filter((id) => id !== playerId)
      const target = d.round0.find((x) => x.id === targetGroupId)
      if (!target) return
      if (index == null || index >= target.playerIds.length) target.playerIds.push(playerId)
      else target.playerIds.splice(Math.max(0, index), 0, playerId)
      // Keep bye flag honest.
      if (target.playerIds.length > 1) target.isBye = false
    })
  },

  lockTournament() {
    commit((d) => {
      d.round0 = d.round0.filter((g) => g.playerIds.length > 0)
      for (const g of d.round0) g.isBye = g.playerIds.length === 1
      d.locked = true
    })
  },

  unlockTournament() {
    commit((d) => {
      d.locked = false
    })
  },

  // --- running: stations & results -----------------------------------------

  assignToStation(sig: string, station: StationId) {
    commit((d) => {
      // Remove this match from the other station if present.
      const other: StationId = station === 'A' ? 'B' : 'A'
      if (d.stations[other] === sig) d.stations[other] = null
      d.stations[station] = sig
    })
  },

  clearStation(station: StationId) {
    commit((d) => {
      d.stations[station] = null
    })
  },

  submitResult(sig: string, order: PlayerId[], track?: string) {
    commit((d) => {
      d.results[sig] = { order: [...order], track: track?.trim() || undefined, at: Date.now() }
      // Free the station this race was running at.
      if (d.stations.A === sig) d.stations.A = null
      if (d.stations.B === sig) d.stations.B = null
    })
  },

  setTrack(sig: string, track: string) {
    commit((d) => {
      const entry = d.results[sig]
      if (entry) entry.track = track.trim() || undefined
    })
  },

  reopenMatch(sig: string) {
    commit((d) => {
      delete d.results[sig]
    })
  },

  // --- global ---------------------------------------------------------------

  undo() {
    if (!undoStack.length) return
    const prev = undoStack.pop()!
    state = prev
    persist()
    broadcast()
    emit()
  },

  reset() {
    commit(() => emptyInput())
  },

  importInput(next: TournamentInput) {
    commit(() => ({ ...emptyInput(), ...next }))
  },
}

/** Convenience used by the Round-1 builder for new empty groups. */
export function emptyGroup(): GroupStruct {
  return { id: newId(), playerIds: [], isBye: false }
}

export { signature }
