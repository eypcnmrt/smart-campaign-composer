import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import BriefSummary from './BriefSummary.vue'

// happy-dom has no layout: the measurement is given by hand.
function cut(scrollHeight: number, clientHeight: number): void {
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(scrollHeight)
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(clientHeight)
}

afterEach(() => vi.restoreAllMocks())

describe('the brief above the steps', () => {
  it('shows the brief as plain text, and no button while it fits', () => {
    cut(48, 48)
    const w = mount(BriefSummary, { props: { brief: 'Kısa brief <b>x</b>' } })
    expect(w.get('.brief-summary__text').text()).toBe('Kısa brief <b>x</b>')
    expect(w.find('b').exists()).toBe(false)
    expect(w.find('button').exists()).toBe(false)
  })

  it('offers "Tamamını göster" when it is cut, opens it and folds it again', async () => {
    cut(120, 48)
    const w = mount(BriefSummary, { props: { brief: 'uzun '.repeat(80) } })
    await flushPromises()
    const button = w.get('button')
    expect(button.text()).toBe('Tamamını göster')
    expect(button.attributes('aria-expanded')).toBe('false')
    expect(w.get('.brief-summary__text').classes()).toContain('brief-summary__text--clamped')

    await button.trigger('click')
    expect(w.get('button').text()).toBe('Daha az göster')
    expect(w.get('button').attributes('aria-expanded')).toBe('true')
    expect(w.get('.brief-summary__text').classes()).not.toContain('brief-summary__text--clamped')

    await w.get('button').trigger('click')
    expect(w.get('button').text()).toBe('Tamamını göster')
  })
})
