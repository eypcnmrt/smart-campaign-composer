// Turkish number and date formats. They are used for every figure and date the screens show, so "2.001" and "3 Kas 2026"
// look the same everywhere.

const LOCALE = 'tr-TR'

const numberFormat = new Intl.NumberFormat(LOCALE)

/** 2001 → "2.001". */
export function formatNumber(n: number): string {
  return numberFormat.format(n)
}

/** An amount in Turkish lira: 1500 → "1.500 TL", 1234.5 → "1.234,50 TL". Whole amounts show no decimals. */
export function formatLira(amount: number): string {
  const whole = Number.isInteger(amount)
  return `${new Intl.NumberFormat(LOCALE, { minimumFractionDigits: whole ? 0 : 2, maximumFractionDigits: 2 }).format(amount)} TL`
}

const dateTime = new Intl.DateTimeFormat(LOCALE, { day: 'numeric', month: 'long', year: 'numeric', hour: '2-digit', minute: '2-digit' })

/** An ISO timestamp as "14 Kasım 2026 15:40"; an unreadable value is returned as it is. */
export function formatDateTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : dateTime.format(d)
}

/** At most `max` characters (code points, so an emoji is never cut in half), with an ellipsis where text was left out. Line breaks become spaces. */
export function truncate(text: string, max: number): string {
  const flat = text.replace(/\s+/g, ' ').trim()
  const chars = Array.from(flat)
  return chars.length <= max ? flat : `${chars.slice(0, max - 1).join('').trimEnd()}…`
}

const oneDecimal = new Intl.NumberFormat(LOCALE, { maximumFractionDigits: 1 })

/** A rate in messages per second: 98.4 → "98,4", 100 → "100"; one decimal below 100, whole above. */
export function formatRate(perSecond: number): string {
  return perSecond >= 100 ? formatNumber(Math.round(perSecond)) : oneDecimal.format(Math.round(perSecond * 10) / 10)
}

/** A duration given in seconds: 4.8 → "4,8 sn", 65 → "1 dk 05 sn", 3700 → "1 sa 01 dk". */
export function formatDuration(seconds: number): string {
  if (seconds < 10) return `${oneDecimal.format(Math.round(seconds * 10) / 10)} sn`
  const whole = Math.round(seconds)
  if (whole < 60) return `${whole} sn`
  const two = (n: number) => String(n).padStart(2, '0')
  if (whole < 3600) return `${Math.floor(whole / 60)} dk ${two(whole % 60)} sn`
  return `${Math.floor(whole / 3600)} sa ${two(Math.floor((whole % 3600) / 60))} dk`
}
