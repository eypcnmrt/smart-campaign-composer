import type { SmsInfo } from '@/api/types'

// A port of AnalyzeSMS and of the character count of the Go backend (backend/internal/content/sms.go). The editor shows these
// numbers while the marketer types, before anything is saved; the server computes them again for the saved text. Both are
// checked against the same cases (backend/internal/content/testdata/sms_cases.json), so they cannot drift apart.

/**
 * The number of characters the content limits count: code points, not UTF-16 units and not what the eye sees as one letter.
 * "😀" is 1 (its string length is 2); a decomposed "i" plus a combining dot is 2; a family emoji joined with ZWJ is 7.
 */
export function runeCount(text: string): number {
  let n = 0
  for (const _ of text) n++
  return n
}

// The GSM 03.38 default alphabet (one septet each; 127 characters, the escape code's place is left out) and its extension
// table (two septets each). Do not "tidy" these: a look-alike letter would flip texts to UCS-2 or back.
const GSM7_BASIC =
  '@£$¥èéùìòÇ\nØø\rÅå' +
  'Δ_ΦΓΛΩΠΨΣΘΞ' +
  'ÆæßÉ !"#¤%&\'()*+,-./0123456789:;<=>?' +
  '¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§' +
  '¿abcdefghijklmnopqrstuvwxyzäöñüà'
const GSM7_EXTENSION = '^{}\\[~]|€\f'

const BASIC = new Set(GSM7_BASIC)
const EXTENSION = new Set(GSM7_EXTENSION)

export const GSM7_BASIC_SIZE = BASIC.size
export const GSM7_EXTENSION_SIZE = EXTENSION.size

const LIMITS = {
  'GSM-7': { single: 160, multi: 153 },
  'UCS-2': { single: 70, multi: 67 },
} as const

/**
 * How a text would be sent: the encoding, the units it takes and the number of messages. A text stays GSM-7 only if every
 * character is in the GSM alphabet (of the Turkish letters Ü Ö Ç ü ö are, ç ğ ı İ ş Ş Ğ are not, and one of them switches the
 * whole text to UCS-2, 70 / 67 per message). Emoji take two UTF-16 units; extension characters (€ { } …) two septets. A
 * character is never split between messages. For a template this is an estimate, since the variables change the length.
 */
export function analyzeSms(text: string): SmsInfo {
  const chars = Array.from(text) // code points
  const gsm = chars.every((ch) => BASIC.has(ch) || EXTENSION.has(ch))
  const encoding = gsm ? 'GSM-7' : 'UCS-2'
  const { single, multi } = LIMITS[encoding]

  const size = (ch: string): number => {
    if (gsm && EXTENSION.has(ch)) return 2
    if (!gsm && (ch.codePointAt(0) ?? 0) > 0xffff) return 2 // a surrogate pair
    return 1
  }

  let units = 0
  for (const ch of chars) units += size(ch)
  if (units === 0) return { encoding, units, segments: 0, single, multi }
  if (units <= single) return { encoding, units, segments: 1, single, multi }

  let segments = 1
  let used = 0
  for (const ch of chars) {
    const s = size(ch)
    if (used + s > multi) {
      segments++
      used = s
    } else {
      used += s
    }
  }
  return { encoding, units, segments, single, multi }
}
