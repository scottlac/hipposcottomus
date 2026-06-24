import { useState } from 'react'
import { actions } from '../../store'
import type { Player } from '../../types'

export function RosterEditor({ players }: { players: Player[] }) {
  const [bulk, setBulk] = useState('')
  const [single, setSingle] = useState('')

  const addBulk = () => {
    actions.addPlayersFromText(bulk)
    setBulk('')
  }
  const addSingle = () => {
    actions.addPlayer(single)
    setSingle('')
  }

  return (
    <div className="card p-4">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="font-display text-xl uppercase tracking-wide">
          Roster <span className="text-turbo">·</span> {players.length}{' '}
          <span className="text-sm text-white/50">racers</span>
        </h2>
        <div className="flex gap-2">
          <button className="btn btn-sm" onClick={() => actions.shuffleSeeds()} disabled={players.length < 2}>
            🎲 Shuffle seeds
          </button>
        </div>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        {/* Bulk paste */}
        <div>
          <label className="mb-1 block text-xs uppercase tracking-wide text-white/50">
            Paste names (one per line)
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

          <div className="mt-3 max-h-44 overflow-auto rounded-lg border border-curb">
            {players.length === 0 ? (
              <p className="p-3 text-sm text-white/40">No racers yet. Paste or add names to begin.</p>
            ) : (
              <ul>
                {players.map((p, i) => (
                  <li
                    key={p.id}
                    className="flex items-center gap-2 border-b border-curb/50 px-2 py-1.5 last:border-0"
                  >
                    <span className="w-6 text-right font-display text-white/40 tnum">{i + 1}</span>
                    <input
                      className="flex-1 bg-transparent font-semibold outline-none focus:text-turbo"
                      value={p.name}
                      onChange={(e) => actions.renamePlayer(p.id, e.target.value)}
                    />
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
