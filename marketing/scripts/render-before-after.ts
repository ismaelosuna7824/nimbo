/**
 * Renders the before → after LinkedIn carousel: serves ../dist plus the app screenshots
 * (out/before-after/screens, under /shots/), opens /before-after/<lang>/ in headless Chrome,
 * captures every .cs-slide as a 1080×1350 PNG and assembles those PNGs into a PDF (one slide
 * per page, for LinkedIn document posts).
 *
 *   bun run render:before-after             # build + every language
 *   bun scripts/render-before-after.ts es   # one language (after `bun run build`)
 *
 * Writes out/before-after/<lang>/NN-<slide>.png and out/before-after/nimbo-before-after-<lang>.pdf.
 * Needs Chrome/Chromium (set CHROME_PATH to override).
 */
import { existsSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import puppeteer from "puppeteer-core";
import { LANGS } from "../src/render/carousel";

const root = join(import.meta.dir, "..");
const dist = join(root, "dist");
const out = join(root, "out", "before-after");
const screens = join(out, "screens");

const CHROME_CANDIDATES = [
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/Applications/Chromium.app/Contents/MacOS/Chromium",
  "/usr/bin/google-chrome",
  "/usr/bin/google-chrome-stable",
  "/usr/bin/chromium",
  "/usr/bin/chromium-browser",
  "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
];

const chrome = process.env.CHROME_PATH ?? CHROME_CANDIDATES.find((p) => existsSync(p));
if (!chrome) throw new Error("Chrome/Chromium not found; set CHROME_PATH");
if (!existsSync(join(dist, "before-after"))) throw new Error("dist/before-after missing; run `bun run build` first");
if (!existsSync(screens)) throw new Error(`screenshots missing: ${screens}`);

const wanted = process.argv.slice(2);
const langs = wanted.length ? LANGS.filter((l) => wanted.includes(l)) : LANGS;
if (!langs.length) throw new Error(`unknown language; use one of ${LANGS.join(", ")}`);

const server = Bun.serve({
  port: 0,
  hostname: "127.0.0.1",
  async fetch(req) {
    const path = decodeURIComponent(new URL(req.url).pathname);
    const file = path.startsWith("/shots/")
      ? Bun.file(join(screens, path.slice("/shots/".length)))
      : path.startsWith("/slides/")
        ? Bun.file(join(out, path.slice("/slides/".length)))
        : Bun.file(join(dist, path.endsWith("/") ? `${path}index.html` : path));
    return (await file.exists()) ? new Response(file) : new Response("not found", { status: 404 });
  },
});

const browser = await puppeteer.launch({
  executablePath: chrome,
  headless: true,
  args: ["--force-color-profile=srgb", "--hide-scrollbars"],
});

try {
  for (const lang of langs) {
    const dir = join(out, lang);
    mkdirSync(dir, { recursive: true });
    const page = await browser.newPage();
    await page.setViewport({ width: 1080, height: 1350, deviceScaleFactor: 1 });
    await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
    await page.goto(`http://127.0.0.1:${server.port}/before-after/${lang}/`, { waitUntil: "networkidle0" });
    const missing = await page.evaluate(async () => {
      await document.fonts.ready;
      await Promise.all([...document.images].map((img) => img.decode().catch(() => {})));
      return [...document.images].filter((img) => !img.naturalWidth).map((img) => img.src);
    });
    if (missing.length) throw new Error(`images failed to load: ${missing.join(", ")}`);

    // One viewport as tall as the deck, so every slide is painted before it is clipped out.
    const slides = await page.$$eval(".cs-slide", (els) =>
      els.map((el) => ({ name: (el as HTMLElement).dataset.slide!, y: (el as HTMLElement).offsetTop })),
    );
    await page.setViewport({ width: 1080, height: 1350 * slides.length, deviceScaleFactor: 1 });
    await page.evaluate(() => {
      const { promise, resolve } = Promise.withResolvers<void>();
      requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
      return promise;
    });
    for (const { name, y } of slides) {
      await page.screenshot({ path: join(dir, `${name}.png`), type: "png", clip: { x: 0, y, width: 1080, height: 1350 } });
    }
    await page.close();

    // The PDF is the PNGs, one per page: what LinkedIn shows is exactly what was reviewed
    // (print rendering of shadows and gradient text differs between PDF viewers).
    const doc = await browser.newPage();
    const base = `http://127.0.0.1:${server.port}/slides/${lang}`;
    const pages = slides.map(({ name }) => `<img src="${base}/${name}.png">`).join("");
    await doc.setContent(
      `<!doctype html><style>@page{size:1080px 1350px;margin:0}html,body{margin:0}img{display:block;width:1080px;height:1350px;break-after:page}</style>${pages}`,
      { waitUntil: "load" },
    );
    const pdf = join(out, `nimbo-before-after-${lang}.pdf`);
    await doc.pdf({ path: pdf, width: "1080px", height: "1350px", printBackground: true, margin: { top: 0, right: 0, bottom: 0, left: 0 } });
    await doc.close();
    console.log(`${lang}: ${slides.length} slides → ${dir}, ${pdf}`);
  }
} finally {
  await browser.close();
  server.stop(true);
}
