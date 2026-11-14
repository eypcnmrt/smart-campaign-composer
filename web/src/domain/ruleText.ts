import { formatLira, formatNumber } from './format'

// The segment rule in Turkish, for the audience card. The wording follows the vocabulary of the backend
// (backend/internal/segment/vocab.go); backend/internal/segment/testdata/vocab.json lists it and ruleText.test.ts checks that
// every field, operator, event and language in that list has a wording here.
//
// `conditions` arrives as raw JSON (the server validated it, but a newer server may know more than this file): anything that is
// not understood is shown as the raw expression, marked `known: false`, never dropped and never guessed.

export interface RuleLine {
  text: string
  /** False when the condition is not in this file's vocabulary and `text` is its raw form. */
  known: boolean
  /** For an OR group: its conditions. `text` is then the heading ("Şunlardan en az biri"). */
  anyOf?: RuleLine[]
}

type Kind = 'enum' | 'int' | 'money'

export const FIELDS: Record<string, { label: string; kind: Kind }> = {
  city: { label: 'Şehir', kind: 'enum' },
  language: { label: 'Dil', kind: 'enum' },
  total_orders: { label: 'Sipariş sayısı', kind: 'int' },
  total_spent: { label: 'Toplam harcama', kind: 'money' },
}

export const LANGUAGES: Record<string, string> = { tr: 'Türkçe', en: 'İngilizce' }

/** The event, as a verb in the past tense and in its negative form ("sepete ekledi" / "sepete eklemedi"). */
export const EVENTS: Record<string, { did: string; didNot: string }> = {
  cart_add: { did: 'sepete ekledi', didNot: 'sepete eklemedi' },
  purchase: { did: 'satın aldı', didNot: 'satın almadı' },
}

export const EVENT_OPS: readonly string[] = ['performed', 'not_performed']

/** Operators of the lists (a city is one of several) and of the numbers. */
export const ENUM_OPS: readonly string[] = ['eq', 'neq', 'in', 'not_in']
export const NUMBER_OPS: readonly string[] = ['eq', 'neq', 'gt', 'gte', 'lt', 'lte']

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** "a", "a veya b", "a, b veya c". */
function joinOr(items: string[]): string {
  return join(items, 'veya')
}

/** "a", "a ve b", "a, b ve c". */
function joinAnd(items: string[]): string {
  return join(items, 've')
}

function join(items: string[], word: string): string {
  if (items.length <= 1) return items.join('')
  return `${items.slice(0, -1).join(', ')} ${word} ${items[items.length - 1]}`
}

function raw(value: unknown): string {
  const text = JSON.stringify(value) ?? String(value)
  return text.length > 80 ? `${text.slice(0, 80)}…` : text
}

function unknownLine(node: unknown): RuleLine {
  return { text: raw(node), known: false }
}

/** One value of a field, as text; null when it is not the kind of value the field takes. */
function valueText(field: string, kind: Kind, value: unknown): string | null {
  if (kind === 'enum') {
    if (typeof value !== 'string') return null
    return field === 'language' ? (LANGUAGES[value] ?? null) : value
  }
  if (typeof value !== 'number' || !Number.isFinite(value)) return null
  return kind === 'money' ? formatLira(value) : formatNumber(value)
}

function attributeLine(node: Record<string, unknown>): RuleLine {
  const { field, op, value } = node
  const spec = typeof field === 'string' ? FIELDS[field] : undefined
  if (spec === undefined || typeof op !== 'string') return unknownLine(node)

  if (spec.kind === 'enum') {
    if (!ENUM_OPS.includes(op)) return unknownLine(node)
    const list = op === 'in' || op === 'not_in'
    const values = list ? (Array.isArray(value) ? value : null) : [value]
    const texts = values?.map((v) => valueText(field as string, spec.kind, v))
    if (texts === undefined || texts.length === 0 || texts.includes(null)) return unknownLine(node)
    const names = texts as string[]
    const phrase = op === 'eq' ? names[0] : op === 'in' ? joinOr(names) : `${joinAnd(names)} dışında`
    return { text: `${spec.label}: ${phrase}`, known: true }
  }

  if (!NUMBER_OPS.includes(op)) return unknownLine(node)
  const amount = valueText(field as string, spec.kind, value)
  if (amount === null) return unknownLine(node)
  // "ve üzeri" / "ve altı" include the number, "üzerinde" / "altında" do not. No case suffix is attached to a number
  // (3'ten, 1.500 TL'den), because its vowel harmony depends on how the number is read.
  const phrase: Record<string, string> = {
    eq: `tam ${amount}`,
    neq: `${amount} dışında`,
    gt: `${amount} üzerinde`,
    gte: `${amount} ve üzeri`,
    lt: `${amount} altında`,
    lte: `${amount} ve altı`,
  }
  return { text: `${spec.label}: ${phrase[op]}`, known: true }
}

function eventLine(node: Record<string, unknown>): RuleLine {
  const { event, op, within_days: days } = node
  const spec = typeof event === 'string' ? EVENTS[event] : undefined
  if (spec === undefined || !EVENT_OPS.includes(op as string) || typeof days !== 'number' || !Number.isInteger(days) || days < 1) {
    return unknownLine(node)
  }
  return { text: `Son ${formatNumber(days)} günde ${op === 'performed' ? spec.did : spec.didNot}`, known: true }
}

function lineOf(node: unknown): RuleLine {
  if (!isRecord(node)) return unknownLine(node)
  switch (node.type) {
    case 'attribute':
      return attributeLine(node)
    case 'event':
      return eventLine(node)
    case 'any_of': {
      if (!Array.isArray(node.conditions) || node.conditions.length === 0) return unknownLine(node)
      const children = node.conditions.map(lineOf)
      return { text: 'Şunlardan en az biri', known: children.every((c) => c.known), anyOf: children }
    }
    default:
      return unknownLine(node)
  }
}

/**
 * The conditions of a rule, one line each. All lines apply together ("ve"); an OR group is one line with its own list.
 * Anything that is not a list of conditions comes back as a single unknown line, so the card never shows nothing for a rule.
 */
export function describeConditions(conditions: unknown): RuleLine[] {
  if (!Array.isArray(conditions)) return [unknownLine(conditions)]
  return conditions.map(lineOf)
}
