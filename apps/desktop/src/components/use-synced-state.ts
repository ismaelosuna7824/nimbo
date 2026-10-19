import { useState } from "react"

/**
 * Local editable copy of an external value that resets whenever the external
 * value changes (adjusting state during render instead of in an effect).
 * `carry` builds the reset value from the new external value and the current
 * local copy, for local-only data that must survive a reset.
 */
export function useSyncedState<T>(value: T, carry?: (next: T, current: T) => T) {
  const [state, setState] = useState(value)
  const [prev, setPrev] = useState(value)
  if (!Object.is(prev, value)) {
    setPrev(value)
    setState(carry ? carry(value, state) : value)
  }
  return [state, setState] as const
}
