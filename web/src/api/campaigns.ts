import { badResponse, isRecord, isStringList, request } from './client'
import type {
  ApproveResponse,
  Campaign,
  CampaignStatus,
  CampaignSummary,
  ChannelCounts,
  Content,
  PreviewSample,
  ProviderState,
  Stats,
} from './types'

// One function per endpoint. Every response is checked for the fields the screens rely on before it is returned, so a
// server that answers with something else fails here, as a `bad_response`, and not later inside a component.

const STATUSES: readonly string[] = [
  'draft',
  'audience_ready',
  'content_ready',
  'approved',
  'sending',
  'completed',
  'completed_with_failures',
] satisfies CampaignStatus[]

const PROVIDER_STATES: readonly string[] = ['ok', 'provider_unreachable', 'worker_down'] satisfies ProviderState[]

function isNullOr(value: unknown, check: (v: unknown) => boolean): boolean {
  return value === null || check(value)
}

function isContent(value: unknown): value is Content {
  return (
    isRecord(value) &&
    typeof value.sms === 'string' &&
    typeof value.email_subject === 'string' &&
    typeof value.email_body === 'string'
  )
}

/** Checks the parts of a campaign the UI depends on. The rest is passed through as the server sent it. */
export function parseCampaign(data: unknown): Campaign {
  if (
    !isRecord(data) ||
    typeof data.id !== 'number' ||
    typeof data.brief !== 'string' ||
    typeof data.status !== 'string' ||
    !STATUSES.includes(data.status) ||
    typeof data.version !== 'number' ||
    !isStringList(data.channels) ||
    !Array.isArray(data.warnings) ||
    !isNullOr(data.segment, (s) => isRecord(s) && typeof s.explanation === 'string' && isStringList(s.unsupported_criteria)) ||
    !isNullOr(data.audience, (a) => isRecord(a) && typeof a.matched === 'number' && typeof a.total_users === 'number') ||
    !isNullOr(data.content, isContent) ||
    !isNullOr(data.sms_info, (s) => isRecord(s) && typeof s.segments === 'number') ||
    typeof data.segment_changed_after_content !== 'boolean'
  ) {
    throw badResponse('campaign')
  }
  return data as unknown as Campaign
}

function parseSummary(data: unknown): CampaignSummary {
  if (
    !isRecord(data) ||
    typeof data.id !== 'number' ||
    typeof data.brief !== 'string' ||
    typeof data.status !== 'string' ||
    !STATUSES.includes(data.status) ||
    typeof data.warning_count !== 'number' ||
    !isNullOr(data.audience, (a) => isRecord(a) && typeof a.matched === 'number')
  ) {
    throw badResponse('campaign list')
  }
  return data as unknown as CampaignSummary
}

const COUNT_KEYS = ['queued', 'sending', 'retrying', 'sent', 'failed', 'skipped', 'total'] as const

function parseChannelCounts(data: unknown): ChannelCounts {
  if (
    !isRecord(data) ||
    !COUNT_KEYS.every((k) => typeof data[k] === 'number') ||
    !isRecord(data.provider) ||
    typeof data.provider.state !== 'string' ||
    !PROVIDER_STATES.includes(data.provider.state)
  ) {
    throw badResponse('delivery statistics')
  }
  return data as unknown as ChannelCounts
}

export function parseStats(data: unknown): Stats {
  if (
    !isRecord(data) ||
    typeof data.campaign_id !== 'number' ||
    typeof data.status !== 'string' ||
    !STATUSES.includes(data.status) ||
    !isRecord(data.channels)
  ) {
    throw badResponse('delivery statistics')
  }
  for (const counts of Object.values(data.channels)) parseChannelCounts(counts)
  return data as unknown as Stats
}

function parsePreview(data: unknown): PreviewSample[] {
  if (!isRecord(data) || !Array.isArray(data.samples)) throw badResponse('preview')
  for (const s of data.samples) {
    if (!isRecord(s) || !isRecord(s.user) || typeof s.user.first_name !== 'string') throw badResponse('preview')
  }
  return data.samples as PreviewSample[]
}

/** Reads of the page's own state: the server answers these at once, so a slow one is a broken connection. */
const READ_TIMEOUT_MS = 10_000

const base = (id: number) => `/api/campaigns/${id}`

export async function listCampaigns(limit = 50, signal?: AbortSignal): Promise<CampaignSummary[]> {
  const data = await request<unknown>(`/api/campaigns?limit=${limit}`, { signal, timeoutMs: READ_TIMEOUT_MS })
  if (!isRecord(data) || !Array.isArray(data.campaigns)) throw badResponse('campaign list')
  return data.campaigns.map(parseSummary)
}

/** Creates the draft and, in the same request, generates its segment. A failed generation leaves the draft (details.campaign_id). */
export async function createCampaign(brief: string, signal?: AbortSignal): Promise<Campaign> {
  return parseCampaign(await request<unknown>('/api/campaigns', { method: 'POST', body: { brief }, signal }))
}

export async function getCampaign(id: number, signal?: AbortSignal): Promise<Campaign> {
  return parseCampaign(await request<unknown>(base(id), { signal, timeoutMs: READ_TIMEOUT_MS }))
}

/** (Re)generates the segment from the brief. */
export async function generateSegment(id: number, signal?: AbortSignal): Promise<Campaign> {
  return parseCampaign(await request<unknown>(`${base(id)}/segment`, { method: 'POST', signal }))
}

export async function setChannels(id: number, channels: string[], signal?: AbortSignal): Promise<Campaign> {
  return parseCampaign(await request<unknown>(`${base(id)}/channels`, { method: 'PUT', body: { channels }, signal }))
}

export async function saveContent(id: number, content: Content, signal?: AbortSignal): Promise<Campaign> {
  return parseCampaign(await request<unknown>(`${base(id)}/content`, { method: 'PUT', body: content, signal }))
}

export async function getPreview(id: number, limit = 3, signal?: AbortSignal): Promise<PreviewSample[]> {
  return parsePreview(await request<unknown>(`${base(id)}/preview?limit=${limit}`, { signal, timeoutMs: READ_TIMEOUT_MS }))
}

/** `version` is the version the marketer reviewed, as shown on screen. */
export async function approveCampaign(
  id: number,
  version: number,
  acknowledgeWarnings: boolean,
  signal?: AbortSignal,
): Promise<ApproveResponse> {
  const data = await request<unknown>(`${base(id)}/approve`, {
    method: 'POST',
    body: { version, acknowledge_warnings: acknowledgeWarnings },
    signal,
  })
  if (!isRecord(data) || !isRecord(data.enqueued)) throw badResponse('approval')
  return { campaign: parseCampaign(data.campaign), enqueued: data.enqueued as Record<string, number> }
}

export async function getStats(id: number, signal?: AbortSignal): Promise<Stats> {
  return parseStats(await request<unknown>(`${base(id)}/stats`, { signal, timeoutMs: READ_TIMEOUT_MS }))
}
