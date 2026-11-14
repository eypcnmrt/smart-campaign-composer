import { describe, expect, it } from 'vitest'
import base from './base.css?raw'
import fonts from './fonts.css?raw'
import tokensCss from './tokens.css?raw'

// The design tokens are checked as written in tokens.css, so a colour that is edited later cannot silently drop below the
// contrast the design system promises (design-system/MASTER.md).

function parseTokens(css: string): Map<string, string> {
  const tokens = new Map<string, string>()
  for (const m of css.replace(/\/\*[\s\S]*?\*\//g, '').matchAll(/--([\w-]+):\s*([^;]+);/g)) {
    tokens.set(m[1] as string, (m[2] as string).trim())
  }
  return tokens
}

const tokens = parseTokens(tokensCss)

function value(name: string): string {
  let v = tokens.get(name)
  if (v === undefined) throw new Error(`token --${name} is not defined`)
  const ref = /^var\(--([\w-]+)\)$/.exec(v)
  if (ref) v = value(ref[1] as string)
  return v
}

function rgb(name: string): [number, number, number] {
  const hex = /^#([0-9a-f]{6})$/i.exec(value(name))
  if (!hex) throw new Error(`token --${name} is not a #rrggbb colour: ${value(name)}`)
  const n = parseInt(hex[1] as string, 16)
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255]
}

function luminance(name: string): number {
  const [r, g, b] = rgb(name).map((c) => {
    const v = c / 255
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4
  }) as [number, number, number]
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number]
  return (hi + 0.05) / (lo + 0.05)
}

function hue(name: string): number {
  const [r, g, b] = rgb(name).map((c) => c / 255) as [number, number, number]
  const max = Math.max(r, g, b)
  const d = max - Math.min(r, g, b)
  if (d === 0) return 0
  const h = max === r ? ((g - b) / d) % 6 : max === g ? (b - r) / d + 2 : (r - g) / d + 4
  return (h * 60 + 360) % 360
}

function hueDistance(a: string, b: string): number {
  const d = Math.abs(hue(a) - hue(b))
  return Math.min(d, 360 - d)
}

const GROUNDS = ['bg-page', 'bg-surface', 'bg-sunken']
const STATES = ['success', 'warning', 'danger'] as const

describe('text contrast (4.5:1)', () => {
  const pairs: [string, string][] = [
    ...['text', 'text-soft', 'action', 'danger', 'success', 'warning'].flatMap((fg) => GROUNDS.map((bg): [string, string] => [fg, bg])),
    ['on-action', 'action'],
    ['on-action', 'action-strong'],
    ['action', 'action-tint'],
    ['text', 'action-tint'],
    ['on-mark', 'mark'],
    ...STATES.map((s): [string, string] => [s, `${s}-bg`]),
  ]
  it.each(pairs)('%s on %s', (fg, bg) => {
    expect(contrast(fg, bg)).toBeGreaterThanOrEqual(4.5)
  })
})

describe('non-text contrast (3:1)', () => {
  it.each(['bg-page', 'bg-surface'])('control borders on %s', (bg) => {
    expect(contrast('border-control', bg)).toBeGreaterThanOrEqual(3)
  })
  it.each(GROUNDS)('focus ring on %s', (bg) => {
    expect(contrast('focus', bg)).toBeGreaterThanOrEqual(3)
  })
  it.each(STATES)('%s border on white', (s) => {
    expect(contrast(`${s}-border`, 'bg-surface')).toBeGreaterThanOrEqual(3)
  })
})

describe('state colours stay apart', () => {
  it('success is far from the action colour in hue, so a "sent" mark is never read as a button', () => {
    expect(hueDistance('success-border', 'action')).toBeGreaterThanOrEqual(45)
  })

  it('warnings are orange, not the highlighter yellow that means "not final text"', () => {
    expect(hueDistance('warning-border', 'mark')).toBeGreaterThanOrEqual(15)
    for (const s of ['warning', 'warning-bg', 'warning-border']) expect(value(s)).not.toBe(value('mark'))
  })

  it('errors are red, apart from the orange of warnings', () => {
    expect(hueDistance('danger-border', 'warning-border')).toBeGreaterThanOrEqual(20)
  })

  it('every state has its own text colour', () => {
    expect(new Set(STATES.map((s) => value(s))).size).toBe(STATES.length)
  })
})

describe('type', () => {
  it('shows raw code in the system monospace fonts, with no font package behind it', () => {
    const mono = value('font-mono')
    expect(mono).toMatch(/monospace$/)
    for (const family of fonts.matchAll(/font-family:\s*'([^']+)'/g)) {
      expect(mono).not.toContain(family[1] as string)
    }
    expect(base).toMatch(/\.mono\s*\{[^}]*font-family:\s*var\(--font-mono\)/)
  })

  it('numbers that change are set with tabular figures, in the body font', () => {
    expect(base).toMatch(/\.num\s*\{[^}]*font-variant-numeric:\s*tabular-nums/)
    // Schibsted Grotesk's tabular digits space the separator as wide as a digit; Source Sans 3 is tabular by default.
    expect(base).toMatch(/\.num\s*\{[^}]*font-family:\s*var\(--font-body\)/)
  })
})

describe('layout', () => {
  it('has a narrow column of 760 px and a wide one of 1120 px', () => {
    expect(value('column-narrow')).toBe('47.5rem')
    expect(value('column-wide')).toBe('70rem')
  })
})
