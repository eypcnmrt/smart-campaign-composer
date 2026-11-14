import { describe, expect, it } from 'vitest'
import { ApiError } from './client'
import { describeError, describeProblem, fieldLabel } from './errors'

const ctx = { variables: ['first_name', 'city'] }

function apiError(code: string, extra: { status?: number; details?: unknown; retryAfterSeconds?: number | null; message?: string } = {}) {
  return new ApiError({ status: extra.status ?? 400, code, message: extra.message ?? `English ${code}`, details: extra.details, retryAfterSeconds: extra.retryAfterSeconds })
}

// The codes the backend can answer with (backend/internal/api/errors.go) plus the four made in client.ts.
const SERVER_CODES = [
  'network_error',
  'timeout',
  'http_error',
  'bad_response',
  'stream_incomplete',
  'invalid_json',
  'invalid_utf8',
  'body_too_large',
  'validation_failed',
  'acknowledgement_required',
  'no_recipients',
  'not_found',
  'invalid_state',
  'state_changed',
  'version_mismatch',
  'content_exists',
  'generation_in_progress',
  'llm_refused',
  'llm_rate_limited',
  'llm_invalid_output',
  'llm_output_truncated',
  'llm_error',
  'llm_unavailable',
  'llm_busy',
  'llm_timeout',
  'server_shutting_down',
  'client_too_slow',
  'client_closed',
  'approval_unconfirmed',
  'method_not_allowed',
  'not_ready',
  'internal',
]

describe('describeError', () => {
  it.each(SERVER_CODES)('has Turkish text for %s that does not contain the server sentence', (code) => {
    const d = describeError(apiError(code))
    expect(d.title).not.toBe('')
    expect(d.body).not.toBe('')
    expect(`${d.title} ${d.body}`).not.toContain('English')
    // A code that has its own text lists nothing technical, except llm_invalid_output with problems (none here).
    expect(d.technical).toEqual([])
  })

  it('says the exact sentence the brief asks for when the version moved on', () => {
    expect(describeError(apiError('version_mismatch', { status: 409, details: { current_version: 7 } })).title).toBe(
      'Kampanya değişti, lütfen tekrar gözden geçirin',
    )
  })

  it('shows the server message and code for a code it does not know', () => {
    const d = describeError(apiError('brand_new_code', { message: 'Something new' }))
    expect(d.title).toBe('İşlem tamamlanamadı')
    expect(d.technical).toEqual(['brand_new_code: Something new'])
    expect(d.retryable).toBe(false)
  })

  it('carries the wait of a rate limit so the screen can count it down', () => {
    const d = describeError(apiError('llm_rate_limited', { status: 429, retryAfterSeconds: 30 }))
    expect(d.retryAfterSeconds).toBe(30)
    expect(d.retryable).toBe(true)
  })

  it('carries the 5 second wait of a busy server', () => {
    expect(describeError(apiError('llm_busy', { status: 503, retryAfterSeconds: 5 })).retryAfterSeconds).toBe(5)
  })

  it('summarises llm_invalid_output in Turkish and keeps the English problems for the technical panel', () => {
    const d = describeError(
      apiError('llm_invalid_output', {
        status: 502,
        details: { stage: 'content', attempts: 3, problems: ['sms: is 200 characters long; the limit is 160', 'email_body: must not be empty'] },
      }),
    )
    expect(d.title).toBe('Model geçerli bir çıktı üretemedi')
    expect(d.body).toContain('3 denemede')
    expect(d.body).toContain('içeriği')
    expect(d.body).not.toContain('characters')
    expect(d.technical).toEqual(['sms: is 200 characters long; the limit is 160', 'email_body: must not be empty'])
  })

  it('names the segment when that is what failed', () => {
    const d = describeError(apiError('llm_invalid_output', { status: 502, details: { stage: 'segment', attempts: 2, problems: [] } }))
    expect(d.body).toContain('kitle kuralını')
    expect(d.body).toContain('2 denemede')
  })

  it('survives llm_invalid_output without details', () => {
    const d = describeError(apiError('llm_invalid_output', { status: 502 }))
    expect(d.body).toContain('birkaç denemede')
    expect(d.technical).toEqual([])
  })

  it('gives the request id of an internal error', () => {
    expect(describeError(apiError('internal', { status: 500, details: { request_id: 'abc123' } })).requestId).toBe('abc123')
    expect(describeError(apiError('internal', { status: 500 })).requestId).toBeNull()
  })

  it('gives the id of the draft that a failed creation left behind', () => {
    expect(describeError(apiError('llm_timeout', { status: 504, details: { campaign_id: 12 } })).campaignId).toBe(12)
    expect(describeError(apiError('llm_timeout', { status: 504 })).campaignId).toBeNull()
  })

  it('does not swallow an error that is not an ApiError', () => {
    const d = describeError(new TypeError('x is undefined'))
    expect(d.title).toBe('Beklenmeyen bir hata oluştu')
    expect(d.technical).toEqual(['x is undefined'])
  })

  it('translates the fields of a validation error and ignores malformed entries', () => {
    const d = describeError(
      apiError('validation_failed', {
        status: 422,
        details: [
          { path: 'sms', code: 'too_long', message: 'is 161 characters long; the limit is 160', params: { length: 161, limit: 160 } },
          { path: 'email_subject', code: 'unknown_variable', message: 'unknown', params: { name: 'last_name', char: 9 } },
          { nope: true },
          'text',
        ],
      }),
    )
    expect(d.fields).toEqual([
      { path: 'sms', label: 'SMS metni', text: '161 karakter; sınır 160.' },
      {
        path: 'email_subject',
        label: 'E-posta konusu',
        text: 'Bilinmeyen değişken {{last_name}} (9. karakter). Kullanılabilecekler: {{first_name}} ve {{city}}.',
      },
    ])
  })
})

