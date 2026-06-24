import { useMemo, useSyncExternalStore } from 'react'
import { getSnapshot, subscribe } from '../store'
import { resolve } from '../bracket'
import type { DerivedState, TournamentInput } from '../types'

/** The raw, persisted input object (re-renders on any change, in any window). */
export function useInput(): TournamentInput {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}

/** The fully-resolved bracket, recomputed whenever inputs change. */
export function useDerived(): DerivedState {
  const input = useInput()
  return useMemo(() => resolve(input), [input])
}
