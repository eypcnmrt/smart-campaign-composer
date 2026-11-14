import { describe, expect, it } from 'vitest'
import fixture from '../../../backend/internal/content/testdata/template_cases.json'
import { templateProblems, validateContent, validateField } from './template'

const VARIABLES = ['first_name', 'city']

interface Case {
  name: string
  field: 'sms' | 'email_subject' | 'email_body'
  text?: string
  repeat?: { unit: string; count: number }
  problems: { code: string; params: Record<string, number | string> }[]
}

const limits = fixture.limits as Record<Case['field'], number>
const cases: Case[] = fixture.cases as Case[]

describe('the cases shared with the Go backend', () => {
  it('uses the limits of the server', () => {
    expect(limits).toEqual({ sms: 160, email_subject: 80, email_body: 2000 })
  })

  it.each(cases.map((c) => [c.name, c] as const))('%s', (_name, c) => {
    const text = c.repeat ? c.repeat.unit.repeat(c.repeat.count) : (c.text ?? '')
    expect(validateField(text, limits[c.field], VARIABLES)).toEqual(c.problems)
  })
})

describe('what only the browser can see', () => {
  it('reports a lone surrogate as a damaged character, as the server does after JSON turns it into U+FFFD', () => {
    expect(validateField('a' + String.fromCharCode(0xd83d) + 'b', 160, VARIABLES)).toEqual([{ code: 'invalid_utf8', params: {} }])
  })

  it('does not take a proper surrogate pair for damage', () => {
    expect(validateField('😀', 160, VARIABLES)).toEqual([])
  })

  it('knows only the variables it is given', () => {
    expect(templateProblems('{{city}}', ['first_name'])).toEqual([{ code: 'unknown_variable', params: { name: 'city', char: 1 } }])
    expect(templateProblems('{{city}}', VARIABLES)).toEqual([])
  })

  it('finds nothing in a text without braces, however long', () => {
    expect(templateProblems('a'.repeat(10_000), VARIABLES)).toEqual([])
  })

  it('flags an unknown variable at once, before the closing braces are typed', () => {
    // While someone types {{first_na the text has an unclosed variable; once it is closed with a wrong name it has an unknown one.
    expect(templateProblems('Merhaba {{first_na', VARIABLES)).toEqual([{ code: 'unclosed_variable', params: { char: 9 } }])
    expect(templateProblems('Merhaba {{first_na}}', VARIABLES)).toEqual([{ code: 'unknown_variable', params: { name: 'first_na', char: 9 } }])
  })
})

describe('validateContent', () => {
  it('lists only the fields that have problems', () => {
    const got = validateContent(
      { sms: 'Merhaba {{first_name}}', email_subject: '', email_body: 'Selam {{x}}' },
      { sms: 160, email_subject: 80, email_body: 2000 },
      VARIABLES,
    )
    expect(Object.keys(got).sort()).toEqual(['email_body', 'email_subject'])
    expect(got.email_subject).toEqual([{ code: 'empty', params: {} }])
    expect(got.email_body).toEqual([{ code: 'unknown_variable', params: { name: 'x', char: 7 } }])
  })

  it('returns nothing for valid content', () => {
    expect(validateContent({ sms: 'a', email_subject: 'b', email_body: 'c' }, { sms: 160, email_subject: 80, email_body: 2000 }, VARIABLES)).toEqual({})
  })
})
