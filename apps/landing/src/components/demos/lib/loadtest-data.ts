/**
 * Deterministic samples for the LoadTestLive demo: a stress ramp to 500 req/s
 * where throughput saturates near 350 req/s and latency climbs. Shared by the
 * build-time chart paths and the client-side KPI counters.
 */

export interface Sample {
  target: number;
  rps: number;
  p50: number;
  p95: number;
  p99: number;
}

export const SAMPLE_COUNT = 60;
export const MAX_RPS = 500;
export const MAX_MS = 400;
/** First sample at or past the breaking point. */
export const BREAK_AT = 42;

function noise(seed: number): () => number {
  let a = seed;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export const SAMPLES: Sample[] = (() => {
  const r = noise(7);
  return Array.from({ length: SAMPLE_COUNT }, (_, i) => {
    const target = (MAX_RPS * i) / (SAMPLE_COUNT - 1);
    const over = Math.max(0, i - BREAK_AT);
    const rps = over > 0 ? 338 + r() * 24 : target * (0.96 + r() * 0.04);
    const p50 = 7 + r() * 3 + over * 2.2;
    const p95 = 13 + r() * 6 + over ** 1.45 * 5;
    const p99 = p95 * (1.35 + r() * 0.2) + 6;
    return { target, rps, p50, p95, p99 };
  });
})();

/** Polyline in a 0..300 × 0..100 box (y grows downwards). */
export function seriesPath(key: keyof Sample, max: number): string {
  return SAMPLES.map((s, i) => {
    const x = (i / (SAMPLE_COUNT - 1)) * 300;
    const y = 100 - Math.min(s[key] / max, 1) * 96;
    return `${i ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`;
  }).join("");
}

export interface Kpis {
  requests: number;
  peak: number;
  p50: number;
  p95: number;
  /** Fraction 0..1 of failed requests so far. */
  errorRate: number;
}

/** Running KPIs after sample `i` (one sample per second). */
export function kpisAt(i: number): Kpis {
  let requests = 0;
  let errors = 0;
  let peak = 0;
  for (let k = 0; k <= i; k++) {
    const s = SAMPLES[k]!;
    requests += s.rps;
    errors += s.rps * Math.max(0, k - BREAK_AT) * 0.0011;
    peak = Math.max(peak, s.rps);
  }
  const s = SAMPLES[i]!;
  return { requests, peak, p50: s.p50, p95: s.p95, errorRate: requests ? errors / requests : 0 };
}
