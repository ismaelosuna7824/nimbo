import { ChevronRightIcon } from "lucide-react"
import { createContext, memo, useContext, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react"
import { Separator } from "@/components/ui/separator"
import { cn } from "@/lib/utils"
import { useSectionOpen, useSectionState } from "./section-state"

// The structure every inspector shares (Terraform, Docker, Kubernetes): a scrolling body of collapsible sections, each
// remembered by type, with a sticky row of chips to jump between them once the body is long enough to scroll.

interface Entry {
  id: string
  label: string
  el: HTMLElement
}

interface Registry {
  register: (entry: Entry) => () => void
}

const RegistryContext = createContext<Registry | null>(null)

/** Fewer sections than this read fine by scrolling: no nav. */
const NAV_MIN_SECTIONS = 3

function byDomOrder(a: Entry, b: Entry) {
  return a.el.compareDocumentPosition(b.el) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1
}

/** The scrolling part of an inspector, under its header: holds the sections and their nav. */
export function InspectorBody({ children, className }: { children: React.ReactNode; className?: string }) {
  const scroller = useRef<HTMLDivElement>(null)
  const [entries, setEntries] = useState<Entry[]>([])
  // Stable: sections register once when they mount, so typing in a field never re-renders the nav.
  const registry = useMemo<Registry>(
    () => ({
      register(entry) {
        setEntries((list) => [...list.filter((e) => e.id !== entry.id), entry].sort(byDomOrder))
        return () => setEntries((list) => list.filter((e) => e !== entry))
      },
    }),
    [],
  )
  return (
    <div ref={scroller} className="relative min-h-0 flex-1 overflow-y-auto" data-slot="inspector-body">
      <InspectorSectionNav entries={entries} scroller={scroller} />
      <RegistryContext value={registry}>
        <div className={cn("flex flex-col gap-5 p-4", className)}>{children}</div>
      </RegistryContext>
    </div>
  )
}

function useScrollable(scroller: React.RefObject<HTMLDivElement | null>) {
  const [scrollable, setScrollable] = useState(false)
  useEffect(() => {
    const el = scroller.current
    if (!el) return
    const check = () => setScrollable(el.scrollHeight > el.clientHeight + 1)
    const observer = new ResizeObserver(check)
    observer.observe(el)
    // The content grows and shrinks as sections open and close.
    for (const child of el.children) observer.observe(child)
    check()
    return () => observer.disconnect()
  }, [scroller])
  return scrollable
}

/**
 * The section in view: the first one crossing the band under the nav (top 40% of the body), or the last one once the
 * body is scrolled to the end (a short last section never reaches the band). After a chip click the clicked section
 * stays current until the user scrolls by hand: the smooth scroll passes other sections on its way.
 */
function useActiveSection(entries: Entry[], scroller: React.RefObject<HTMLDivElement | null>, enabled: boolean) {
  const [active, setActive] = useState<string | null>(null)
  const pinned = useRef(false)
  useEffect(() => {
    const root = scroller.current
    if (!enabled || !root) return
    const visible = new Set<string>()
    const ids = new Map(entries.map((e) => [e.el, e.id]))
    const atEnd = () => root.scrollTop + root.clientHeight >= root.scrollHeight - 2
    const observer = new IntersectionObserver(
      (records) => {
        for (const r of records) {
          const id = ids.get(r.target as HTMLElement)
          if (!id) continue
          if (r.isIntersecting) visible.add(id)
          else visible.delete(id)
        }
        if (pinned.current || atEnd()) return
        const first = entries.find((e) => visible.has(e.id))
        if (first) setActive(first.id)
      },
      { root, rootMargin: "-48px 0px -60% 0px" },
    )
    for (const e of entries) observer.observe(e.el)
    const unpin = () => {
      pinned.current = false
    }
    const onScroll = () => {
      if (!pinned.current && atEnd()) setActive(entries[entries.length - 1].id)
    }
    root.addEventListener("wheel", unpin, { passive: true })
    root.addEventListener("touchmove", unpin, { passive: true })
    root.addEventListener("keydown", unpin)
    root.addEventListener("scroll", onScroll, { passive: true })
    return () => {
      observer.disconnect()
      root.removeEventListener("wheel", unpin)
      root.removeEventListener("touchmove", unpin)
      root.removeEventListener("keydown", unpin)
      root.removeEventListener("scroll", onScroll)
    }
  }, [entries, scroller, enabled])
  const pin = (id: string) => {
    pinned.current = true
    setActive(id)
  }
  return [active, pin] as const
}

/** Chips that jump to each section, highlighting the one in view. Only when there is something to jump over. */
const InspectorSectionNav = memo(function InspectorSectionNav({
  entries,
  scroller,
}: {
  entries: Entry[]
  scroller: React.RefObject<HTMLDivElement | null>
}) {
  const scrollable = useScrollable(scroller)
  const shown = scrollable && entries.length >= NAV_MIN_SECTIONS
  const [active, pin] = useActiveSection(entries, scroller, shown)
  const setOpen = useSectionState((s) => s.setOpen)
  if (!shown) return null
  const current = active ?? entries[0]?.id
  return (
    <nav
      aria-label="Inspector sections"
      className="sticky top-0 z-10 flex gap-1 overflow-x-auto border-b bg-background/95 px-4 py-1.5 backdrop-blur-sm [scrollbar-width:none]"
      data-testid="inspector-section-nav"
    >
      {entries.map((e) => (
        <button
          key={e.id}
          type="button"
          aria-current={e.id === current ? "true" : undefined}
          className={cn(
            "h-6 shrink-0 rounded-full px-2.5 text-[11px] font-medium whitespace-nowrap text-muted-foreground transition-colors",
            "hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none",
            "aria-[current=true]:bg-primary/10 aria-[current=true]:text-primary",
          )}
          onClick={() => {
            // Jumping to a closed section means wanting to see it.
            if (e.el.querySelector(":scope > div > h3 > button[aria-expanded=false]")) setOpen(e.id, true)
            pin(e.id)
            const still = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches
            e.el.scrollIntoView({ block: "start", behavior: still ? "auto" : "smooth" })
          }}
        >
          {e.label}
        </button>
      ))}
    </nav>
  )
})

/**
 * A top-level inspector section: a heading that collapses it (open/closed remembered per `id` across nodes and
 * restarts), an anchor for the nav and its content. `id` names the section's type, e.g. "settings" or "docker:ports".
 * Pass `icon` as a constant element: the heading is memoized, so typing in the section's fields leaves it alone.
 */
export function InspectorSection({
  id,
  title,
  navLabel,
  icon,
  action,
  meta,
  defaultOpen = true,
  forceOpen = false,
  className,
  children,
  "data-testid": testId,
}: {
  id: string
  title: string
  /** Shorter name for the nav chip (defaults to the title). */
  navLabel?: string
  icon?: React.ReactNode
  /** Controls on the heading's right (stay visible when collapsed). */
  action?: React.ReactNode
  /** A quiet summary next to the title, e.g. "3 set". */
  meta?: React.ReactNode
  defaultOpen?: boolean
  /** Keeps it open whatever the user chose, e.g. while a search matches inside. */
  forceOpen?: boolean
  className?: string
  children: React.ReactNode
  "data-testid"?: string
}) {
  const [userOpen] = useSectionOpen(id, defaultOpen)
  const open = userOpen || forceOpen
  const ref = useRef<HTMLElement>(null)
  const contentId = useId()
  const registry = useContext(RegistryContext)
  const label = navLabel ?? title
  useLayoutEffect(() => registry?.register({ id, label, el: ref.current! }), [registry, id, label])
  return (
    <section ref={ref} id={`inspector-section-${id}`} data-inspector-section={id} className="flex scroll-mt-12 flex-col gap-3" data-testid={testId}>
      <SectionHeading id={id} title={title} icon={icon} meta={meta} action={action} open={open} contentId={contentId} />
      {open && (
        <div id={contentId} className={cn("flex flex-col gap-3", className)}>
          {children}
        </div>
      )}
    </section>
  )
}

const SectionHeading = memo(function SectionHeading({
  id,
  title,
  icon,
  meta,
  action,
  open,
  contentId,
}: {
  id: string
  title: string
  icon?: React.ReactNode
  meta?: React.ReactNode
  action?: React.ReactNode
  open: boolean
  contentId: string
}) {
  const setOpen = useSectionState((s) => s.setOpen)
  return (
    <div className="flex min-h-6 items-center gap-2">
      <h3 className="min-w-0">
        <button
          type="button"
          aria-expanded={open}
          aria-controls={open ? contentId : undefined}
          onClick={() => setOpen(id, !open)}
          className="-ml-1 flex min-w-0 items-center gap-1.5 rounded-md px-1 text-[13px] font-semibold tracking-[-0.005em] hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
        >
          <ChevronRightIcon aria-hidden className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")} />
          {icon}
          <span className="truncate">{title}</span>
        </button>
      </h3>
      {meta && <span className="shrink-0 text-[11px] text-muted-foreground">{meta}</span>}
      <Separator className="flex-1 opacity-70" />
      {action}
    </div>
  )
})

/** A titled part inside a section (e.g. Ports inside "Ports & networking"): a label, never collapsible. */
export function InspectorSubsection({
  title,
  icon,
  action,
  children,
  className,
  "data-testid": testId,
}: {
  title: string
  icon?: React.ReactNode
  action?: React.ReactNode
  children: React.ReactNode
  className?: string
  "data-testid"?: string
}) {
  return (
    <section className={cn("flex flex-col gap-2", className)} data-testid={testId}>
      <div className="flex min-h-5 items-center gap-1.5 text-muted-foreground">
        {icon}
        <h4 className="text-xs font-medium text-foreground/80">{title}</h4>
        <div className="ml-auto flex items-center gap-1">{action}</div>
      </div>
      {children}
    </section>
  )
}
