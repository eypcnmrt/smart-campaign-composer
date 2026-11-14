import { effectScope, type EffectScope } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useCountdown } from './useCountdown'

let scope: EffectScope

beforeEach(() => {
  vi.useFakeTimers()
  scope = effectScope()
})

afterEach(() => {
  scope.stop()
  vi.useRealTimers()
})

/** The countdown lives in an effect scope, as it does in a component. */
function countdown() {
  const c = scope.run(() => useCountdown())
  if (!c) throw new Error('no countdown')
  return c
}

describe('useCountdown', () => {
  it('counts down in whole seconds, rounded up, and stops at zero', () => {
    const { remaining, start } = countdown()
    start(3)
    expect(remaining.value).toBe(3)
    vi.advanceTimersByTime(1000)
    expect(remaining.value).toBe(2)
    vi.advanceTimersByTime(2000)
    expect(remaining.value).toBe(0)
    vi.advanceTimersByTime(5000)
    expect(remaining.value).toBe(0)
  })

  it('goes by the clock, not by the number of ticks: a long pause (a background tab) catches up at once', () => {
    const { remaining, start } = countdown()
    start(30)
    vi.setSystemTime(Date.now() + 25_000)
    vi.advanceTimersByTime(250)
    expect(remaining.value).toBe(5)
  })

  it('does not count for zero, a negative number or nothing', () => {
    const { remaining, start } = countdown()
    start(0)
    expect(remaining.value).toBe(0)
    start(-4)
    expect(remaining.value).toBe(0)
    start(Number.NaN)
    expect(remaining.value).toBe(0)
  })

  it('starting again replaces the countdown that is running', () => {
    const { remaining, start } = countdown()
    start(10)
    vi.advanceTimersByTime(4000)
    start(2)
    expect(remaining.value).toBe(2)
    vi.advanceTimersByTime(2000)
    expect(remaining.value).toBe(0)
  })

  it('stop ends it and leaves the value', () => {
    const { remaining, start, stop } = countdown()
    start(10)
    stop()
    vi.advanceTimersByTime(5000)
    expect(remaining.value).toBe(10)
  })

  it('leaves no timer behind when its scope ends', () => {
    countdown().start(60)
    expect(vi.getTimerCount()).toBe(1)
    scope.stop()
    expect(vi.getTimerCount()).toBe(0)
  })
})
