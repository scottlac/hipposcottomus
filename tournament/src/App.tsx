import { useEffect, useState } from 'react'
import { ControlPanel } from './components/control/ControlPanel'
import { DisplayView } from './components/display/DisplayView'

/** Minimal hash router: #/control (default) and #/display. */
function useHashRoute(): string {
  const [hash, setHash] = useState(() => window.location.hash)
  useEffect(() => {
    const onChange = () => setHash(window.location.hash)
    window.addEventListener('hashchange', onChange)
    return () => window.removeEventListener('hashchange', onChange)
  }, [])
  return hash.replace(/^#\/?/, '').split('?')[0].toLowerCase() || 'control'
}

export function App() {
  const route = useHashRoute()
  if (route === 'display') return <DisplayView />
  return <ControlPanel />
}
