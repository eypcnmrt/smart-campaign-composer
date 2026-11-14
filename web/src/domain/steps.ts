import type { CampaignStatus } from '@/api/types'

// The steps of a campaign are derived from its status on the server, never from where the user clicked last, so a reload or a
// shared link opens where the campaign stands. The URL only says which of the allowed steps is shown (?step=).

export type StepId = 'brief' | 'audience' | 'content' | 'approve' | 'send'

export const STEPS: readonly { id: StepId; label: string }[] = [
  { id: 'brief', label: 'Brief' },
  { id: 'audience', label: 'Kitle' },
  { id: 'content', label: 'İçerik' },
  { id: 'approve', label: 'Onay' },
  { id: 'send', label: 'Gönderim' },
]

const IDS: readonly string[] = STEPS.map((s) => s.id)

/** Steps that may be opened. After approval the campaign is frozen: only the delivery is left to watch. */
const ALLOWED: Record<CampaignStatus, readonly StepId[]> = {
  draft: ['brief'],
  audience_ready: ['brief', 'audience', 'content'],
  content_ready: ['brief', 'audience', 'content', 'approve'],
  approved: ['send'],
  sending: ['send'],
  completed: ['send'],
  completed_with_failures: ['send'],
}

/** Where a campaign in this status opens when the URL names no step. */
const DEFAULT: Record<CampaignStatus, StepId> = {
  draft: 'brief',
  audience_ready: 'audience',
  content_ready: 'content',
  approved: 'send',
  sending: 'send',
  completed: 'send',
  completed_with_failures: 'send',
}

/** How many steps are finished in this status (a draft has finished none, a completed campaign all five). */
const FINISHED: Record<CampaignStatus, number> = {
  draft: 0,
  audience_ready: 1,
  content_ready: 3,
  approved: 4,
  sending: 4,
  completed: 5,
  completed_with_failures: 5,
}

export function allowedSteps(status: CampaignStatus): readonly StepId[] {
  return ALLOWED[status]
}

export function defaultStep(status: CampaignStatus): StepId {
  return DEFAULT[status]
}

/** The step to show: the requested one if it is allowed in this status, else the default one. */
export function resolveStep(requested: unknown, status: CampaignStatus): StepId {
  if (typeof requested === 'string' && IDS.includes(requested) && ALLOWED[status].includes(requested as StepId)) {
    return requested as StepId
  }
  return DEFAULT[status]
}

export type StepState = 'done' | 'current' | 'available' | 'locked'

export interface StepItem {
  id: StepId
  label: string
  /** 1-based position in the sequence. */
  number: number
  state: StepState
  /** Whether the step can be opened from the rail. The current step is not a link to itself. */
  link: boolean
}

/** The rail of a campaign: each step with its state, for the current step and status. */
export function stepItems(current: StepId, status: CampaignStatus): StepItem[] {
  const finished = FINISHED[status]
  return STEPS.map((s, i) => {
    const allowed = ALLOWED[status].includes(s.id)
    const state: StepState = s.id === current ? 'current' : i < finished ? 'done' : allowed ? 'available' : 'locked'
    return { id: s.id, label: s.label, number: i + 1, state, link: allowed && s.id !== current }
  })
}

/** The rail of a campaign that does not exist yet: the brief is being written, nothing else can be opened. */
export function newCampaignItems(): StepItem[] {
  return STEPS.map((s, i) => ({ id: s.id, label: s.label, number: i + 1, state: i === 0 ? 'current' : 'locked', link: false }))
}
