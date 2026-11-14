import { describe, expect, it } from 'vitest'
import fixture from '../../../backend/internal/content/testdata/sms_cases.json'
import { analyzeSms, GSM7_BASIC_SIZE, GSM7_EXTENSION_SIZE, runeCount, SHORTEN_HINT_MAX, unitsOverSingle } from './sms'

interface Case {
  name: string
  text?: string
  parts?: [string, number][]
  runes: number
  encoding: string
  units: number
  segments: number
}

function textOf(c: Case): string {
  return (c.text ?? '') + (c.parts ?? []).map(([s, n]) => s.repeat(n)).join('')
}

// JSON arrays have no tuple type; the shape is checked by the Go side of the same file.
const cases = fixture.cases as unknown as Case[]

describe('the cases shared with the Go backend', () => {
  it('has the cases the plan names: a decomposed i and a ZWJ emoji', () => {
    const names = cases.map((c) => c.name).join('|')
    expect(names).toContain('decomposed i')
    expect(names).toContain('ZWJ family')
    expect(cases.length).toBeGreaterThanOrEqual(40)
  })

  it.each(cases.map((c) => [c.name, c] as const))('%s', (_name, c) => {
    const text = textOf(c)
    expect(runeCount(text)).toBe(c.runes)
    const info = analyzeSms(text)
    expect(info.encoding).toBe(c.encoding)
    expect(info.units).toBe(c.units)
    expect(info.segments).toBe(c.segments)
    expect(info.single).toBe(c.encoding === 'GSM-7' ? 160 : 70)
    expect(info.multi).toBe(c.encoding === 'GSM-7' ? 153 : 67)
  })
})

describe('the alphabets', () => {
  it('have the sizes of GSM 03.38: 127 characters and 10 extensions', () => {
    // A typo or a look-alike letter would flip texts to UCS-2 silently.
    expect(GSM7_BASIC_SIZE).toBe(127)
    expect(GSM7_EXTENSION_SIZE).toBe(10)
  })
})

describe('runeCount', () => {
  it('counts code points, not UTF-16 units and not letters as the eye sees them', () => {
    expect('😀'.length).toBe(2)
    expect(runeCount('😀')).toBe(1)
    expect(runeCount('ğüşiöç')).toBe(6)
    expect(runeCount('i' + String.fromCharCode(0x307))).toBe(2)
    expect(runeCount('')).toBe(0)
  })

  it('does not depend on the locale: Turkish capital I is one character either way', () => {
    expect(runeCount('İ')).toBe(1)
    expect(runeCount('ı')).toBe(1)
    expect(runeCount('i'.toLocaleUpperCase('tr-TR'))).toBe(1)
    expect('i'.toLocaleUpperCase('tr-TR')).toBe('İ')
  })
})

describe('unitsOverSingle', () => {
  it('says how many units a text that just spills into a second message must lose', () => {
    expect(unitsOverSingle(analyzeSms('a'.repeat(163)))).toBe(3)
    expect(unitsOverSingle(analyzeSms('ş'.repeat(75)))).toBe(5)
    expect(unitsOverSingle(analyzeSms('a'.repeat(160 + SHORTEN_HINT_MAX)))).toBe(SHORTEN_HINT_MAX)
  })

  it('has nothing to say for a text that fits, or one that is far over', () => {
    expect(unitsOverSingle(analyzeSms('a'.repeat(160)))).toBeNull()
    expect(unitsOverSingle(analyzeSms(''))).toBeNull()
    expect(unitsOverSingle(analyzeSms('a'.repeat(161 + SHORTEN_HINT_MAX)))).toBeNull()
  })
})
