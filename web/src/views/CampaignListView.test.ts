import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from '@/App.vue'
import type { CampaignSummary } from '@/api/types'
import { createAppRouter } from '@/router'
import { error, installFakeApi } from '@/test/fakeApi'
import { audience, config } from '@/test/fixtures'

afterEach(() => {
  vi.unstubAllGlobals()
})

function summary(over: Partial<CampaignSummary> = {}): CampaignSummary {
  return {
    id: 1,
    brief: 'Ankara’daki müşterilere indirim',
    status: 'audience_ready',
    version: 2,
    channels: ['sms', 'email'],
    audience,
    warning_count: 0,
    approved_at: null,
    completed_at: null,
    created_at: '2026-11-14T12:00:00Z',
    updated_at: '2026-11-14T12:00:00Z',
    ...over,
  }
}

async function open(handlers: Parameters<typeof installFakeApi>[0]) {
  const api = installFakeApi({ 'GET /api/config': { body: config }, ...handlers })
  const router = createAppRouter(createMemoryHistory())
  await router.push('/')
  await router.isReady()
  const wrapper = mount(App, { global: { plugins: [router] }, attachTo: document.body })
  await vi.waitFor(() => expect(wrapper.find('h1').exists()).toBe(true))
  await flushPromises()
  return { wrapper, router, api }
}

const LIST = 'GET /api/campaigns'
const rows = (w: Awaited<ReturnType<typeof open>>['wrapper']) => w.findAll('tbody tr')

describe('the campaign list', () => {
  it('says it is loading, then shows a table with a caption and column headings', async () => {
    let release: () => void = () => undefined
    const { wrapper } = await open({
      [LIST]: async () => {
        await new Promise<void>((resolve) => (release = resolve))
        return { body: { campaigns: [summary()] } }
      },
    })
    expect(wrapper.get('main [role="status"]').text()).toContain('Kampanyalar yükleniyor')
    release()
    await flushPromises()
    expect(wrapper.find('main [role="status"]').exists()).toBe(false)
    expect(wrapper.get('caption').text()).toContain('Kampanyalar')
    expect(wrapper.findAll('thead th').map((h) => h.text())).toEqual(['Brief', 'Durum', 'Kitle', 'Uyarı', 'Oluşturulma'])
    wrapper.unmount()
  })

  it('shows each campaign’s status, audience, warnings and date', async () => {
    const { wrapper } = await open({
      [LIST]: {
        body: {
          campaigns: [
            summary({ id: 3, status: 'completed_with_failures', warning_count: 2, audience: { ...audience, matched: 1234 } }),
            summary({ id: 2, status: 'draft', audience: null, brief: 'Taslak brief' }),
          ],
        },
      },
    })
    const [first, second] = rows(wrapper)
    expect(first?.get('.badge').text()).toBe('Hatalarla tamamlandı')
    expect(first?.get('[data-col="audience"]').text()).toContain('1.234')
    expect(first?.get('[data-col="warnings"]').text()).toBe('2 uyarı')
    expect(first?.get('[data-col="created"]').text()).toContain('Kasım 2026')
    expect(second?.get('.badge').text()).toBe('Taslak')
    expect(second?.get('[data-col="audience"]').text()).toContain('Henüz yok')
    expect(second?.get('[data-col="warnings"]').text()).toBe('Yok')
    wrapper.unmount()
  })

  it('links each row to its campaign', async () => {
    const { wrapper } = await open({ [LIST]: { body: { campaigns: [summary({ id: 5 })] } } })
    expect(rows(wrapper)[0]?.get('a').attributes('href')).toBe('/campaigns/5')
    wrapper.unmount()
  })

  it('shortens a long brief to one line and shows it as text, never as markup', async () => {
    const long = 'Çok uzun brief '.repeat(30)
    const { wrapper } = await open({
      [LIST]: { body: { campaigns: [summary({ id: 1, brief: long }), summary({ id: 2, brief: '<img src=x onerror=alert(1)>' })] } },
    })
    const [first, second] = rows(wrapper)
    expect(Array.from(first?.get('a').text() ?? '').length).toBeLessThanOrEqual(110)
    expect(first?.get('a').text().endsWith('…')).toBe(true)
    expect(wrapper.find('tbody img').exists()).toBe(false)
    expect(second?.get('a').text()).toBe('<img src=x onerror=alert(1)>')
    wrapper.unmount()
  })

  it('invites the first campaign when there are none', async () => {
    const { wrapper } = await open({ [LIST]: { body: { campaigns: [] } } })
    expect(wrapper.find('table').exists()).toBe(false)
    expect(wrapper.get('.empty h2').text()).toBe('Henüz kampanya yok')
    expect(wrapper.find('.empty a').exists()).toBe(false)
    expect(wrapper.get('.empty').text()).toContain('üstteki “Yeni kampanya” düğmesini')
    wrapper.unmount()
  })

  it('shows a failure in the panel with a retry that asks again', async () => {
    let up = false
    const { wrapper, api } = await open({
      [LIST]: () => (up ? { body: { campaigns: [summary()] } } : error(503, 'not_ready')),
    })
    expect(wrapper.get('main [role="alert"]').text()).toContain('Sunucu henüz hazır değil')
    expect(wrapper.find('table').exists()).toBe(false)
    up = true
    await wrapper.get('main [role="alert"] button').trigger('click')
    await flushPromises()
    expect(api.callsTo(LIST)).toHaveLength(2)
    expect(wrapper.find('main [role="alert"]').exists()).toBe(false)
    expect(rows(wrapper)).toHaveLength(1)
    wrapper.unmount()
  })

  it('makes the table reachable by keyboard when it scrolls sideways on a narrow screen', async () => {
    const { wrapper } = await open({ [LIST]: { body: { campaigns: [summary()] } } })
    const region = wrapper.get('[role="region"]')
    expect(region.attributes('tabindex')).toBe('0')
    expect(region.attributes('aria-label')).toBe('Kampanya listesi')
    wrapper.unmount()
  })

  it('has one way to start a new campaign, the one in the header, not a second one in the page', async () => {
    const { wrapper } = await open({ [LIST]: { body: { campaigns: [summary()] } } })
    expect(wrapper.findAll('a[href="/campaigns/new"]')).toHaveLength(1)
    expect(wrapper.find('main a[href="/campaigns/new"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
