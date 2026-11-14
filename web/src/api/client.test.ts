import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, getConfig, parseConfig, request } from './client'

const config = {
  llm_mode: 'mock',
  model: 'mock',
  limits: { sms: 160, email_subject: 80, email_body: 2000, brief: 2000 },
  variables: ['first_name', 'city'],
  channels: ['sms', 'email'],
}

function json(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' }, ...init })
}

function stubFetch(impl: (input: string, init?: RequestInit) => Promise<Response>) {
  const fn = vi.fn(impl)
  vi.stubGlobal('fetch', fn)
  return fn
}

afterEach(() => {
  vi.unstubAllGlobals()
})

async function failure(promise: Promise<unknown>): Promise<ApiError> {
  const err = await promise.then(
    () => null,
    (e: unknown) => e,
  )
  expect(err).toBeInstanceOf(ApiError)
  return err as ApiError
}

describe('request', () => {
  it('sends JSON and returns the parsed body', async () => {
    const fetchFn = stubFetch(async () => json({ ok: true }))
    const out = await request<{ ok: boolean }>('/api/x', { method: 'POST', body: { a: 1 } })
    expect(out).toEqual({ ok: true })
    const [path, init] = fetchFn.mock.calls[0] ?? []
    expect(path).toBe('/api/x')
    expect(init?.method).toBe('POST')
    expect(init?.body).toBe('{"a":1}')
    expect((init?.headers as Record<string, string>)['Content-Type']).toBe('application/json')
  })

  it('reads the error envelope into an ApiError', async () => {
    stubFetch(async () =>
      json({ error: { code: 'version_mismatch', message: 'old', details: { current_version: 7 } } }, { status: 409 }),
    )
    const err = await failure(request('/api/x'))
    expect(err.status).toBe(409)
    expect(err.code).toBe('version_mismatch')
    expect(err.message).toBe('old')
    expect(err.details).toEqual({ current_version: 7 })
    expect(err.retryAfterSeconds).toBeNull()
  })

  it('takes the wait from the Retry-After header first, then from the details', async () => {
    stubFetch(async () =>
      json(
        { error: { code: 'llm_rate_limited', message: 'slow down', details: { retry_after_seconds: 30 } } },
        { status: 429, headers: { 'Content-Type': 'application/json', 'Retry-After': '12' } },
      ),
    )
    expect((await failure(request('/api/x'))).retryAfterSeconds).toBe(12)

    stubFetch(async () => json({ error: { code: 'llm_rate_limited', message: 'slow down', details: { retry_after_seconds: 30 } } }, { status: 429 }))
    expect((await failure(request('/api/x'))).retryAfterSeconds).toBe(30)
  })

  it('turns an answer that is not the envelope (a proxy page) into http_error', async () => {
    stubFetch(async () => new Response('<html>Bad Gateway</html>', { status: 502 }))
    const err = await failure(request('/api/x'))
    expect(err.code).toBe('http_error')
    expect(err.status).toBe(502)
  })

  it('treats a success that is not JSON as bad_response', async () => {
    stubFetch(async () => new Response('hello', { status: 200 }))
    expect((await failure(request('/api/x'))).code).toBe('bad_response')
  })

  it('reports a network failure as network_error with status 0', async () => {
    stubFetch(async () => {
      throw new TypeError('Failed to fetch')
    })
    const err = await failure(request('/api/x'))
    expect(err.code).toBe('network_error')
    expect(err.status).toBe(0)
  })

  it('does not wrap the error of a request the caller canceled', async () => {
    const controller = new AbortController()
    stubFetch(async () => {
      controller.abort()
      throw new DOMException('aborted', 'AbortError')
    })
    const err = await request('/api/x', { signal: controller.signal }).then(
      () => null,
      (e: unknown) => e,
    )
    expect(err).not.toBeInstanceOf(ApiError)
    expect((err as DOMException).name).toBe('AbortError')
  })
})

describe('config', () => {
  it('is returned as the contract says', async () => {
    stubFetch(async () => json(config))
    expect(await getConfig()).toEqual(config)
  })

  it.each([
    ['an unknown llm_mode', { ...config, llm_mode: 'other' }],
    ['a missing model', { ...config, model: undefined }],
    ['a zero limit', { ...config, limits: { ...config.limits, sms: 0 } }],
    ['variables that are not strings', { ...config, variables: [1] }],
    ['not an object', []],
  ])('is rejected with %s', (_name, body) => {
    expect(() => parseConfig(body)).toThrow(ApiError)
  })
})
