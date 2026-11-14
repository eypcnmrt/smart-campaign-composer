import { describe, expect, it } from 'vitest'
import type { CampaignStatus } from '@/api/types'
import { allowedSteps, defaultStep, newCampaignItems, resolveStep, stepItems, type StepId } from './steps'

const STATUSES: CampaignStatus[] = ['draft', 'audience_ready', 'content_ready', 'approved', 'sending', 'completed', 'completed_with_failures']

describe('where a campaign opens', () => {
  it.each([
    ['draft', 'brief'],
    ['audience_ready', 'audience'],
    ['content_ready', 'content'],
    ['sending', 'send'],
    ['completed', 'send'],
    ['completed_with_failures', 'send'],
  ] as const)('%s opens at %s', (status, step) => {
    expect(defaultStep(status)).toBe(step)
    expect(resolveStep(undefined, status)).toBe(step)
  })

  it('always opens at a step it allows', () => {
    for (const s of STATUSES) expect(allowedSteps(s)).toContain(defaultStep(s))
  })
})

describe('resolveStep', () => {
  it('keeps a requested step that the status allows', () => {
    expect(resolveStep('brief', 'content_ready')).toBe('brief')
    expect(resolveStep('approve', 'content_ready')).toBe('approve')
    expect(resolveStep('content', 'audience_ready')).toBe('content')
  })

  it('moves a step that is not open yet to the default one (no approval before there is content)', () => {
    expect(resolveStep('approve', 'audience_ready')).toBe('audience')
    expect(resolveStep('content', 'draft')).toBe('brief')
    expect(resolveStep('send', 'content_ready')).toBe('content')
  })

  it('moves every step of an approved campaign to the delivery', () => {
    for (const step of ['brief', 'audience', 'content', 'approve'] as StepId[]) expect(resolveStep(step, 'sending')).toBe('send')
  })

  it.each([[undefined], [null], [''], ['nonsense'], [['brief', 'audience']], [5]])('takes %j for no request', (requested) => {
    expect(resolveStep(requested, 'content_ready')).toBe('content')
  })
})

describe('stepItems', () => {
  const states = (current: StepId, status: CampaignStatus) => stepItems(current, status).map((s) => s.state)

  it('shows a fresh segment: the brief is done, the audience is where we are, the content can be opened', () => {
    expect(states('audience', 'audience_ready')).toEqual(['done', 'current', 'available', 'locked', 'locked'])
  })

  it('shows a campaign with content: only the delivery is locked', () => {
    expect(states('content', 'content_ready')).toEqual(['done', 'done', 'current', 'available', 'locked'])
    expect(states('approve', 'content_ready')).toEqual(['done', 'done', 'done', 'current', 'locked'])
  })

  it('shows a draft: only the brief', () => {
    expect(states('brief', 'draft')).toEqual(['current', 'locked', 'locked', 'locked', 'locked'])
  })

  it('shows a sending campaign: the earlier steps are done but cannot be opened, the delivery is the current one', () => {
    const items = stepItems('send', 'sending')
    expect(items.map((s) => s.state)).toEqual(['done', 'done', 'done', 'done', 'current'])
    expect(items.some((s) => s.link)).toBe(false)
  })

  it('shows a finished campaign: all done, the delivery is still the page', () => {
    expect(states('send', 'completed')).toEqual(['done', 'done', 'done', 'done', 'current'])
  })

  it('links the steps that can be opened, never the current one', () => {
    const items = stepItems('audience', 'content_ready')
    expect(items.filter((s) => s.link).map((s) => s.id)).toEqual(['brief', 'content', 'approve'])
  })

  it('numbers the steps from one', () => {
    expect(stepItems('brief', 'draft').map((s) => s.number)).toEqual([1, 2, 3, 4, 5])
  })
})

describe('newCampaignItems', () => {
  it('has the brief as the current step and nothing else open', () => {
    const items = newCampaignItems()
    expect(items.map((s) => s.state)).toEqual(['current', 'locked', 'locked', 'locked', 'locked'])
    expect(items.some((s) => s.link)).toBe(false)
  })
})
