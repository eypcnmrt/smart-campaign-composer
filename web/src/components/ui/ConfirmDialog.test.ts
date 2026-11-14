import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ConfirmDialog from './ConfirmDialog.vue'

const props = { title: 'Emin misiniz?', confirmLabel: 'Evet, yeniden üret' }

describe('ConfirmDialog', () => {
  it('is a native dialog with a title, and stays closed until it is opened', () => {
    const wrapper = mount(ConfirmDialog, { props: { ...props, open: false }, slots: { default: '<p>Metin kaybolur.</p>' }, attachTo: document.body })
    const dialog = wrapper.get('dialog').element as HTMLDialogElement
    expect(dialog.open).toBe(false)
    expect(wrapper.get('dialog').attributes('aria-labelledby')).toBe('confirm-title')
    expect(wrapper.get('#confirm-title').text()).toBe('Emin misiniz?')
    wrapper.unmount()
  })

  it('opens modally when `open` turns true and puts the focus on the cancel button, so Enter does not confirm', async () => {
    const wrapper = mount(ConfirmDialog, { props: { ...props, open: false }, attachTo: document.body })
    await wrapper.setProps({ open: true })
    await wrapper.vm.$nextTick()
    expect((wrapper.get('dialog').element as HTMLDialogElement).open).toBe(true)
    expect(document.activeElement).toBe(wrapper.findAll('button')[0]?.element)
    expect(wrapper.findAll('button')[0]?.text()).toBe('Vazgeç')
    wrapper.unmount()
  })

  it('closes when `open` turns false and gives the focus back to what had it', async () => {
    const opener = document.createElement('button')
    document.body.append(opener)
    opener.focus()
    const wrapper = mount(ConfirmDialog, { props: { ...props, open: false }, attachTo: document.body })
    await wrapper.setProps({ open: true })
    await wrapper.vm.$nextTick()
    await wrapper.setProps({ open: false })
    expect((wrapper.get('dialog').element as HTMLDialogElement).open).toBe(false)
    expect(document.activeElement).toBe(opener)
    wrapper.unmount()
    opener.remove()
  })

  it('says what was chosen: confirm, cancel, and Escape counts as cancel', async () => {
    const wrapper = mount(ConfirmDialog, { props: { ...props, open: true }, attachTo: document.body })
    const [cancel, confirm] = wrapper.findAll('button')
    await confirm?.trigger('click')
    await cancel?.trigger('click')
    await wrapper.get('dialog').trigger('cancel')
    expect(wrapper.emitted('confirm')).toHaveLength(1)
    expect(wrapper.emitted('cancel')).toHaveLength(2)
    wrapper.unmount()
  })

  it('uses the labels it was given', () => {
    const wrapper = mount(ConfirmDialog, { props: { ...props, open: false, cancelLabel: 'Hayır' }, attachTo: document.body })
    expect(wrapper.findAll('button').map((b) => b.text())).toEqual(['Hayır', 'Evet, yeniden üret'])
    wrapper.unmount()
  })
})
