import { onScopeDispose, shallowRef, type ShallowRef } from 'vue'
import { streamContent } from '@/api/sse'
import type { Campaign, Content, ContentField } from '@/api/types'

export type StreamStatus = 'idle' | 'streaming' | 'error'

export interface ContentStream {
  /** The text written so far, a preview: what counts is the campaign that comes with `done`. */
  fields: Readonly<ShallowRef<Content>>
  /** The attempt the model is on (1 for the first). 0 before the first event. */
  attempt: Readonly<ShallowRef<number>>
  /** The raw JSON text, when the answer stopped looking like fields; '' otherwise. */
  raw: Readonly<ShallowRef<string>>
  status: Readonly<ShallowRef<StreamStatus>>
  /** What went wrong when `status` is 'error'. */
  error: Readonly<ShallowRef<unknown>>
  /** Starts a generation. Resolves with the saved campaign, or null when it failed (see `error`) or was stopped. */
  start: (id: number, options?: { regenerate?: boolean }) => Promise<Campaign | null>
  /** Stops the request in flight; the server cancels the model call when the connection closes. */
  abort: () => void
}

const EMPTY: Content = { sms: '', email_subject: '', email_body: '' }

/**
 * One content generation at a time, read from the server's event stream. A new `attempt` event means the model's previous
 * answer was rejected and it is writing again: the text shown so far is cleared, since it no longer counts. The generation is
 * aborted when the scope ends (the page is left), and an answer that arrives after an abort is ignored.
 */
export function useContentStream(): ContentStream {
  const fields = shallowRef<Content>({ ...EMPTY })
  const attempt = shallowRef(0)
  const raw = shallowRef('')
  const status = shallowRef<StreamStatus>('idle')
  const error = shallowRef<unknown>(null)
  let current: AbortController | null = null

  function abort(): void {
    current?.abort()
    current = null
    if (status.value === 'streaming') status.value = 'idle'
  }

  async function start(id: number, options: { regenerate?: boolean } = {}): Promise<Campaign | null> {
    current?.abort()
    const mine = new AbortController()
    current = mine
    fields.value = { ...EMPTY }
    attempt.value = 0
    raw.value = ''
    error.value = null
    status.value = 'streaming'
    // The first raw event of an attempt carries everything written so far and replaces the field view; the next ones add to it.
    let rawStarted = false
    try {
      const campaign = await streamContent(id, {
        regenerate: options.regenerate ?? false,
        signal: mine.signal,
        onEvent: (event) => {
          if (mine.signal.aborted) return
          if (event.type === 'attempt') {
            fields.value = { ...EMPTY }
            raw.value = ''
            rawStarted = false
            attempt.value = event.attempt
          } else if (event.type === 'delta') {
            const field: ContentField = event.field
            fields.value = { ...fields.value, [field]: fields.value[field] + event.text }
          } else if (rawStarted) {
            raw.value += event.text
          } else {
            rawStarted = true
            raw.value = event.text
          }
        },
      })
      if (mine.signal.aborted) return null
      status.value = 'idle'
      return campaign
    } catch (err) {
      if (mine.signal.aborted) return null
      error.value = err
      status.value = 'error'
      return null
    } finally {
      if (current === mine) current = null
    }
  }

  onScopeDispose(() => current?.abort())

  return { fields, attempt, raw, status, error, start, abort }
}
