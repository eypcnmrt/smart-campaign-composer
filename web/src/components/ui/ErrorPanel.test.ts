import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import ErrorPanel from './ErrorPanel.vue'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

function err(code: string, extra: { details?: unknown; retryAfterSeconds?: number | null } = {}) {
  return new ApiError({ status: 500, code, message: `English ${code}`, ...extra })
}

describe('ErrorPanel', () => {
  it('says what happened and what to do, in Turkish, as an alert', () => {
    const wrapper = mount(ErrorPanel, { props: { error: err('llm_timeout') } })
    expect(wrapper.attributes('role')).toBe('alert')
    expect(wrapper.text()).toContain('Model zamanında yanıt vermedi')
    expect(wrapper.text()).toContain('Hata:') // the hidden word for screen readers
    expect(wrapper.text()).not.toContain('English')
  })

  it('offers a retry for an error that can pass, and not for one that needs a different request', async () => {
    const retryable = mount(ErrorPanel, { props: { error: err('llm_timeout') } })
    await retryable.get('button').trigger('click')
    expect(retryable.emitted('retry')).toHaveLength(1)

    expect(mount(ErrorPanel, { props: { error: err('no_recipients') } }).find('button').exists()).toBe(false)
    expect(mount(ErrorPanel, { props: { error: err('llm_timeout'), showRetry: false } }).find('button').exists()).toBe(false)
  })

  it('counts a requested wait down on the button, which stays disabled until it is over', async () => {
    const wrapper = mount(ErrorPanel, { props: { error: err('llm_rate_limited', { retryAfterSeconds: 12 }) } })
    const button = wrapper.get('button')
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.text()).toContain('12 sn')

    vi.advanceTimersByTime(5000)
    await wrapper.vm.$nextTick()
    expect(button.text()).toContain('7 sn')

    vi.advanceTimersByTime(8000)
    await wrapper.vm.$nextTick()
    expect(button.attributes('disabled')).toBeUndefined()
    expect(button.text()).toBe('Tekrar dene')
    expect(wrapper.text()).toContain('Tekrar deneyebilirsiniz.') // said once, in a live region
  })

  it('starts a new countdown when a new error arrives', async () => {
    const wrapper = mount(ErrorPanel, { props: { error: err('llm_rate_limited', { retryAfterSeconds: 3 }) } })
    vi.advanceTimersByTime(3000)
    await wrapper.vm.$nextTick()
    expect(wrapper.get('button').attributes('disabled')).toBeUndefined()
    await wrapper.setProps({ error: err('llm_busy', { retryAfterSeconds: 5 }) })
    expect(wrapper.get('button').attributes('disabled')).toBeDefined()
    expect(wrapper.get('button').text()).toContain('5 sn')
  })

  it('lists the fields of a validation error with their Turkish names and sentences', () => {
    const wrapper = mount(ErrorPanel, {
      props: {
        error: err('validation_failed', {
          details: [{ path: 'sms', code: 'too_long', message: 'English', params: { length: 161, limit: 160 } }],
        }),
      },
    })
    expect(wrapper.text()).toContain('SMS metni:')
    expect(wrapper.text()).toContain('161 karakter; sınır 160.')
  })

  it('keeps the English problems of an invalid model output in a closed technical panel', () => {
    const wrapper = mount(ErrorPanel, {
      props: {
        error: err('llm_invalid_output', {
          details: { stage: 'content', attempts: 3, problems: ['sms: must not be empty'] },
        }),
      },
    })
    const details = wrapper.get('details')
    expect(details.attributes('open')).toBeUndefined()
    expect(details.get('summary').text()).toBe('Teknik ayrıntılar')
    expect(details.get('ul').attributes('lang')).toBe('en')
    expect(details.text()).toContain('sms: must not be empty')
    // The summary outside the panel is Turkish and does not repeat the English.
    expect(wrapper.get('.note__body > p:nth-of-type(2)').text()).toContain('3 denemede')
  })

  it('shows the request id of an internal error', () => {
    const wrapper = mount(ErrorPanel, { props: { error: err('internal', { details: { request_id: 'abc123' } }) } })
    expect(wrapper.text()).toContain('Hata kodu: abc123')
  })

  it('does not show a technical panel when there is nothing technical', () => {
    expect(mount(ErrorPanel, { props: { error: err('llm_timeout') } }).find('details').exists()).toBe(false)
  })

  it('shows an unknown code with the server’s message, so a new error is never silent', () => {
    const wrapper = mount(ErrorPanel, { props: { error: err('brand_new') } })
    expect(wrapper.text()).toContain('İşlem tamamlanamadı')
    expect(wrapper.get('details').text()).toContain('brand_new: English brand_new')
  })

  it('shows something for a thrown value that is not an API error', () => {
    const wrapper = mount(ErrorPanel, { props: { error: new TypeError('boom') } })
    expect(wrapper.text()).toContain('Beklenmeyen bir hata oluştu')
    expect(wrapper.get('details').text()).toContain('boom')
  })

  it('has a slot for what the screen can offer next', () => {
    const wrapper = mount(ErrorPanel, { props: { error: err('llm_timeout') }, slots: { default: '<a href="/x">Taslağı aç</a>' } })
    expect(wrapper.get('a').text()).toBe('Taslağı aç')
  })
})
