import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import FieldEditor from './FieldEditor.vue'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

type Props = InstanceType<typeof FieldEditor>['$props']

function render(props: Partial<Props> & { modelValue?: string } = {}) {
  return mount(FieldEditor, {
    props: {
      field: 'sms',
      label: 'SMS metni',
      limit: 160,
      variables: ['first_name', 'city'],
      multiline: true,
      modelValue: '',
      'onUpdate:modelValue': (v: string) => wrapperRef.setProps({ modelValue: v }),
      ...props,
    } as Props,
    attachTo: document.body,
  })
}

// The parent keeps the value: v-model is wired by hand so typing in the test updates the editor like it does in the app.
let wrapperRef: ReturnType<typeof render>
function renderBound(props: Partial<Props> & { modelValue?: string } = {}) {
  wrapperRef = render(props)
  return wrapperRef
}

describe('FieldEditor, the field and its counter', () => {
  it('has a visible label tied to the control', () => {
    const wrapper = renderBound()
    const label = wrapper.get('label')
    expect(label.text()).toBe('SMS metni')
    expect(wrapper.get(`#${label.attributes('for')}`).element.tagName).toBe('TEXTAREA')
  })

  it('is a single-line input for a field that is not multiline', () => {
    const wrapper = renderBound({ field: 'email_subject', label: 'E-posta konusu', multiline: false, limit: 80 })
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.find('input[type="text"]').exists()).toBe(true)
  })

  it('does not set maxlength: it counts UTF-16 units and would cut an emoji in half', () => {
    expect(renderBound().get('textarea').attributes('maxlength')).toBeUndefined()
    expect(renderBound({ multiline: false }).get('input').attributes('maxlength')).toBeUndefined()
  })

  it('counts characters the way the server does: an emoji is one, a combining mark is one more', () => {
    const wrapper = renderBound({ modelValue: '😀😀i̇' }) // 2 emoji, i + U+0307 = 4 code points
    expect(wrapper.get('.editor__count').text()).toContain('4 / 160')
    expect(wrapper.get('.editor__count').classes()).toContain('num')
  })

  it('says how far over the limit a text is, in words and in the field’s state, and does not cut it', async () => {
    const wrapper = renderBound({ limit: 10, modelValue: 'a'.repeat(13) })
    expect(wrapper.get('.editor__count').classes()).toContain('editor__count--over')
    expect(wrapper.get('textarea').attributes('aria-invalid')).toBe('true')
    expect(wrapper.get('.editor__problems').text()).toContain('13 karakter; sınır 10.')
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toHaveLength(13)
  })

  it('accepts a text exactly at the limit', () => {
    const wrapper = renderBound({ limit: 10, modelValue: 'a'.repeat(10) })
    expect(wrapper.get('textarea').attributes('aria-invalid')).toBe('false')
    expect(wrapper.get('.editor__problems').text()).toBe('')
  })

  it('announces the counter only near the limit and only after typing has paused', async () => {
    const wrapper = renderBound({ limit: 100, modelValue: 'a'.repeat(50) })
    const live = () => wrapper.get('p[role="status"]')
    await wrapper.get('textarea').setValue('a'.repeat(95))
    expect(live().text()).toBe('') // still typing
    vi.advanceTimersByTime(1000)
    await wrapper.vm.$nextTick()
    expect(live().text()).toBe('95 karakter; sınır 100.')
  })
})

