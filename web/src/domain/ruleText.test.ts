import { describe, expect, it } from 'vitest'
import vocabJson from '../../../backend/internal/segment/testdata/vocab.json'
import { describeConditions, ENUM_OPS, EVENT_OPS, EVENTS, FIELDS, LANGUAGES, NUMBER_OPS } from './ruleText'

// The JSON has no type for fields without a list of values; say it.
interface Vocab {
  fields: Record<string, { kind: string; ops: string[]; enum?: string[] }>
  events: string[]
  event_ops: string[]
  cities: string[]
  languages: string[]
  channels: string[]
}
const vocab: Vocab = vocabJson

const attr = (field: string, op: string, value: unknown) => ({ type: 'attribute', field, op, value })
const event = (name: string, op: string, days: number) => ({ type: 'event', event: name, op, within_days: days })
const one = (node: unknown) => describeConditions([node])[0]

describe('the vocabulary of the backend has a Turkish wording here', () => {
  // backend/internal/segment/testdata/vocab.json is checked against vocab.go by a Go test; this checks it against this file.
  it('every field, with its kind', () => {
    expect(Object.keys(FIELDS).sort()).toEqual(Object.keys(vocab.fields).sort())
    for (const [name, spec] of Object.entries(vocab.fields)) expect(FIELDS[name]?.kind).toBe(spec.kind)
  })

  it('every operator of every field', () => {
    for (const spec of Object.values(vocab.fields)) {
      const handled = spec.kind === 'enum' ? ENUM_OPS : NUMBER_OPS
      expect([...spec.ops].sort()).toEqual([...handled].sort())
    }
  })

  it('every event and every event operator', () => {
    expect(Object.keys(EVENTS).sort()).toEqual([...vocab.events].sort())
    expect([...EVENT_OPS].sort()).toEqual([...vocab.event_ops].sort())
  })

  it('every language', () => {
    expect(Object.keys(LANGUAGES).sort()).toEqual([...vocab.languages].sort())
  })

  it('every combination in the vocabulary is understood, none falls back to the raw form', () => {
    const lines: unknown[] = []
    for (const [field, spec] of Object.entries(vocab.fields)) {
      for (const op of spec.ops) {
        if (spec.kind === 'enum') {
          const values = spec.enum ?? []
          lines.push(attr(field, op, op === 'in' || op === 'not_in' ? values.slice(0, 2) : values[0]))
        } else {
          lines.push(attr(field, op, 100))
        }
      }
    }
    for (const e of vocab.events) for (const op of vocab.event_ops) lines.push(event(e, op, 7))
    for (const line of describeConditions(lines)) expect(line.known, line.text).toBe(true)
  })

  it('every city is shown as it is written, with its Turkish capital letters', () => {
    for (const city of vocab.cities) expect(one(attr('city', 'eq', city))?.text).toBe(`Şehir: ${city}`)
  })
})

