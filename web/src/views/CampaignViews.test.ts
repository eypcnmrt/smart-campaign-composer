import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from '@/App.vue'
import { createAppRouter } from '@/router'
import { error, installFakeApi } from '@/test/fakeApi'
import { audienceReady, campaign, config, contentReady, draft } from '@/test/fixtures'

afterEach(() => {
  vi.unstubAllGlobals()
})

async function open(path: string, handlers: Parameters<typeof installFakeApi>[0]) {
  const api = installFakeApi({ 'GET /api/config': { body: config }, ...handlers })
  const router = createAppRouter(createMemoryHistory())
  await router.push(path)
  await router.isReady()
  const wrapper = mount(App, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { wrapper, router, api }
}

const step = (w: ReturnType<typeof mount>) => w.get('.step[class*="step--current"] .step__label').text()

describe('the new campaign page', () => {
  it('shows the brief form once the configuration is read, with the limit from it', async () => {
    const { wrapper } = await open('/campaigns/new', {})
    expect(wrapper.get('h2').text()).toBe('Brief')
    expect(wrapper.get('#brief-count').text()).toContain('/ 2.000')
    expect(step(wrapper)).toBe('Brief')
    wrapper.unmount()
  })

  it('creates the campaign and moves to its audience step', async () => {
    const { wrapper, router, api } = await open('/campaigns/new', {
      'POST /api/campaigns': { status: 201, body: audienceReady },
      'GET /api/campaigns/7': { body: audienceReady },
    })
    await wrapper.get('textarea').setValue('  Ankara’daki müşterilere indirim  ')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(api.callsTo('POST /api/campaigns')[0]?.body).toEqual({ brief: 'Ankara’daki müşterilere indirim' })
    // The campaign page is a lazily loaded component: wait for the navigation to finish.
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/campaigns/7?step=audience'))
    await flushPromises()
    expect(step(wrapper)).toBe('Kitle')
    wrapper.unmount()
  })

  it('shows the form as busy while the model works, and ignores a second submit', async () => {
    let finish: () => void = () => undefined
    const { wrapper, api } = await open('/campaigns/new', {
      'POST /api/campaigns': async () => {
        await new Promise<void>((resolve) => (finish = resolve))
        return { status: 201, body: audienceReady }
      },
      'GET /api/campaigns/7': { body: audienceReady },
    })
    await wrapper.get('textarea').setValue('brief')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('form').attributes('aria-busy')).toBe('true')
    await wrapper.get('form').trigger('submit')
    expect(api.callsTo('POST /api/campaigns')).toHaveLength(1)
    finish()
    await flushPromises()
    wrapper.unmount()
  })

  it('keeps the brief and offers the draft when the model failed after the draft was saved', async () => {
    const { wrapper, router } = await open('/campaigns/new', {
      'POST /api/campaigns': error(504, 'llm_timeout', { campaign_id: 12 }),
    })
    await wrapper.get('textarea').setValue('Benim brief’im')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(router.currentRoute.value.path).toBe('/campaigns/new')
    expect(wrapper.get('[role="alert"]').text()).toContain('Model zamanında yanıt vermedi')
    expect(wrapper.get('a[href="/campaigns/12"]').text()).toBe('Taslağı aç')
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('Benim brief’im')
    expect(wrapper.get('textarea').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('does not take a campaign that was not made for a failure to reach the server', async () => {
    const { wrapper, router } = await open('/campaigns/new', {
      'POST /api/campaigns': () => {
        throw new TypeError('Failed to fetch')
      },
    })
    await wrapper.get('textarea').setValue('brief')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/campaigns/new')
    expect(wrapper.get('[role="alert"]').text()).toContain('Sunucuya ulaşılamıyor')
    wrapper.unmount()
  })
})

describe('the campaign page', () => {
  it('opens a campaign with an audience at its audience step, and writes the step in the address', async () => {
    const { wrapper, router } = await open('/campaigns/7', { 'GET /api/campaigns/7': { body: audienceReady } })
    expect(router.currentRoute.value.fullPath).toBe('/campaigns/7?step=audience')
    expect(step(wrapper)).toBe('Kitle')
    wrapper.unmount()
  })

  it('opens a campaign with content at its content step', async () => {
    const { wrapper, router } = await open('/campaigns/7', { 'GET /api/campaigns/7': { body: contentReady } })
    expect(router.currentRoute.value.query.step).toBe('content')
    expect(step(wrapper)).toBe('İçerik')
    wrapper.unmount()
  })

  it('moves a step that is not open yet to the one that is (an old link, a typed address)', async () => {
    const { wrapper, router } = await open('/campaigns/7?step=approve', { 'GET /api/campaigns/7': { body: audienceReady } })
    expect(router.currentRoute.value.query.step).toBe('audience')
    expect(step(wrapper)).toBe('Kitle')
    wrapper.unmount()
  })

  it('shows the brief of a campaign that has an audience as it was written, unchangeable', async () => {
    const { wrapper } = await open('/campaigns/7?step=brief', { 'GET /api/campaigns/7': { body: audienceReady } })
    expect(wrapper.get('h2').text()).toBe('Brief')
    expect(wrapper.get('blockquote').text()).toBe(audienceReady.brief)
    expect(wrapper.find('textarea').exists()).toBe(false)
    wrapper.unmount()
  })

  it('follows a link of the rail to another step without reading the campaign again', async () => {
    const { wrapper, router, api } = await open('/campaigns/7', { 'GET /api/campaigns/7': { body: contentReady } })
    await wrapper.get('a[href="/campaigns/7?step=brief"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.step).toBe('brief')
    expect(step(wrapper)).toBe('Brief')
    expect(api.callsTo('GET /api/campaigns/7')).toHaveLength(1)
    wrapper.unmount()
  })

  it('moves the focus to the heading of the new step', async () => {
    const { wrapper } = await open('/campaigns/7', { 'GET /api/campaigns/7': { body: contentReady } })
    await wrapper.get('a[href="/campaigns/7?step=brief"]').trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(wrapper.get('h2').element)
    wrapper.unmount()
  })

  it('says a campaign was not found, with a way back to the list', async () => {
    const { wrapper } = await open('/campaigns/99', { 'GET /api/campaigns/99': error(404, 'not_found') })
    expect(wrapper.get('[role="alert"]').text()).toContain('Kampanya bulunamadı')
    expect(wrapper.get('main a[href="/"]').text()).toContain('Kampanyalara dön')
    wrapper.unmount()
  })

  it('reads the campaign again when the retry is pressed', async () => {
    let up = false
    const { wrapper, api } = await open('/campaigns/7', {
      'GET /api/campaigns/7': () => {
        if (!up) throw new TypeError('Failed to fetch')
        return { body: audienceReady }
      },
    })
    expect(wrapper.get('[role="alert"]').text()).toContain('Sunucuya ulaşılamıyor')
    up = true
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(step(wrapper)).toBe('Kitle')
    expect(api.callsTo('GET /api/campaigns/7')).toHaveLength(2)
    wrapper.unmount()
  })

  it('says so when a reload failed while a campaign is on screen, and reads again on request', async () => {
    let up = true
    const { wrapper, api } = await open('/campaigns/7', {
      'GET /api/campaigns/7': () => {
        if (!up) throw new TypeError('Failed to fetch')
        return { body: audienceReady }
      },
      'PUT /api/campaigns/7/channels': () => {
        throw new TypeError('Failed to fetch')
      },
    })
    up = false
    // The channel change is lost on the way; the page reads the campaign again, and that fails too: the page must say so.
    await wrapper.findAll('input[type="checkbox"]')[0]?.setValue(false)
    await flushPromises()
    expect(step(wrapper)).toBe('Kitle') // the last campaign that arrived stays
    const banners = wrapper.findAll('[role="alert"]')
    expect(banners.length).toBeGreaterThan(0)
    up = true
    const retry = wrapper.findAll('button').find((b) => b.text().includes('Kampanyayı yeniden yükle'))
    expect(retry).toBeDefined()
    await retry?.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('button').some((b) => b.text().includes('Kampanyayı yeniden yükle'))).toBe(false)
    expect(api.callsTo('GET /api/campaigns/7').length).toBeGreaterThanOrEqual(3)
    wrapper.unmount()
  })

  describe('the audience step', () => {
    it('puts a channel box back when the change failed, and reads the campaign again', async () => {
      const { wrapper, api } = await open('/campaigns/7', {
        'GET /api/campaigns/7': { body: audienceReady },
        'PUT /api/campaigns/7/channels': () => {
          throw new TypeError('Failed to fetch')
        },
      })
      const boxes = () => wrapper.findAll('input[type="checkbox"]').map((b) => (b.element as HTMLInputElement).checked)
      expect(boxes()).toEqual([true, true])
      await wrapper.findAll('input[type="checkbox"]')[0]?.setValue(false)
      await flushPromises()
      expect(boxes()).toEqual([true, true])
      expect(api.callsTo('GET /api/campaigns/7')).toHaveLength(2)
      wrapper.unmount()
    })

    it('shows the new state of the campaign after a channel was closed', async () => {
      const { wrapper } = await open('/campaigns/7', {
        'GET /api/campaigns/7': { body: audienceReady },
        'PUT /api/campaigns/7/channels': {
          body: campaign({ channels: ['email'], version: 3, warnings: [{ code: 'matches_everyone', message: 'English' }] }),
        },
      })
      expect(wrapper.find('[data-warning]').exists()).toBe(false)
      await wrapper.findAll('input[type="checkbox"]')[0]?.setValue(false)
      await flushPromises()
      expect(wrapper.find('[data-warning="matches_everyone"]').exists()).toBe(true)
      expect(wrapper.findAll('input[type="checkbox"]').map((b) => (b.element as HTMLInputElement).checked)).toEqual([false, true])
      wrapper.unmount()
    })

    it('goes to the content step when asked, without calling the model', async () => {
      const { wrapper, router, api } = await open('/campaigns/7', { 'GET /api/campaigns/7': { body: audienceReady } })
      await wrapper.get('main button.btn--primary').trigger('click')
      await vi.waitFor(() => expect(router.currentRoute.value.query.step).toBe('content'))
      await flushPromises()
      expect(step(wrapper)).toBe('İçerik')
      expect(api.calls.filter((c) => c.method !== 'GET')).toHaveLength(0)
      wrapper.unmount()
    })
  })

  describe('a draft, whose audience could not be made', () => {
    it('opens at the brief with a retry', async () => {
      const { wrapper, router } = await open('/campaigns/7', { 'GET /api/campaigns/7': { body: draft } })
      expect(router.currentRoute.value.query.step).toBe('brief')
      expect(wrapper.text()).toContain('Kitle henüz oluşturulamadı')
      wrapper.unmount()
    })

    it('makes the audience again and moves to it', async () => {
      const { wrapper, router, api } = await open('/campaigns/7', {
        'GET /api/campaigns/7': { body: draft },
        'POST /api/campaigns/7/segment': { body: campaign({ version: 2 }) },
      })
      await wrapper.get('button.btn--primary').trigger('click')
      await flushPromises()
      expect(api.callsTo('POST /api/campaigns/7/segment')).toHaveLength(1)
      expect(router.currentRoute.value.query.step).toBe('audience')
      expect(step(wrapper)).toBe('Kitle')
      wrapper.unmount()
    })

    it('shows why the retry failed and stays on the brief', async () => {
      const { wrapper, router } = await open('/campaigns/7', {
        'GET /api/campaigns/7': { body: draft },
        'POST /api/campaigns/7/segment': error(429, 'llm_rate_limited', { retry_after_seconds: 30 }, { 'Retry-After': '30' }),
      })
      await wrapper.get('button.btn--primary').trigger('click')
      await flushPromises()
      expect(router.currentRoute.value.query.step).toBe('brief')
      const alert = wrapper.get('[role="alert"]')
      expect(alert.text()).toContain('istek sınırında')
      wrapper.unmount()
    })
  })
})
