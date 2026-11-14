import { onScopeDispose, shallowRef, type Ref } from 'vue'

export interface Countdown {
  /** Whole seconds left, rounded up; 0 when there is no wait. */
  remaining: Readonly<Ref<number>>
  /** Starts counting down from `seconds`; zero or less ends it at once. Replaces a countdown that is running. */
  start: (seconds: number) => void
  stop: () => void
}

/**
 * A countdown for a wait the server asked for (Retry-After). It counts against the clock, not by adding up timer ticks, so a
 * tab that was in the background (where timers are slowed down) shows the right time when it comes back.
 */
export function useCountdown(): Countdown {
  const remaining = shallowRef(0)
  let timer: ReturnType<typeof setInterval> | null = null
  let deadline = 0

  function stop(): void {
    if (timer !== null) clearInterval(timer)
    timer = null
  }

  function tick(): void {
    const left = Math.ceil((deadline - Date.now()) / 1000)
    if (left <= 0) {
      remaining.value = 0
      stop()
    } else {
      remaining.value = left
    }
  }

  function start(seconds: number): void {
    stop()
    if (!(seconds > 0)) {
      remaining.value = 0
      return
    }
    deadline = Date.now() + seconds * 1000
    remaining.value = Math.ceil(seconds)
    timer = setInterval(tick, 250)
  }

  onScopeDispose(stop)
  return { remaining, start, stop }
}
