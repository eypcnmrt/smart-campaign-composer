import type { Config } from './types'

export interface ApiErrorInit {
  status: number
  code: string
  message: string
  details?: unknown
  retryAfterSeconds?: number | null
}

/**
 * Everything that can go wrong with a request, in one shape. `code` is the contract (the UI maps it to Turkish text);
 * `message` is the server's English sentence and is for logs and the "technical details" view, not for users.
 *
 * Besides the server's own codes there are three that are made here: `network_error` (no answer, status 0),
 * `http_error` (an answer that is not our error envelope, for example a proxy's 502 page) and `bad_response`
 * (a success that is not what the contract says).
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details: unknown
  /** Seconds to wait, from the Retry-After header or details.retry_after_seconds; null when the server gave none. */
  readonly retryAfterSeconds: number | null

  constructor(init: ApiErrorInit) {
    super(init.message)
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.details = init.details ?? null
    this.retryAfterSeconds = init.retryAfterSeconds ?? null
  }
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function retryAfterSeconds(res: Response, details: unknown): number | null {
  const header = res.headers.get('Retry-After')?.trim()
  if (header !== undefined && /^\d+$/.test(header)) return Number(header)
  if (isRecord(details) && typeof details.retry_after_seconds === 'number') return details.retry_after_seconds
  return null
}

/** Turns a non-2xx response into an ApiError. This is the only place the error envelope is read. */
export function toApiError(res: Response, body: unknown): ApiError {
  const envelope = isRecord(body) ? body.error : undefined
  if (isRecord(envelope) && typeof envelope.code === 'string' && typeof envelope.message === 'string') {
    return new ApiError({
      status: res.status,
      code: envelope.code,
      message: envelope.message,
      details: envelope.details ?? null,
      retryAfterSeconds: retryAfterSeconds(res, envelope.details),
    })
  }
  return new ApiError({
    status: res.status,
    code: 'http_error',
    message: `The server answered HTTP ${res.status}.`,
    retryAfterSeconds: retryAfterSeconds(res, null),
  })
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT'
  body?: unknown
  signal?: AbortSignal
}

/** The error of a request the caller canceled is not an ApiError: it is rethrown as it is, for the caller to ignore. */
function networkError(): ApiError {
  return new ApiError({ status: 0, code: 'network_error', message: 'The server could not be reached.' })
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, signal } = options
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  let res: Response
  let text: string
  try {
    res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal })
    text = await res.text()
  } catch (err) {
    if (signal?.aborted) throw err
    throw networkError()
  }

  let data: unknown
  if (text !== '') {
    try {
      data = JSON.parse(text)
    } catch {
      data = undefined
    }
  }
  if (!res.ok) throw toApiError(res, data)
  if (data === undefined) {
    throw new ApiError({ status: res.status, code: 'bad_response', message: 'The server answered with something that is not JSON.' })
  }
  return data as T
}

export function badResponse(what: string): ApiError {
  return new ApiError({ status: 200, code: 'bad_response', message: `The server's ${what} does not match the API contract.` })
}

export function isStringList(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === 'string')
}

/** Checks the one response the whole UI depends on before anything uses it. */
export function parseConfig(data: unknown): Config {
  if (!isRecord(data)) throw badResponse('configuration')
  const { llm_mode, model, limits, variables, channels } = data
  if (llm_mode !== 'mock' && llm_mode !== 'anthropic') throw badResponse('configuration')
  if (typeof model !== 'string' || !isStringList(variables) || !isStringList(channels) || !isRecord(limits)) {
    throw badResponse('configuration')
  }
  const { sms, email_subject, email_body, brief } = limits
  if (![sms, email_subject, email_body, brief].every((n) => typeof n === 'number' && Number.isFinite(n) && n > 0)) {
    throw badResponse('configuration limits')
  }
  return {
    llm_mode,
    model,
    limits: { sms: sms as number, email_subject: email_subject as number, email_body: email_body as number, brief: brief as number },
    variables,
    channels,
  }
}

export async function getConfig(signal?: AbortSignal): Promise<Config> {
  return parseConfig(await request<unknown>('/api/config', { signal }))
}
