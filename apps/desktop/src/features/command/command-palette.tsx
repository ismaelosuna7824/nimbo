import { useQuery } from "@tanstack/react-query"
import {
  ActivityIcon,
  ImageDownIcon,
  NetworkIcon,
  BotIcon,
  ArchiveIcon,
  CloudUploadIcon,
  DownloadIcon,
  FileCheck2Icon,
  FileCodeIcon,
  GaugeIcon,
  HammerIcon,
  HomeIcon,
  ListChecksIcon,
  ClipboardCheckIcon,
  MoonIcon,
  PanelBottomIcon,
  PanelRightIcon,
  PlayIcon,
  PlugIcon,
  PlusIcon,
  SquareIcon,
  Trash2Icon,
  TriangleAlertIcon,
  UploadIcon,
  SettingsIcon,
  VariableIcon,
  WandSparklesIcon,
  WorkflowIcon,
} from "lucide-react"
import { useTheme } from "next-themes"
import { useEffect, useState } from "react"
import { useArcadeStore } from "@/components/arcade/arcade-store"
import { ThemedIcon } from "@/components/arcade/themed-icon"
import { ResourceIcon } from "@/components/resource-icon"
import { DEFAULT_TILE_COLOR } from "@/components/workflow-icon/colors"
import { glyphById } from "@/components/workflow-icon/glyphs"
import { PixelGlyph } from "@/components/workflow-icon/pixel-glyph"
import { WorkflowIconTile } from "@/components/workflow-icon/workflow-icon-tile"
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
  CommandShortcut,
} from "@/components/ui/command"
import { Kbd, KbdGroup } from "@/components/ui/kbd"
import { api } from "@/lib/api/client"
import type { BottomTab } from "@/state/workflow-store"
import { useWorkflowStore } from "@/state/workflow-store"
import { addResource, canvasHandle } from "../canvas/actions"
import { addFromPalette, dockerCanvasHandle } from "../docker/actions"
import { contentCount } from "../docker/badge"
import { loadDockerImages } from "../docker/intel"
import { matchesImage } from "../docker/palette"
import { addFromPalette as addKubeItem, kubeCanvasHandle } from "../kubernetes/actions"
import { K8sIcon } from "@/components/k8s-icons/k8s-icon"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { switchView, useArchUi, useCanvasView, VIEW_SHORTCUT } from "../architecture/store"
import { MOD_KEY, openWorkflow, runAction, sandboxAction } from "../toolbar/actions"
import { useUiStore } from "../toolbar/ui-store"
import { archiveWorkflow, closeWorkflow } from "../toolbar/workflow-actions"
import { KIND_META } from "../connections/meta"
import { useConnections, useConnectionsStore } from "../connections/store"
import { openSetupChecklist } from "../onboarding/store"
import { useSettingsView } from "../settings/store"

const PANELS: { tab: BottomTab; label: string; icon: React.ReactNode }[] = [
  { tab: "terraform", label: "Code — Terraform & workflow files", icon: <FileCodeIcon /> },
  { tab: "variables", label: "Variables & environments", icon: <VariableIcon /> },
  { tab: "deploy", label: "Deploy log", icon: <CloudUploadIcon /> },
  { tab: "endpoints", label: "Endpoints — URLs & snippets", icon: <PlugIcon /> },
  { tab: "loadtest", label: "Load test", icon: <GaugeIcon /> },
  { tab: "problems", label: "Problems", icon: <TriangleAlertIcon /> },
]

function Shortcut({ keys }: { keys: string[] }) {
  return (
    <CommandShortcut>
      <KbdGroup>
        {keys.map((k) => (
          <Kbd key={k}>{k}</Kbd>
        ))}
      </KbdGroup>
    </CommandShortcut>
  )
}

export function CommandPalette() {
  const open = useUiStore((s) => s.commandOpen)
  const setOpen = useUiStore((s) => s.setCommandOpen)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        useUiStore.getState().setCommandOpen(!useUiStore.getState().commandOpen)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  return (
    <CommandDialog
      open={open}
      onOpenChange={setOpen}
      title="Command palette"
      description="Search resources, actions and workflows"
      className="sm:max-w-xl"
    >
      <PaletteBody />
    </CommandDialog>
  )
}

