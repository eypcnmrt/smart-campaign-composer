import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from './client'
import { createSseParser, parseStreamEvent, streamContent, type RawSseEvent, type StreamProgress } from './sse'

afterEach(() => {
  vi.unstubAllGlobals()
})

function parse(chunks: string[]): RawSseEvent[] {
  const out: RawSseEvent[] = []
  const parser = createSseParser((e) => out.push(e))
  for (const c of chunks) parser.feed(c)
  parser.flush()
  return out
}

/** What the server writes (backend/internal/api/stream.go): a retry line, events, pings, ends with a blank line. */
const WIRE =
  'retry: 3600000\n\n' +
  'event: attempt\ndata: {"attempt":1}\n\n' +
  ': ping\n\n' +
  'event: delta\ndata: {"field":"sms","text":"Merhaba Ayşe 😀"}\n\n' +
  'event: delta\ndata: {"field":"sms","text":" ğüşiöç"}\n\n'

const WIRE_EVENTS: RawSseEvent[] = [
  { event: 'attempt', data: '{"attempt":1}' },
  { event: 'delta', data: '{"field":"sms","text":"Merhaba Ayşe 😀"}' },
  { event: 'delta', data: '{"field":"sms","text":" ğüşiöç"}' },
]

describe('createSseParser', () => {
  it('reads events from one chunk; retry lines and comments deliver nothing', () => {
    expect(parse([WIRE])).toEqual(WIRE_EVENTS)
  })

  it('gives the same events however the text is cut', () => {
    // Every single cut point, and one character at a time.
    for (let i = 0; i <= WIRE.length; i++) expect(parse([WIRE.slice(0, i), WIRE.slice(i)])).toEqual(WIRE_EVENTS)
    expect(parse(WIRE.split(''))).toEqual(WIRE_EVENTS)
  })

  it('accepts CRLF and CR line ends, also when a CR and its LF are in different chunks', () => {
    const crlf = WIRE.replace(/\n/g, '\r\n')
    expect(parse([crlf])).toEqual(WIRE_EVENTS)
    for (let i = 0; i <= crlf.length; i++) expect(parse([crlf.slice(0, i), crlf.slice(i)])).toEqual(WIRE_EVENTS)
    expect(parse([WIRE.replace(/\n/g, '\r')])).toEqual(WIRE_EVENTS)
    expect(parse(crlf.split(''))).toEqual(WIRE_EVENTS)
  })

  it('does not count the LF after a CR as an empty line (which would end an event early)', () => {
    expect(parse(['event: a\r', '\ndata: 1\r', '\n\r', '\n'])).toEqual([{ event: 'a', data: '1' }])
  })

  it('joins several data lines with a newline', () => {
    expect(parse(['event: x\ndata: one\ndata: two\ndata:\ndata: four\n\n'])).toEqual([{ event: 'x', data: 'one\ntwo\n\nfour' }])
  })

  it('removes one space after the colon, not more, and accepts a field without one', () => {
    expect(parse(['data:  two spaces\n\n', 'data:none\n\n'])).toEqual([
      { event: 'message', data: ' two spaces' },
      { event: 'message', data: 'none' },
    ])
  })

  it('names an event "message" when it has no event field, and does not carry a name into the next event', () => {
    expect(parse(['event: a\ndata: 1\n\ndata: 2\n\n'])).toEqual([
      { event: 'a', data: '1' },
      { event: 'message', data: '2' },
    ])
  })

  it('ignores unknown fields, id and retry', () => {
    expect(parse(['id: 5\nretry: 10\nfoo: bar\nevent: a\ndata: 1\n\n'])).toEqual([{ event: 'a', data: '1' }])
  })

  it('drops an event that was not closed by its blank line', () => {
    expect(parse(['event: a\ndata: 1\n\nevent: b\ndata: 2'])).toEqual([{ event: 'a', data: '1' }])
  })

  it('skips a byte order mark at the start', () => {
    expect(parse(['﻿data: 1\n\n'])).toEqual([{ event: 'message', data: '1' }])
  })

  it('refuses a line that never ends instead of buffering it', () => {
    const parser = createSseParser(() => undefined)
    expect(() => parser.feed('data: ' + 'x'.repeat(1 << 20))).toThrow(RangeError)
  })
})

