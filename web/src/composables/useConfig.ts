import { shallowRef, type ShallowRef } from 'vue'
import { getConfig } from '@/api/client'
import type { Config } from '@/api/types'

export type ConfigState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ready'; config: Config }
  | { status: 'error'; error: Error }

export interface ConfigStore {
  state: Readonly<ShallowRef<ConfigState>>
  /** Loads the configuration once. Calling it again while it is loading shares the request; after an error it tries again. */
  load: () => Promise<void>
}

/** The configuration is read once at start-up (limits, variables, the model); a store per call keeps tests independent. */
export function createConfigStore(fetchConfig: () => Promise<Config>): ConfigStore {
  const state = shallowRef<ConfigState>({ status: 'idle' })
  let inflight: Promise<void> | null = null

  function load(): Promise<void> {
    if (state.value.status === 'ready') return Promise.resolve()
    if (inflight) return inflight
    state.value = { status: 'loading' }
    inflight = fetchConfig()
      .then(
        (config) => {
          state.value = { status: 'ready', config }
        },
        (error: unknown) => {
          state.value = { status: 'error', error: error instanceof Error ? error : new Error(String(error)) }
        },
      )
      .finally(() => {
        inflight = null
      })
    return inflight
  }

  return { state, load }
}

const store = createConfigStore(() => getConfig())

export function useConfig(): ConfigStore {
  return store
}