/** Mounted only while the dialog is open, so the search resets on every open. */
function PaletteBody() {
  const setOpen = useUiStore((s) => s.setCommandOpen)
  const workflow = useWorkflowStore((s) => s.workflow)
  const mode = useWorkflowStore((s) => s.mode)
  const sandbox = useWorkflowStore((s) => s.sandbox)
  const bottomOpen = useWorkflowStore((s) => s.bottomOpen)
  const { resolvedTheme, setTheme } = useTheme()
  const arcade = useArcadeStore((s) => s.arcade)
  const [search, setSearch] = useState("")
  const q = search.trim()
  const view = useCanvasView()

  const docker = workflow?.kind === "docker"
  const kubernetes = workflow?.kind === "kubernetes"
  const terraform = !docker && !kubernetes
  const resources = useQuery({
    queryKey: ["resources", { q, limit: 15 }],
    queryFn: () => api.resources({ q, limit: 15 }),
    enabled: !!workflow && terraform && q.length >= 2,
    staleTime: 60_000,
  })
  const images = useQuery({ queryKey: ["docker-images"], queryFn: loadDockerImages, staleTime: Infinity, enabled: docker })
  const imageHits = docker && q.length >= 2 ? (images.data ?? []).filter((i) => matchesImage(i, q)).slice(0, 15) : []
  const kubeCatalog = useQuery({ queryKey: ["kubernetes-catalog"], queryFn: api.kubernetesCatalog, staleTime: Infinity, enabled: kubernetes })
  const needle = q.toLowerCase()
  const kubeHits =
    kubernetes && q.length >= 2
      ? (kubeCatalog.data ?? [])
          .filter((i) => `${i.label} ${i.image ?? ""} ${i.kinds.join(" ")} ${i.chart?.name ?? ""}`.toLowerCase().includes(needle))
          .slice(0, 15)
      : []
  const workflows = useQuery({ queryKey: ["workflows"], queryFn: api.listWorkflows })
  const connections = useConnections()

  const run = (fn: () => void) => {
    setOpen(false)
    fn()
  }
  const sandboxUp = sandbox?.state === "running" || sandbox?.state === "starting"

  return (
    <Command>
      <CommandInput value={search} onValueChange={setSearch} placeholder="Type a command or search resources…" />
      <CommandList className="max-h-[420px]">
        <CommandEmpty>{q.length < 2 && workflow ? "Type at least 2 letters to search resources." : "No results."}</CommandEmpty>
        {kubernetes && kubeHits.length > 0 && (
          <CommandGroup heading="Add to the cluster">
            {kubeHits.map((item) => (
              <CommandItem key={item.id} value={`add ${item.label} ${item.id} ${q}`} onSelect={() => run(() => void addKubeItem(item.id))}>
                {item.group === "starter" ? <K8sIcon icon={item.kinds[0] ?? ""} size={16} /> : <TechIcon icon={item.icon} size={16} />}
                <span className="shrink-0">{item.label}</span>
                <span className="truncate font-mono text-xs text-muted-foreground">
                  {item.chart ? `${item.chart.name} ${item.chart.version}` : (item.image ?? item.kinds.join(" + "))}
                </span>
              </CommandItem>
            ))}
          </CommandGroup>
        )}
        {docker && imageHits.length > 0 && (
          <CommandGroup heading="Add service">
            {imageHits.map((img) => (
              <CommandItem
                key={img.id}
                value={`add ${img.label} ${img.image} ${q}`}
                onSelect={() => run(() => void addFromPalette({ kind: "image", catalogId: img.id, label: img.label }))}
              >
                <TechIcon icon={img.icon} size={16} />
                <span className="shrink-0">{img.label}</span>
                <span className="truncate font-mono text-xs text-muted-foreground">
                  {img.image}:{img.tag}
                </span>
              </CommandItem>
            ))}
          </CommandGroup>
        )}
        {workflow && (resources.data ?? []).length > 0 && (
          <CommandGroup heading="Add resource">
            {resources.data!.map((r) => (
              <CommandItem
                key={`${r.kind}:${r.type}`}
                value={`add ${r.label} ${r.type} ${q}`}
                onSelect={() => run(() => void addResource(r.kind, r.type))}
              >
                <ResourceIcon name={r.icon} />
                <span className="shrink-0">{r.label}</span>
                <span className="truncate font-mono text-xs text-muted-foreground">{r.type}</span>
                {!r.flociSupported && <span className="ml-auto text-[10px] text-muted-foreground">export only</span>}
              </CommandItem>
            ))}
          </CommandGroup>
        )}
        {workflow && (
          <>
            <CommandGroup heading="Run">
              {terraform && (
                <>
                  <CommandItem value="validate check" onSelect={() => run(() => void runAction("validate"))}>
                    <ThemedIcon icon={FileCheck2Icon} pixel="check" />
                    Validate
                  </CommandItem>
                  <CommandItem value="plan preview changes" onSelect={() => run(() => void runAction("plan"))}>
                    <ThemedIcon icon={ListChecksIcon} pixel="list" />
                    Plan
                  </CommandItem>
                </>
              )}
              <CommandItem value="deploy apply up start containers" onSelect={() => run(() => void runAction("apply"))}>
                <ThemedIcon icon={CloudUploadIcon} glyph="rocket" />
                {docker ? "Deploy (docker compose up)" : kubernetes ? "Deploy to the local cluster" : "Deploy to sandbox"}
              </CommandItem>
              {docker && (
                <CommandItem value="build images docker compose build" onSelect={() => run(() => void runAction("build"))}>
                  <HammerIcon />
                  Build images
                </CommandItem>
              )}
              <CommandItem value="destroy teardown down stop containers" onSelect={() => run(() => useUiStore.getState().setConfirmDestroy(true))}>
                <ThemedIcon icon={Trash2Icon} pixel="trash" />
                {docker ? "Destroy (docker compose down)…" : kubernetes ? "Destroy (delete from the cluster)…" : "Destroy…"}
              </CommandItem>
              <CommandItem value="load test traffic" onSelect={() => run(() => useWorkflowStore.getState().showBottom("loadtest"))}>
                <ThemedIcon icon={ActivityIcon} glyph="bolt" />
                Load test
              </CommandItem>
              {kubernetes ? (
                <>
                  <CommandItem value="restart workloads rollout kubernetes" onSelect={() => run(() => void runAction("restart"))}>
                    <PlayIcon />
                    Restart workloads
                  </CommandItem>
                  <CommandItem value="stop cluster kubernetes k3s" onSelect={() => run(() => void runAction("cluster-stop"))}>
                    <SquareIcon />
                    Stop local cluster
                  </CommandItem>
                </>
              ) : docker ? (
                <CommandItem value="convert to kubernetes manifests helm kustomize k8s" onSelect={() => run(() => useUiStore.getState().setConvertKubernetes(true))}>
                  <TechIcon icon="kubernetes" size={16} />
                  Convert to Kubernetes…
                </CommandItem>
              ) : sandboxUp ? (
                <CommandItem value="stop sandbox" onSelect={() => run(() => void sandboxAction("stop"))}>
                  <SquareIcon />
                  Stop sandbox
                </CommandItem>
              ) : (
                <CommandItem value="start sandbox" onSelect={() => run(() => void sandboxAction("start"))}>
                  <PlayIcon />
                  Start sandbox
                </CommandItem>
              )}
              <CommandItem
                value="tidy up auto layout arrange"
                onSelect={() => run(() => void (docker ? dockerCanvasHandle() : kubernetes ? kubeCanvasHandle() : canvasHandle())?.tidy())}
              >
                <WandSparklesIcon />
                Tidy up layout
              </CommandItem>
            </CommandGroup>
            <CommandSeparator />
            <CommandGroup heading="View">
              <CommandItem
                value="architecture diagram view build canvas switch"
                onSelect={() => run(() => switchView(view === "build" ? "architecture" : "build"))}
              >
                <NetworkIcon />
                {view === "build" ? "Show architecture diagram" : "Back to Build canvas"}
                <Shortcut keys={VIEW_SHORTCUT} />
              </CommandItem>
              <CommandItem
                value="export architecture diagram image png svg share"
                onSelect={() =>
                  run(() => {
                    switchView("architecture")
                    useArchUi.getState().openExport("architecture")
                  })
                }
              >
                <ImageDownIcon />
                Export architecture diagram…
              </CommandItem>
              {view === "build" && (
                <CommandItem value="export canvas image png build screenshot" onSelect={() => run(() => useArchUi.getState().openExport("build"))}>
                  <ImageDownIcon />
                  Export Build canvas as PNG…
                </CommandItem>
              )}
            </CommandGroup>
            <CommandSeparator />
            <CommandGroup heading="Panels">
              {PANELS.filter((p) => !(!terraform && p.tab === "variables")).map((p) => (
                <CommandItem key={p.tab} value={`open panel ${p.label}`} onSelect={() => run(() => useWorkflowStore.getState().showBottom(p.tab))}>
                  {p.icon}
                  {p.label}
                </CommandItem>
              ))}
              <CommandItem
                value="toggle bottom panel"
                onSelect={() => run(() => useWorkflowStore.getState().setBottomOpen(!bottomOpen))}
              >
                <PanelBottomIcon />
                {bottomOpen ? "Hide" : "Show"} bottom panel
                <Shortcut keys={[MOD_KEY, "J"]} />
              </CommandItem>
              <CommandItem
                value="toggle inspector"
                onSelect={() => run(() => useUiStore.getState().setInspectorOpen(!useUiStore.getState().inspectorOpen))}
              >
                <PanelRightIcon />
                Toggle inspector
                <Shortcut keys={[MOD_KEY, "I"]} />
              </CommandItem>
            </CommandGroup>
            <CommandSeparator />
          </>
        )}
        <CommandGroup heading="Connections">
          <CommandItem value="new connection deployed service api database aws" onSelect={() => run(() => useConnectionsStore.getState().openView("new"))}>
            <PlusIcon />
            New connection…
          </CommandItem>
          <CommandItem value="open connections library test deployed service" onSelect={() => run(() => useConnectionsStore.getState().openView())}>
            <PlugIcon />
            Open connections
          </CommandItem>
          {(connections.data ?? []).map((c) => {
            const meta = KIND_META[c.kind]
            return (
              <CommandItem
                key={c.id}
                value={`open connection ${c.name} ${meta.label} ${c.id}`}
                onSelect={() => run(() => useConnectionsStore.getState().openView(c.id))}
              >
                <meta.icon />
                Open “{c.name}”
                <span className="ml-auto text-xs text-muted-foreground">{meta.label}</span>
              </CommandItem>
            )
          })}
        </CommandGroup>
        <CommandSeparator />
        <CommandGroup heading="Preferences">
          <CommandItem
            value="toggle mode guided pro"
            onSelect={() => run(() => useWorkflowStore.getState().setMode(mode === "guided" ? "pro" : "guided"))}
          >
            <WorkflowIcon />
            Switch to {mode === "guided" ? "Pro" : "Guided"} mode
          </CommandItem>
          <CommandItem
            value="toggle theme dark light"
            onSelect={() => run(() => setTheme(resolvedTheme === "light" ? "dark" : "light"))}
          >
            <MoonIcon />
            Switch to {resolvedTheme === "light" ? "dark" : "light"} theme
          </CommandItem>
          <CommandItem value="theme arcade 8-bit pixel retro style" onSelect={() => run(() => useArcadeStore.getState().setArcade(!arcade))}>
            <PixelGlyph glyph={glyphById("gamepad")!} color={DEFAULT_TILE_COLOR} size={16} />
            {arcade ? "Theme: turn off Arcade" : "Theme: Arcade"}
            <span className="ml-auto text-xs text-muted-foreground">8-bit pixel mode</span>
          </CommandItem>
          <CommandItem value="open settings preferences mcp ai agents docker images region" onSelect={() => run(() => useSettingsView.getState().openView())}>
            <SettingsIcon />
            Open settings
            <KbdGroup className="ml-auto">
              <Kbd>{MOD_KEY}</Kbd>
              <Kbd>,</Kbd>
            </KbdGroup>
          </CommandItem>
          <CommandItem value="connect ai agent mcp claude cursor codex vscode install" onSelect={() => run(() => useSettingsView.getState().openView("agents"))}>
            <BotIcon />
            Connect an AI agent (MCP)
          </CommandItem>
          <CommandItem value="run setup checklist onboarding docker install doctor requirements health" onSelect={() => run(openSetupChecklist)}>
            <ClipboardCheckIcon />
            Run setup checklist
          </CommandItem>
        </CommandGroup>
        <CommandSeparator />
        <CommandGroup heading="Workflows">
          <CommandItem value="new workflow template" onSelect={() => run(() => useWorkflowStore.getState().openDialog("new-workflow"))}>
            <PlusIcon />
            New workflow…
          </CommandItem>
          <CommandItem value="import terraform docker compose project folder" onSelect={() => run(() => useWorkflowStore.getState().openDialog("import"))}>
            <UploadIcon />
            Import project (Terraform or Docker Compose)…
          </CommandItem>
          {workflow && workflow.kind !== "docker" && (
            <CommandItem value="export terraform" onSelect={() => run(() => useWorkflowStore.getState().openDialog("export"))}>
              <DownloadIcon />
              Export Terraform…
            </CommandItem>
          )}
          {workflow && (
            <CommandItem value="close workflow go home" onSelect={() => run(() => void closeWorkflow())}>
              <HomeIcon />
              Close workflow
              <Shortcut keys={[MOD_KEY, "W"]} />
            </CommandItem>
          )}
          {workflow && (
            <CommandItem
              value="archive this workflow hide stop sandbox"
              onSelect={() => run(() => void archiveWorkflow(workflow.id, workflow.name))}
            >
              <ArchiveIcon />
              Archive this workflow
            </CommandItem>
          )}
          {(workflows.data ?? [])
            .filter((w) => w.id !== workflow?.id && !w.archived)
            .map((w) => (
              <CommandItem key={w.id} value={`open workflow ${w.name} ${w.id}`} onSelect={() => run(() => void openWorkflow(w.id))}>
                <WorkflowIconTile icon={w.icon} size={18} />
                Open “{w.name}”
                <span className="ml-auto text-xs text-muted-foreground">{contentCount(w)}</span>
              </CommandItem>
            ))}
        </CommandGroup>
      </CommandList>
    </Command>
  )
}
