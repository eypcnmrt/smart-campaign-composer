import { mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'
import { describe, expect, it } from 'vitest'
import type { Config } from '@/api/types'
import type { ConfigState } from '@/composables/useConfig'
import { createAppRouter } from '@/router'
import AppHeader from './AppHeader.vue'

const config: Config = {
  llm_mode: 'mock',
  model: 'mock',
  limits: { sms: 160, email_subject: 80, email_body: 2000, brief: 2000 },
  variables: ['first_name', 'city'],
  channels: ['sms', 'email'],
}

async function render(configState: ConfigState) {
  const router = createAppRouter(createMemoryHistory())
  await router.push('/')
  await router.isReady()
  return mount(AppHeader, { props: { configState }, global: { plugins: [router] } })
}

describe('AppHeader', () => {
  it('shows the Mock LLM badge with the model name once the configuration is ready', async () => {
    const wrapper = await render({ status: 'ready', config })
    expect(wrapper.text()).toContain('Mock LLM')
    expect(wrapper.text()).toContain('Model: mock')
  })

  it('does not show the badge for the real model', async () => {
    const wrapper = await render({ status: 'ready', config: { ...config, llm_mode: 'anthropic', model: 'claude-opus-5-5' } })
    expect(wrapper.text()).not.toContain('Mock LLM')
    expect(wrapper.text()).toContain('claude-opus-5-5')
  })

  it('says the model is loading, inside a polite live region', async () => {
    const wrapper = await render({ status: 'loading' })
    expect(wrapper.find('[aria-live="polite"]').text()).toContain('Model bilgisi yükleniyor')
  })

  it('offers a retry when the configuration could not be loaded', async () => {
    const wrapper = await render({ status: 'error', error: new Error('down') })
    expect(wrapper.text()).toContain('Sunucuya ulaşılamıyor')
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
  })

  it('links to the campaign list and to a new campaign, and marks the current page', async () => {
    const wrapper = await render({ status: 'loading' })
    const links = wrapper.findAll('a')
    const hrefs = links.map((a) => a.attributes('href'))
    expect(hrefs).toContain('/')
    expect(hrefs).toContain('/campaigns/new')
    expect(wrapper.get('nav a[aria-current="page"]').text()).toBe('Kampanyalar')
  })
})
