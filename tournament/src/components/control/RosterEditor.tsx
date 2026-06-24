import { useRef, useState } from 'react'
import { actions } from '../../store'
import { importRegistrationFile } from '../../importRegistration'
import type { Player, SkillLevel } from '../../types'
import { SKILLS, SkillDot, skillStyle } from '../common/ui'

function skillTally(players: { skill?: SkillLevel }[]): string {
  const c = { Expert: 0, Intermediate: 0, Beginner: 0, Unrated: 0 }
  for (const p of players) c[p.skill ?? 'Unrated']++
  const parts: string[] = []
  if (c.Expert) parts.push(`${c.Expert} Expert`)
  if (c.Intermediate) parts.push(`${c.Intermediate} Intermediate`)
  if (c.Beginner) parts.push(`${c.Beginner} Beginner`)
  if (c.Unrated) parts.push(`${c.Unrated} Unrated`)
  return parts.join(' · ')
}

export function RosterEditor({ players }: { players: Player[] }) {
  const [bulk, setBulk] = useState('')
  const [single, setSingle] = useState('')
  const [importMsg, setImportMsg] = useState<{ text: string; warn?: boolean } | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)

  const addBulk = () => {
    actions.addPlayersFromText(bulk)
    setBulk('')
  }
  const addSingle = () => {
    actions.addPlayer(single)
    setSingle('')
  }

  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    e.target.value = '' // allow re-importing the same filename
    if (!file) return
    setImportMsg(null)
    try {
      const res = await importRegistrationFile(file)
      if (!res.entries.length) {
        setImportMsg({ text: 'No entrants found in that file — check it has a name column.', warn: true })
        return
      }
      const mode =
        players.length === 0
          ? 'replace'
          : confirm(
                `Found ${res.entries.length} entrants in "${file.name}".\n\n` +
                  `OK  → replace the current ${players.length}-racer roster\n` +
                  `Cancel → add them to it`,
              )
            ? 'replace'
            : 'append'
      actions.importRoster(res.entries, mode)
      const tally = skillTally(res.entries)
      setImportMsg({
        text: `✓ Imported ${res.entries.length} racers${tally ? ` · ${tally}` : ''}.${
          res.warnings.length ? ' ' + res.warnings.join(' ') : ''
        }`,
        warn: res.warnings.length > 0,
      })
    } catch (err) {
      setImportMsg({ text: err instanceof Error ? err.message : String(err), warn: true })
    }
  }

  const tally = skillTally(players)
  const hasSkills = players.some((p) => p.skill)

  return (
    <div className="card p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <h2 className="font-display text-xl uppercase tracking-wide">
          Roster <span className="text-turbo">·</span> {players.length}{' '}
          <span className="text-sm text-white/50">racers</span>
          {hasSkills && <span className="ml-2 text-sm font-normal text-white/50">({tally})</span>}
        </h2>
        <div className="flex gap-2">
          <input
            ref={fileRef}
            type="file"
            accept=".xlsx,.csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,text/csv"
            className="hidden"
            onChange={onFile}
          />
          <button className="btn btn-go btn-sm" onClick={() => fileRef.current?.click()}>
            ⬆ Import sign-up sheet
          </button>
          <button className="btn btn-sm" onClick={() => actions.shuffleSeeds()} disabled={players.length < 2}>
            🎲 Shuffle
          </button>
        </div>
      </div>

      {importMsg && (
        <div
          className={`mb-3 rounded-lg border px-3 py-2 text-sm ${
            importMsg.warn ? 'border-nitro/60 bg-nitro/10 text-nitro' : 'border-advance/60 bg-advance/10 text-advance'
          }`}
        >
          {importMsg.text}
        </div>
      )}

      <div className="grid gap-3 md:grid-cols-2">
        {/* Bulk paste */}
        <div>
          <label className="mb-1 block text-xs uppercase tracking-wide text-white/50">
            Paste names (one per line) — or import the .xlsx/.csv above
          </label>
          <textarea
            className="input h-28 resize-y font-mono text-sm"
            placeholder={'Avery\nJordan\nSam\nRiley'}
            value={bulk}
            onChange={(e) => setBulk(e.target.value)}
          />
          <button className="btn btn-primary mt-2 w-full" onClick={addBulk} disabled={!bulk.trim()}>
            ＋ Add list
          </button>
        </div>

        {/* Quick add + walk-ups */}
        <div>
          <label className="mb-1 block text-xs uppercase tracking-wide text-white/50">
            Add one (walk-ups welcome)
          </label>
          <div className="flex gap-2">
            <input
              className="input"
              placeholder="Name…"
              value={single}
              onChange={(e) => setSingle(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && addSingle()}
            />
            <button className="btn btn-primary" onClick={addSingle} disabled={!single.trim()}>
              Add
            </button>
          </div>

          <div className="mt-3 max-h-52 overflow-auto rounded-lg border border-curb">
            {players.length === 0 ? (
              <p className="p-3 text-sm text-white/40">
                No racers yet. Import your sign-up sheet, paste names, or add them one by one.
              </p>
            ) : (
              <ul>
                {players.map((p, i) => (
                  <li
                    key={p.id}
                    className="flex items-center gap-2 border-b border-curb/50 px-2 py-1.5 last:border-0"
                  >
                    <span className="w-6 text-right font-display text-white/40 tnum">{i + 1}</span>
                    <SkillDot skill={p.skill} />
                    <input
                      className="min-w-0 flex-1 bg-transparent font-semibold outline-none focus:text-turbo"
                      value={p.name}
                      onChange={(e) => actions.renamePlayer(p.id, e.target.value)}
                    />
                    <select
                      className={`rounded border border-curb bg-asphalt px-1 py-0.5 text-xs ${skillStyle(p.skill).chip}`}
                      value={p.skill ?? ''}
                      onChange={(e) =>
                        actions.setPlayerSkill(p.id, (e.target.value || undefined) as SkillLevel | undefined)
                      }
                      title="Skill level"
                    >
                      <option value="">—</option>
                      {SKILLS.map((s) => (
                        <option key={s} value={s}>
                          {s}
                        </option>
                      ))}
                    </select>
                    <button
                      className="btn btn-ghost btn-sm"
                      onClick={() => actions.movePlayer(p.id, -1)}
                      disabled={i === 0}
                      title="Move up"
                    >
                      ↑
                    </button>
                    <button
                      className="btn btn-ghost btn-sm"
                      onClick={() => actions.movePlayer(p.id, 1)}
                      disabled={i === players.length - 1}
                      title="Move down"
                    >
                      ↓
                    </button>
                    <button
                      className="btn btn-ghost btn-sm text-speed"
                      onClick={() => actions.removePlayer(p.id)}
                      title="Remove"
                    >
                      ✕
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
