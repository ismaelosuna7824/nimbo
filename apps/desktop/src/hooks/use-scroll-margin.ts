import { useLayoutEffect, useState, type RefObject } from "react"

/**
 * Offset of `list` from the top of `scroller`'s content: the `scrollMargin`
 * of a list virtualized inside a scroll container it shares with other
 * content. Follows content above the list that resizes, mounts or unmounts.
 */
export function useScrollMargin(list: RefObject<HTMLElement | null>, scroller: HTMLElement | null) {
  const [margin, setMargin] = useState(0)
  useLayoutEffect(() => {
    const el = list.current
    if (!el || !scroller) return
    const update = () =>
      setMargin(Math.round(el.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop))
    const resize = new ResizeObserver(update)
    const observeChildren = () => {
      for (const child of scroller.children) resize.observe(child)
    }
    const children = new MutationObserver(() => {
      observeChildren()
      update()
    })
    observeChildren()
    children.observe(scroller, { childList: true })
    update()
    return () => {
      resize.disconnect()
      children.disconnect()
    }
  }, [list, scroller])
  return margin
}
