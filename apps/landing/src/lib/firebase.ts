/**
 * Firebase Analytics, loaded lazily and only when:
 *  1. every PUBLIC_FIREBASE_* value is set at build time,
 *  2. it runs in a browser,
 *  3. the visitor accepted the consent banner (caller's responsibility),
 *  4. `isSupported()` resolves true.
 * The SDK is imported dynamically, so it is never part of the initial bundle.
 */
import type * as AnalyticsSdk from "firebase/analytics";
import { attachSender, disableTracking } from "./analytics";

const config = {
  apiKey: import.meta.env.PUBLIC_FIREBASE_API_KEY,
  authDomain: import.meta.env.PUBLIC_FIREBASE_AUTH_DOMAIN,
  projectId: import.meta.env.PUBLIC_FIREBASE_PROJECT_ID,
  storageBucket: import.meta.env.PUBLIC_FIREBASE_STORAGE_BUCKET,
  messagingSenderId: import.meta.env.PUBLIC_FIREBASE_MESSAGING_SENDER_ID,
  appId: import.meta.env.PUBLIC_FIREBASE_APP_ID,
  measurementId: import.meta.env.PUBLIC_FIREBASE_MEASUREMENT_ID,
};

export const firebaseConfigured = Object.values(config).every(
  (value) => typeof value === "string" && value.trim() !== "",
);

let started: Promise<{ mod: typeof AnalyticsSdk; instance: AnalyticsSdk.Analytics } | null> | null = null;

/** Starts Firebase Analytics after consent. Resolves false when unavailable. */
export async function startAnalytics(): Promise<boolean> {
  if (!firebaseConfigured || typeof window === "undefined") return false;
  started ??= (async () => {
    const [{ initializeApp }, mod] = await Promise.all([import("firebase/app"), import("firebase/analytics")]);
    if (!(await mod.isSupported())) return null;
    const instance = mod.getAnalytics(initializeApp(config));
    return { mod, instance };
  })().catch((error: unknown) => {
    console.warn("[nimbo] analytics unavailable:", error);
    return null;
  });
  const ready = await started;
  if (!ready) {
    disableTracking();
    return false;
  }
  ready.mod.setAnalyticsCollectionEnabled(ready.instance, true);
  attachSender((name, params) => ready.mod.logEvent(ready.instance, name, params));
  return true;
}

/** Stops collection if the SDK was already started in this page view. */
export async function stopAnalytics(): Promise<void> {
  disableTracking();
  const ready = started ? await started : null;
  if (ready) ready.mod.setAnalyticsCollectionEnabled(ready.instance, false);
}
