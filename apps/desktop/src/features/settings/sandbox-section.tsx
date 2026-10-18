import { useState } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { InfoIcon, RotateCcwIcon, RefreshCwIcon } from "lucide-react"
import { toast } from "sonner"
import { CLOUD_META } from "@/components/cloud-meta"
import { errorMessage } from "@/components/errors"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api/client"
import { ALL_CLOUDS, type Settings } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { Row, Section } from "./common"
import { SETTINGS_KEY, useSettings, useSettingsDefaults } from "./store"

const AWS_REGIONS: { id: string; label: string }[] = [
  { id: "us-east-1", label: "US East (N. Virginia)" },
  { id: "us-east-2", label: "US East (Ohio)" },
  { id: "us-west-1", label: "US West (N. California)" },
  { id: "us-west-2", label: "US West (Oregon)" },
  { id: "ca-central-1", label: "Canada (Central)" },
  { id: "sa-east-1", label: "South America (São Paulo)" },
  { id: "eu-west-1", label: "Europe (Ireland)" },
  { id: "eu-west-2", label: "Europe (London)" },
  { id: "eu-west-3", label: "Europe (Paris)" },
  { id: "eu-central-1", label: "Europe (Frankfurt)" },
  { id: "eu-north-1", label: "Europe (Stockholm)" },
  { id: "eu-south-1", label: "Europe (Milan)" },
  { id: "eu-south-2", label: "Europe (Spain)" },
  { id: "me-central-1", label: "Middle East (UAE)" },
  { id: "af-south-1", label: "Africa (Cape Town)" },
  { id: "ap-south-1", label: "Asia Pacific (Mumbai)" },
  { id: "ap-northeast-1", label: "Asia Pacific (Tokyo)" },
  { id: "ap-northeast-2", label: "Asia Pacific (Seoul)" },
  { id: "ap-southeast-1", label: "Asia Pacific (Singapore)" },
  { id: "ap-southeast-2", label: "Asia Pacific (Sydney)" },
]

function sameSettings(a: Settings, b: Settings) {
  return (
    a.docker === b.docker &&
    a.terraformImage === b.terraformImage &&
    a.defaultAWSRegion === b.defaultAWSRegion &&
    ALL_CLOUDS.every((c) => (a.images[c] ?? "") === (b.images[c] ?? ""))
  )
}

export function SandboxSection() {
  const settings = useSettings()
  if (settings.isError) {
    return (
      <Section title="Sandbox & emulators">
        <p className="text-sm text-destructive">Couldn’t load settings: {errorMessage(settings.error)}</p>
      </Section>
    )
  }
  if (!settings.data) {
    return (
      <Section title="Sandbox & emulators">
        <Skeleton className="h-64" />
      </Section>
    )
  }
  return <SandboxForm saved={settings.data} />
}