describe('describeProblem', () => {
  const p = (code: string, params?: Record<string, unknown>, path = 'sms') => describeProblem({ path, code, message: 'English message', params }, ctx)

  it('writes lengths the Turkish way', () => {
    expect(p('too_long', { length: 2001, limit: 2000 }, 'email_body')).toBe('2.001 karakter; sınır 2.000.')
  })

  it.each([
    ['empty', undefined, 'Boş bırakılamaz.'],
    ['invalid_utf8', undefined, 'Metinde bozuk bir karakter (�) var; yeniden yazın.'],
    ['unclosed_variable', { char: 9 }, '{{ açılmış ama }} ile kapatılmamış (9. karakter).'],
    ['malformed_variable', { char: 3 }, 'Değişken hatalı yazılmış (3. karakter). Biçim: {{first_name}}.'],
    ['required', undefined, 'Zorunlu alan.'],
  ])('%s', (code, params, text) => {
    expect(p(code, params)).toBe(text)
  })

  it('tells an opening brace from a closing one', () => {
    expect(p('unbalanced_brace', { brace: '{', char: 9 })).toContain('Tek başına { kullanılamaz (9. karakter)')
    expect(p('unbalanced_brace', { brace: '}', char: 20 })).toContain('Tek başına } kullanılamaz (20. karakter)')
  })

  it('lists the allowed variables from the configuration, not from a fixed list', () => {
    const text = describeProblem({ path: 'sms', code: 'unknown_variable', message: '', params: { name: 'x', char: 1 } }, { variables: ['first_name'] })
    expect(text).toBe('Bilinmeyen değişken {{x}} (1. karakter). Kullanılabilecekler: {{first_name}}.')
  })

  it('says the channel list needs a channel, not that "a field is empty"', () => {
    expect(p('empty', undefined, 'channels')).toBe('En az bir kanal seçilmeli.')
    expect(p('unknown_channel', { value: 'fax' }, 'channels[1]')).toBe('Bilinmeyen kanal: fax.')
    expect(p('duplicate_channel', { value: 'sms' }, 'channels[1]')).toBe('Kanal iki kez seçilmiş: sms.')
  })

  it('describes an out-of-range number, with and without a range', () => {
    expect(p('invalid_param', { min: 1, max: 10 }, 'limit')).toBe('1 ile 10 arasında bir sayı olmalı.')
    expect(p('invalid_param', undefined, 'limit')).toBe('Geçersiz değer.')
  })

  it('shows the server message for a code it does not know, never a guess', () => {
    expect(p('something_new')).toBe('English message')
  })

  it('tolerates missing params', () => {
    expect(p('too_long')).toBe('? karakter; sınır ?.')
    expect(p('unknown_variable')).toContain('Bilinmeyen değişken {{}}')
  })
})

describe('fieldLabel', () => {
  it('names known fields, also inside a list, and falls back to the path', () => {
    expect(fieldLabel('email_body')).toBe('E-posta gövdesi')
    expect(fieldLabel('channels[1]')).toBe('Kanallar')
    expect(fieldLabel('whatever')).toBe('whatever')
  })
})
