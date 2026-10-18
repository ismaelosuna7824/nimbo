// Virtual clock for frame-exact rendering. Loaded as a classic inline script
// in <head>, before the demo modules run, so every time source the landing
// demos use is virtual:
//   - requestAnimationFrame / performance.now drive lib/timeline.ts,
//   - IntersectionObserver decides which demo is "on screen" (playing),
//   - CSS transitions/animations are paused and seeked to the virtual time.
// Nothing moves unless window.__clock.advance(ms) is called.
(() => {
  let now = 0;
  let nextId = 0;
  let queue = new Map();
  /** Virtual time at which each CSS animation/transition was first seen. */
  const born = new WeakMap();
  const observers = [];

  const realRaf = window.requestAnimationFrame.bind(window);
  Object.defineProperty(performance, "now", { configurable: true, value: () => now });
  window.requestAnimationFrame = (cb) => {
    const id = ++nextId;
    queue.set(id, cb);
    return id;
  };
  window.cancelAnimationFrame = (id) => {
    queue.delete(id);
  };

  class VirtualIntersectionObserver {
    constructor(cb) {
      this.cb = cb;
      this.targets = new Set();
      observers.push(this);
    }
    observe(el) {
      this.targets.add(el);
    }
    unobserve(el) {
      this.targets.delete(el);
    }
    disconnect() {
      this.targets.clear();
    }
    takeRecords() {
      return [];
    }
  }
  window.IntersectionObserver = VirtualIntersectionObserver;

  // getAnimations() flushes style first, so transitions started by class
  // changes in this tick are picked up with the current virtual time.
  const syncAnimations = () => {
    for (const anim of document.getAnimations()) {
      let start = born.get(anim);
      if (start === undefined) {
        start = now;
        born.set(anim, start);
        anim.pause();
      }
      anim.currentTime = now - start;
    }
  };

  window.__clock = {
    get now() {
      return now;
    },
    /** Moves virtual time forward, runs due animation frames, seeks CSS animations. */
    advance(ms) {
      now += ms;
      const due = queue;
      queue = new Map();
      for (const cb of due.values()) cb(now);
      syncAnimations();
    },
    /** Reports `el` as (not) intersecting the viewport to its observers. */
    setVisible(el, visible) {
      for (const o of observers) {
        if (o.targets.has(el)) o.cb([{ target: el, isIntersecting: visible, intersectionRatio: visible ? 1 : 0 }], o);
      }
      syncAnimations();
    },
    sync: syncAnimations,
    /** Resolves after one real rendering frame (layout, ResizeObserver callbacks). */
    realFrame() {
      const { promise, resolve } = Promise.withResolvers();
      realRaf(() => resolve());
      return promise;
    },
  };
})();