describe('FieldEditor, problems while typing', () => {
  it('flags an unknown variable at once, with its name and the ones that exist', async () => {
    const wrapper = renderBound({ modelValue: 'Merhaba' })
    await wrapper.get('textarea').setValue('Merhaba {{unknown}}')
    const text = wrapper.get('.editor__problems').text()
    expect(text).toContain('Bilinmeyen değişken {{unknown}}')
    expect(text).toContain('{{first_name}} ve {{city}}')
    expect(wrapper.get('textarea').attributes('aria-invalid')).toBe('true')
  })

  it('flags a stray brace and an unclosed variable', async () => {
    const wrapper = renderBound({ modelValue: 'x' })
    await wrapper.get('textarea').setValue('Selam { dünya')
    expect(wrapper.get('.editor__problems').text()).toContain('Tek başına {')
    await wrapper.get('textarea').setValue('Selam {{first_name')
    expect(wrapper.get('.editor__problems').text()).toContain('kapatılmamış')
  })

  it('flags an empty field', () => {
    expect(renderBound({ modelValue: '  ' }).get('.editor__problems').text()).toContain('Boş bırakılamaz.')
  })

  it('does not flag a text with the whitelisted variables', () => {
    const wrapper = renderBound({ modelValue: 'Merhaba {{first_name}}, {{city}} için indirim' })
    expect(wrapper.get('.editor__problems').text()).toBe('')
    expect(wrapper.get('textarea').attributes('aria-invalid')).toBe('false')
  })

  it('shows what the server said about the field beside it, without repeating what it already says', () => {
    const wrapper = renderBound({
      modelValue: 'a'.repeat(13),
      limit: 10,
      serverProblems: ['13 karakter; sınır 10.', 'Sunucudan başka bir cümle.'],
    })
    const lines = wrapper.findAll('.problem').map((p) => p.text())
    expect(lines.filter((l) => l.includes('13 karakter; sınır 10.'))).toHaveLength(1)
    expect(lines.some((l) => l.includes('Sunucudan başka bir cümle.'))).toBe(true)
  })

  it('marks a problem with a word for screen readers, not only red text', () => {
    expect(renderBound({ modelValue: '' }).get('.problem').text()).toContain('Hata:')
  })
})

describe('FieldEditor, variables', () => {
  it('has a button for each whitelisted variable, with a name that says where it goes', () => {
    const wrapper = renderBound()
    const buttons = wrapper.findAll('.variable')
    expect(buttons.map((b) => b.text())).toEqual(['{{first_name}}', '{{city}}'])
    expect(buttons[0]?.attributes('aria-label')).toBe('SMS metni alanına first_name değişkenini ekle')
  })

  it('inserts the variable at the caret and puts the caret after it', async () => {
    const wrapper = renderBound({ modelValue: 'Merhaba , hoş geldin' })
    const el = wrapper.get('textarea').element as HTMLTextAreaElement
    el.setSelectionRange(8, 8)
    await wrapper.get('.variable').trigger('click')
    await vi.waitFor(() => expect(el.value).toBe('Merhaba {{first_name}}, hoş geldin'))
    expect(el.selectionStart).toBe(8 + '{{first_name}}'.length)
    expect(document.activeElement).toBe(el)
  })

  it('replaces the selected text with the variable', async () => {
    const wrapper = renderBound({ modelValue: 'Merhaba müşteri' })
    const el = wrapper.get('textarea').element as HTMLTextAreaElement
    el.setSelectionRange(8, 15)
    await wrapper.get('.variable').trigger('click')
    await vi.waitFor(() => expect(el.value).toBe('Merhaba {{first_name}}'))
  })

  it('puts a space between the variable and a word that touches it, but not before punctuation', async () => {
    // Text that arrived from outside (the model wrote it): the caret is at the end, and the last word must not run into the variable.
    let wrapper = renderBound({ modelValue: 'Siparişiniz bekliyor' })
    let el = wrapper.get('textarea').element as HTMLTextAreaElement
    await wrapper.get('.variable').trigger('click')
    await vi.waitFor(() => expect(el.value).toBe('Siparişiniz bekliyor {{first_name}}'))

    wrapper = renderBound({ modelValue: 'Merhaba,hoş geldin' })
    el = wrapper.get('textarea').element as HTMLTextAreaElement
    el.setSelectionRange(8, 8)
    await wrapper.get('.variable').trigger('click')
    await vi.waitFor(() => expect(el.value).toBe('Merhaba, {{first_name}} hoş geldin'))
    // The caret is behind the variable, not behind the space that was added after it.
    expect(el.selectionStart).toBe(9 + '{{first_name}}'.length)

    wrapper = renderBound({ modelValue: '(), ' })
    el = wrapper.get('textarea').element as HTMLTextAreaElement
    el.setSelectionRange(1, 1)
    await wrapper.get('.variable').trigger('click')
    await vi.waitFor(() => expect(el.value).toBe('({{first_name}}), '))
  })

  it('inserts where the caret was when the field lost the focus, and hands the focus back', async () => {
    const wrapper = renderBound({ modelValue: 'Merhaba , hoş geldin' })
    const el = wrapper.get('textarea').element as HTMLTextAreaElement
    el.focus()
    el.setSelectionRange(8, 8)
    el.blur()
    // Something moved the caret while the field was out of focus (the browsers do this when the value is set).
    el.setSelectionRange(el.value.length, el.value.length)
    await wrapper.get('.variable').trigger('click')
    await vi.waitFor(() => expect(el.value).toBe('Merhaba {{first_name}}, hoş geldin'))
    expect(document.activeElement).toBe(el)
    expect(el.selectionStart).toBe(8 + '{{first_name}}'.length)
  })

  it('forgets the remembered caret when the text is replaced from outside', async () => {
    const wrapper = renderBound({ modelValue: 'Eski metin burada' })
    const el = wrapper.get('textarea').element as HTMLTextAreaElement
    el.focus()
    el.setSelectionRange(3, 3)
    el.blur()
    await wrapper.setProps({ modelValue: 'Yeni metin' })
    el.setSelectionRange(el.value.length, el.value.length)
    await wrapper.get('.variable').trigger('click')
    await vi.waitFor(() => expect(el.value).toBe('Yeni metin {{first_name}}'))
  })

  it('does not insert while locked', async () => {
    const wrapper = renderBound({ modelValue: 'x', disabled: true })
    expect(wrapper.get('.variable').attributes('disabled')).toBeDefined()
    expect(wrapper.get('textarea').attributes('disabled')).toBeDefined()
  })
})

