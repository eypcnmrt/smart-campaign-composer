import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  approveCampaign,
  createCampaign,
  getCampaign,
  getPreview,
  getStats,
  listCampaigns,
  parseCampaign,
  saveContent,
  setChannels,
} from './campaigns'
import { ApiError } from './client'

const campaign = {
  id: 7,
  brief: 'Ankara’daki müşterilere indirim',
  status: 'content_ready',
  version: 4,
  channels: ['sms', 'email'],
  segment: { conditions: [], explanation: 'Ankara', unsupported_criteria: [], requested_channels: [] },
  audience: { matched: 12, sms: 8, email: 12, total_users: 24, counted_at: '2026-11-14T12:00:00Z' },
  warnings: [],
  content: { sms: 'a', email_subject: 'b', email_body: 'c' },
  sms_info: { encoding: 'GSM-7', units: 1, segments: 1, single: 160, multi: 153 },
  content_origin: 'generated',
  segment_changed_after_content: false,
  approved_at: null,
  completed_at: null,
  created_at: '2026-11-14T12:00:00Z',
  updated_at: '2026-11-14T12:00:00Z',
}

const counts = { queued: 0, sending: 0, retrying: 0, sent: 5, failed: 0, skipped: 1, total: 6 }
const provider = { state: 'ok', circuit: 'closed', last_error: '', updated_at: null, age_seconds: null }
const stats = { campaign_id: 7, status: 'sending', channels: { sms: { ...counts, provider } } }

function json(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' }, ...init })
}

function stubFetch(body: unknown) {
  const fn = vi.fn(async (_input: string, _init?: RequestInit) => json(body))
  vi.stubGlobal('fetch', fn)
  return fn
}

afterEach(() => {
  vi.unstubAllGlobals()
})

async function rejection(promise: Promise<unknown>): Promise<ApiError> {
  const err = await promise.then(
    () => null,
    (e: unknown) => e,
  )
  expect(err).toBeInstanceOf(ApiError)
  return err as ApiError
}

describe('the campaign endpoints', () => {
  it('create: POST /api/campaigns with the brief', async () => {
    const fetchFn = stubFetch(campaign)
    expect((await createCampaign('brief')).id).toBe(7)
    const [path, init] = fetchFn.mock.calls[0] ?? []
    expect(path).toBe('/api/campaigns')
    expect(init?.method).toBe('POST')
    expect(init?.body).toBe('{"brief":"brief"}')
  })

  it('get: GET /api/campaigns/{id}', async () => {
    const fetchFn = stubFetch(campaign)
    await getCampaign(7)
    expect(fetchFn.mock.calls[0]?.[0]).toBe('/api/campaigns/7')
  })

  it('set channels: PUT with the list', async () => {
    const fetchFn = stubFetch(campaign)
    await setChannels(7, ['email'])
    const [path, init] = fetchFn.mock.calls[0] ?? []
    expect(path).toBe('/api/campaigns/7/channels')
    expect(init?.method).toBe('PUT')
    expect(init?.body).toBe('{"channels":["email"]}')
  })

  it('save content: PUT with the three fields', async () => {
    const fetchFn = stubFetch(campaign)
    await saveContent(7, { sms: 'a', email_subject: 'b', email_body: 'c' })
    expect(fetchFn.mock.calls[0]?.[0]).toBe('/api/campaigns/7/content')
    expect(fetchFn.mock.calls[0]?.[1]?.body).toBe('{"sms":"a","email_subject":"b","email_body":"c"}')
  })

  it('approve: sends the reviewed version and the acknowledgement', async () => {
    const fetchFn = stubFetch({ campaign: { ...campaign, status: 'sending' }, enqueued: { sms: 8, email: 12 } })
    const res = await approveCampaign(7, 4, true)
    expect(fetchFn.mock.calls[0]?.[1]?.body).toBe('{"version":4,"acknowledge_warnings":true}')
    expect(res.campaign.status).toBe('sending')
    expect(res.enqueued).toEqual({ sms: 8, email: 12 })
  })

  it('preview: asks for a number of samples and returns them', async () => {
    const sample = { user: { id: 1, first_name: 'Ayşe', city: 'Ankara' }, sms: 'Merhaba Ayşe', email_subject: null, email_body: null }
    const fetchFn = stubFetch({ samples: [sample] })
    expect(await getPreview(7, 2)).toEqual([sample])
    expect(fetchFn.mock.calls[0]?.[0]).toBe('/api/campaigns/7/preview?limit=2')
  })

  it('stats: returns the counts per channel', async () => {
    stubFetch(stats)
    expect((await getStats(7)).channels.sms?.sent).toBe(5)
  })

  it('list: returns the summaries', async () => {
    const fetchFn = stubFetch({ campaigns: [{ id: 7, brief: 'b', status: 'draft', warning_count: 0, audience: null }] })
    expect(await listCampaigns(20)).toHaveLength(1)
    expect(fetchFn.mock.calls[0]?.[0]).toBe('/api/campaigns?limit=20')
  })
})

describe('a response that does not match the contract is a bad_response', () => {
  it.each([
    ['a campaign without a status', { ...campaign, status: undefined }],
    ['a campaign in an unknown status', { ...campaign, status: 'paused' }],
    ['a campaign whose channels are not strings', { ...campaign, channels: [1] }],
    ['a campaign with content that is not three strings', { ...campaign, content: { sms: 'a' } }],
    ['a campaign without the segment hint', { ...campaign, segment_changed_after_content: undefined }],
    ['a list item that is not a campaign', 5],
  ])('%s', async (_name, body) => {
    stubFetch(body)
    expect((await rejection(getCampaign(7))).code).toBe('bad_response')
  })

  it('a list that is not a list', async () => {
    stubFetch({ campaigns: 'x' })
    expect((await rejection(listCampaigns())).code).toBe('bad_response')
  })

  it('statistics with an unknown provider state', async () => {
    stubFetch({ ...stats, channels: { sms: { ...counts, provider: { ...provider, state: 'on_fire' } } } })
    expect((await rejection(getStats(7))).code).toBe('bad_response')
  })

  it('statistics with a missing count', async () => {
    stubFetch({ ...stats, channels: { sms: { ...counts, sent: undefined, provider } } })
    expect((await rejection(getStats(7))).code).toBe('bad_response')
  })

  it('an approval without the enqueued counts', async () => {
    stubFetch({ campaign })
    expect((await rejection(approveCampaign(7, 4, false))).code).toBe('bad_response')
  })

  it('a preview whose sample has no user', async () => {
    stubFetch({ samples: [{ sms: 'x' }] })
    expect((await rejection(getPreview(7))).code).toBe('bad_response')
  })
})

describe('parseCampaign', () => {
  it('accepts a draft: no segment, audience or content yet', () => {
    const draft = { ...campaign, status: 'draft', segment: null, audience: null, content: null, sms_info: null, content_origin: null }
    expect(parseCampaign(draft).status).toBe('draft')
  })
})
