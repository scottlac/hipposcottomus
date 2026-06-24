import { useMemo, useState } from 'react'
import type { DerivedMatch, Player } from '../../types'

const CONFETTI_COLORS = ['#ffcb2d', '#00e5ff', '#ff2d55', '#21d07a', '#f5f7ff', '#d98a3d']

function Confetti() {
  const pieces = useMemo(
    () =>
      Array.from({ length: 90 }).map((_, i) => ({
        id: i,
        left: Math.random() * 100,
        color: CONFETTI_COLORS[i % CONFETTI_COLORS.length],
        delay: Math.random() * 3,
        duration: 2.6 + Math.random() * 2.4,
        rotate: Math.random() * 360,
      })),
    [],
  )
  return (
    <div className="pointer-events-none absolute inset-0 overflow-hidden">
      {pieces.map((p) => (
        <span
          key={p.id}
          className="confetti-piece"
          style={{
            left: `${p.left}%`,
            background: p.color,
            animationDelay: `${p.delay}s`,
            animationDuration: `${p.duration}s`,
            transform: `rotate(${p.rotate}deg)`,
          }}
        />
      ))}
    </div>
  )
}

export function ChampionOverlay({
  champion,
  finalMatch,
}: {
  champion: Player
  finalMatch?: DerivedMatch
}) {
  const [hidden, setHidden] = useState(false)

  if (hidden) {
    return (
      <button
        className="btn fixed right-4 top-4 z-50"
        onClick={() => setHidden(false)}
      >
        🏆 Show champion
      </button>
    )
  }

  const podium = finalMatch?.result?.slice(0, 3) ?? [champion]

  return (
    <div className="absolute inset-0 z-40 flex flex-col items-center justify-center bg-pit/95 text-center">
      <Confetti />
      <div className="checker-strip absolute inset-x-0 top-0 h-3" />
      <div className="checker-strip absolute inset-x-0 bottom-0 h-3" />

      <div className="relative z-10 px-6">
        <div className="mb-2 font-display text-3xl uppercase tracking-[0.4em] text-turbo">Champion</div>
        <div className="champion-glow font-display text-[clamp(3rem,12vw,9rem)] uppercase leading-none text-gold">
          {champion.name}
        </div>
        <div className="mt-2 text-5xl">🏆</div>

        {podium.length > 1 && (
          <div className="mt-8 flex items-end justify-center gap-4">
            {[1, 0, 2].map((slot) => {
              const p = podium[slot]
              if (!p) return null
              const heights = ['h-28', 'h-40', 'h-20']
              const tones = ['podium-2', 'podium-1', 'podium-3']
              const medals = ['🥈', '🥇', '🥉']
              return (
                <div key={p.id} className="flex w-40 flex-col items-center">
                  <div className="mb-1 text-3xl">{medals[slot]}</div>
                  <div className={`mb-1 truncate font-display text-2xl uppercase ${tones[slot]}`}>
                    {p.name}
                  </div>
                  <div
                    className={`w-full rounded-t-md border-x border-t border-curb bg-asphalt-2 ${heights[slot]}`}
                  />
                </div>
              )
            })}
          </div>
        )}

        <button className="btn mt-10" onClick={() => setHidden(true)}>
          View full bracket
        </button>
      </div>
    </div>
  )
}
