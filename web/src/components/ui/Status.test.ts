import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import PageColumn from './PageColumn.vue'
import StatusBadge from './StatusBadge.vue'
import StatusIcon, { type Tone } from './StatusIcon.vue'
import StatusNote from './StatusNote.vue'

const TONES: Tone[] = ['success', 'warning', 'danger', 'info', 'neutral']

describe('StatusIcon', () => {
  it('is hidden from assistive technology: the text carries the meaning', () => {
    const wrapper = mount(StatusIcon, { props: { tone: 'success' } })
    expect(wrapper.attributes('aria-hidden')).toBe('true')
  })

  it('draws a different shape for every tone, so no state depends on colour', () => {
    const shapes = TONES.map((tone) => mount(StatusIcon, { props: { tone } }).find('svg').element.innerHTML)
    expect(new Set(shapes).size).toBe(TONES.length)
  })
})

describe('StatusBadge', () => {
  it.each(TONES)('%s shows its icon and its text', (tone) => {
    const wrapper = mount(StatusBadge, { props: { tone, label: 'Gönderildi' } })
    expect(wrapper.text()).toBe('Gönderildi')
    expect(wrapper.find('svg[data-tone="' + tone + '"]').exists()).toBe(true)
    expect(wrapper.classes()).toContain(`badge--${tone}`)
  })
})

describe('StatusNote', () => {
  it.each([
    ['success', 'Başarılı'],
    ['warning', 'Uyarı'],
    ['danger', 'Hata'],
    ['info', 'Bilgi'],
    ['neutral', 'Durum'],
  ] as const)('%s names its state for screen readers, next to the icon', (tone, word) => {
    const wrapper = mount(StatusNote, { props: { tone, title: 'Başlık' }, slots: { default: 'Ayrıntı' } })
    expect(wrapper.get('.visually-hidden').text()).toBe(`${word}:`)
    expect(wrapper.text()).toContain('Başlık')
    expect(wrapper.text()).toContain('Ayrıntı')
    expect(wrapper.find(`svg[data-tone="${tone}"]`).exists()).toBe(true)
  })

  it('names the state even without a title', () => {
    const wrapper = mount(StatusNote, { props: { tone: 'warning' }, slots: { default: 'Kural herkesi kapsıyor.' } })
    expect(wrapper.get('.visually-hidden').text()).toBe('Uyarı:')
  })

  it('is a live region only when asked: an alert for errors, a status for the rest', () => {
    expect(mount(StatusNote, { props: { tone: 'danger' } }).attributes('role')).toBeUndefined()
    expect(mount(StatusNote, { props: { tone: 'danger', announce: true } }).attributes('role')).toBe('alert')
    expect(mount(StatusNote, { props: { tone: 'warning', announce: true } }).attributes('role')).toBe('status')
  })
})

describe('PageColumn', () => {
  it('is the narrow column by default and the wide one on request', () => {
    expect(mount(PageColumn).classes()).not.toContain('column--wide')
    expect(mount(PageColumn, { props: { wide: true } }).classes()).toContain('column--wide')
  })

  it('renders its content', () => {
    expect(mount(PageColumn, { slots: { default: '<p>içerik</p>' } }).text()).toBe('içerik')
  })
})
