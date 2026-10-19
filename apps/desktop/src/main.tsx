import { QueryClientProvider } from "@tanstack/react-query"
import { invoke, isTauri } from "@tauri-apps/api/core"
import { ThemeProvider } from "next-themes"
import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { ArcadeCelebration } from "@/components/arcade/celebration"
import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"
import { setApiBaseUrl } from "@/lib/api/client"
import { queryClient } from "@/lib/query-client"
import App from "./App"
import "./index.css"

async function bootstrap() {
  const root = createRoot(document.getElementById("root")!)
  if (isTauri()) {
    try {
      setApiBaseUrl(await invoke<string>("engine_url"))
    } catch (err) {
      root.render(
        <div style={{ padding: 32, fontFamily: "system-ui", color: "#e5e5e5", background: "#171717", height: "100vh" }}>
          <h1 style={{ fontSize: 18, marginBottom: 8 }}>The Nimbo engine failed to start</h1>
          <pre style={{ whiteSpace: "pre-wrap", fontSize: 13, opacity: 0.8 }}>{String(err)}</pre>
          <p style={{ fontSize: 13, opacity: 0.8 }}>Quit and reopen the app. If it keeps failing, reinstall Nimbo.</p>
        </div>,
      )
      return
    }
  }
  root.render(
    <StrictMode>
      <ThemeProvider attribute="class" defaultTheme="dark" enableSystem={false} storageKey="nimbo.theme" disableTransitionOnChange>
        <QueryClientProvider client={queryClient}>
          <TooltipProvider delayDuration={300}>
            <App />
            <Toaster position="bottom-right" richColors closeButton />
            <ArcadeCelebration />
          </TooltipProvider>
        </QueryClientProvider>
      </ThemeProvider>
    </StrictMode>,
  )
}

void bootstrap()
