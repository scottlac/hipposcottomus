import { useEffect, useRef, useState } from 'react'
import type { DerivedMatch, PlayerId } from '../../types'
import { ordinal, RankBadge } from '../common/ui'

interface Props {
  match: DerivedMatch
  onSubmit: (order: PlayerId[], track?: string) => void
  onCancel?: () => void
  submitLabel?: string
  autoFocus?: boolean
}

/**
 * Click-in-order (or press number keys) finish-order entry. Players are tapped
 * 1st → last; Backspace removes the last, Enter submits once everyone is placed.
 */
export function ResultEntry({ match, onSubmit, onCancel, submitLabel = 'Save result', autoFocus }: Props) {
  const [order, setOrder] = useState<PlayerId[]>(match.result?.map((p) => p.id) ?? [])
  const [track, setTrack] = useState(match.track ?? '')
  const rootRef = useRef<HTMLDivElement>(null)

  // Re-sync if the underlying match identity changes (e.g. switching stations).
  useEffect(() => {
    setOrder(match.result?.map((p) => p.id) ?? [])
    setTrack(match.track ?? '')
  }, [match.sig])

  useEffect(() => {
    if (autoFocus) rootRef.current?.focus()
  }, [autoFocus, match.sig])

  const placed = new Set(order)
  const remaining = match.players.filter((p) => !placed.has(p.id))
  const complete = order.length === match.players.length
  const advanceN = Math.min(match.advanceCount, match.players.length)
  const everyoneAdvances = advanceN >= match.players.length

  const place = (id: PlayerId) => setOrder((o) => (o.includes(id) ? o : [...o, id]))
  const removeLast = () => setOrder((o) => o.slice(0, -1))
  const removeAt = (id: PlayerId) => setOrder((o) => o.filter((x) => x !== id))
  const submit = () => {
    if (complete) onSubmit(order, track)
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key >= '1' && e.key <= '9') {
      const idx = Number(e.key) - 1
      if (remaining[idx]) {
        e.preventDefault()
        place(remaining[idx].id)
      }
    } else if (e.key === 'Backspace') {
      e.preventDefault()
      removeLast()
    } else if (e.key === 'Enter') {
      e.preventDefault()
      submit()
    } else if (e.key === 'Escape') {
      onCancel?.()
    }
  }

  return (
    <div
      ref={rootRef}
      tabIndex={0}
      onKeyDown={onKeyDown}
      className="rounded-lg outline-none focus:ring-2 focus:ring-turbo/70"
    >
      <div className="mb-2 flex items-center justify-between text-xs uppercase tracking-wide text-white/60">
        <span>
          Tap in finishing order{' '}
          <span className="text-white/40">· keys 1–{Math.min(9, match.players.length)} · ⌫ undo · ⏎ save</span>
        </span>
        <span className={everyoneAdvances ? 'text-advance' : 'text-turbo'}>
          {everyoneAdvances ? 'Everyone advances' : `Top ${advanceN} advance`}
        </span>
      </div>

      {/* Finishing order so far */}
      <ol className="mb-2 space-y-1">
        {order.map((id, i) => {
          const p = match.players.find((x) => x.id === id)
          if (!p) return null
          const advancing = i < advanceN
          return (
            <li
              key={id}
              className={`pop-in flex items-center gap-2 rounded-md border px-2 py-1.5 ${
                advancing ? 'border-advance/60 bg-advance/10' : 'border-curb bg-black/20'
              }`}
            >
              <RankBadge rank={i + 1} size="sm" />
              <span className="flex-1 truncate font-semibold">{p.name}</span>
              {advancing ? (
                <span className="text-xs font-bold text-advance">▲ ADV</span>
              ) : (
                <span className="text-xs font-bold text-white/40">OUT</span>
              )}
              <button className="btn btn-ghost btn-sm" onClick={() => removeAt(id)} title="Remove">
                ✕
              </button>
            </li>
          )
        })}
        {order.length === 0 && (
          <li className="rounded-md border border-dashed border-curb px-2 py-2 text-sm text-white/40">
            No placements yet — tap the {ordinal(1)}-place racer first.
          </li>
        )}
      </ol>

      {/* Remaining players to place */}
      {remaining.length > 0 && (
        <div className="mb-2 flex flex-wrap gap-2">
          {remaining.map((p, i) => (
            <button key={p.id} className="btn btn-sm" onClick={() => place(p.id)}>
              <span className="mr-1 inline-flex h-4 w-4 items-center justify-center rounded bg-black/40 text-[0.65rem] text-white/60">
                {i + 1}
              </span>
              {p.name}
            </button>
          ))}
        </div>
      )}

      <div className="flex items-center gap-2">
        <input
          className="input flex-1"
          placeholder="Track (optional)"
          value={track}
          onChange={(e) => setTrack(e.target.value)}
        />
        {order.length > 0 && (
          <button className="btn btn-sm" onClick={() => setOrder([])}>
            Clear
          </button>
        )}
        {onCancel && (
          <button className="btn btn-sm" onClick={onCancel}>
            Cancel
          </button>
        )}
        <button className="btn btn-go" disabled={!complete} onClick={submit}>
          {submitLabel}
        </button>
      </div>
    </div>
  )
}
