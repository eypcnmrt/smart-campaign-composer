import { effectScope, type EffectScope } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { error, installFakeApi, openSse, sseFrame, sseResponse } from '@/test/fakeApi'
import { contentReady } from '@/test/fixtures'
import { useContentStream } from './useContentStream'

let scope: EffectScope

beforeEach(() => {
  scope = effectScope()
})

afterEach(() => {
  scope.stop()
  vi.unstubAllGlobals()
})

function stream() {
  const s = scope.run(() => useContentStream())
  if (!s) throw new Error('no stream')
  return s
}

const attempt = (n: number) => sseFrame('attempt', { attempt: n })
const delta = (field: string, text: string) => sseFrame('delta', { field, text })
const raw = (text: string) => sseFrame('raw', { text })

describe('useContentStream', () => {
  it('collects the text of each field as it is written and ends with the saved campaign', async () => {
    installFakeApi({
      'GET /api/campaigns/7/content/stream': sseResponse([
        attempt(1),
        delta('sms', 'Merhaba '),
        delta('sms', 'dünya'),
        delta('email_subject', 'Konu'),
        sseFrame('done', contentReady),
      ]),
    })
    const s = stream()
    const done = await s.start(7)
    expect(done?.id).toBe(7)
    expect(s.fields.value).toEqual({ sms: 'Merhaba dünya', email_subject: 'Konu', email_body: '' })
    expect(s.status.value).toBe('idle')
    expect(s.attempt.value).toBe(1)
  })

  it('shows what has arrived while the stream is still open', async () => {
    const open = openSse()
    installFakeApi({ 'GET /api/campaigns/7/content/stream': open.response })
    const s = stream()
    const result = s.start(7)
    open.push(attempt(1))
    open.push(delta('sms', 'Selam'))
    await vi.waitFor(() => expect(s.fields.value.sms).toBe('Selam'))
    expect(s.status.value).toBe('streaming')
    open.push(sseFrame('done', contentReady))
    open.close()
    await result
    expect(s.status.value).toBe('idle')
  })

  it('clears the text when the model starts another attempt, since the rejected answer no longer counts', async () => {
    const open = openSse()
    installFakeApi({ 'GET /api/campaigns/7/content/stream': open.response })
    const s = stream()
    const result = s.start(7)
    open.push(attempt(1))
    open.push(delta('sms', 'Kötü metin'))
    await vi.waitFor(() => expect(s.fields.value.sms).toBe('Kötü metin'))
    open.push(attempt(2))
    await vi.waitFor(() => expect(s.attempt.value).toBe(2))
    expect(s.fields.value).toEqual({ sms: '', email_subject: '', email_body: '' })
    open.push(delta('sms', 'İyi metin'))
    await vi.waitFor(() => expect(s.fields.value.sms).toBe('İyi metin'))
    open.push(sseFrame('done', contentReady))
    open.close()
    await result
  })

  it('replaces the field view with the raw text on the first raw event, and adds the next ones to it', async () => {
    installFakeApi({
      'GET /api/campaigns/7/content/stream': sseResponse([
        attempt(1),
        delta('sms', 'Selam'),
        raw('{"sms":"Selam", oops'),
        raw(' more'),
        sseFrame('done', contentReady),
      ]),
    })
    const s = stream()
    await s.start(7)
    expect(s.raw.value).toBe('{"sms":"Selam", oops more')
  })

  it('starts the raw text again with a new attempt', async () => {
    installFakeApi({
      'GET /api/campaigns/7/content/stream': sseResponse([attempt(1), raw('first'), attempt(2), raw('second'), sseFrame('done', contentReady)]),
    })
    const s = stream()
    await s.start(7)
    expect(s.raw.value).toBe('second')
  })

  it('asks to replace the existing content only when told to', async () => {
    const api = installFakeApi({ 'GET /api/campaigns/7/content/stream': () => sseResponse([sseFrame('done', contentReady)]) })
    const s = stream()
    await s.start(7)
    await s.start(7, { regenerate: true })
    expect(api.calls.map((c) => c.path)).toEqual([
      '/api/campaigns/7/content/stream',
      '/api/campaigns/7/content/stream?regenerate=true',
    ])
  })

  it('reports an error event as the failure, with the server’s code', async () => {
    installFakeApi({
      'GET /api/campaigns/7/content/stream': sseResponse([
        attempt(1),
        sseFrame('error', { error: { code: 'llm_invalid_output', message: 'English', details: { stage: 'content', attempts: 3, problems: ['x'] } } }),
      ]),
    })
    const s = stream()
    expect(await s.start(7)).toBeNull()
    expect(s.status.value).toBe('error')
    expect((s.error.value as ApiError).code).toBe('llm_invalid_output')
  })

  it('reports a refusal before the stream (content exists) as the failure', async () => {
    installFakeApi({ 'GET /api/campaigns/7/content/stream': error(409, 'content_exists') })
    const s = stream()
    expect(await s.start(7)).toBeNull()
    expect((s.error.value as ApiError).code).toBe('content_exists')
  })

  it('reports a stream that ended without a result', async () => {
    installFakeApi({ 'GET /api/campaigns/7/content/stream': sseResponse([attempt(1), delta('sms', 'yarım')]) })
    const s = stream()
    await s.start(7)
    expect((s.error.value as ApiError).code).toBe('stream_incomplete')
  })

  it('clears an earlier error and text when started again', async () => {
    const api = installFakeApi({ 'GET /api/campaigns/7/content/stream': error(503, 'llm_busy') })
    const s = stream()
    await s.start(7)
    expect(s.status.value).toBe('error')
    api.set('GET /api/campaigns/7/content/stream', sseResponse([sseFrame('done', contentReady)]))
    await s.start(7)
    expect(s.status.value).toBe('idle')
    expect(s.error.value).toBeNull()
  })

  it('stops the request when asked, and ignores what comes after', async () => {
    const open = openSse()
    installFakeApi({ 'GET /api/campaigns/7/content/stream': open.response })
    const s = stream()
    const result = s.start(7)
    open.push(attempt(1))
    open.push(delta('sms', 'Selam'))
    await vi.waitFor(() => expect(s.fields.value.sms).toBe('Selam'))
    s.abort()
    expect(s.status.value).toBe('idle')
    expect(await result).toBeNull()
    expect(s.error.value).toBeNull()
  })

  it('aborts the request when its scope ends: leaving the page cancels the generation', async () => {
    let signal: AbortSignal | undefined
    const open = openSse()
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init?: RequestInit) => {
        signal = init?.signal ?? undefined
        return open.response
      }),
    )
    const s = stream()
    void s.start(7)
    await flushPromises()
    expect(signal?.aborted).toBe(false)
    scope.stop()
    expect(signal?.aborted).toBe(true)
  })

  it('does not mix two generations: a new start replaces the one in flight', async () => {
    const first = openSse()
    const second = sseResponse([attempt(1), delta('sms', 'ikinci'), sseFrame('done', contentReady)])
    let n = 0
    installFakeApi({ 'GET /api/campaigns/7/content/stream': () => (++n === 1 ? first.response : second) })
    const s = stream()
    const a = s.start(7)
    await flushPromises()
    const b = s.start(7)
    expect(await a).toBeNull()
    expect((await b)?.id).toBe(7)
    first.push(delta('sms', 'birinci'))
    await flushPromises()
    expect(s.fields.value.sms).toBe('ikinci')
  })
})
