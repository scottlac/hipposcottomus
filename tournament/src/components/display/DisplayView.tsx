import { useDerived, useInput } from '../../hooks/useStore'
import { useFitScale } from '../../hooks/useFitScale'
import { CheckeredFlag } from '../common/ui'
import { ChampionOverlay } from './ChampionOverlay'
import { MatchCard } from './MatchCard'

function LiveLabel({ station, label }: { station: string; label: string | null }) {
  return (
    <div
      className={`flex items-center gap-2 rounded-lg border-2 px-3 py-1.5 ${
        label ? 'now-racing border-speed bg-speed/10' : 'border-curb opacity-50'
      }`}
    >
      <span className={`chip ${station === 'A' ? 'bg-turbo text-[#03222a]' : 'bg-nitro text-[#3a2f00]'}`}>
        STATION {station}
      </span>
      <span className="font-display text-lg uppercase">{label ?? 'Idle'}</span>
    </div>
  )
}

export function DisplayView() {
  const input = useInput()
  const derived = useDerived()
  const { containerRef, contentRef, scale } = useFitScale<HTMLDivElement>()

  const anyLive = derived.live.A || derived.live.B
  const lastRound = derived.rounds[derived.rounds.length - 1]
  const finalMatch = derived.champion ? lastRound?.matches[0] : undefined

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-pit text-white">
      {/* Header */}
      <header className="shrink-0">
        <div className="checker-strip h-3" />
        <div className="flex items-center gap-4 px-6 py-3">
          <CheckeredFlag className="h-12 w-12 shrink-0 flag-wave" />
          <h1 className="flex-1 truncate font-display text-4xl uppercase tracking-wide italic-speed">
            {input.title}
          </h1>
          {anyLive && (
            <div className="flex items-center gap-3">
              <LiveLabel station="A" label={derived.live.A?.label ?? null} />
              <LiveLabel station="B" label={derived.live.B?.label ?? null} />
            </div>
          )}
        </div>
        {!input.locked && (
          <div className="checker-fade px-6 py-1 text-center font-display text-lg uppercase tracking-[0.3em] text-nitro">
            ⚑ Warm-up grid — waiting for the green flag ⚑
          </div>
        )}
      </header>

      {/* Bracket (auto-scaled to fit) */}
      <main ref={containerRef} className="relative flex-1 overflow-hidden">
        {derived.rounds.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-4 text-center">
            <CheckeredFlag className="h-24 w-24 flag-wave" />
            <p className="font-display text-3xl uppercase tracking-wide text-white/70">
              Set up the bracket on the control panel
            </p>
            <p className="text-white/40">Add racers, generate Round 1, then lock &amp; start.</p>
          </div>
        ) : (
          <div className="absolute inset-0 flex items-center justify-center p-6">
            <div
              ref={contentRef}
              className="flex items-stretch gap-10"
              style={{ transform: `scale(${scale})`, transformOrigin: 'center' }}
            >
              {derived.rounds.map((r) => (
                <div key={r.index} className="flex shrink-0 flex-col">
                  <div className="mb-3 text-center font-display text-2xl uppercase tracking-widest text-turbo">
                    {r.label}
                    <span className="ml-2 text-base text-white/40">▲{r.advanceCount}</span>
                  </div>
                  <div className="flex flex-1 flex-col justify-center gap-5">
                    {r.matches.map((m) => (
                      <MatchCard key={m.sig} m={m} />
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {derived.champion && <ChampionOverlay champion={derived.champion} finalMatch={finalMatch} />}
      </main>

      {/* On deck strip */}
      <footer className="shrink-0 border-t-2 border-curb bg-asphalt/80 px-6 py-3">
        <div className="flex items-center gap-4">
          <span className="font-display text-xl uppercase tracking-widest text-nitro">On deck</span>
          {derived.onDeck.length === 0 ? (
            <span className="text-white/40">
              {derived.champion ? 'Race complete — see the podium above.' : 'No races queued.'}
            </span>
          ) : (
            <div className="flex flex-1 gap-3 overflow-hidden">
              {derived.onDeck.slice(0, 4).map((m) => (
                <div
                  key={m.sig}
                  className="flex min-w-0 flex-1 items-center gap-2 rounded-lg border border-curb bg-black/30 px-3 py-1.5"
                >
                  <span className="font-display uppercase text-white/70">{m.label}</span>
                  <span className="truncate text-sm text-white/50">
                    {m.players.map((p) => p.name).join(' · ')}
                  </span>
                </div>
              ))}
              {derived.onDeck.length > 4 && (
                <span className="self-center font-display text-white/50">+{derived.onDeck.length - 4}</span>
              )}
            </div>
          )}
        </div>
      </footer>
    </div>
  )
}
