import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { createMemoryHistory } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from '@/App.vue'
import { createAppRouter } from '@/router'
import { installFakeApi } from '@/test/fakeApi'
import { config } from '@/test/fixtures'

afterEach(() => {
  vi.unstubAllGlobals()
})

async function open(errorHandler?: (err: unknown) => void) {
  installFakeApi({ 'GET /api/config': { body: config }, 'GET /api/campaigns': { body: { campaigns: [] } } })
  const router = createAppRouter(createMemoryHistory())
  await router.push('/')
  await router.isReady()
  const wrapper = mount(App, { global: { plugins: [router], config: errorHandler ? { errorHandler } : {} }, attachTo: document.body })
  await flushPromises()
  return { wrapper, router }
}

const banner = (w: ReturnType<typeof mount>) => w.find('main [role="alert"]')

describe('failures that would leave the page dead', () => {
  it('says so when a page cannot be loaded (the app was updated under an open tab), and clears when a page loads', async () => {
    const { wrapper, router } = await open()
    router.addRoute({ path: '/gone', component: () => Promise.reject(new TypeError('Failed to fetch dynamically imported module')) })
    await router.push('/gone').catch(() => undefined)
    await flushPromises()
    expect(banner(wrapper).exists()).toBe(true)
    expect(banner(wrapper).text()).toContain('Sayfa yüklenemedi')
    expect(banner(wrapper).find('button').text()).toBe('Sayfayı yenile')

    await router.push('/campaigns/new')
    await flushPromises()
    expect(banner(wrapper).exists()).toBe(false)
    wrapper.unmount()
  })

  it('says so when a component fails while it renders, and still lets the error be logged', async () => {
    const handled = vi.fn()
    const { wrapper, router } = await open(handled)
    const Broken = defineComponent({
      setup() {
        throw new Error('render bug')
      },
      render: () => h('div'),
    })
    router.addRoute({ path: '/broken', component: Broken })
    await router.push('/broken').catch(() => undefined)
    await flushPromises()
    expect(banner(wrapper).text()).toContain('Sayfada bir sorun oluştu')
    expect(handled).toHaveBeenCalled() // passed on to the app's own handler (which logs it)
    wrapper.unmount()
  })
})
