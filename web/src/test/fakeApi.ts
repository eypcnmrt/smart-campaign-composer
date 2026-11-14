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
export type Handler = Reply | ((call: Call) => Reply | Promise<Reply>)

export function error(status: number, code: string, details: unknown = null, headers: Record<string, string> = {}): Reply {
  return { status, body: { error: { code, message: `English ${code}`, details } }, headers }
}

function toResponse(reply: Reply): Response {
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
    return toResponse(typeof handler === 'function' ? await handler(call) : handler)
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
