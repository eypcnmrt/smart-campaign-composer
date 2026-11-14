// Types of the API contract. They mirror the Go structs one to one (backend/internal/api and campaign); field names are the
// JSON names, which is why they are snake_case.

/** Which client the API uses for generation: the real model or the demo mock. */
export type LlmMode = 'mock' | 'anthropic'

/** GET /api/config: everything the UI would otherwise hard-code. */
export interface Config {
  llm_mode: LlmMode
  model: string
  limits: {
    sms: number
    email_subject: number
    email_body: number
    brief: number
  }
  /** Whitelisted personalization variables, without braces. */
  variables: string[]
  channels: string[]
}

/** The body of every error response. `details` depends on `code`. */
export interface ErrorEnvelope {
  error: {
    code: string
    message: string
    details: unknown
  }
}

export type Channel = 'sms' | 'email'

/** `approved` exists only inside the approval transaction; the API never shows it. */
export type CampaignStatus =
  | 'draft'
  | 'audience_ready'
  | 'content_ready'
  | 'approved'
  | 'sending'
  | 'completed'
  | 'completed_with_failures'

/** The rule as validated by the server. `conditions` stays raw (see domain/ruleText.ts, which reads it defensively). */
export interface Segment {
  conditions: unknown
  explanation: string
  /** Parts of the brief the model could not express as a rule. */
  unsupported_criteria: string[]
  /** The channels the brief named; empty means it named none. */
  requested_channels: string[]
}

/** The counts of one moment, not live. */
export interface Audience {
  matched: number
  sms: number
  email: number
  total_users: number
  counted_at: string
}

export type WarningCode = 'matches_everyone' | 'unsupported_criteria' | 'no_recipients'

export interface Warning {
  /** Usually a WarningCode; a code this UI does not know is shown with the server's message. */
  code: string
  message: string
  /** For unsupported_criteria: the parts of the brief that were not understood. */
  details?: string[]
}

export interface Content {
  sms: string
  email_subject: string
  email_body: string
}

export type ContentField = keyof Content

export type SmsEncoding = 'GSM-7' | 'UCS-2'

export interface SmsInfo {
  encoding: SmsEncoding
  /** Septets (GSM-7) or UTF-16 code units (UCS-2). */
  units: number
  /** Messages needed; 0 for an empty text. */
  segments: number
  /** Capacity of a one-part message. */
  single: number
  /** Capacity of each part of a longer message. */
  multi: number
}

export type ContentOrigin = 'generated' | 'edited'

/** GET /api/campaigns/{id} and every change of it. */
export interface Campaign {
  id: number
  brief: string
  status: CampaignStatus
  version: number
  channels: string[]
  /** null while the campaign is a draft. */
  segment: Segment | null
  audience: Audience | null
  /** What must be acknowledged to approve; after approval, what was acknowledged. */
  warnings: Warning[]
  /** null until there is content. */
  content: Content | null
  sms_info: SmsInfo | null
  content_origin: ContentOrigin | null
  /** True while the segment is newer than the last content save: the text was written for another audience. */
  segment_changed_after_content: boolean
  approved_at: string | null
  completed_at: string | null
  created_at: string
  updated_at: string
}

/** One row of GET /api/campaigns. */
export interface CampaignSummary {
  id: number
  brief: string
  status: CampaignStatus
  version: number
  channels: string[]
  audience: Audience | null
  warning_count: number
  approved_at: string | null
  completed_at: string | null
  created_at: string
  updated_at: string
}

export interface PreviewSample {
  user: { id: number; first_name: string; city: string }
  /** null for a channel the campaign does not use or the user did not consent to. */
  sms: string | null
  email_subject: string | null
  email_body: string | null
}

export interface ApproveResponse {
  campaign: Campaign
  /** Deliveries queued per channel. */
  enqueued: Record<string, number>
}

export type ProviderState = 'ok' | 'provider_unreachable' | 'worker_down'

export interface ProviderInfo {
  state: ProviderState
  circuit: string
  last_error: string
  updated_at: string | null
  age_seconds: number | null
}

export interface ChannelCounts {
  queued: number
  sending: number
  retrying: number
  sent: number
  failed: number
  skipped: number
  total: number
  provider: ProviderInfo
}

/** GET /api/campaigns/{id}/stats. */
export interface Stats {
  campaign_id: number
  status: CampaignStatus
  channels: Record<string, ChannelCounts>
}

/** details of validation_failed: one entry per invalid field. `message` is English; the UI translates `code` and `params`. */
export interface FieldProblem {
  path: string
  code: string
  message: string
  params?: Record<string, unknown>
}

/** The events of GET .../content/stream. `done` and `error` end the stream, exactly one of them arrives. */
export type StreamEvent =
  | { type: 'attempt'; attempt: number }
  | { type: 'delta'; field: ContentField; text: string }
  | { type: 'raw'; text: string }
  | { type: 'done'; campaign: Campaign }
  | { type: 'error'; error: ErrorEnvelope['error'] }