describe('parseStreamEvent', () => {
  const campaign = {
    id: 1,
    brief: 'b',
    status: 'content_ready',
    version: 3,
    channels: ['sms'],
    segment: null,
    audience: null,
    warnings: [],
    content: { sms: 'a', email_subject: 'b', email_body: 'c' },
    sms_info: null,
    content_origin: 'generated',
    segment_changed_after_content: false,
  }
  const ev = (event: string, payload: unknown): RawSseEvent => ({ event, data: JSON.stringify(payload) })

  it('understands the five events', () => {
    expect(parseStreamEvent(ev('attempt', { attempt: 2 }))).toEqual({ type: 'attempt', attempt: 2 })
    expect(parseStreamEvent(ev('delta', { field: 'email_body', text: 'x' }))).toEqual({ type: 'delta', field: 'email_body', text: 'x' })
    expect(parseStreamEvent(ev('raw', { text: '{"sms":' }))).toEqual({ type: 'raw', text: '{"sms":' })
    expect(parseStreamEvent(ev('done', campaign))).toMatchObject({ type: 'done', campaign: { id: 1 } })
    expect(parseStreamEvent(ev('error', { error: { code: 'llm_timeout', message: 'm', details: null } }))).toEqual({
      type: 'error',
      error: { code: 'llm_timeout', message: 'm', details: null },
    })
  })

  it('returns null for an event it does not know, and for a field it does not have', () => {
    expect(parseStreamEvent(ev('progress', { percent: 3 }))).toBeNull()
    expect(parseStreamEvent(ev('delta', { field: 'push_title', text: 'x' }))).toBeNull()
  })

  it.each([
    ['attempt without a number', ev('attempt', { attempt: '1' })],
    ['delta without text', ev('delta', { field: 'sms' })],
    ['raw that is not an object', ev('raw', 'x')],
    ['done that is not a campaign', ev('done', { id: 1 })],
    ['error without a code', ev('error', { error: { message: 'm' } })],
    ['data that is not JSON', { event: 'delta', data: '{oops' }],
  ])('rejects %s as bad_response', (_name, raw) => {
    expect(() => parseStreamEvent(raw)).toThrow(expect.objectContaining({ code: 'bad_response' }))
  })
})

// --- streamContent -----------------------------------------------------------------------------------------------------

const enc = new TextEncoder()

/** A fetch that answers with the given byte chunks as an event stream. */
function stubStream(chunks: Uint8Array[], init: { status?: number; contentType?: string | null; failAfter?: Error } = {}) {
  const { status = 200, contentType = 'text/event-stream', failAfter } = init
  const fetchFn = vi.fn(async (_url: string, _init?: RequestInit) => {
    let i = 0
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        const chunk = chunks[i++]
        if (chunk !== undefined) controller.enqueue(chunk)
        else if (failAfter) controller.error(failAfter)
        else controller.close()
      },
    })
    const headers = new Headers()
    if (contentType !== null) headers.set('Content-Type', contentType)
    return new Response(body, { status, headers })
  })
  vi.stubGlobal('fetch', fetchFn)
  return fetchFn
}

const doneCampaign = {
  id: 9,
  brief: 'b',
  status: 'content_ready',
  version: 4,
  channels: ['sms', 'email'],
  segment: null,
  audience: null,
  warnings: [],
  content: { sms: 'Merhaba', email_subject: 's', email_body: 'b' },
  sms_info: null,
  content_origin: 'generated',
  segment_changed_after_content: false,
}

const SUCCESS =
  'retry: 3600000\n\n' +
  'event: attempt\ndata: {"attempt":1}\n\n' +
  'event: delta\ndata: {"field":"sms","text":"Merhaba 😀"}\n\n' +
  `event: done\ndata: ${JSON.stringify(doneCampaign)}\n\n`

function collect() {
  const events: StreamProgress[] = []
  return { events, onEvent: (e: StreamProgress) => events.push(e) }
}

