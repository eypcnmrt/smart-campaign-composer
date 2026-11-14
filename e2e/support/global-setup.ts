import { request } from '@playwright/test'

// Runs once before the first test. A run that was killed (or `make e2e KEEP=1`, which reuses the stack) can leave the mock
// provider's outage switch on, and the next run would start against a provider that refuses everything. The tests turn it off
// again in their afterEach, but that needs the process to live; this closes it for the ones that did not.
const PROVIDER_URL = process.env.E2E_PROVIDER_URL ?? 'http://mock-provider:8081'

export default async function globalSetup(): Promise<void> {
  const api = await request.newContext()
  try {
    const state = await api.get(`${PROVIDER_URL}/control/outage`)
    if (!state.ok()) throw new Error(`the provider's outage switch answered ${state.status()}: this is not the e2e stack (MOCK_CONTROL is off)`)
    const wasOn = ((await state.json()) as { outage?: boolean }).outage === true
    const off = await api.post(`${PROVIDER_URL}/control/outage/off`)
    if (!off.ok()) throw new Error(`closing the outage failed: the switch answered ${off.status()}`)
    if (wasOn) console.log('global setup: an outage left on by an earlier run was closed')
    // The same for "the next N messages get a 429": a leftover would turn the retries of the first scenario into someone else's.
    const throttle = await api.post(`${PROVIDER_URL}/control/throttle-next/0`)
    if (!throttle.ok()) throw new Error(`clearing the forced 429s failed: the switch answered ${throttle.status()}`)
  } finally {
    await api.dispose()
  }
}
