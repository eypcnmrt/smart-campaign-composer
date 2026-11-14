import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import LlmBadge from './LlmBadge.vue'

describe('LlmBadge', () => {
  it('shows the "Mock LLM" badge and the model name in mock mode', () => {
    const wrapper = mount(LlmBadge, { props: { mode: 'mock', model: 'mock' } })
    expect(wrapper.text()).toContain('Mock LLM')
    expect(wrapper.text()).toContain('Model: mock')
    // Formatting a wrapper's HTML loads js-beautify, whose patched dependencies are pinned through overrides.
    expect(wrapper.html()).toContain('llm__flag')
  })

  it('shows only the model name for the real model', () => {
    const wrapper = mount(LlmBadge, { props: { mode: 'anthropic', model: 'claude-opus-5-5' } })
    expect(wrapper.text()).not.toContain('Mock LLM')
    expect(wrapper.text()).toContain('Model: claude-opus-5-5')
  })
})
