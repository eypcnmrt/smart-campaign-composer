import { effectScope, nextTick, shallowRef } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { audienceReady, campaign, contentReady } from '@/test/fixtures'
import { error, installFakeApi } from '@/test/fakeApi'
import { useCampaign } from './useCampaign'

afterEach(() => {
  vi.unstubAllGlobals()
})

function setup(id: number | (() => number)) {
  const scope = effectScope()
  const store = scope.run(() => useCampaign(id))
  if (!store) throw new Error('no store')
  return { store, scope }
}

/** Lets the pending promises of a request settle. */
async function settle() {
  for (let i = 0; i < 5; i++) await Promise.resolve()
  await nextTick()
}

describe('useCampaign', () => {
  it('starts loading and holds the campaign once it arrives', async () => {
    installFakeApi({ 'GET /api/campaigns/7': { body: audienceReady } })
    const { store } = setup(7)
    expect(store.status.value).toBe('loading')
    expect(store.campaign.value).toBeNull()
    await settle()
    expect(store.status.value).toBe('ready')
    expect(store.campaign.value?.id).toBe(7)
    expect(store.error.value).toBeNull()
  })

  it('keeps the error of a failed load and tries again on load()', async () => {
    const api = installFakeApi({ 'GET /api/campaigns/7': error(404, 'not_found') })
    const { store } = setup(7)
    await settle()
    expect(store.status.value).toBe('error')
    expect(store.error.value).toBeInstanceOf(ApiError)
    expect((store.error.value as ApiError).code).toBe('not_found')
    expect(store.campaign.value).toBeNull()

    api.set('GET /api/campaigns/7', { body: audienceReady })
    await store.load()
    expect(store.status.value).toBe('ready')
    expect(store.error.value).toBeNull()
  })

  it('keeps the campaign on screen while it is read again, and shows the new one after', async () => {
    const api = installFakeApi({ 'GET /api/campaigns/7': { body: audienceReady } })
    const { store } = setup(7)
    await settle()
    api.set('GET /api/campaigns/7', { body: contentReady })
    const reload = store.load()
    expect(store.status.value).toBe('ready')
    expect(store.campaign.value?.status).toBe('audience_ready')
    await reload
    expect(store.campaign.value?.status).toBe('content_ready')
  })

  it('takes the campaign an action returned', async () => {
    installFakeApi({ 'GET /api/campaigns/7': { body: audienceReady } })
    const { store } = setup(7)
    await settle()
    store.replace(contentReady)
    expect(store.campaign.value?.version).toBe(3)
  })

  it('drops the answer of a read that started before an action replaced the campaign', async () => {
    let releaseOld: () => void = () => undefined
    installFakeApi({
      'GET /api/campaigns/7': async () => {
        await new Promise<void>((resolve) => (releaseOld = resolve))
        return { body: audienceReady }
      },
    })
    const { store } = setup(7)
    await settle()
    store.replace(contentReady) // an action answered while the first read was still out
    releaseOld()
    await settle()
    expect(store.campaign.value?.status).toBe('content_ready')
    expect(store.status.value).toBe('ready')
  })

  it('ignores a campaign with another id', async () => {
    installFakeApi({ 'GET /api/campaigns/7': { body: audienceReady } })
    const { store } = setup(7)
    await settle()
    store.replace(campaign({ id: 8, version: 9 }))
    expect(store.campaign.value?.id).toBe(7)
  })

  it('starts over when the id changes, and drops the answer of the old request', async () => {
    let releaseOld: () => void = () => undefined
    installFakeApi({
      'GET /api/campaigns/7': async () => {
        await new Promise<void>((resolve) => (releaseOld = resolve))
        return { body: audienceReady }
      },
      'GET /api/campaigns/8': { body: campaign({ id: 8, brief: 'other' }) },
    })
    const id = shallowRef(7)
    const { store } = setup(() => id.value)
    await settle()
    id.value = 8
    await settle()
    expect(store.campaign.value?.id).toBe(8)
    releaseOld() // the first request answers late
    await settle()
    expect(store.campaign.value?.id).toBe(8)
    expect(store.status.value).toBe('ready')
  })

  it('aborts its request when the scope ends', async () => {
    let signal: AbortSignal | null | undefined
    const fetchFn = vi.fn(async (_url: string, init?: RequestInit) => {
      signal = init?.signal
      return new Promise<Response>(() => undefined)
    })
    vi.stubGlobal('fetch', fetchFn)
    const { scope } = setup(7)
    await settle()
    expect(signal?.aborted).toBe(false)
    scope.stop()
    expect(signal?.aborted).toBe(true)
  })
})
