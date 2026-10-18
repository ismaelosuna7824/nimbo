// @ts-check
import { defineConfig } from "astro/config";
import sitemap from "@astrojs/sitemap";
import tailwindcss from "@tailwindcss/vite";
import { loadEnv } from "vite";

// SITE_URL (e.g. https://nimbo.dev) makes canonical/OG URLs absolute and
// enables the sitemap. Read from the shell or from .env / .env.local.
const env = loadEnv(process.env.NODE_ENV ?? "production", process.cwd(), "");
const site = (process.env.SITE_URL || env.SITE_URL || "").replace(/\/+$/, "") || undefined;

export default defineConfig({
  site,
  output: "static",
  trailingSlash: "ignore",
  devToolbar: { enabled: false },
  build: { format: "directory" },
  i18n: {
    defaultLocale: "en",
    locales: ["en", "es"],
    routing: { prefixDefaultLocale: false },
  },
  integrations: site
    ? [
        sitemap({
          i18n: { defaultLocale: "en", locales: { en: "en", es: "es" } },
          filter: (page) => !page.includes("/404"),
        }),
      ]
    : [],
  vite: {
    plugins: [tailwindcss()],
  },
});