describe('FieldEditor, the single-message hint', () => {
  it('tells an SMS that is a few units over to shorten by that many, and says nothing otherwise', async () => {
    const wrapper = renderBound({ modelValue: 'a'.repeat(163), limit: 1000 })
    expect(wrapper.get('.sms-hint').text()).toBe('Tek mesaja sığması için 3 birim kısaltın.')
    await wrapper.setProps({ modelValue: 'a'.repeat(160) })
    expect(wrapper.find('.sms-hint').exists()).toBe(false)
    await wrapper.setProps({ modelValue: 'a'.repeat(400) })
    expect(wrapper.find('.sms-hint').exists()).toBe(false)
  })

  it('is for the SMS only', () => {
    const wrapper = renderBound({ field: 'email_body', label: 'E-posta gövdesi', modelValue: 'a'.repeat(163), limit: 1000 })
    expect(wrapper.find('.sms-hint').exists()).toBe(false)
  })
})

describe('FieldEditor, how an SMS would be sent', () => {
  it('shows the encoding, the units and the messages for the SMS, as information', () => {
    const wrapper = renderBound({ modelValue: 'Merhaba dünya' })
    const info = wrapper.get('.sms-info').text()
    expect(info).toContain('GSM-7')
    expect(info).toContain('13 birim')
    expect(info).toContain('1 mesaj')
    expect(info).toContain('Yalnızca bilgi')
  })

  it('switches to UCS-2 for a Turkish letter that is not in the GSM alphabet, and counts the parts', () => {
    const wrapper = renderBound({ modelValue: 'ş'.repeat(71) })
    const info = wrapper.get('.sms-info').text()
    expect(info).toContain('UCS-2')
    expect(info).toContain('71 birim')
    expect(info).toContain('2 mesaj')
  })

  it('is shown for the SMS only', () => {
    expect(renderBound({ field: 'email_body', label: 'E-posta gövdesi', limit: 2000, modelValue: 'x' }).find('.sms-info').exists()).toBe(false)
  })

  it('does not stop a text that takes more than one message: it is a note, not a limit', () => {
    const wrapper = renderBound({ limit: 500, modelValue: 'a'.repeat(165) })
    expect(wrapper.get('textarea').attributes('aria-invalid')).toBe('false')
    expect(wrapper.get('.sms-info').text()).toContain('2 mesaj')
  })
})

describe('FieldEditor, model', () => {
  it('passes what is typed to the parent', async () => {
    const wrapper = renderBound()
    await wrapper.get('textarea').setValue('Yeni metin')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual(['Yeni metin'])
  })
})
