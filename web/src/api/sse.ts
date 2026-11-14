import { parseCampaign } from './campaigns'
import { ApiError, isRecord, toApiError } from './client'
import type { Campaign, ContentField, StreamEvent } from './types'

// The content stream (GET /api/campaigns/{id}/content/stream) is Server-Sent Events, read with fetch and a ReadableStream
// instead of EventSource. EventSource cannot send a header, cannot tell a refusal (409, 429) from a network drop, and
// reconnects by itself, which would start a second paid generation. Here the caller decides what a failure means.

/** One event as it is on the wire, before it is understood. */
export interface RawSseEvent {
  /** The `event:` field; "message" when there was none. */
  event: string
  /** The `data:` lines joined with "\n". */
  data: string
}

/** A line or an event larger than this is not something the server sends; the stream is refused instead of buffered. */
const MAX_BUFFER = 1 << 20

export interface SseParser {
  /** Feeds decoded text. It may end anywhere: inside a line, between a CR and its LF, inside an event. */
  feed(text: string): void
  /** Ends the input. An event that was not closed by its blank line is dropped, as the SSE specification says. */
  flush(): void
}

/**
 * An incremental parser for the text/event-stream format (WHATWG HTML, "Server-sent events"): lines end with LF, CR or CRLF,
 * a blank line ends an event, lines starting with ":" are comments, `data:` lines are joined with "\n", and an event that
 * has no data is not delivered (so the server's `retry:` line and its `: ping` comments produce nothing).
 */
export function createSseParser(onEvent: (event: RawSseEvent) => void): SseParser {
  let buffer = ''
  let skipLf = false // the last chunk ended with CR: a LF that opens the next one belongs to it
  let started = false
  let eventName = ''
  let data: string[] = []

  function line(text: string): void {
    if (text === '') {
      if (data.length > 0) onEvent({ event: eventName === '' ? 'message' : eventName, data: data.join('\n') })
      eventName = ''
      data = []
      return
    }
    if (text.startsWith(':')) return
    const colon = text.indexOf(':')
    const field = colon === -1 ? text : text.slice(0, colon)
    let value = colon === -1 ? '' : text.slice(colon + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'event') eventName = value
    else if (field === 'data') data.push(value)
    // `id` and `retry` are not used: nothing here reconnects by itself.
  }

  return {
    feed(chunk: string): void {
      let text = chunk
      if (!started && text !== '') {
        started = true
        if (text.startsWith('﻿')) text = text.slice(1)
      }
      if (skipLf && text.startsWith('\n')) text = text.slice(1)
      if (text !== '') skipLf = false
      buffer += text

      let start = 0
      for (let i = 0; i < buffer.length; i++) {
        const ch = buffer[i]
        if (ch !== '\n' && ch !== '\r') continue
        line(buffer.slice(start, i))
        if (ch === '\r') {
          if (i + 1 < buffer.length) {
            if (buffer[i + 1] === '\n') i++
          } else {
            skipLf = true
          }
        }
        start = i + 1
      }
      buffer = buffer.slice(start)
      if (buffer.length > MAX_BUFFER) throw new RangeError('The event stream sent a line that is too long.')
    },

    flush(): void {
      buffer = ''
      eventName = ''
      data = []
      skipLf = false
    },
  }
}

const CONTENT_FIELDS: readonly string[] = ['sms', 'email_subject', 'email_body'] satisfies ContentField[]

function badEvent(name: string): ApiError {
  return new ApiError({ status: 200, code: 'bad_response', message: `The ${name} event of the content stream does not match the API contract.` })
}

/**
 * Understands one event. Returns null for an event this client does not know (a newer server may send more), and throws
 * `bad_response` for a known event with the wrong shape.
 */