async function rejection(promise: Promise<unknown>): Promise<ApiError> {
  const err = await promise.then(
    () => null,
    (e: unknown) => e,
  )
  expect(err).toBeInstanceOf(ApiError)
  return err as ApiError
}

describe('streamContent', () => {
  it('delivers the progress, resolves with the saved campaign and asks for an event stream', async () => {
    const fetchFn = stubStream([enc.encode(SUCCESS)])
    const { events, onEvent } = collect()
    const campaign = await streamContent(9, { onEvent })
    expect(campaign.id).toBe(9)
    expect(events).toEqual([
      { type: 'attempt', attempt: 1 },
      { type: 'delta', field: 'sms', text: 'Merhaba 😀' },
    ])
    const [url, init] = fetchFn.mock.calls[0] ?? []
    expect(url).toBe('/api/campaigns/9/content/stream')
    expect((init?.headers as Record<string, string>).Accept).toBe('text/event-stream')
  })

  it('asks to regenerate only when told to', async () => {
    const fetchFn = stubStream([enc.encode(SUCCESS)])
    await streamContent(9, { regenerate: true, onEvent: () => undefined })
    expect(fetchFn.mock.calls[0]?.[0]).toBe('/api/campaigns/9/content/stream?regenerate=true')
  })

  it('reads the same events however the bytes are cut, also inside a multi-byte character', async () => {
    const bytes = enc.encode(SUCCESS)
    // The emoji is four bytes: cut the stream at every byte position, so each cut inside it is tried.
    for (let cut = 1; cut < bytes.length; cut += 1) {
      stubStream([bytes.slice(0, cut), bytes.slice(cut)])
      const { events, onEvent } = collect()
      await streamContent(9, { onEvent })
      expect(events[1]).toEqual({ type: 'delta', field: 'sms', text: 'Merhaba 😀' })
    }
  })

  it('reads a stream that arrives one byte at a time', async () => {
    const bytes = enc.encode(SUCCESS)
    stubStream(Array.from(bytes, (_b, i) => bytes.slice(i, i + 1)))
    const { events, onEvent } = collect()
    expect((await streamContent(9, { onEvent })).id).toBe(9)
    expect(events).toHaveLength(2)
  })

  it('rejects with the code of an error event, and carries its wait', async () => {
    stubStream([
      enc.encode(
        'retry: 3600000\n\nevent: attempt\ndata: {"attempt":1}\n\n' +
          'event: error\ndata: {"error":{"code":"llm_rate_limited","message":"slow","details":{"retry_after_seconds":20}}}\n\n',
      ),
    ])
    const { events, onEvent } = collect()
    const err = await rejection(streamContent(9, { onEvent }))
    expect(err.code).toBe('llm_rate_limited')
    expect(err.retryAfterSeconds).toBe(20)
    expect(events).toEqual([{ type: 'attempt', attempt: 1 }])
  })

  it('keeps the problems of llm_invalid_output for the technical panel', async () => {
    const details = { stage: 'content', attempts: 3, problems: ['sms: must not be empty'] }
    stubStream([enc.encode(`event: error\ndata: ${JSON.stringify({ error: { code: 'llm_invalid_output', message: 'm', details } })}\n\n`)])
    expect((await rejection(streamContent(9, { onEvent: () => undefined }))).details).toEqual(details)
  })

  it('ignores whatever follows the result', async () => {
    stubStream([enc.encode(SUCCESS + 'event: delta\ndata: {"field":"sms","text":"late"}\n\nevent: error\ndata: {"error":{"code":"x","message":"y"}}\n\n')])
    const { events, onEvent } = collect()
    await streamContent(9, { onEvent })
    expect(events).toHaveLength(2)
  })

  it('reports a stream that ends without a result as stream_incomplete', async () => {
    stubStream([enc.encode('retry: 3600000\n\nevent: attempt\ndata: {"attempt":1}\n\n')])
    expect((await rejection(streamContent(9, { onEvent: () => undefined }))).code).toBe('stream_incomplete')
  })

  it('reports an event that was cut off before its blank line as stream_incomplete', async () => {
    stubStream([enc.encode(SUCCESS.slice(0, -1))])
    expect((await rejection(streamContent(9, { onEvent: () => undefined }))).code).toBe('stream_incomplete')
  })

  it('reports a broken connection in the middle as stream_incomplete', async () => {
    stubStream([enc.encode('event: attempt\ndata: {"attempt":1}\n\n')], { failAfter: new TypeError('network error') })
    const { events, onEvent } = collect()
    expect((await rejection(streamContent(9, { onEvent }))).code).toBe('stream_incomplete')
    expect(events).toHaveLength(1)
  })

  it('does not mind the data of an event it does not know', async () => {
    stubStream([enc.encode('event: heartbeat\ndata: not json at all\n\n' + SUCCESS)])
    const { events, onEvent } = collect()
    await streamContent(9, { onEvent })
    expect(events).toHaveLength(2)
  })

  it('does not report a failure of its own consumer as a broken connection', async () => {
    stubStream([enc.encode(SUCCESS)])
    const err = await streamContent(9, {
      onEvent: () => {
        throw new TypeError('bug in the handler')
      },
    }).then(
      () => null,
      (e: unknown) => e,
    )
    expect(err).toBeInstanceOf(TypeError)
  })

  it('reports a line that is far too long as bad_response, not as a broken connection', async () => {
    stubStream([enc.encode('data: ' + 'x'.repeat(1 << 20))])
    expect((await rejection(streamContent(9, { onEvent: () => undefined }))).code).toBe('bad_response')
  })

  it('reports a result that does not match the contract as bad_response', async () => {
    stubStream([enc.encode('event: done\ndata: {"id":1}\n\n')])
    expect((await rejection(streamContent(9, { onEvent: () => undefined }))).code).toBe('bad_response')
  })

  it('reads a refusal before the stream as an ordinary JSON error', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify({ error: { code: 'content_exists', message: 'm', details: { hint: 'regenerate=true' } } }), { status: 409 })),
    )
    const err = await rejection(streamContent(9, { onEvent: () => undefined }))
    expect(err.status).toBe(409)
    expect(err.code).toBe('content_exists')
  })

  it('reads the wait of a refusal from the Retry-After header', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(JSON.stringify({ error: { code: 'llm_busy', message: 'm', details: null } }), { status: 503, headers: { 'Retry-After': '5' } }),
      ),
    )
    expect((await rejection(streamContent(9, { onEvent: () => undefined }))).retryAfterSeconds).toBe(5)
  })

  it('does not take a proxy page for a stream, whatever the status', async () => {
    stubStream([enc.encode('<html>hello</html>')], { contentType: 'text/html' })
    expect((await rejection(streamContent(9, { onEvent: () => undefined }))).code).toBe('bad_response')
    vi.stubGlobal('fetch', vi.fn(async () => new Response('<html>Bad Gateway</html>', { status: 502 })))
    expect((await rejection(streamContent(9, { onEvent: () => undefined }))).code).toBe('http_error')
  })

  it('reports an unreachable server as network_error', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch')
      }),
    )
    expect((await rejection(streamContent(9, { onEvent: () => undefined }))).code).toBe('network_error')
  })

  it('passes the abort of a canceled request through untouched', async () => {
    const controller = new AbortController()
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, init?: RequestInit) => {
        // A stream that never ends until it is canceled.
        const body = new ReadableStream<Uint8Array>({
          start(c) {
            c.enqueue(enc.encode('event: attempt\ndata: {"attempt":1}\n\n'))
            init?.signal?.addEventListener('abort', () => c.error(new DOMException('aborted', 'AbortError')))
          },
        })
        return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
      }),
    )
    const seen: StreamProgress[] = []
    const promise = streamContent(9, {
      signal: controller.signal,
      onEvent: (e) => {
        seen.push(e)
        controller.abort() // the user leaves the page while the text is being written
      },
    })
    const err = await promise.then(
      () => null,
      (e: unknown) => e,
    )
    expect(err).not.toBeInstanceOf(ApiError)
    expect((err as DOMException).name).toBe('AbortError')
    expect(seen).toHaveLength(1)
  })
})
