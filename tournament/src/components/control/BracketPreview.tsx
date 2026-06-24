import type { DerivedMatch, DerivedState } from '../../types'

function MiniMatch({ m }: { m: DerivedMatch }) {
  const advancing = new Set(m.advancers.map((p) => p.id))
  const eliminated = new Set(m.eliminated.map((p) => p.id))
  const border =
    m.status === 'in_progress'
      ? 'now-racing border-speed'
      : m.status === 'done'
        ? 'border-curb'
        : 'border-dashed border-curb/70'
  return (
    <div className={`rounded-md border bg-black/30 p-1.5 ${border}`}>
      <div className="mb-0.5 flex items-center justify-between text-[0.6rem] uppercase tracking-wide text-white/40">
        <span>{m.label}</span>
        {m.station && <span className="font-bold text-speed">▶ {m.station}</span>}
        {m.isBye && <span className="text-nitro">bye</span>}
      </div>
      <ul className="space-y-0.5">
        {(m.result ?? m.players).map((p) => {
          const adv = advancing.has(p.id)
          const out = eliminated.has(p.id)
          return (
            <li
              key={p.id}
              className={`truncate text-xs leading-tight ${
                adv ? 'font-bold text-advance' : out ? 'text-white/30 line-through' : 'text-white/80'
              }`}
            >
              {adv && '▲ '}
              {p.name}
            </li>
          )
        })}
      </ul>
    </div>
  )
}

export function BracketPreview({ derived }: { derived: DerivedState }) {
  if (!derived.rounds.length) {
    return (
      <div className="card p-4">
        <h2 className="mb-1 font-display text-xl uppercase tracking-wide">Live bracket preview</h2>
        <p className="text-sm text-white/40">Appears once Round 1 is generated.</p>
      </div>
    )
  }
  return (
    <div className="card overflow-x-auto p-3">
      <h2 className="mb-2 font-display text-xl uppercase tracking-wide">
        Live bracket preview
        {derived.champion && <span className="ml-2 text-gold">· 🏆 {derived.champion.name}</span>}
      </h2>
      <div className="flex min-w-max gap-3">
        {derived.rounds.map((r) => (
          <div key={r.index} className="flex w-40 shrink-0 flex-col gap-2">
            <div className="sticky top-0 text-center text-[0.65rem] font-bold uppercase tracking-wide text-turbo">
              {r.label}
            </div>
            {r.matches.map((m) => (
              <MiniMatch key={m.sig} m={m} />
            ))}
          </div>
        ))}
      </div>
    </div>
  )
}
