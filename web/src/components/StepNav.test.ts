import { mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'
import { describe, expect, it } from 'vitest'
import { newCampaignItems, stepItems } from '@/domain/steps'
import { createAppRouter } from '@/router'
import StepNav from './StepNav.vue'

async function render(items: ReturnType<typeof stepItems>, campaignId: number | null = 7) {
  const router = createAppRouter(createMemoryHistory())
  await router.push('/')
  await router.isReady()
  return mount(StepNav, { props: { items, campaignId }, global: { plugins: [router] } })
}

describe('StepNav', () => {
  it('lists the five steps in order, in a labelled navigation', async () => {
    const wrapper = await render(stepItems('audience', 'audience_ready'))
    expect(wrapper.get('nav').attributes('aria-label')).toBe('Kampanya adımları')
    expect(wrapper.findAll('li').map((li) => li.get('.step__label').text())).toEqual(['Brief', 'Kitle', 'İçerik', 'Onay', 'Gönderim'])
  })

  it('marks the current step for assistive technology, and says each step’s state in words', async () => {
    const wrapper = await render(stepItems('audience', 'audience_ready'))
    const current = wrapper.findAll('[aria-current="step"]')
    expect(current).toHaveLength(1)
    expect(current[0]?.text()).toContain('Kitle')
    const states = wrapper.findAll('li').map((li) => li.text())
    expect(states[0]).toContain('tamamlandı')
    expect(states[1]).toContain('şu anki adım')
    expect(states[2]).toContain('açılabilir')
    expect(states[3]).toContain('henüz açık değil')
  })

  it('links the steps that can be opened with their step in the address, and nothing else', async () => {
    const wrapper = await render(stepItems('audience', 'content_ready'))
    const hrefs = wrapper.findAll('a').map((a) => a.attributes('href'))
    expect(hrefs).toEqual(['/campaigns/7?step=brief', '/campaigns/7?step=content', '/campaigns/7?step=approve'])
  })

  it('shows the finished steps with a check and the others with their number', async () => {
    const wrapper = await render(stepItems('content', 'content_ready'))
    const marks = wrapper.findAll('.step__mark')
    expect(marks.map((m) => m.find('svg').exists())).toEqual([true, true, false, false, false])
    expect(marks[2]?.text()).toBe('3')
  })

  it('has no links for a campaign that does not exist yet', async () => {
    const wrapper = await render(newCampaignItems(), null)
    expect(wrapper.findAll('a')).toHaveLength(0)
    expect(wrapper.get('[aria-current="step"]').text()).toContain('Brief')
  })

  it('has no links once the campaign is sending: the earlier steps are done and closed', async () => {
    const wrapper = await render(stepItems('send', 'sending'))
    expect(wrapper.findAll('a')).toHaveLength(0)
    expect(wrapper.findAll('li').map((li) => li.classes().find((c) => c.startsWith('step--')))).toEqual([
      'step--done',
      'step--done',
      'step--done',
      'step--done',
      'step--current',
    ])
  })
})
