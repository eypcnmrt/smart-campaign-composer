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