export function parseStreamEvent(raw: RawSseEvent): StreamEvent | null {
  let payload: unknown
  try {
    payload = JSON.parse(raw.data)
  } catch {
    throw badEvent(raw.event)
  }
  switch (raw.event) {
    case 'attempt':
      if (!isRecord(payload) || typeof payload.attempt !== 'number') throw badEvent('attempt')
      return { type: 'attempt', attempt: payload.attempt }
    case 'delta':
      if (!isRecord(payload) || typeof payload.field !== 'string' || typeof payload.text !== 'string') throw badEvent('delta')
      // A field this UI does not have has nowhere to go.
      if (!CONTENT_FIELDS.includes(payload.field)) return null
      return { type: 'delta', field: payload.field as ContentField, text: payload.text }
    case 'raw':
      if (!isRecord(payload) || typeof payload.text !== 'string') throw badEvent('raw')
      return { type: 'raw', text: payload.text }
    case 'done':
      return { type: 'done', campaign: parseCampaign(payload) }
    case 'error': {
      const e = isRecord(payload) ? payload.error : undefined
      if (!isRecord(e) || typeof e.code !== 'string' || typeof e.message !== 'string') throw badEvent('error')
      return { type: 'error', error: { code: e.code, message: e.message, details: e.details ?? null } }
    }
    default:
      return null
  }
}

/** What onEvent receives: the progress of a generation. The end of it is the promise's result. */
export type StreamProgress = Extract<StreamEvent, { type: 'attempt' | 'delta' | 'raw' }>

export interface StreamOptions {
  /** True to generate again for a campaign that already has content (the server refuses that otherwise with content_exists). */
  regenerate?: boolean
  signal?: AbortSignal
  onEvent: (event: StreamProgress) => void
}

/** The error that arrives as an event: the HTTP status was 200, the failure is in the stream. */
function streamError(error: { code: string; message: string; details: unknown }): ApiError {
  const wait = isRecord(error.details) && typeof error.details.retry_after_seconds === 'number' ? error.details.retry_after_seconds : null
  return new ApiError({ status: 200, code: error.code, message: error.message, details: error.details, retryAfterSeconds: wait })
}

function incomplete(): ApiError {
  return new ApiError({ status: 200, code: 'stream_incomplete', message: 'The content stream ended before the result arrived.' })
}

/**
 * Generates the content of a campaign and resolves with the saved campaign (the `done` event). Rejects with an ApiError for
 * a refusal before the stream (409 content_exists, 429, 503 ...), for an `error` event, for a stream that ends without a
 * result (`stream_incomplete`) and for an unreachable server (`network_error`). A request that `signal` canceled rejects with
 * the abort error itself, untouched, for the caller to ignore.
 */
export async function streamContent(id: number, options: StreamOptions): Promise<Campaign> {
  const { regenerate = false, signal, onEvent } = options
  const url = `/api/campaigns/${id}/content/stream${regenerate ? '?regenerate=true' : ''}`

  let res: Response
  try {
    res = await fetch(url, { method: 'GET', headers: { Accept: 'text/event-stream' }, signal })
  } catch (err) {
    if (signal?.aborted) throw err
    throw new ApiError({ status: 0, code: 'network_error', message: 'The server could not be reached.' })
  }

  // A refusal before the stream is an ordinary JSON error.
  if (!res.ok) {
    let body: unknown
    try {
      body = JSON.parse(await res.text())
    } catch (err) {
      if (signal?.aborted) throw err
      body = undefined
    }
    throw toApiError(res, body)
  }
  if (!res.headers.get('Content-Type')?.toLowerCase().startsWith('text/event-stream') || res.body === null) {
    throw new ApiError({ status: res.status, code: 'bad_response', message: 'The server did not answer with an event stream.' })
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder('utf-8') // `stream: true` below keeps a character that is split between two chunks whole

  // A holder, not a variable: the parser callback assigns it, which the compiler does not follow through a plain let.
  const end: { result: { campaign: Campaign } | { error: ApiError } | null } = { result: null }
  const parser = createSseParser((raw) => {
    if (end.result !== null) return // nothing after the result counts
    const event = parseStreamEvent(raw)
    if (event === null) return
    if (event.type === 'done') end.result = { campaign: event.campaign }
    else if (event.type === 'error') end.result = { error: streamError(event.error) }
    else onEvent(event)
  })

  try {
    while (end.result === null) {
      const { done, value } = await reader.read()
      if (done) {
        parser.feed(decoder.decode())
        break
      }
      parser.feed(decoder.decode(value, { stream: true }))
    }
  } catch (err) {
    if (signal?.aborted) throw err
    if (err instanceof ApiError) throw err
    // The connection broke, or the stream was malformed beyond reading.
    throw incomplete()
  } finally {
    parser.flush()
    // The result is in; the server has nothing more to say. Cancel so the connection is released.
    reader.cancel().catch(() => undefined)
  }

  const result = end.result
  if (result === null) throw incomplete()
  if ('error' in result) throw result.error
  return result.campaign
}
