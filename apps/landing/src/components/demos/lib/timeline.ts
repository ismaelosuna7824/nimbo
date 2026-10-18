/**
 * Tiny scroll-triggered timeline for the landing demos.
 *
 * Convention: the server-rendered markup IS the final frame (what no-JS and
 * reduced-motion visitors see). Steps are registered with the time they run
 * at; each one knows how to undo itself, so a loop reset replays every undo
 * in reverse to reach the opening frame. The clock only advances while the
 * demo is on screen and the tab is visible.
 */
import { track } from "../../../lib/analytics";

type Target = string | Element | readonly Element[];

interface Step {
  at: number;
  dur: number;
  run: (p: number) => void;
  undo: () => void;
  done: boolean;
}

export interface DemoOptions {
  /** Runs on mount and on every resize (edge geometry, cursor targets). */
  layout?: () => void;
  /** Loop length in ms; defaults to the last step plus a hold. */
  length?: number;
}

const HOLD = 2800;
const FADE = 450;

export const easeOut = (p: number) => 1 - (1 - p) ** 3;

export class Timeline {
  readonly steps: Step[] = [];

  constructor(readonly root: HTMLElement) {}

  $<T extends Element = HTMLElement>(selector: string): T {
    const el = this.root.querySelector<T>(selector);
    if (!el) throw new Error(`demo ${this.root.dataset.demo}: missing ${selector}`);
    return el;
  }

  els(target: Target): HTMLElement[] {
    if (typeof target === "string") return [...this.root.querySelectorAll<HTMLElement>(target)];
    return (Array.isArray(target) ? target : [target]) as HTMLElement[];
  }

  /** End of the last step in ms. */
  get end(): number {
    return this.steps.reduce((max, s) => Math.max(max, s.at + s.dur), 0);
  }

  /** A one-off step; `undo` restores the state before it. Returns `at`. */
  add(at: number, run: () => void, undo: () => void): number {
    this.steps.push({ at, dur: 0, run, undo, done: false });
    return at;
  }

  /** A step that runs every frame for `dur` ms with progress 0..1; undo is run(0). Returns its end. */
  tween(at: number, dur: number, run: (p: number) => void): number {
    this.steps.push({ at, dur, run, undo: () => run(0), done: false });
    return at + dur;
  }

  /** Adds (`on`) or removes a class at `at`; undo does the opposite. */
  cls(at: number, target: Target, cls: string, on = true): number {
    const els = this.els(target);
    return this.add(
      at,
      () => els.forEach((el) => el.classList.toggle(cls, on)),
      () => els.forEach((el) => el.classList.toggle(cls, !on)),
    );
  }

  /** Reveals elements (they start hidden through `.is-off`). */
  show(at: number, target: Target): number {
    return this.cls(at, target, "is-off", false);
  }

  /** Hides elements that start visible. */
  hide(at: number, target: Target): number {
    return this.cls(at, target, "is-off", true);
  }

  /** Adds a class for `dur` ms (presses, flashes). */
  pulse(at: number, target: Target, cls: string, dur: number): number {
    this.cls(at, target, cls, true);
    return this.cls(at + dur, target, cls, false);
  }

  /** Sets `data-state`; reset restores `data-state0` from the markup. */
  state(at: number, target: Target, value: string): number {
    const els = this.els(target);
    return this.add(
      at,
      () => els.forEach((el) => (el.dataset.state = value)),
      () => els.forEach((el) => (el.dataset.state = el.dataset.state0 ?? "")),
    );
  }

  /** Types the element's text (`data-text`, else its rendered text) at `cps` characters per second. Returns the end. */
  type(at: number, target: Target, cps = 40): number {
    const el = this.els(target)[0]!;
    const text = el.dataset.text ?? el.textContent ?? "";
    return this.tween(at, (text.length / cps) * 1000, (p) => {
      el.textContent = text.slice(0, Math.round(p * text.length));
      el.classList.toggle("is-typing", p > 0 && p < 1);
    });
  }

  /**
   * Types `[data-line]` rows (text wrapped in `.ln-t`) one after another by
   * clipping them to `--n` characters, so syntax spans survive. Rows not
   * reached yet get `.is-pending`.
   */
  lines(at: number, target: Target, cps = 80): number {
    const rows = this.els(target).flatMap((el) => (el.matches("[data-line]") ? [el] : [...el.querySelectorAll<HTMLElement>("[data-line]")]));
    const lens = rows.map((r) => Math.max((r.querySelector(".ln-t") ?? r).textContent?.length ?? 0, 4));
    const total = lens.reduce((a, b) => a + b, 0);
    return this.tween(at, (total / cps) * 1000, (p) => {
      let left = p * total;
      rows.forEach((row, i) => {
        const len = lens[i]!;
        const n = Math.max(0, Math.min(len, left));
        left -= len;
        row.style.setProperty("--n", p >= 1 ? "9999" : String(Math.floor(n)));
        row.classList.toggle("is-typing", n > 0 && n < len);
        row.classList.toggle("is-pending", n === 0 && p < 1);
      });
    });
  }

