import { useCallback, useEffect, useState } from 'react'
import { actions, canUndo } from '../../store'
import { exportCSV, exportJSON } from '../../export'
import { useDerived, useInput } from '../../hooks/useStore'
import { CheckeredFlag } from '../common/ui'
import { AdvanceControls } from './AdvanceControls'
import { BracketPreview } from './BracketPreview'
import { RosterEditor } from './RosterEditor'
import { RoundOneBuilder } from './RoundOneBuilder'
import { Run } from './Run'

interface Toast {
  id: number
  msg: string
  tone: 'go' | 'info' | 'warn'
}

let toastId = 0

function openDisplayWindow() {
  const base = window.location.href.split('#')[0]
  window.open(`${base}#/display`, 'ci360_display', 'noopener')
}

export function ControlPanel() {
  const input = useInput()
  const derived = useDerived()
  const [toasts, setToasts] = useState<Toast[]>([])

  const notify = useCallback((msg: string, tone: Toast['tone'] = 'info') => {
    const id = ++toastId
    setToasts((t) => [...t, { id, msg, tone }])
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 2800)
  }, [])

  // Ctrl/Cmd+Z → undo
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'z') {
        const target = e.target as HTMLElement
        if (target?.tagName === 'INPUT' || target?.tagName === 'TEXTAREA') return
        e.preventDefault()
        actions.undo()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const assigned = new Set(input.round0.flatMap((g) => g.playerIds))
  const unassignedCount = input.players.filter((p) => !assigned.has(p.id)).length
  const canLock = input.round0.length > 0 && unassignedCount === 0 && input.players.length >= 2

  const roundProgress = (() => {
    const active = derived.rounds.find((r) => !r.complete)
    if (!active) return null
    const done = active.matches.filter((m) => m.status === 'done').length
    return { label: active.label, done, total: active.matches.length }
  })()

  return (
    <div className="mx-auto min-h-full max-w-[1400px] px-4 pb-24 pt-3">
      {/* Header */}
      <header className="card mb-4 overflow-hidden">
        <div className="checker-strip h-2" />
        <div className="flex flex-wrap items-center gap-3 p-3">
          <CheckeredFlag className="h-9 w-9 shrink-0" />
          <input
            className="min-w-[260px] flex-1 bg-transparent font-display text-2xl uppercase tracking-wide outline-none focus:text-turbo"
            value={input.title}
            onChange={(e) => actions.setTitle(e.target.value)}
            aria-label="Event title"
          />
          <div className="flex flex-wrap items-center gap-2">
            <button className="btn btn-primary" onClick={openDisplayWindow}>
              🖥 Open display
            </button>
            <button className="btn" onClick={() => actions.undo()} disabled={!canUndo()} title="Undo (Ctrl/Cmd+Z)">
              ↶ Undo
            </button>
            <button className="btn" onClick={() => exportJSON(input, derived)} disabled={!input.players.length}>
              JSON
            </button>
            <button className="btn" onClick={() => exportCSV(input, derived)} disabled={!input.players.length}>
              CSV
            </button>
            <button
              className="btn btn-danger"
              onClick={() => {
                if (confirm('Reset the entire tournament? This clears the roster and all results.')) {
                  actions.reset()
                  notify('Tournament reset', 'warn')
                }
              }}
            >
              Reset
            </button>
          </div>
        </div>

        {/* Status strip */}
        <div className="flex flex-wrap items-center gap-x-5 gap-y-1 border-t border-curb px-3 py-2 text-sm">
          <span className="chip bg-turbo/15 text-turbo">{input.locked ? 'LIVE' : 'SETUP'}</span>
          <span className="text-white/70">
            <b className="font-display">{input.players.length}</b> racers
          </span>
          <span className="text-white/70">
            <b className="font-display">{derived.remaining}</b> still in
          </span>
          {roundProgress && (
            <span className="text-white/70">
              {roundProgress.label}:{' '}
              <b className="font-display">
                {roundProgress.done}/{roundProgress.total}
              </b>{' '}
              races done
            </span>
          )}
          {derived.champion && <span className="font-display text-gold">🏆 {derived.champion.name}</span>}
          <span className="ml-auto text-white/40">
            Display window: open it, drag to the projector, press F11 / fullscreen.
          </span>
        </div>
      </header>

      {/* Champion banner */}
      {derived.champion && (
        <div className="card pop-in mb-4 border-gold bg-gradient-to-r from-gold/20 to-transparent p-4">
          <div className="flex items-center gap-3">
            <span className="text-4xl">🏆</span>
            <div>
              <div className="text-xs uppercase tracking-widest text-gold">Champion</div>
              <div className="champion-glow font-display text-3xl uppercase text-gold">
                {derived.champion.name}
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Body */}
      {!input.locked ? (
        <div className="space-y-4">
          <RosterEditor players={input.players} />
          <div className="grid gap-4 lg:grid-cols-[2fr_1fr]">
            <RoundOneBuilder input={input} />
            <div className="space-y-4">
              <AdvanceControls input={input} derived={derived} />
              <div className="card p-4">
                <h2 className="mb-2 font-display text-xl uppercase tracking-wide">Start tournament</h2>
                {canLock ? (
                  <p className="mb-3 text-sm text-white/60">
                    {input.round0.length} races ready. Locking freezes the roster &amp; Round 1; results
                    can still be edited live.
                  </p>
                ) : (
                  <p className="mb-3 text-sm text-nitro">
                    {input.players.length < 2
                      ? 'Add at least 2 racers.'
                      : input.round0.length === 0
                        ? 'Generate Round 1 groups first.'
                        : `${unassignedCount} racer${unassignedCount === 1 ? '' : 's'} still unassigned — drag into a race.`}
                  </p>
                )}
                <button
                  className="btn btn-go w-full text-lg"
                  disabled={!canLock}
                  onClick={() => {
                    actions.lockTournament()
                    notify('🏁 Tournament started — green flag!', 'go')
                  }}
                >
                  🏁 Lock &amp; start
                </button>
              </div>
            </div>
          </div>
          <BracketPreview derived={derived} />
        </div>
      ) : (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="text-sm text-white/50">
              Assign races to a station, then tap finishers 1st → last. Mistakes? Edit or re-open any
              completed race — the bracket re-resolves automatically.
            </p>
            <button
              className="btn btn-sm"
              onClick={() => {
                if (confirm('Re-open setup? The bracket stays saved; you can edit the roster / Round 1.')) {
                  actions.unlockTournament()
                }
              }}
            >
              ✎ Edit setup
            </button>
          </div>
          <div className="grid gap-4 lg:grid-cols-[2fr_1fr]">
            <Run derived={derived} notify={notify} />
            <AdvanceControls input={input} derived={derived} />
          </div>
          <BracketPreview derived={derived} />
        </div>
      )}

      {/* Toasts */}
      <div className="pointer-events-none fixed bottom-5 left-1/2 z-50 flex -translate-x-1/2 flex-col items-center gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            className={`toast rounded-lg border px-4 py-2 font-semibold shadow-lg ${
              t.tone === 'go'
                ? 'border-advance bg-advance/90 text-[#04250f]'
                : t.tone === 'warn'
                  ? 'border-nitro bg-nitro/90 text-[#3a2f00]'
                  : 'border-turbo bg-turbo/90 text-[#03222a]'
            }`}
          >
            {t.msg}
          </div>
        ))}
      </div>
    </div>
  )
}
