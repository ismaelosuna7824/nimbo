/**
 * Drives the whole promo from the virtual clock (./virtual-clock.js):
 * scene cross-fades, caption and card reveals, which landing demo is playing
 * and how fast. `window.__seek(ms)` moves the video forward to `ms` and leaves
 * the page showing exactly that frame; `?t=<ms>` opens the page at a frame.
 */
import { CTA, DURATION, FPS, HOOK, POSTER_AT, SCENES, type DemoScene } from "./scenes";

interface VirtualClock {
  readonly now: number;
  advance(ms: number): void;
  setVisible(el: Element, visible: boolean): void;
  sync(): void;
  realFrame(): Promise<void>;
}

declare global {
  interface Window {
    __clock: VirtualClock;
    __seek: (ms: number) => void;
    __promo: { duration: number; fps: number; posterAt: number; ready: Promise<void> };
  }
}

/** Sub-step of the virtual clock (ms): demo steps land within one 60 fps frame of their time. */
const SUB = 1000 / 60;
/** A scene starts fading in LEAD ms before its start, while the previous one fades out over its last EXIT ms. */
const LEAD = 200;
const ENTER = 420;
const EXIT = 300;

const clock = window.__clock;
const ease = (p: number) => 1 - (1 - Math.min(1, Math.max(0, p))) ** 3;

interface Live {
  scene: DemoScene;
  el: HTMLElement;
  demo: HTMLElement;
  cap: HTMLElement;
  shot: HTMLElement;
  showAt: number;
  hideAt: number;
  state: "idle" | "playing" | "done";
}

interface Card {
  el: HTMLElement;
  start: number;
  end: number;
  items: { el: HTMLElement; at: number; pop: boolean }[];
}

function card(id: string, span: { start: number; end: number }): Card {
  const el = document.querySelector<HTMLElement>(`[data-scene="${id}"]`)!;
  const items = [...el.querySelectorAll<HTMLElement>("[data-in]")].map((item) => ({
    el: item,
    at: span.start + Number(item.dataset.in),
    pop: item.dataset.pop !== undefined,
  }));
  return { el, ...span, items };
}

const live: Live[] = SCENES.map((scene) => {
  const el = document.querySelector<HTMLElement>(`[data-scene="${scene.id}"]`)!;
  return {
    scene,
    el,
    demo: el.querySelector<HTMLElement>("[data-demo]")!,
    cap: el.querySelector<HTMLElement>(".pr-cap")!,
    shot: el.querySelector<HTMLElement>(".pr-shot")!,
    showAt: scene.start - LEAD,
    hideAt: scene.end + 100,
    state: "idle",
  };
});
const cards = [card("hook", HOOK), card("cta", CTA)];
const glow = document.querySelector<HTMLElement>(".pr-bg-glow")!;

let v = 0;

/** Opacity/offset of a scene: fades in from `start`, out before `end` (except the first/last). */
function presence(t: number, start: number, end: number, delay = 0): { o: number; y: number } {
  const pin = start === 0 ? 1 : ease((t - start + LEAD - delay) / ENTER);
  const out = Math.min(1, Math.max(0, (t - end + EXIT) / EXIT));
  const pout = end >= DURATION ? 0 : out * out;
  return { o: pin * (1 - pout), y: (1 - pin) * 22 - pout * 18 };
}

function place(el: HTMLElement, o: number, y: number): void {
  el.style.opacity = o.toFixed(4);
  el.style.translate = `0 ${y.toFixed(2)}px`;
}

function render(t: number): void {
  for (const l of live) {
    // Captions swap (the new one starts once the old one is gone); demo windows cross-dissolve.
    const cap = presence(t, l.scene.start, l.scene.end, LEAD);
    const shot = presence(t, l.scene.start, l.scene.end, 60);
    l.el.style.visibility = cap.o > 0 || shot.o > 0 ? "visible" : "hidden";
    place(l.cap, cap.o, cap.y);
    place(l.shot, shot.o, shot.y);
  }
  for (const c of cards) {
    const { o, y } = presence(t, c.start, c.end);
    c.el.style.visibility = o > 0 ? "visible" : "hidden";
    place(c.el, o, y);
    for (const item of c.items) {
      const p = ease((t - item.at) / 520);
      item.el.style.opacity = p.toFixed(4);
      item.el.style.translate = `0 ${((1 - p) * 16).toFixed(2)}px`;
      if (item.pop) item.el.style.scale = (0.82 + 0.18 * p).toFixed(4);
    }
  }
  // Slow drift of the background glow across the whole video.
  const k = t / DURATION;
  glow.style.translate = `${(k * 60 - 30).toFixed(2)}px ${(Math.sin(k * Math.PI * 2) * 18).toFixed(2)}px`;
}

function trigger(t: number): void {
  for (const l of live) {
    if (l.state === "idle" && t >= l.showAt) {
      l.state = "playing";
      clock.setVisible(l.demo, true);
    } else if (l.state === "playing" && t >= l.hideAt) {
      l.state = "done";
      clock.setVisible(l.demo, false);
    }
  }
}

function seek(ms: number): void {
  if (ms < v) throw new Error(`__seek is forward-only (at ${v} ms, asked ${ms} ms); reload with ?t=${ms}`);
  while (v < ms) {
    const step = Math.min(SUB, ms - v);
    trigger(v);
    const speed = SCENES.find((s) => v >= s.start && v < s.end)?.speed ?? 1;
    clock.advance(step * speed);
    v += step;
  }
  trigger(v);
  clock.sync();
  render(v);
}

/** Demos without a cursor are scaled down to fit the shot; cursor demos are cropped (fade at the bottom). */
function fit(): void {
  for (const l of live) {
    const room = l.shot.clientHeight;
    const natural = l.demo.offsetHeight;
    l.shot.classList.remove("pr-cropped");
    l.demo.style.scale = "";
    if (natural <= room) continue;
    if (l.scene.cursor) l.shot.classList.add("pr-cropped");
    else l.demo.style.scale = String(Math.max(0.6, room / natural));
  }
}

async function boot(): Promise<void> {
  await document.fonts.ready;
  await clock.realFrame();
  await clock.realFrame();
  fit();
  // Fast-forward demos that open mid-way, before the video's first frame.
  for (const l of live) {
    const pre = l.scene.preroll ?? 0;
    if (!pre) continue;
    clock.setVisible(l.demo, true);
    for (let t = 0; t < pre; t += SUB) clock.advance(Math.min(SUB, pre - t));
    clock.setVisible(l.demo, false);
  }
  render(0);
  await clock.realFrame();
  const t = Number(new URLSearchParams(location.search).get("t") ?? 0);
  if (t > 0) seek(t);
}

window.__seek = seek;
window.__promo = { duration: DURATION, fps: FPS, posterAt: POSTER_AT, ready: boot() };