  /** Counts a number up to `to`, formatting every frame. Returns the end. */
  count(at: number, dur: number, target: Target, to: number, format: (v: number) => string): number {
    const els = this.els(target);
    return this.tween(at, dur, (p) => {
      const text = format(to * easeOut(p));
      els.forEach((el) => (el.textContent = text));
    });
  }

  /** Glides a `[data-cursor]` element onto `target` (fx/fy: point inside it). Skips hidden targets. */
  cursor(at: number, cursor: HTMLElement, target: Target, fx = 0.5, fy = 0.5): number {
    return this.add(
      at,
      () => {
        const el = this.els(target)[0];
        const parent = cursor.offsetParent;
        if (!el || !parent || el.getClientRects().length === 0) return;
        const box = el.getBoundingClientRect();
        const base = parent.getBoundingClientRect();
        const x = box.left - base.left + box.width * fx - cursor.offsetLeft;
        const y = box.top - base.top + box.height * fy - cursor.offsetTop;
        cursor.style.translate = `${Math.round(x)}px ${Math.round(y)}px`;
      },
      () => (cursor.style.translate = ""),
    );
  }

  /** Cursor press feedback. */
  click(at: number, cursor: HTMLElement): number {
    return this.pulse(at, cursor, "is-down", 220);
  }
}

function start(root: HTMLElement, id: string, setup: (tl: Timeline) => DemoOptions | void): void {
  const tl = new Timeline(root);
  const opts = setup(tl) ?? {};
  const steps = tl.steps.sort((a, b) => a.at - b.at);
  const length = opts.length ?? tl.end + HOLD;
  const motion = matchMedia("(prefers-reduced-motion: reduce)");
  let t = 0;
  let raf = 0;
  let last = 0;
  let visible = false;
  let tracked = false;
  let fading = false;

  const reset = () => {
    root.classList.add("d-instant");
    for (let i = steps.length - 1; i >= 0; i--) {
      steps[i]!.undo();
      steps[i]!.done = false;
    }
    void root.offsetWidth;
    root.classList.remove("d-instant");
    t = 0;
  };

  const frame = (now: number) => {
    t += Math.min(now - last, 50);
    last = now;
    for (const s of steps) {
      if (s.at > t) break;
      if (s.done) continue;
      const p = s.dur ? Math.min(1, (t - s.at) / s.dur) : 1;
      s.run(p);
      if (p >= 1) s.done = true;
    }
    if (!fading && t >= length - FADE) {
      fading = true;
      root.classList.add("d-fade");
    }
    if (t >= length) {
      reset();
      fading = false;
      root.classList.remove("d-fade");
    }
    raf = requestAnimationFrame(frame);
  };

  const play = () => {
    if (raf || motion.matches || !visible || document.hidden) return;
    root.classList.remove("d-paused");
    last = performance.now();
    raf = requestAnimationFrame(frame);
  };

  const pause = () => {
    cancelAnimationFrame(raf);
    raf = 0;
    root.classList.add("d-paused");
  };

  const applyMotion = () => {
    pause();
    if (motion.matches) {
      for (const s of steps) {
        if (s.done) continue;
        s.run(1);
        s.done = true;
      }
      root.classList.add("d-static");
      root.classList.remove("d-fade");
      fading = false;
    } else {
      root.classList.remove("d-static");
      reset();
      play();
    }
  };

  root.classList.add("d-paused");
  if (motion.matches) root.classList.add("d-static");
  else reset();
  if (opts.layout) new ResizeObserver(opts.layout).observe(root);

  motion.addEventListener("change", applyMotion);
  document.addEventListener("visibilitychange", () => (document.hidden ? pause() : play()));
  new IntersectionObserver(
    ([entry]) => {
      visible = !!entry?.isIntersecting;
      if (visible && !tracked) {
        tracked = true;
        track("demo_view", { demo: id, lang: root.dataset.lang });
      }
      if (visible) play();
      else pause();
    },
    { threshold: 0.25 },
  ).observe(root);
}

/** Starts the timeline for every `[data-demo="<id>"]` on the page. */
export function mountDemo(id: string, setup: (tl: Timeline) => DemoOptions | void): void {
  for (const root of document.querySelectorAll<HTMLElement>(`[data-demo="${id}"]`)) start(root, id, setup);
}
