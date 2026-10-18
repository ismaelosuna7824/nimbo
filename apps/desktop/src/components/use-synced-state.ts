import { useState } from "react"

/**
 * Local editable copy of an external value that resets whenever the external
 * value changes (adjusting state during render instead of in an effect).
 */
export function useSyncedState<T>(value: T) {
  const [state, setState] = useState(value)
  const [prev, setPrev] = useState(value)
  if (!Object.is(prev, value)) {
    setPrev(value)
    setState(value)
  }
  return [state, setState] as const
}
