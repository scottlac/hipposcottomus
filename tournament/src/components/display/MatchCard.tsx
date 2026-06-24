import type { DerivedMatch } from '../../types'
import { SkillDot } from '../common/ui'

const medal = ['🥇', '🥈', '🥉']

export function MatchCard({ m }: { m: DerivedMatch }) {
  const advancing = new Set(m.advancers.map((p) => p.id))
  const eliminated = new Set(m.eliminated.map((p) => p.id))
  const rows = m.result ?? m.players

  const frame =
    m.status === 'in_progress'
      ? 'now-racing border-speed bg-speed/10'
      : m.status === 'done'
        ? 'border-curb'
        : 'border-dashed border-curb/70'

  return (
    <div className={`card slide-in w-64 shrink-0 border-2 p-3 ${frame}`}>
      <div className="mb-2 flex items-center justify-between">
        <span className="font-display text-base uppercase tracking-wide text-white/70">{m.label}</span>
        {m.status === 'in_progress' && m.station ? (
          <span className="chip bg-speed text-white">NOW · {m.station}</span>
        ) : m.isBye ? (
          <span className="chip bg-nitro/25 text-nitro">BYE</span>
        ) : m.status === 'done' ? (
          <span className="chip bg-advance/20 text-advance">DONE</span>
        ) : (
          <span className="chip bg-white/10 text-white/50">QUEUED</span>
        )}
      </div>

      <ul className="space-y-1.5">
        {rows.map((p, i) => {
          const adv = advancing.has(p.id)
          const out = eliminated.has(p.id)
          const rank = m.result ? i + 1 : null
          return (
            <li
              key={p.id}
              className={`flex items-center gap-2 rounded-md px-2 py-1.5 text-lg font-bold leading-tight ${
                adv
                  ? 'bg-advance/20 text-advance'
                  : out
                    ? 'text-white/35 line-through'
                    : 'bg-black/30 text-white/90'
              }`}
            >
              {rank && rank <= 3 ? (
                <span className="text-xl">{medal[rank - 1]}</span>
              ) : rank ? (
                <span className="w-6 text-center font-display text-white/50 tnum">{rank}</span>
              ) : (
                <SkillDot skill={p.skill} />
              )}
              <span className="flex-1 truncate">{p.name}</span>
              {adv && <span className="font-display text-advance">▶</span>}
            </li>
          )
        })}
      </ul>

      {m.track && <div className="mt-2 text-right text-xs uppercase tracking-wide text-white/40">🏁 {m.track}</div>}
    </div>
  )
}
