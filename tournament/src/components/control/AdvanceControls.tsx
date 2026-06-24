import { actions } from '../../store'
import type { DerivedState, TournamentInput } from '../../types'

function Stepper({
  value,
  min = 1,
  max = 9,
  onChange,
}: {
  value: number
  min?: number
  max?: number
  onChange: (n: number) => void
}) {
  return (
    <span className="inline-flex items-center overflow-hidden rounded-md border border-curb">
      <button
        className="px-2 py-0.5 hover:bg-white/10 disabled:opacity-30"
        onClick={() => onChange(value - 1)}
        disabled={value <= min}
      >
        −
      </button>
      <span className="w-7 text-center font-display tnum">{value}</span>
      <button
        className="px-2 py-0.5 hover:bg-white/10 disabled:opacity-30"
        onClick={() => onChange(value + 1)}
        disabled={value >= max}
      >
        ＋
      </button>
    </span>
  )
}

export function AdvanceControls({
  input,
  derived,
}: {
  input: TournamentInput
  derived: DerivedState
}) {
  return (
    <div className="card p-4">
      <h2 className="mb-3 font-display text-xl uppercase tracking-wide">Advancement</h2>

      <div className="mb-3 flex items-center justify-between gap-2">
        <div>
          <div className="font-semibold">Default per race</div>
          <div className="text-xs text-white/50">How many advance from each race unless overridden.</div>
        </div>
        <Stepper value={input.defaultAdvance} onChange={(n) => actions.setDefaultAdvance(n)} />
      </div>

      {derived.rounds.length > 0 && (
        <div className="space-y-1.5">
          <div className="text-xs uppercase tracking-wide text-white/50">Per-round override</div>
          {derived.rounds.map((r) => {
            const overridden = input.roundAdvance[r.index] != null
            const single = r.matches.length === 1
            return (
              <div key={r.index} className="flex items-center justify-between gap-2 rounded-md bg-black/20 px-2 py-1.5">
                <span className="font-semibold">
                  {r.label}
                  <span className="ml-2 text-xs text-white/40">
                    {r.matches.length} {r.matches.length === 1 ? 'race' : 'races'}
                    {single && ' · crowns champion'}
                  </span>
                </span>
                <div className="flex items-center gap-2">
                  <Stepper
                    value={r.advanceCount}
                    onChange={(n) => actions.setRoundAdvance(r.index, n)}
                  />
                  {overridden && (
                    <button
                      className="btn btn-ghost btn-sm"
                      onClick={() => actions.setRoundAdvance(r.index, null)}
                      title="Use default"
                    >
                      reset
                    </button>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
