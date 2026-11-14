import { onScopeDispose, shallowRef, watch, type ShallowRef } from 'vue'

/**
 * The value of `source`, delayed until it has stopped changing for `ms`. For text that a screen reader announces (a character
 * counter near its limit): announcing every keystroke would make the page unusable, announcing the last state is enough.
 */
export function useDebounced<T>(source: () => T, ms: number): Readonly<ShallowRef<T>> {
  const out = shallowRef(source()) as ShallowRef<T>
  let timer: ReturnType<typeof setTimeout> | null = null
  watch(source, (value) => {
    if (timer !== null) clearTimeout(timer)
    timer = setTimeout(() => {
      out.value = value
      timer = null
    }, ms)
  })
  onScopeDispose(() => {
    if (timer !== null) clearTimeout(timer)
  })
  return out
}
