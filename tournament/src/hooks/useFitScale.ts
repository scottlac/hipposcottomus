import { useLayoutEffect, useRef, useState } from 'react'

/**
 * Auto-scale a content block to fit inside its container without scrolling.
 * `transform: scale()` doesn't affect the element's layout (scrollWidth), so we
 * can safely measure the unscaled size and observe it for changes — no loops.
 */
export function useFitScale<T extends HTMLElement = HTMLDivElement>() {
  const containerRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<T>(null)
  const [scale, setScale] = useState(1)

  useLayoutEffect(() => {
    const recompute = () => {
      const container = containerRef.current
      const content = contentRef.current
      if (!container || !content) return
      const availW = container.clientWidth
      const availH = container.clientHeight
      const cw = content.scrollWidth
      const ch = content.scrollHeight
      if (!cw || !ch || !availW || !availH) return
      const next = Math.min(1, availW / cw, availH / ch)
      setScale(next > 0 ? next : 1)
    }

    recompute()
    const ro = new ResizeObserver(recompute)
    if (containerRef.current) ro.observe(containerRef.current)
    if (contentRef.current) ro.observe(contentRef.current)
    window.addEventListener('resize', recompute)
    return () => {
      ro.disconnect()
      window.removeEventListener('resize', recompute)
    }
  }, [])

  return { containerRef, contentRef, scale }
}
