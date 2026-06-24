import { useState } from 'react'
import { actions } from '../../store'
import type { Player, TournamentInput } from '../../types'
import { SkillDot } from '../common/ui'

function PlayerChip({ player, onDragStart }: { player: Player; onDragStart: (id: string) => void }) {
  return (
    <span
      draggable
      onDragStart={(e) => {
        e.dataTransfer.setData('text/plain', player.id)
        e.dataTransfer.effectAllowed = 'move'
        onDragStart(player.id)
      }}
      className="chip cursor-grab select-none border border-curb bg-black/30 active:cursor-grabbing"
      title={player.skill ? `${player.name} · ${player.skill}` : 'Drag to another race'}
    >
      <SkillDot skill={player.skill} /> {player.name}
    </span>
  )
}

export function RoundOneBuilder({ input }: { input: TournamentInput }) {
  const [hover, setHover] = useState<string | null>(null)
  const byId = new Map(input.players.map((p) => [p.id, p]))
  const assigned = new Set(input.round0.flatMap((g) => g.playerIds))
  const unassigned = input.players.filter((p) => !assigned.has(p.id))

  const onDrop = (groupId: string | 'unassigned') => (e: React.DragEvent) => {
    e.preventDefault()
    const id = e.dataTransfer.getData('text/plain')
    setHover(null)
    if (!id) return
    if (groupId === 'unassigned') actions.unassignPlayer(id)
    else actions.movePlayerToGroup(id, groupId)
  }
  const allowDrop = (key: string) => (e: React.DragEvent) => {
    e.preventDefault()
    setHover(key)
  }

  const hasSkills = input.players.some((p) => p.skill)

  if (input.round0.length === 0) {
    return (
      <div className="card p-4">
        <h2 className="mb-2 font-display text-xl uppercase tracking-wide">Round 1 grid</h2>
        <p className="mb-3 text-sm text-white/60">
          Auto-suggest groups of 4. Uneven counts split into smaller races (3 or 2) so nobody
          races alone — then drag racers around however you like before locking.
          {hasSkills && ' Seed by skill spreads the strongest racers across different races so they meet in later rounds.'}
        </p>
        <div className="flex flex-wrap gap-2">
          {hasSkills && (
            <button
              className="btn btn-go"
              onClick={() => actions.seedRound1BySkill()}
              disabled={input.players.length < 2}
              title="Spread strong players across races"
            >
              🎯 Seed by skill
            </button>
          )}
          <button
            className={hasSkills ? 'btn' : 'btn btn-primary'}
            onClick={() => actions.generateRound1()}
            disabled={input.players.length < 2}
          >
            🏁 Generate {hasSkills ? 'in seed order' : 'Round 1 groups'}
          </button>
        </div>
        {input.players.length < 2 && (
          <p className="mt-2 text-sm text-white/40">Add at least 2 racers first.</p>
        )}
      </div>
    )
  }

  return (
    <div className="card p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <h2 className="font-display text-xl uppercase tracking-wide">
          Round 1 grid <span className="text-sm text-white/50">· {input.round0.length} races · drag to edit</span>
        </h2>
        <div className="flex gap-2">
          <button className="btn btn-sm" onClick={() => actions.addGroup()}>
            ＋ Add race
          </button>
          {hasSkills && (
            <button
              className="btn btn-go btn-sm"
              onClick={() => actions.seedRound1BySkill()}
              title="Spread strong players across races"
            >
              🎯 Seed by skill
            </button>
          )}
          <button className="btn btn-sm" onClick={() => actions.generateRound1()} title="Re-balance in seed order">
            ↻ Re-suggest
          </button>
        </div>
      </div>

      {unassigned.length > 0 && (
        <div
          onDragOver={allowDrop('unassigned')}
          onDragLeave={() => setHover(null)}
          onDrop={onDrop('unassigned')}
          className={`mb-3 rounded-lg border-2 border-dashed p-2 ${
            hover === 'unassigned' ? 'border-turbo bg-turbo/10' : 'border-nitro/60 bg-nitro/5'
          }`}
        >
          <div className="mb-1 text-xs font-bold uppercase tracking-wide text-nitro">
            Unassigned ({unassigned.length}) — drag into a race
          </div>
          <div className="flex flex-wrap gap-2">
            {unassigned.map((p) => (
              <PlayerChip key={p.id} player={p} onDragStart={() => {}} />
            ))}
          </div>
        </div>
      )}

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {input.round0.map((g, i) => {
          const players = g.playerIds.map((id) => byId.get(id)).filter(Boolean) as Player[]
          const isBye = g.isBye || players.length <= 1
          return (
            <div
              key={g.id}
              onDragOver={allowDrop(g.id)}
              onDragLeave={() => setHover(null)}
              onDrop={onDrop(g.id)}
              className={`rounded-lg border p-2 transition ${
                hover === g.id ? 'border-turbo bg-turbo/10' : 'border-curb bg-black/20'
              }`}
            >
              <div className="mb-2 flex items-center justify-between">
                <span className="font-display uppercase tracking-wide">
                  Race {i + 1}
                  <span className="ml-2 text-xs text-white/50">
                    {players.length} {players.length === 1 ? 'racer' : 'racers'}
                  </span>
                </span>
                <div className="flex items-center gap-1">
                  {isBye ? (
                    <span className="chip bg-nitro/20 text-nitro">BYE ▲</span>
                  ) : players.length < 4 ? (
                    <span className="chip bg-turbo/15 text-turbo">SHORT</span>
                  ) : null}
                  <button
                    className="btn btn-ghost btn-sm text-speed"
                    onClick={() => actions.removeGroup(g.id)}
                    title="Remove race"
                  >
                    ✕
                  </button>
                </div>
              </div>
              <div className="flex min-h-9 flex-wrap gap-2">
                {players.length === 0 ? (
                  <span className="text-sm text-white/30">drop racers here…</span>
                ) : (
                  players.map((p) => <PlayerChip key={p.id} player={p} onDragStart={() => {}} />)
                )}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
