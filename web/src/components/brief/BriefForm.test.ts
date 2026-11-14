import { mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { createAppRouter } from '@/router'
import BriefForm from './BriefForm.vue'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

type Props = InstanceType<typeof BriefForm>['$props']

async function render(props: Partial<Props> = {}) {
  const router = createAppRouter(createMemoryHistory())
  await router.push('/campaigns/new')
  await router.isReady()
  return mount(BriefForm, { props: { mode: 'create', limit: 2000, ...props } as Props, global: { plugins: [router] } })
}

const submitButton = (w: Awaited<ReturnType<typeof render>>) => w.get('button[type="submit"]')

describe('BriefForm, creating a campaign', () => {
  it('has a visible label for the textarea and a heading for the step', async () => {
    const wrapper = await render()
    expect(wrapper.get('h2').text()).toBe('Brief')
    expect(wrapper.get('label[for="brief-input"]').text()).toBe('Kampanya brief’i')
    expect(wrapper.find('textarea#brief-input').exists()).toBe(true)
  })

  it('does not set maxlength: it counts UTF-16 units, the limit is in characters', async () => {
    const wrapper = await render()
    expect(wrapper.get('textarea').attributes('maxlength')).toBeUndefined()
  })

  it('counts characters the way the server does: an emoji is one, and the text is trimmed first', async () => {
    const wrapper = await render({ limit: 10 })
    await wrapper.get('textarea').setValue('  😀😀😀  ')
    expect(wrapper.get('#brief-count').text()).toContain('3 / 10')
  })

  it('disables the button and says why while the brief is empty', async () => {
    const wrapper = await render()
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.get('#brief-problem').text()).toBe('Göndermek için brief yazın.')
    await wrapper.get('textarea').setValue('   ')
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
  })

  it('refuses a brief over the limit, in words and in the field’s state, and allows one at the limit', async () => {
    const wrapper = await render({ limit: 20 })
    await wrapper.get('textarea').setValue('a'.repeat(20))
    expect(submitButton(wrapper).attributes('disabled')).toBeUndefined()
    expect(wrapper.get('textarea').attributes('aria-invalid')).toBe('false')

    await wrapper.get('textarea').setValue('a'.repeat(23))
    expect(submitButton(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.get('#brief-problem').text()).toBe('Brief 3 karakter fazla; sınır 20.')
    expect(wrapper.get('textarea').attributes('aria-invalid')).toBe('true')
    expect(wrapper.get('#brief-count').classes()).toContain('counter--over')
  })

  it('submits the trimmed brief', async () => {
    const wrapper = await render()
    await wrapper.get('textarea').setValue('  İstanbul’daki müşterilere indirim  ')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')).toEqual([['İstanbul’daki müşterilere indirim']])
  })

  it('does not submit an empty brief even by pressing Enter in the form', async () => {
    const wrapper = await render()
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')).toBeUndefined()
  })

  it('offers examples while it is empty, fills the field from one, and stops offering once it is edited', async () => {
    const wrapper = await render()
    const examples = wrapper.findAll('.example')
    expect(examples).toHaveLength(3)
    await examples[1]?.trigger('click')
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe(examples[1]?.text())
    expect(wrapper.findAll('.example')).toHaveLength(3) // still an example: nothing of the user's to lose

    await wrapper.get('textarea').setValue((wrapper.get('textarea').element as HTMLTextAreaElement).value + ' Bir de hafta sonu.')
    expect(wrapper.find('.example').exists()).toBe(false)
  })

  it('says it is working while the model makes the audience, and locks the form', async () => {
    const wrapper = await render({ busy: true })
    await wrapper.get('textarea').setValue('x')
    expect(wrapper.get('form').attributes('aria-busy')).toBe('true')
    expect(wrapper.get('textarea').attributes('disabled')).toBeDefined()
    expect(submitButton(wrapper).text()).toBe('Kitle oluşturuluyor…')
    expect(wrapper.find('[role="status"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Model brief’i kitle kuralına çeviriyor')
  })

  it('announces the counter only near the limit, and only after typing has paused', async () => {
    const wrapper = await render({ limit: 100 })
    const live = () => wrapper.findAll('[role="status"]').find((n) => n.classes().includes('visually-hidden') && n.element.tagName === 'P')
    await wrapper.get('textarea').setValue('a'.repeat(50))
    vi.advanceTimersByTime(1000)
    await wrapper.vm.$nextTick()
    expect(live()?.text()).toBe('')

    await wrapper.get('textarea').setValue('a'.repeat(95))
    expect(live()?.text()).toBe('') // not yet: still typing
    vi.advanceTimersByTime(1000)
    await wrapper.vm.$nextTick()
    expect(live()?.text()).toBe('95 karakter yazıldı; sınır 100.')
  })

  it('shows a refused brief beside the field, not in the panel', async () => {
    const error = new ApiError({
      status: 422,
      code: 'validation_failed',
      message: 'English',
      details: [{ path: 'brief', code: 'too_long', message: 'English', params: { length: 2001, limit: 2000 } }],
    })
    const wrapper = await render({ error })
    expect(wrapper.get('#brief-problem').text()).toBe('2.001 karakter; sınır 2.000.')
    expect(wrapper.get('textarea').attributes('aria-invalid')).toBe('true')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('shows a brief with a damaged character beside the field, and a body that was not UTF-8 in the panel', async () => {
    const field = new ApiError({
      status: 422,
      code: 'validation_failed',
      message: 'English',
      details: [{ path: 'brief', code: 'invalid_utf8', message: 'English' }],
    })
    const refused = await render({ error: field })
    expect(refused.get('#brief-problem').text()).toBe('Metinde bozuk bir karakter (�) var; yeniden yazın.')
    expect(refused.find('[role="alert"]').exists()).toBe(false)

    const body = new ApiError({ status: 400, code: 'invalid_utf8', message: 'English' })
    const panel = await render({ error: body })
    expect(panel.get('[role="alert"]').text()).toContain('Metindeki karakterler bozuk geldi')
  })

  it('shows any other failure in the panel, with a link to the draft the server kept', async () => {
    const error = new ApiError({ status: 504, code: 'llm_timeout', message: 'English', details: { campaign_id: 12 } })
    const wrapper = await render({ error, draftId: 12 })
    expect(wrapper.get('[role="alert"]').text()).toContain('Model zamanında yanıt vermedi')
    const link = wrapper.get('a')
    expect(link.text()).toBe('Taslağı aç')
    expect(link.attributes('href')).toBe('/campaigns/12')
  })
})

describe('BriefForm, an existing campaign', () => {
  it('shows the brief as it was written, with a way to a new campaign, and no field to change it', async () => {
    const wrapper = await render({ mode: 'view', brief: 'Satır bir\nSatır iki' })
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.get('blockquote').text()).toBe('Satır bir\nSatır iki')
    expect(wrapper.get('a').attributes('href')).toBe('/campaigns/new')
  })

  it('shows a brief as text, never as markup', async () => {
    const wrapper = await render({ mode: 'view', brief: '<img src=x onerror=alert(1)>' })
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.get('blockquote').text()).toBe('<img src=x onerror=alert(1)>')
  })

  it('offers to make the audience again for a draft, and says why it is a draft', async () => {
    const wrapper = await render({ mode: 'draft', brief: 'b' })
    expect(wrapper.text()).toContain('Kitle henüz oluşturulamadı')
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
  })

  it('locks the retry while it runs', async () => {
    const wrapper = await render({ mode: 'draft', brief: 'b', busy: true })
    expect(wrapper.get('button').attributes('disabled')).toBeDefined()
    expect(wrapper.get('button').text()).toBe('Kitle oluşturuluyor…')
  })
})
