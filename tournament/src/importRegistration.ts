// ---------------------------------------------------------------------------
// Registration import: parse a Microsoft Forms export (.xlsx) or a .csv into
// roster entries, pulling the name column and the skill/experience column.
//
// Fully offline: .xlsx is just a ZIP of XML, so we unzip with fflate and read
// the cells with the browser's DOMParser — no heavy spreadsheet dependency.
// ---------------------------------------------------------------------------

import { strFromU8, unzipSync } from 'fflate'
import type { SkillLevel } from './types'

export interface ImportedEntry {
  name: string
  skill?: SkillLevel
}

export interface ImportResult {
  entries: ImportedEntry[]
  nameHeader: string | null
  skillHeader: string | null
  warnings: string[]
}

/** Map a free-text experience answer onto one of our three buckets. */
export function normalizeSkill(raw: string | undefined): SkillLevel | undefined {
  const s = (raw ?? '').trim().toLowerCase()
  if (!s) return undefined
  if (/(expert|advanc|\bpro\b|competit|experienc|hard ?core|veteran)/.test(s)) return 'Expert'
  if (/(begin|novice|new\b|never|casual|easy|rookie|first)/.test(s)) return 'Beginner'
  if (/(inter|medium|average|moderate|some)/.test(s)) return 'Intermediate'
  return undefined
}

function colToIndex(letters: string): number {
  let n = 0
  for (const ch of letters.toUpperCase()) n = n * 26 + (ch.charCodeAt(0) - 64)
  return n - 1
}

/** Locate the name + skill columns from the header row (with sensible fallbacks). */
function pickColumns(header: string[]): {
  nameCol: number
  skillCol: number
  nameHeader: string | null
  skillHeader: string | null
} {
  const lower = header.map((h) => (h ?? '').trim().toLowerCase())
  const findIdx = (pred: (h: string, i: number) => boolean) => lower.findIndex(pred)

  // Skill / experience question.
  let skillCol = findIdx((h) => /experience|skill|level|expertise/.test(h))
  if (skillCol < 0) skillCol = 8 // column I fallback

  // Name: Forms metadata has "Name" (auto from email) at col E and the actual
  // form field "Name:" at col G. Prefer the form field (ends with ':' / col >= G).
  let nameCol = findIdx((h) => h === 'name:')
  if (nameCol < 0) nameCol = findIdx((h, i) => h.includes('name') && h.endsWith(':') && i !== skillCol)
  if (nameCol < 0) nameCol = findIdx((h, i) => h.includes('name') && i >= 6 && i !== skillCol)
  if (nameCol < 0) nameCol = findIdx((h, i) => h.includes('name') && i !== skillCol)
  if (nameCol < 0) nameCol = 6 // column G fallback

  return {
    nameCol,
    skillCol,
    nameHeader: header[nameCol]?.trim() ?? null,
    skillHeader: header[skillCol]?.trim() ?? null,
  }
}

function gridToResult(grid: string[][]): ImportResult {
  const warnings: string[] = []
  if (grid.length === 0) return { entries: [], nameHeader: null, skillHeader: null, warnings: ['File had no rows.'] }

  const header = grid[0].map((h) => (h ?? '').trim())
  const { nameCol, skillCol, nameHeader, skillHeader } = pickColumns(header)

  const entries: ImportedEntry[] = []
  let unrecognizedSkills = 0
  for (const row of grid.slice(1)) {
    const name = (row[nameCol] ?? '').trim()
    if (!name) continue
    const rawSkill = (row[skillCol] ?? '').trim()
    const skill = normalizeSkill(rawSkill)
    if (rawSkill && !skill) unrecognizedSkills++
    entries.push({ name, skill })
  }

  if (!/name/i.test(nameHeader ?? '')) {
    warnings.push(`Used column ${String.fromCharCode(65 + nameCol)} for names — double-check the roster.`)
  }
  if (unrecognizedSkills > 0) {
    warnings.push(`${unrecognizedSkills} skill value(s) weren't recognized and were left blank.`)
  }
  return { entries, nameHeader, skillHeader, warnings }
}

// --- .xlsx ------------------------------------------------------------------

function textOf(el: Element | null | undefined): string {
  return el?.textContent ?? ''
}

