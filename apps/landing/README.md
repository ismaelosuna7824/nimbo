# Nimbo website

The public landing page for Nimbo: [Astro](https://astro.build) + Bun,
Tailwind CSS v4, static output, hosted on Firebase Hosting with optional
Google Analytics for Firebase (consent-gated).

- English at `/`, Spanish at `/es/` (same page, `src/components/Landing.astro`).
- Core copy: `src/i18n/en.ts` / `src/i18n/es.ts` (Spanish is type-checked
  against English, so a missing key fails `bun run check`).
- Animated demos: `src/components/demos/` (`index.ts` lists them). They start
  when scrolled into view, pause offscreen and show a static final frame with
  `prefers-reduced-motion`.
- Links and version-independent URLs: `src/config.ts`.

## Develop

```sh
cd apps/landing
bun install
bun run dev        # http://localhost:4321
bun run check      # astro check (types + templates)
bun run build      # static site in dist/
bun run preview    # serve dist/
```

## Environment

Copy `.env.example` to `.env` (or `.env.local`). Everything is optional and
read at **build time**:

| Variable | Effect |
|---|---|
| `SITE_URL` | Public origin, e.g. `https://nimbo.web.app`. Makes canonical, `hreflang` and Open Graph URLs absolute, adds `Sitemap:` to `robots.txt` and generates `/sitemap-index.xml`. Without it URLs are root-relative and there is no sitemap. |
| `PUBLIC_FIREBASE_*` (7 keys) | Firebase web app config. Only when **all seven** are set does the site render the cookie banner and, after the visitor clicks *Accept*, load the Firebase SDK. Otherwise no banner, no SDK, no requests. |

## Analytics

`src/lib/firebase.ts` loads `firebase/app` + `firebase/analytics` with a
dynamic `import()` only in the browser, only after consent, and only if
`isSupported()` is true. The choice is stored in `localStorage`
(`nimbo:analytics-consent`) and can be changed from **Cookie settings** in the
footer; declining later turns collection off for the current page too.

Client code reports events through one helper:

```ts
import { track } from "../lib/analytics";
track(name: string, params?: Record<string, string | number | boolean | undefined>): void
```

It never throws; before consent events are queued (max 50) and dropped on
decline or when Firebase is not configured.

| Event | Params | When |
|---|---|---|
| `cta_click` | `cta` (`download` · `github` · `setup_copy`), `os`, `lang`, `placement` | Any element with `data-cta` |
| `language_switch` | `from`, `to` | EN/ES switcher |
| `faq_open` | `question`, `lang` | A FAQ item is expanded |
| `demo_view` | `demo`, `lang` | A demo is seen for the first time |

Mark the custom params as custom dimensions in Google Analytics if you want
them in reports.

## Deploy to Firebase Hosting

1. Create a project at <https://console.firebase.google.com> and enable
   **Google Analytics** for it (Project settings → Integrations).
2. Add a **Web app** (Project settings → General → Your apps → `</>`), then copy
   its `firebaseConfig` values into `.env` as the `PUBLIC_FIREBASE_*` keys, and
   set `SITE_URL` to your Hosting URL (`https://<project-id>.web.app` or your
   custom domain).
3. Log in and select the project:

   ```sh
   bunx firebase-tools login
   cp .firebaserc.example .firebaserc   # then put your project id in it
   # or: bunx firebase-tools use --add
   ```

4. Build and deploy:

   ```sh
   bun run deploy     # = bun run build && bunx firebase-tools deploy --only hosting
   ```

`firebase.json` serves `dist/` with clean URLs and `404.html`, caches
`/_astro/**` and fonts for a year (`immutable`, content-hashed names), images
for a day and HTML for zero seconds (always revalidated).

To preview the Hosting behaviour locally: `bun run build && bunx firebase-tools emulators:start --only hosting`.

## Assets

- `public/favicon.svg`, `src/assets/nimbo-mark.svg`: the app's pixel mascot
  (`apps/desktop/src/assets/nimbo-mark.svg`).
- `public/apple-touch-icon.png`: the app icon resized to 180 px.
- `public/og.png` (1200×630): social preview, rendered once from the site's
  own fonts and mascot and committed. Re-render it if the headline changes.
