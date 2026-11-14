import { onScopeDispose, shallowRef, toValue, watch, type MaybeRefOrGetter, type ShallowRef } from 'vue'
import { getCampaign } from '@/api/campaigns'
import type { Campaign } from '@/api/types'

export type CampaignLoad = 'loading' | 'ready' | 'error'

export interface CampaignStore {
  campaign: Readonly<ShallowRef<Campaign | null>>
  status: Readonly<ShallowRef<CampaignLoad>>
  /** What went wrong when `status` is 'error' (an ApiError, shown through describeError). */
  error: Readonly<ShallowRef<unknown>>
  /** Reads the campaign again from the server. The one on screen stays until the new one arrives. */
  load: () => Promise<void>
  /** Takes the campaign an action returned (a new segment, saved content, the approval) as the current one. */
  replace: (campaign: Campaign) => void
}

/**
 * The campaign on screen. The server holds the truth: every change comes back as a whole campaign, which replaces this one, so
 * the steps are always derived from what the server says. Loading again is also how a screen recovers from `version_mismatch`.
 * A request that is no longer wanted (the id changed, the page was left) is aborted and its answer ignored.
 */
export function useCampaign(id: MaybeRefOrGetter<number>): CampaignStore {
  const campaign = shallowRef<Campaign | null>(null)
  const status = shallowRef<CampaignLoad>('loading')
  const error = shallowRef<unknown>(null)
  let current: AbortController | null = null

  async function load(): Promise<void> {
    current?.abort()
    const mine = new AbortController()
    current = mine
    // A campaign that is already on screen stays there during a reload; only a first load shows the loading state.
    if (campaign.value === null) status.value = 'loading'
    try {
      const loaded = await getCampaign(toValue(id), mine.signal)
      if (mine.signal.aborted) return
      campaign.value = loaded
      error.value = null
      status.value = 'ready'
    } catch (err) {
      if (mine.signal.aborted) return
      error.value = err
      status.value = 'error'
    }
  }

  function replace(next: Campaign): void {
    if (next.id !== toValue(id)) return
    // An answer to a read that started before this action would be older than `next`: it must not land on top of it.
    current?.abort()
    current = null
    campaign.value = next
    error.value = null
    status.value = 'ready'
  }

  watch(
    () => toValue(id),
    () => {
      campaign.value = null
      status.value = 'loading'
      void load()
    },
    { immediate: true },
  )
  onScopeDispose(() => current?.abort())

  return { campaign, status, error, load, replace }
}
