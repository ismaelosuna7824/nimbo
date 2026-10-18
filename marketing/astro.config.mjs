// @ts-check
import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";

// Render page for the promo video. It reuses the landing's demo components
// and design tokens straight from ../apps/landing/src (relative imports), so
// the video always matches the site.
export default defineConfig({
  output: "static",
  devToolbar: { enabled: false },
  server: { port: 7866, host: "127.0.0.1" },
  vite: {
    plugins: [tailwindcss()],
    resolve: { dedupe: ["astro", "tailwindcss"] },
    server: { fs: { allow: [".."] } },
  },
});
