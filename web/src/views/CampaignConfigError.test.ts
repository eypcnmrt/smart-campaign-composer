import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from '@/App.vue'
import { createAppRouter } from '@/router'
import { installFakeApi } from '@/test/fakeApi'
import { config, contentReady, draft } from '@/test/fixtures'

// The configuration is read once per page load and kept in a module-level store, so a failing first read needs a file of its
// own (every test file gets a fresh module).

afterEach(() => {
  vi.unstubAllGlobals()
})

async function open(path: string, campaignBody: unknown, configUp: { value: boolean }) {
  const api = installFakeApi({
    'GET /api/config': () => {
      if (!configUp.value) throw new TypeError('Failed to fetch')
      return { body: config }
    },
    'GET /api/campaigns/7': { body: campaignBody },
  })
  const router = createAppRouter(createMemoryHistory())
  await router.push(path)
  await router.isReady()
  const wrapper = mount(App, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { wrapper, api }
}

describe('a configuration that cannot be read', () => {
  it('is an error on the content step too', async () => {
    const up = { value: false }
    const { wrapper } = await open('/campaigns/7?step=content', contentReady, up)
    expect(wrapper.text()).not.toContain('Ayarlar yükleniyor')
    expect(wrapper.findAll('[role="alert"]').some((a) => a.text().includes('Sunucuya ulaşılamıyor'))).toBe(true)
    wrapper.unmount()
  })

  // Last: its retry succeeds, and the store keeps the configuration from then on.
  it('is an error with a retry on the brief step, not a spinner for ever, and the retry brings the brief', async () => {
    const up = { value: false }
    const { wrapper } = await open('/campaigns/7?step=brief', draft, up)
    expect(wrapper.text()).not.toContain('Ayarlar yükleniyor')
    const alert = wrapper.findAll('[role="alert"]').find((a) => a.text().includes('Sunucuya ulaşılamıyor'))
    expect(alert).toBeDefined()

    up.value = true
    await alert?.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Kitle henüz oluşturulamadı')
    wrapper.unmount()
  })
})
