import type { ReactNode } from 'react'
import type { SkillLevel } from '../../types'

export const SKILLS: SkillLevel[] = ['Beginner', 'Intermediate', 'Expert']

/** Color treatment per skill bucket (Expert = hot, Intermediate = cyan, Beginner = green). */
export function skillStyle(skill?: SkillLevel): { label: string; dot: string; chip: string } {
  switch (skill) {
    case 'Expert':
      return { label: 'Expert', dot: 'bg-speed', chip: 'bg-speed/20 text-speed' }
    case 'Intermediate':
      return { label: 'Intermediate', dot: 'bg-turbo', chip: 'bg-turbo/20 text-turbo' }
    case 'Beginner':
      return { label: 'Beginner', dot: 'bg-advance', chip: 'bg-advance/20 text-advance' }
    default:
      return { label: 'Unrated', dot: 'bg-white/25', chip: 'bg-white/10 text-white/50' }
  }
}

export function SkillDot({ skill, className = '' }: { skill?: SkillLevel; className?: string }) {
  const s = skillStyle(skill)
  return (
    <span
      className={`inline-block h-2.5 w-2.5 shrink-0 rounded-full ${s.dot} ${className}`}
      title={s.label}
    />
  )
}

export function ordinal(n: number): string {
  const s = ['th', 'st', 'nd', 'rd']
  const v = n % 100
  return n + (s[(v - 20) % 10] || s[v] || s[0])
}

/** Small checkered-flag glyph (original CSS/SVG, no third-party artwork). */
export function CheckeredFlag({ className = '' }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden="true">
      <rect width="24" height="24" rx="3" fill="#0a0e1f" />
      {Array.from({ length: 6 }).flatMap((_, r) =>
        Array.from({ length: 6 }).map((__, c) =>
          (r + c) % 2 === 0 ? (
            <rect key={`${r}-${c}`} x={c * 4} y={r * 4} width="4" height="4" fill="#f5f7ff" />
          ) : null,
        ),
      )}
    </svg>
  )
}

const podiumClass: Record<number, string> = {
  1: 'bg-gold text-[#3a2a00] border-[#caa017]',
  2: 'bg-silver text-[#23282e] border-[#aeb6bf]',
  3: 'bg-bronze text-[#3a2410] border-[#b06f2c]',
}

/** A rank badge (1st = gold, 2nd = silver, 3rd = bronze, otherwise neutral). */
export function RankBadge({ rank, size = 'md' }: { rank: number; size?: 'sm' | 'md' | 'lg' }) {
  const dims =
    size === 'lg'
      ? 'w-9 h-9 text-lg'
      : size === 'sm'
        ? 'w-5 h-5 text-[0.7rem]'
        : 'w-7 h-7 text-sm'
  const tone = podiumClass[rank] ?? 'bg-[#2a3358] text-white border-[#46507f]'
  return (
    <span
      className={`inline-flex items-center justify-center rounded-full border font-display font-extrabold tnum ${dims} ${tone}`}
    >
      {rank}
    </span>
  )
}

export function StationBadge({ station }: { station: 'A' | 'B' }) {
  const tone = station === 'A' ? 'bg-turbo text-[#03222a]' : 'bg-nitro text-[#3a2f00]'
  return (
    <span className={`chip ${tone}`}>
      <span className="font-display">STATION {station}</span>
    </span>
  )
}

export function Pill({
  children,
  className = '',
}: {
  children: ReactNode
  className?: string
}) {
  return <span className={`chip ${className}`}>{children}</span>
}
