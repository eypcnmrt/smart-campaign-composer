import { vi } from 'vitest'

// A stand-in for the API for component tests: the global fetch answers from a table keyed "METHOD /path" (the query string is
// not part of the key; calls keep it). An unlisted request gets the server's own 404 envelope, so a screen that asks for
// something the test did not expect fails loudly instead of waiting forever.

export interface Call {
  method: string
  /** Path with its query string. */
  path: string
  body: unknown
}

export type Reply = { status?: number; body: unknown; headers?: Record<string, string> }
/** A ready-made Response (an event stream) is passed through as it is. */
export type Handler = Reply | Response | ((call: Call) => Reply | Response | Promise<Reply | Response>)

export function error(status: number, code: string, details: unknown = null, headers: Record<string, string> = {}): Reply {
  return { status, body: { error: { code, message: `English ${code}`, details } }, headers }
}

function toResponse(reply: Reply | Response): Response {
  if (reply instanceof Response) return reply
  return new Response(JSON.stringify(reply.body), {
    status: reply.status ?? 200,
    headers: { 'Content-Type': 'application/json', ...reply.headers },
  })
}

export interface FakeApi {
  calls: Call[]
  /** Calls whose key ("METHOD /path") matches. */
  callsTo: (key: string) => Call[]
  /** Replaces or adds an answer. */
  set: (key: string, handler: Handler) => void
}

/**
 * A real fetch rejects a pending body read when the request is aborted; a Response built in a test does not know about the
 * request. This makes an event stream behave like the real thing: aborting ends its reads with an AbortError.
 */
function bindAbort(res: Response, signal: AbortSignal | null | undefined): Response {
  if (!signal || !res.body || !res.headers.get('Content-Type')?.startsWith('text/event-stream')) return res
  const reader = res.body.getReader()
  const aborted = new Promise<never>((_, reject) => {
    const fail = () => reject(new DOMException('The operation was aborted.', 'AbortError'))
    if (signal.aborted) fail()
    else signal.addEventListener('abort', fail, { once: true })
  })
  aborted.catch(() => undefined)
  const body = new ReadableStream<Uint8Array>({
    async pull(controller) {
      try {
        const { done, value } = await Promise.race([reader.read(), aborted])
        if (done) controller.close()
        else controller.enqueue(value)
      } catch (err) {
        controller.error(err)
      }
    },
    cancel: (reason) => reader.cancel(reason),
  })
  return new Response(body, { status: res.status, headers: res.headers })
}

export function installFakeApi(handlers: Record<string, Handler>): FakeApi {
  const table = new Map(Object.entries(handlers))
  const calls: Call[] = []

  const fetchFn = vi.fn(async (input: string | URL | Request, init?: RequestInit): Promise<Response> => {
    const raw = typeof input === 'string' ? input : input instanceof URL ? input.pathname + input.search : input.url
    const method = (init?.method ?? 'GET').toUpperCase()
    const body = typeof init?.body === 'string' && init.body !== '' ? (JSON.parse(init.body) as unknown) : undefined
    const call: Call = { method, path: raw, body }
    calls.push(call)
    const key = `${method} ${raw.split('?')[0]}`
    const handler = table.get(key)
    if (handler === undefined) return toResponse(error(404, 'not_found'))
    const res = toResponse(typeof handler === 'function' ? await handler(call) : handler)
    return bindAbort(res, init?.signal)
  })
  vi.stubGlobal('fetch', fetchFn)

  return {
    calls,
    callsTo: (key) => calls.filter((c) => `${c.method} ${c.path.split('?')[0]}` === key),
    set: (key, handler) => {
      table.set(key, handler)
    },
  }
}

const encoder = new TextEncoder()

/** One server-sent event as the server writes it. */
export function sseFrame(event: string, data: unknown): string {
  return `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`
}

/** An event stream that ends after the given frames (see sseFrame). */
export function sseResponse(frames: string[]): Response {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(encoder.encode('retry: 3600000\n\n'))
      for (const f of frames) controller.enqueue(encoder.encode(f))
      controller.close()
    },
  })
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}

/** An event stream the test feeds frame by frame, to look at the screen between them. */
export function openSse(): { response: Response; push: (frame: string) => void; close: () => void } {
  let controller!: ReadableStreamDefaultController<Uint8Array>
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      controller = c
      c.enqueue(encoder.encode('retry: 3600000\n\n'))
    },
  })
  return {
    response: new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } }),
    push: (frame) => controller.enqueue(encoder.encode(frame)),
    close: () => controller.close(),
  }
}
