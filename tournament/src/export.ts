import { standings } from './bracket'
import type { DerivedState, TournamentInput } from './types'

function triggerDownload(filename: string, text: string, mime: string) {
  const blob = new Blob([text], { type: mime })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1500)
}

function slugify(title: string): string {
  return (
    title
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/(^-|-$)/g, '')
      .slice(0, 48) || 'tournament'
  )
}

function csvCell(value: string | number): string {
  const s = String(value)
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
}

/** Full structured snapshot (state + standings + per-match results). */
export function exportJSON(input: TournamentInput, derived: DerivedState) {
  const data = {
    title: input.title,
    exportedAt: new Date().toISOString(),
    phase: derived.phase,
    champion: derived.champion?.name ?? null,
    defaultAdvance: input.defaultAdvance,
    playerCount: input.players.length,
    standings: standings(input, derived).map((s) => ({
      rank: s.rank,
      player: s.player.name,
      status: s.status,
      roundReached: s.roundReached,
    })),
    rounds: derived.rounds.map((r) => ({
      round: r.index + 1,
      label: r.label,
      advance: r.advanceCount,
      complete: r.complete,
      matches: r.matches.map((m) => ({
        label: m.label,
        status: m.status,
        isBye: m.isBye,
        station: m.station,
        track: m.track ?? null,
        players: m.players.map((p) => p.name),
        finishOrder: m.result?.map((p) => p.name) ?? null,
        advancers: m.advancers.map((p) => p.name),
        eliminated: m.eliminated.map((p) => p.name),
      })),
    })),
  }
  triggerDownload(`${slugify(input.title)}-results.json`, JSON.stringify(data, null, 2), 'application/json')
}

/** Standings as a spreadsheet-friendly CSV. */
export function exportCSV(input: TournamentInput, derived: DerivedState) {
  const header = ['Rank', 'Player', 'Status', 'RoundReached']
  const lines = [header.join(',')]
  for (const s of standings(input, derived)) {
    lines.push([s.rank, s.player.name, s.status, s.roundReached].map(csvCell).join(','))
  }
  triggerDownload(`${slugify(input.title)}-standings.csv`, lines.join('\n'), 'text/csv;charset=utf-8')
}
