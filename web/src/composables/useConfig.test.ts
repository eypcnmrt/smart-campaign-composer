import { describe, expect, it, vi } from 'vitest'
import type { Config } from '@/api/types'
import { createConfigStore } from './useConfig'

const config: Config = {
  llm_mode: 'mock',
  model: 'mock',
  limits: { sms: 160, email_subject: 80, email_body: 2000, brief: 2000 },
  variables: ['first_name', 'city'],
  channels: ['sms', 'email'],
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('createConfigStore', () => {
  it('goes idle -> loading -> ready', async () => {
    const d = deferred<Config>()
    const store = createConfigStore(() => d.promise)
    expect(store.state.value.status).toBe('idle')

    const loading = store.load()
    expect(store.state.value.status).toBe('loading')

    d.resolve(config)
    await loading
    expect(store.state.value).toEqual({ status: 'ready', config })
  })

  it('shares one request between callers and does not fetch again once ready', async () => {
    const fetchConfig = vi.fn(async () => config)
    const store = createConfigStore(fetchConfig)
    await Promise.all([store.load(), store.load()])
    await store.load()
    expect(fetchConfig).toHaveBeenCalledTimes(1)
  })

  it('keeps the error and tries again on the next load', async () => {
    const fetchConfig = vi.fn<() => Promise<Config>>().mockRejectedValueOnce(new Error('down')).mockResolvedValueOnce(config)
    const store = createConfigStore(fetchConfig)

    await store.load()
    const failed = store.state.value
    expect(failed.status).toBe('error')
    expect(failed.status === 'error' && failed.error.message).toBe('down')

    await store.load()
    expect(store.state.value.status).toBe('ready')
    expect(fetchConfig).toHaveBeenCalledTimes(2)
  })

  it('wraps a thrown value that is not an Error', async () => {
    const store = createConfigStore(() => Promise.reject('plain text'))
    await store.load()
    const state = store.state.value
    expect(state.status === 'error' && state.error).toBeInstanceOf(Error)
  })
})
