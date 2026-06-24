import { useState } from 'react'
import { actions } from '../../store'
import type { DerivedMatch, DerivedState, StationId } from '../../types'
import { RankBadge, StationBadge } from '../common/ui'
import { ResultEntry } from './ResultEntry'

type Notify = (msg: string, tone?: 'go' | 'info' | 'warn') => void

function StationCard({
  station,
  match,
  onDeckNext,
  autoFocus,
  notify,
}: {
  station: StationId
  match: DerivedMatch | null
  onDeckNext: DerivedMatch | null
  autoFocus: boolean
  notify: Notify
}) {
  return (
    <div className={`card p-3 ${match ? 'now-racing' : ''}`}>
      <div className="mb-2 flex items-center justify-between">
        <StationBadge station={station} />
        {match ? (
          <span className="chip bg-speed/20 text-speed">NOW RACING</span>
        ) : (
          <span className="chip bg-white/10 text-white/50">IDLE</span>
        )}
      </div>

      {match ? (
        <>
          <div className="mb-2 flex items-center justify-between">
            <span className="font-display text-lg uppercase">{match.label}</span>
            <button
              className="btn btn-ghost btn-sm"
              onClick={() => actions.clearStation(station)}
              title="Send back to queue"
            >
              ⏏ Clear
            </button>
          </div>
          <ResultEntry
            match={match}
            autoFocus={autoFocus}
            onSubmit={(order, track) => {
              actions.submitResult(match.sig, order, track)
              notify(`✓ ${match.label} saved — ${match.players.length} racers scored`, 'go')
            }}
          />
        </>
      ) : (
        <div className="py-3 text-center">
          <p className="mb-3 text-sm text-white/50">No race loaded.</p>
          {onDeckNext ? (
            <button
              className="btn btn-primary"
              onClick={() => actions.assignToStation(onDeckNext.sig, station)}
            >
              ▶ Load {onDeckNext.label}
            </button>
          ) : (
            <p className="text-sm text-white/30">Queue is empty.</p>
          )}
        </div>
      )}
    </div>
  )
}

function FinishSummary({ match }: { match: DerivedMatch }) {
  const advancing = new Set(match.advancers.map((p) => p.id))
  return (
    <ol className="flex flex-wrap gap-1.5">
      {(match.result ?? match.players).map((p, i) => (
        <li
          key={p.id}
          className={`chip border ${
            advancing.has(p.id)
              ? 'border-advance/60 bg-advance/15 text-advance'
              : 'border-curb bg-black/30 text-white/50 line-through'
          }`}
        >
          <RankBadge rank={i + 1} size="sm" /> {p.name}
        </li>
      ))}
    </ol>
  )
}

export function Run({ derived, notify }: { derived: DerivedState; notify: Notify }) {
  const [editingSig, setEditingSig] = useState<string | null>(null)

  const onDeck = derived.onDeck
  const completed = derived.rounds
    .flatMap((r) => r.matches)
    .filter((m) => m.status === 'done' && !m.isBye)
    .sort((a, b) => b.round - a.round || a.indexInRound - b.indexInRound)

  const autoFocusStation: StationId = derived.live.A ? 'A' : 'B'

  return (
    <div className="space-y-4">
      {/* Stations */}
      <div className="grid gap-4 lg:grid-cols-2">
        <StationCard
          station="A"
          match={derived.live.A}
          onDeckNext={onDeck[0] ?? null}
          autoFocus={autoFocusStation === 'A'}
          notify={notify}
        />
        <StationCard
          station="B"
          match={derived.live.B}
          onDeckNext={(derived.live.A ? onDeck[0] : onDeck[1]) ?? onDeck[0] ?? null}
          autoFocus={autoFocusStation === 'B'}
          notify={notify}
        />
      </div>

      {/* On deck */}
      <div className="card p-4">
        <h2 className="mb-2 font-display text-xl uppercase tracking-wide">
          On deck <span className="text-sm text-white/50">· {onDeck.length} queued</span>
        </h2>
        {onDeck.length === 0 ? (
          <p className="text-sm text-white/40">
            {derived.champion
              ? 'Tournament complete.'
              : derived.rounds.some((r) => !r.complete)
                ? 'All current races are loaded or running. Finish them to unlock the next round.'
                : 'Waiting on the next round…'}
          </p>
        ) : (
          <ul className="space-y-2">
            {onDeck.map((m) => (
              <li key={m.sig} className="flex items-center gap-2 rounded-lg bg-black/20 px-2 py-2">
                <span className="font-display uppercase">{m.label}</span>
                <span className="flex-1 truncate text-sm text-white/60">
                  {m.players.map((p) => p.name).join(' · ')}
                </span>
                <button
                  className="btn btn-sm btn-primary"
                  onClick={() => actions.assignToStation(m.sig, 'A')}
                  disabled={!!derived.live.A}
                >
                  → A
                </button>
                <button
                  className="btn btn-sm"
                  onClick={() => actions.assignToStation(m.sig, 'B')}
                  disabled={!!derived.live.B}
                >
                  → B
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      {/* Completed (edit / re-open) */}
      {completed.length > 0 && (
        <div className="card p-4">
          <h2 className="mb-2 font-display text-xl uppercase tracking-wide">
            Completed <span className="text-sm text-white/50">· edit or re-open to fix mistakes</span>
          </h2>
          <ul className="space-y-2">
            {completed.map((m) => (
              <li key={m.sig} className="rounded-lg bg-black/20 p-2">
                <div className="mb-1.5 flex items-center justify-between gap-2">
                  <span className="font-display uppercase">
                    {m.label}
                    {m.track && <span className="ml-2 text-xs font-normal text-white/40">🏁 {m.track}</span>}
                  </span>
                  <div className="flex gap-1">
                    <button
                      className="btn btn-ghost btn-sm"
                      onClick={() => setEditingSig(editingSig === m.sig ? null : m.sig)}
                    >
                      {editingSig === m.sig ? 'Close' : '✎ Edit'}
                    </button>
                    <button
                      className="btn btn-ghost btn-sm text-nitro"
                      onClick={() => {
                        actions.reopenMatch(m.sig)
                        setEditingSig(null)
                        notify(`↩ ${m.label} re-opened — downstream races re-resolved`, 'warn')
                      }}
                    >
                      ↺ Re-open
                    </button>
                  </div>
                </div>
                {editingSig === m.sig ? (
                  <ResultEntry
                    match={m}
                    submitLabel="Update result"
                    autoFocus
                    onSubmit={(order, track) => {
                      actions.submitResult(m.sig, order, track)
                      setEditingSig(null)
                      notify(`✓ ${m.label} updated`, 'go')
                    }}
                    onCancel={() => setEditingSig(null)}
                  />
                ) : (
                  <FinishSummary match={m} />
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
