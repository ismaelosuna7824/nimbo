import { lazy, useEffect, useReducer, useState, type ComponentType } from "react"

/** A code-split module loaded once on first need: by preload(), by its components or by useLazyModule(). */
export interface LazyModule<M> {
  /** Starts loading (once; a failed load is retried by the next call) and resolves with the module. */
  preload(): Promise<M>
  /** The module, once it has loaded. */
  loaded(): M | undefined
}

export function lazyModule<M>(load: () => Promise<M>): LazyModule<M> {
  let mod: M | undefined
  let pending: Promise<M> | undefined
  return {
    preload: () =>
      (pending ??= load().then(
        (m) => (mod = m),
        (err: unknown) => {
          pending = undefined
          throw err
        },
      )),
    loaded: () => mod,
  }
}

/**
 * A component of a lazy module. Mounted after the module loaded (preloaded), it renders the component directly; a
 * React.lazy wrapper would suspend once even then, showing the boundary's fallback and holding the reveal back by
 * React's Suspense throttle. Before that it is the React.lazy wrapper and suspends to the nearest boundary. The choice
 * is made once per mount, so a mounted instance never swaps component types (losing its state) when the module lands.
 */
export function lazyComponent<M, P extends object>(module: LazyModule<M>, pick: (m: M) => ComponentType<P>): ComponentType<P> {
  const Lazy = lazy(() => module.preload().then((m) => ({ default: pick(m) }))) as unknown as ComponentType<P>
  return function LazyModuleComponent(props: P) {
    const [Component] = useState(() => {
      const m = module.loaded()
      return m ? pick(m) : Lazy
    })
    return <Component {...props} />
  }
}

/** The module once loaded, loading it on mount; undefined until then (or while `module` is undefined). */
export function useLazyModule<M>(module: LazyModule<M> | undefined): M | undefined {
  const [, loadedNow] = useReducer((n: number) => n + 1, 0)
  useEffect(() => {
    if (module && !module.loaded()) void module.preload().then(() => loadedNow())
  }, [module])
  return module?.loaded()
}

/** Whether `flag` has ever been true: mounts a lazy dialog on its first open and keeps it for its close animation. */
export function useOnceTrue(flag: boolean): boolean {
  const [seen, setSeen] = useState(flag)
  if (flag && !seen) setSeen(true)
  return seen || flag
}
