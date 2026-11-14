import { describe, expect, it } from 'vitest'
import { formatDateTime, formatDuration, formatLira, formatNumber, formatRate, truncate } from './format'

describe('formatNumber and formatLira', () => {
  it('use the Turkish separators', () => {
    expect(formatNumber(2001)).toBe('2.001')
    expect(formatNumber(1234567)).toBe('1.234.567')
    expect(formatLira(1500)).toBe('1.500 TL')
    expect(formatLira(1234.5)).toBe('1.234,50 TL')
  })
})

describe('formatDateTime', () => {
  it('writes the month in Turkish', () => {
    expect(formatDateTime('2026-11-14T12:00:00Z')).toContain('Kasım 2026')
  })

  it('returns what it cannot read, so a bad value is visible instead of "Invalid Date"', () => {
    expect(formatDateTime('not a date')).toBe('not a date')
  })
})

describe('truncate', () => {
  it('leaves a short text alone', () => {
    expect(truncate('Merhaba', 20)).toBe('Merhaba')
    expect(truncate('a'.repeat(20), 20)).toBe('a'.repeat(20))
  })

  it('cuts a long text to the limit with an ellipsis that counts toward it', () => {
    const out = truncate('a'.repeat(30), 10)
    expect(Array.from(out)).toHaveLength(10)
    expect(out.endsWith('…')).toBe(true)
  })

  it('never cuts an emoji or a combined character in half', () => {
    const out = truncate('😀'.repeat(12), 5)
    expect(out).toBe('😀😀😀😀…')
    expect(out).not.toContain('�')
  })

  it('turns line breaks and runs of spaces into single spaces, for a one-line cell', () => {
    expect(truncate('Satır bir\n\nSatır   iki', 50)).toBe('Satır bir Satır iki')
  })

  it('does not leave a space before the ellipsis', () => {
    expect(truncate('abc def ghi', 5)).toBe('abc…')
  })
})

describe('formatRate and formatDuration', () => {
  it('show a rate with one decimal below 100 and whole above', () => {
    expect(formatRate(98.44)).toBe('98,4')
    expect(formatRate(100)).toBe('100')
    expect(formatRate(1234.6)).toBe('1.235')
    expect(formatRate(0)).toBe('0')
  })

  it('show a duration in seconds, minutes or hours', () => {
    expect(formatDuration(4.82)).toBe('4,8 sn')
    expect(formatDuration(12.4)).toBe('12 sn')
    expect(formatDuration(65)).toBe('1 dk 05 sn')
    expect(formatDuration(3700)).toBe('1 sa 01 dk')
  })
})
