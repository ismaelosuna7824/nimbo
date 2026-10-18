/**
 * Tiny analytics facade. Safe to call from any client script:
 *
 *   import { track } from "../lib/analytics";
 *   track("demo_view", { demo: "load-test", lang: "en" });
 *
 * Events are only sent when Firebase is configured (PUBLIC_FIREBASE_* env)
 * AND the visitor accepted the consent banner. Before that they are queued
 * (max 50) and dropped if the visitor declines. Never throws.
 */
export type AnalyticsParams = Record<string, string | number | boolean | undefined>;
type Sender = (name: string, params: Record<string, string | number | boolean>) => void;

const CONSENT_KEY = "nimbo:analytics-consent";
export type Consent = "granted" | "denied";

let sender: Sender | null = null;
let disabled = false;
const queue: Array<[string, AnalyticsParams | undefined]> = [];

function clean(params: AnalyticsParams | undefined): Record<string, string | number | boolean> {
  const out: Record<string, string | number | boolean> = {};
  if (params) for (const [k, v] of Object.entries(params)) if (v !== undefined) out[k] = v;
  return out;
}

export function track(name: string, params?: AnalyticsParams): void {
  if (disabled) return;
  try {
    if (sender) sender(name, clean(params));
    else if (queue.length < 50) queue.push([name, params]);
  } catch {
    /* analytics must never break the page */
  }
}

export function readConsent(): Consent | null {
  try {
    const v = localStorage.getItem(CONSENT_KEY);
    return v === "granted" || v === "denied" ? v : null;
  } catch {
    return null;
  }
}

export function writeConsent(value: Consent): void {
  try {
    localStorage.setItem(CONSENT_KEY, value);
  } catch {
    /* private mode: choice lasts for this page view only */
  }
}

/** Connects the facade to a live sender and flushes queued events. */
export function attachSender(next: Sender): void {
  sender = next;
  disabled = false;
  for (const [name, params] of queue.splice(0)) track(name, params);
}

/** Stops sending and drops anything queued (no config, declined, unsupported). */
export function disableTracking(): void {
  sender = null;
  disabled = true;
  queue.length = 0;
}
