import type { Audience, Campaign, Config } from '@/api/types'

// Test data shaped like the server's answers (backend/internal/campaign/campaign.go).

export const config: Config = {
  llm_mode: 'mock',
  model: 'mock',
  limits: { sms: 160, email_subject: 80, email_body: 2000, brief: 2000 },
  variables: ['first_name', 'city'],
  channels: ['sms', 'email'],
}

export const audience: Audience = { matched: 12, sms: 8, email: 12, total_users: 24, counted_at: '2026-11-14T12:00:00Z' }

/** A campaign that has its segment and audience but no content yet. */
export const audienceReady: Campaign = {
  id: 7,
  brief: 'Ankara’daki müşterilere indirim SMS’i gönder',
  status: 'audience_ready',
  version: 2,
  channels: ['sms', 'email'],
  segment: {
    conditions: [{ type: 'attribute', field: 'city', op: 'eq', value: 'Ankara' }],
    explanation: 'Ankara’da yaşayan müşteriler',
    unsupported_criteria: [],
    requested_channels: [],
  },
  audience,
  warnings: [],
  content: null,
  sms_info: null,
  content_origin: null,
  segment_changed_after_content: false,
  approved_at: null,
  completed_at: null,
  created_at: '2026-11-14T12:00:00Z',
  updated_at: '2026-11-14T12:00:00Z',
}

export const draft: Campaign = {
  ...audienceReady,
  status: 'draft',
  version: 1,
  segment: null,
  audience: null,
}

export const contentReady: Campaign = {
  ...audienceReady,
  status: 'content_ready',
  version: 3,
  content: { sms: 'Merhaba {{first_name}}, Ankara’ya özel %10 indirim!', email_subject: 'Ankara’ya özel indirim', email_body: 'Selam {{first_name}},\n\n{{city}} için indirim başladı.' },
  sms_info: { encoding: 'UCS-2', units: 49, segments: 1, single: 70, multi: 67 },
  content_origin: 'generated',
}

export const sending: Campaign = { ...contentReady, status: 'sending', version: 4, approved_at: '2026-11-14T12:05:00Z' }

export function campaign(overrides: Partial<Campaign> = {}): Campaign {
  return { ...audienceReady, ...overrides }
}
