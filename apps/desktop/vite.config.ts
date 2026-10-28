import path from "node:path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

const host = process.env.TAURI_DEV_HOST

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(import.meta.dirname, "./src") },
  },
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
    host: host || false,
    hmr: host ? { protocol: "ws", host, port: 1421 } : undefined,
    watch: { ignored: ["**/src-tauri/**"] },
  },
  build: {
    // Icon registries resolve ~700 SVGs by URL (new URL(`./assets/${id}.svg`, import.meta.url)); inlining the small
    // ones as data: URLs put every icon (~1 MB) in the entry chunk. As files they load only when an <img> shows them.
    assetsInlineLimit: (file) => (file.endsWith(".svg") ? false : undefined),
    rolldownOptions: {
      output: {
        codeSplitting: {
          // Left to automatic splitting, every lucide icon shared by two lazy chunks (the workspace kinds, the bottom
          // tabs, the dialogs) becomes a ~300-byte chunk of its own: dozens of extra requests when a workflow opens.
          // The startup's icons keep a chunk of their own; every other icon loads in one chunk with the editor.
          groups: [
            { name: "icons", test: /[\\/]lucide-react[\\/]/, tags: ["$initial"], priority: 2 },
            { name: "lazy-icons", test: /[\\/]lucide-react[\\/]/, priority: 1 },
          ],
        },
      },
    },
  },
})