function parseWorkbook(bytes: Uint8Array): ImportResult {
  const files = unzipSync(bytes)
  const get = (path: string) => (files[path] ? strFromU8(files[path]) : null)
  const parser = new DOMParser()

  // Shared strings table.
  const strings: string[] = []
  const ssXml = get('xl/sharedStrings.xml')
  if (ssXml) {
    const doc = parser.parseFromString(ssXml, 'application/xml')
    const sis = doc.getElementsByTagNameNS('*', 'si')
    for (let i = 0; i < sis.length; i++) {
      const ts = sis[i].getElementsByTagNameNS('*', 't')
      let s = ''
      for (let j = 0; j < ts.length; j++) s += textOf(ts[j])
      strings.push(s)
    }
  }

  // First worksheet (sheet1.xml by convention).
  const sheetPath =
    Object.keys(files)
      .filter((n) => /^xl\/worksheets\/sheet\d+\.xml$/.test(n))
      .sort()[0] ?? 'xl/worksheets/sheet1.xml'
  const sheetXml = get(sheetPath)
  if (!sheetXml) throw new Error('Could not find a worksheet in the file.')

  const doc = parser.parseFromString(sheetXml, 'application/xml')
  const rowEls = doc.getElementsByTagNameNS('*', 'row')
  const grid: string[][] = []
  for (let r = 0; r < rowEls.length; r++) {
    const cellEls = rowEls[r].getElementsByTagNameNS('*', 'c')
    const rowArr: string[] = []
    for (let c = 0; c < cellEls.length; c++) {
      const cell = cellEls[c]
      const ref = cell.getAttribute('r') ?? ''
      const colIdx = ref ? colToIndex(ref.replace(/[0-9]+/g, '')) : c
      const t = cell.getAttribute('t')
      let val = ''
      if (t === 's') {
        const v = cell.getElementsByTagNameNS('*', 'v')[0]
        const idx = Number(textOf(v))
        val = strings[idx] ?? ''
      } else if (t === 'inlineStr') {
        const ts = cell.getElementsByTagNameNS('*', 't')
        for (let j = 0; j < ts.length; j++) val += textOf(ts[j])
      } else {
        val = textOf(cell.getElementsByTagNameNS('*', 'v')[0])
      }
      if (colIdx >= 0) rowArr[colIdx] = val
    }
    grid.push(rowArr)
  }
  return gridToResult(grid)
}

// --- .csv -------------------------------------------------------------------

function parseCsv(text: string): string[][] {
  const clean = text.replace(/^﻿/, '')
  const delim = (clean.split('\n')[0].match(/;/g)?.length ?? 0) > (clean.split('\n')[0].match(/,/g)?.length ?? 0) ? ';' : ','
  const rows: string[][] = []
  let row: string[] = []
  let field = ''
  let inQuotes = false
  for (let i = 0; i < clean.length; i++) {
    const ch = clean[i]
    if (inQuotes) {
      if (ch === '"') {
        if (clean[i + 1] === '"') {
          field += '"'
          i++
        } else inQuotes = false
      } else field += ch
    } else if (ch === '"') {
      inQuotes = true
    } else if (ch === delim) {
      row.push(field)
      field = ''
    } else if (ch === '\n' || ch === '\r') {
      if (ch === '\r' && clean[i + 1] === '\n') i++
      row.push(field)
      rows.push(row)
      row = []
      field = ''
    } else field += ch
  }
  if (field.length > 0 || row.length > 0) {
    row.push(field)
    rows.push(row)
  }
  return rows.filter((r) => r.some((c) => c.trim() !== ''))
}

/** Parse an uploaded registration file (.xlsx or .csv) into roster entries. */
export async function importRegistrationFile(file: File): Promise<ImportResult> {
  const bytes = new Uint8Array(await file.arrayBuffer())
  const isZip = bytes[0] === 0x50 && bytes[1] === 0x4b // "PK" → xlsx/zip
  try {
    if (isZip) return parseWorkbook(bytes)
    return gridToResult(parseCsv(strFromU8(bytes)))
  } catch (err) {
    throw new Error(
      `Couldn't read "${file.name}". Make sure it's the Excel/CSV export from your sign-up form. (${
        err instanceof Error ? err.message : String(err)
      })`,
    )
  }
}