describe('describeConditions', () => {
  it.each([
    [attr('city', 'eq', 'Ankara'), 'Şehir: Ankara'],
    [attr('city', 'in', ['İstanbul']), 'Şehir: İstanbul'],
    [attr('city', 'in', ['İstanbul', 'Kocaeli']), 'Şehir: İstanbul veya Kocaeli'],
    [attr('city', 'in', ['İstanbul', 'Kocaeli', 'Bursa']), 'Şehir: İstanbul, Kocaeli veya Bursa'],
    [attr('city', 'neq', 'Ankara'), 'Şehir: Ankara dışında'],
    [attr('city', 'not_in', ['Ankara', 'İzmir']), 'Şehir: Ankara ve İzmir dışında'],
    [attr('language', 'eq', 'tr'), 'Dil: Türkçe'],
    [attr('language', 'in', ['tr', 'en']), 'Dil: Türkçe veya İngilizce'],
    [attr('total_orders', 'gte', 3), 'Sipariş sayısı: 3 ve üzeri'],
    [attr('total_orders', 'gt', 3), 'Sipariş sayısı: 3 üzerinde'],
    [attr('total_orders', 'lt', 1), 'Sipariş sayısı: 1 altında'],
    [attr('total_orders', 'lte', 5), 'Sipariş sayısı: 5 ve altı'],
    [attr('total_orders', 'eq', 0), 'Sipariş sayısı: tam 0'],
    [attr('total_orders', 'neq', 2), 'Sipariş sayısı: 2 dışında'],
    [attr('total_spent', 'gte', 1500), 'Toplam harcama: 1.500 TL ve üzeri'],
    [attr('total_spent', 'lt', 99.5), 'Toplam harcama: 99,50 TL altında'],
    [attr('total_spent', 'gt', 1234567), 'Toplam harcama: 1.234.567 TL üzerinde'],
    [event('cart_add', 'performed', 30), 'Son 30 günde sepete ekledi'],
    [event('cart_add', 'not_performed', 7), 'Son 7 günde sepete eklemedi'],
    [event('purchase', 'performed', 365), 'Son 365 günde satın aldı'],
    [event('purchase', 'not_performed', 1), 'Son 1 günde satın almadı'],
  ])('%j → %s', (node, text) => {
    expect(one(node)).toEqual({ text, known: true })
  })

  it('shows an OR group as a heading with its own list', () => {
    const line = one({ type: 'any_of', conditions: [attr('city', 'eq', 'Ankara'), event('purchase', 'performed', 30)] })
    expect(line).toEqual({
      text: 'Şunlardan en az biri',
      known: true,
      anyOf: [
        { text: 'Şehir: Ankara', known: true },
        { text: 'Son 30 günde satın aldı', known: true },
      ],
    })
  })

  it('keeps the lines in the order of the rule', () => {
    const lines = describeConditions([event('cart_add', 'performed', 30), attr('city', 'eq', 'Ankara')])
    expect(lines.map((l) => l.text)).toEqual(['Son 30 günde sepete ekledi', 'Şehir: Ankara'])
  })

  describe('what it does not understand is shown raw and marked, never dropped', () => {
    it.each([
      ['an unknown field', attr('age', 'gt', 30), '{"type":"attribute","field":"age","op":"gt","value":30}'],
      ['an unknown operator', attr('city', 'like', 'Ank%'), '{"type":"attribute","field":"city","op":"like","value":"Ank%"}'],
      ['a number operator on a list field', attr('city', 'gt', 'Ankara'), undefined],
      ['a list operator without a list', attr('city', 'in', 'Ankara'), undefined],
      ['an empty list', attr('city', 'in', []), undefined],
      ['a text where a number belongs', attr('total_orders', 'gt', '3'), undefined],
      ['an unknown language', attr('language', 'eq', 'de'), undefined],
      ['an unknown event', event('refund', 'performed', 7), undefined],
      ['an unknown event operator', event('purchase', 'maybe', 7), undefined],
      ['a window of zero days', event('purchase', 'performed', 0), undefined],
      ['a window that is not whole', event('purchase', 'performed', 1.5), undefined],
      ['an unknown type', { type: 'not', conditions: [] }, '{"type":"not","conditions":[]}'],
      ['something that is not an object', 'x', '"x"'],
      ['an empty OR group', { type: 'any_of', conditions: [] }, undefined],
    ])('%s', (_name, node, text) => {
      const line = one(node)
      expect(line?.known).toBe(false)
      expect(line?.text).toBe(text ?? JSON.stringify(node))
    })

    it('marks an OR group unknown when one of its conditions is', () => {
      const line = one({ type: 'any_of', conditions: [attr('city', 'eq', 'Ankara'), attr('age', 'gt', 3)] })
      expect(line?.known).toBe(false)
      expect(line?.anyOf?.map((l) => l.known)).toEqual([true, false])
    })

    it('cuts a very long raw form', () => {
      const text = one(attr('age', 'eq', 'x'.repeat(500)))?.text ?? ''
      expect(text.length).toBeLessThanOrEqual(81)
      expect(text.endsWith('…')).toBe(true)
    })

    it('shows conditions that are not a list as one raw line', () => {
      expect(describeConditions(null)).toEqual([{ text: 'null', known: false }])
      expect(describeConditions({ a: 1 })).toEqual([{ text: '{"a":1}', known: false }])
    })

  })

  it('says a rule without a condition selects every user, in one known line', () => {
    expect(describeConditions([])).toEqual([{ text: 'Koşul yok: tüm kullanıcılar', known: true }])
  })
})
