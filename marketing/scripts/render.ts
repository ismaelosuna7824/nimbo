/**
 * Renders the promo video in every format: serves ../dist, opens it in
 * headless Chrome at the exact output size, steps the virtual clock one frame
 * at a time (window.__seek) and pipes PNG screenshots into ffmpeg.
 *
 *   bun run render                 # build + all formats
 *   bun scripts/render.ts 1080x1350  # only some formats (after `bun run build`)
 *
 * Needs ffmpeg/ffprobe on PATH and Chrome/Chromium (set CHROME_PATH to override).
 * Writes out/nimbo-promo-<format>.{mp4,png}, out/sheets/<format>.png (contact
 * sheet, one frame per second) and copies the videos + posters to
 * ~/Downloads/nimbo-promo/.
 */
import { spawn, spawnSync } from "node:child_process";
import { once } from "node:events";
import { copyFileSync, existsSync, mkdirSync, statSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import puppeteer from "puppeteer-core";
import { FORMATS } from "../src/render/formats";

const root = join(import.meta.dir, "..");
const dist = join(root, "dist");
const out = join(root, "out");
const downloads = join(homedir(), "Downloads", "nimbo-promo");

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
if (!existsSync(join(dist, "index.html"))) throw new Error("dist/ missing; run `bun run build` first");

const wanted = process.argv.slice(2);
const formats = wanted.length ? FORMATS.filter((f) => wanted.includes(f.id)) : FORMATS;
if (!formats.length) throw new Error(`unknown format; use one of ${FORMATS.map((f) => f.id).join(", ")}`);

mkdirSync(join(out, "sheets"), { recursive: true });
mkdirSync(downloads, { recursive: true });

const server = Bun.serve({
  port: 0,
  hostname: "127.0.0.1",
  async fetch(req) {
    const path = decodeURIComponent(new URL(req.url).pathname);
    const file = Bun.file(join(dist, path.endsWith("/") ? `${path}index.html` : path));
    return (await file.exists()) ? new Response(file) : new Response("not found", { status: 404 });
  },
});

const browser = await puppeteer.launch({
  executablePath: chrome,
  headless: true,
  args: ["--force-color-profile=srgb", "--hide-scrollbars", "--disable-lcd-text", "--mute-audio"],
});

/** H.264 High, BT.709-tagged yuv420p, CRF 18, moov atom up front for streaming. */
function encoder(file: string, fps: number) {
  const ff = spawn(
    "ffmpeg",
    [
      "-y", "-loglevel", "error",
      "-f", "image2pipe", "-framerate", String(fps), "-c:v", "png", "-i", "-",
      "-vf", "scale=out_color_matrix=bt709:out_range=tv,format=yuv420p",
      "-c:v", "libx264", "-preset", "slow", "-crf", "18", "-profile:v", "high", "-tune", "animation",
      "-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-color_range", "tv",
      "-r", String(fps), "-movflags", "+faststart",
      file,
    ],
    { stdio: ["pipe", "inherit", "inherit"] },
  );
  const { promise: done, resolve, reject } = Promise.withResolvers<void>();
  ff.on("close", (code) => (code === 0 ? resolve() : reject(new Error(`ffmpeg exited with ${code}`))));
  return {
    async write(frame: Uint8Array) {
      if (!ff.stdin.write(frame)) await once(ff.stdin, "drain");
    },
    async end() {
      ff.stdin.end();
      await done;
    },
  };
}

try {
  for (const f of formats) {
    const started = performance.now();
    const page = await browser.newPage();
    await page.setViewport({ width: f.width, height: f.height, deviceScaleFactor: f.scale });
    await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "no-preference" }]);
    await page.goto(`http://127.0.0.1:${server.port}/?format=${f.id}`, { waitUntil: "load" });
    const { duration, fps, posterAt } = await page.evaluate(async () => {
      await window.__promo.ready;
      return { duration: window.__promo.duration, fps: window.__promo.fps, posterAt: window.__promo.posterAt };
    });

    const name = `nimbo-promo-${f.id}`;
    const video = join(out, `${name}.mp4`);
    const poster = join(out, `${name}.png`);
    const enc = encoder(video, fps);
    const frames = Math.round((duration / 1000) * fps);
    const posterFrame = Math.round((posterAt / 1000) * fps);
    for (let i = 0; i < frames; i++) {
      await page.evaluate((ms) => window.__seek(ms), (i * 1000) / fps);
      const png = await page.screenshot({ type: "png", optimizeForSpeed: true });
      if (i === posterFrame) writeFileSync(poster, png);
      await enc.write(png);
      if (i % (fps * 5) === 0) process.stdout.write(`\r${f.id}: ${i}/${frames} frames`);
    }
    await enc.end();
    await page.close();

    const sheet = join(out, "sheets", `${f.id}.png`);
    const cols = 6;
    const rows = Math.ceil(duration / 1000 / cols);
    const tileW = Math.round((f.width * f.scale) / 4);
    const tile = spawnSync("ffmpeg", [
      "-y", "-loglevel", "error", "-i", video,
      "-vf", `fps=1,scale=${tileW}:-2,tile=${cols}x${rows}:padding=4`,
      "-frames:v", "1", sheet,
    ]);
    if (tile.status !== 0) throw new Error(`contact sheet failed: ${tile.stderr}`);

    for (const file of [video, poster]) copyFileSync(file, join(downloads, file.split("/").pop()!));
    const mb = (statSync(video).size / 1e6).toFixed(1);
    const secs = ((performance.now() - started) / 1000).toFixed(0);
    console.log(`\r${f.id}: ${frames} frames, ${(frames / fps).toFixed(2)} s, ${mb} MB → ${video} (rendered in ${secs} s)`);
  }
} finally {
  await browser.close();
  server.stop(true);
}