function SandboxForm({ saved }: { saved: Settings }) {
  const defaults = useSettingsDefaults()
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState(saved)

  const save = useMutation({
    mutationFn: (s: Settings) => api.saveSettings(s),
    onSuccess: (s) => {
      queryClient.setQueryData(SETTINGS_KEY, s)
      setDraft(s)
      toast.success("Sandbox settings saved", { description: "They apply to sandboxes started from now on." })
    },
  })

  const dirty = !sameSettings(draft, saved)
  const set = (patch: Partial<Settings>) => setDraft({ ...draft, ...patch })
  const regions = AWS_REGIONS.some((r) => r.id === draft.defaultAWSRegion)
    ? AWS_REGIONS
    : [{ id: draft.defaultAWSRegion, label: "Custom" }, ...AWS_REGIONS]

  return (
    <Section
      title="Sandbox & emulators"
      description="Each workflow deploys into its own local sandbox: Floci cloud emulators and Terraform, all running in Docker."
    >
      <Alert>
        <InfoIcon />
        <AlertTitle>Changes apply to newly started sandboxes</AlertTitle>
        <AlertDescription>
          Running sandboxes keep their current containers. Restart a sandbox to use new images; its deployed resources are kept.
        </AlertDescription>
      </Alert>
      <RestartOpenSandbox />

      <form
        className="flex flex-col"
        onSubmit={(e) => {
          e.preventDefault()
          save.mutate({ ...draft, defaultMode: saved.defaultMode })
        }}
      >
        <Row label="Docker command" htmlFor="set-docker" description="Program used to run containers: a name on your PATH or an absolute path (Podman works too).">
          <Input
            id="set-docker"
            value={draft.docker}
            onChange={(e) => set({ docker: e.target.value })}
            placeholder={defaults.data?.docker ?? "docker"}
            className="font-mono"
            spellCheck={false}
          />
        </Row>
        <Row label="Emulator images" description="The Floci image started for each cloud. Pin a version (e.g. floci/floci:2.3.0) for repeatable sandboxes.">
          <div className="flex flex-col gap-2">
            {ALL_CLOUDS.map((c) => (
              <ImageInput
                key={c}
                id={`set-image-${c}`}
                label={CLOUD_META[c].label}
                dot={CLOUD_META[c].dot}
                value={draft.images[c] ?? ""}
                defaultValue={defaults.data?.images[c]}
                onChange={(v) => set({ images: { ...draft.images, [c]: v } })}
              />
            ))}
          </div>
        </Row>
        <Row label="Terraform image" description="Terraform runs inside this container, so you don’t need Terraform installed.">
          <ImageInput
            id="set-tf-image"
            label="Terraform"
            value={draft.terraformImage}
            defaultValue={defaults.data?.terraformImage}
            onChange={(v) => set({ terraformImage: v })}
          />
        </Row>
        <Row
          label="Default AWS region"
          htmlFor="set-region"
          description="Used for new AWS provider blocks and as AWS_REGION when deploying to the sandbox. Existing workflows keep their region."
        >
          <Select value={draft.defaultAWSRegion} onValueChange={(v) => set({ defaultAWSRegion: v })}>
            <SelectTrigger id="set-region" className="w-full max-w-sm">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {regions.map((r) => (
                <SelectItem key={r.id} value={r.id}>
                  <span className="font-mono text-xs">{r.id}</span>
                  <span className="text-muted-foreground">{r.label}</span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Row>
        {save.isError && <p className="pb-2 text-sm text-destructive">{errorMessage(save.error)}</p>}
        <div className="sticky bottom-0 -mx-1 flex items-center justify-end gap-2 border-t bg-background/95 px-1 py-3 backdrop-blur">
          {dirty && <span className="mr-auto text-xs text-muted-foreground">Unsaved changes</span>}
          <Button type="button" variant="ghost" disabled={!dirty || save.isPending} onClick={() => setDraft(saved)}>
            Discard
          </Button>
          <Button type="submit" disabled={!dirty || save.isPending}>
            {save.isPending && <Spinner />} Save changes
          </Button>
        </div>
      </form>
    </Section>
  )
}

function ImageInput({
  id,
  label,
  dot,
  value,
  defaultValue,
  onChange,
}: {
  id: string
  label: string
  dot?: string
  value: string
  defaultValue?: string
  onChange: (v: string) => void
}) {
  const custom = defaultValue !== undefined && value.trim() !== defaultValue
  return (
    <div className="flex items-center gap-2">
      {dot && (
        <label htmlFor={id} className="flex w-28 shrink-0 items-center gap-2 text-xs text-muted-foreground">
          <span className={cn("size-2 rounded-full", dot)} />
          {label}
        </label>
      )}
      <Input
        id={id}
        aria-label={dot ? undefined : `${label} image`}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={defaultValue}
        className="font-mono"
        spellCheck={false}
      />
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={`Reset ${label} image to default`}
            disabled={!custom}
            onClick={() => defaultValue !== undefined && onChange(defaultValue)}
          >
            <RotateCcwIcon />
          </Button>
        </TooltipTrigger>
        <TooltipContent>{defaultValue ? `Reset to ${defaultValue}` : "Reset to default"}</TooltipContent>
      </Tooltip>
    </div>
  )
}

/** Offers to restart the open workflow's running sandbox so new settings take effect. */
function RestartOpenSandbox() {
  const workflow = useWorkflowStore((s) => s.workflow)
  const sandbox = useWorkflowStore((s) => s.sandbox)
  const refreshRuntime = useWorkflowStore((s) => s.refreshRuntime)
  const restart = useMutation({
    mutationFn: async (id: string) => {
      await api.sandboxAction(id, "stop")
      return api.sandboxAction(id, "start")
    },
    onSuccess: () => toast.success("Sandbox restarted with the current settings"),
    onError: (err) => toast.error("Couldn’t restart the sandbox", { description: errorMessage(err) }),
    onSettled: () => void refreshRuntime(),
  })
  if (!workflow || sandbox?.state !== "running") return null
  return (
    <div className="flex items-center gap-3 rounded-lg border px-3 py-2.5 text-sm">
      <span className="min-w-0 flex-1">
        The sandbox of <span className="font-medium">{workflow.name}</span> is running with the settings it started with.
      </span>
      <Button size="sm" variant="outline" disabled={restart.isPending} onClick={() => restart.mutate(workflow.id)}>
        {restart.isPending ? <Spinner /> : <RefreshCwIcon />} Restart sandbox
      </Button>
    </div>
  )
}
