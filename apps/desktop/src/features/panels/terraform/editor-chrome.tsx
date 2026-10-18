import { useEffect, useRef } from "react"
import {
  ChevronRight,
  Copy,
  Download,
  Ellipsis,
  FolderDown,
  GitCompare,
  Link2,
  Lock,
  Pencil,
  RotateCcw,
  Save,
  Search,
  TextWrap,
  X,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Kbd } from "@/components/ui/kbd"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { WorkflowFile } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { formatBytes } from "../lib/format"
import { FileIcon } from "./file-icon"
import { baseName, dirName, LANGUAGE_LABEL, readOnlyReason } from "./file-model"

interface EditorTabsProps {
  tabs: string[]
  active: string | null
  files: Map<string, WorkflowFile>
  modified: (path: string) => boolean
  onSelect: (path: string) => void
  onClose: (path: string) => void
}

/** Open files; middle-click or the × closes one. */
export function EditorTabs({ tabs, active, files, modified, onSelect, onClose }: EditorTabsProps) {
  const strip = useRef<HTMLDivElement>(null)
  // Names shared by several open tabs get their folder as a hint.
  const names: Record<string, number> = {}
  for (const t of tabs) names[baseName(t)] = (names[baseName(t)] ?? 0) + 1

  useEffect(() => {
    strip.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView({ block: "nearest", inline: "nearest" })
  }, [active])

  return (
    <div
      ref={strip}
      role="tablist"
      aria-label="Open files"
      className="flex h-8 shrink-0 items-stretch overflow-x-auto overflow-y-hidden border-b bg-muted/25 [scrollbar-width:none]"
    >
      {tabs.map((path) => {
        const file = files.get(path)
        const selected = path === active
        const dirty = modified(path)
        const name = baseName(path)
        return (
          <div
            key={path}
            role="tab"
            tabIndex={selected ? 0 : -1}
            aria-selected={selected}
            aria-label={name}
            title={path}
            onClick={() => onSelect(path)}
            onAuxClick={(e) => {
              if (e.button === 1) {
                e.preventDefault()
                onClose(path)
              }
            }}
            onMouseDown={(e) => {
              if (e.button === 1) e.preventDefault() // no autoscroll
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault()
                onSelect(path)
              }
            }}
            className={cn(
              "group/tab relative flex max-w-56 shrink-0 cursor-default items-center gap-1.5 border-r pr-1 pl-2.5 text-xs outline-none select-none",
              "text-muted-foreground hover:bg-muted/50 hover:text-foreground focus-visible:bg-muted/60",
              selected && "bg-background text-foreground hover:bg-background",
            )}
          >
            {selected && <span aria-hidden className="absolute inset-x-0 top-0 h-0.5 bg-primary" />}
            {file && <FileIcon file={file} />}
            <span className="truncate">{name}</span>
            {(names[name] ?? 0) > 1 && dirName(path) && (
              <span className="truncate text-[10px] text-muted-foreground/70">{dirName(path)}</span>
            )}
            <span className="relative flex size-5 shrink-0 items-center justify-center">
              {dirty && (
                <span
                  aria-label="modified"
                  className="absolute size-2 rounded-full bg-amber-500 group-hover/tab:invisible"
                />
              )}
              <button
                type="button"
                tabIndex={-1}
                aria-label={`Close ${name}`}
                onClick={(e) => {
                  e.stopPropagation()
                  onClose(path)
                }}
                className={cn(
                  "flex size-5 items-center justify-center rounded-sm text-muted-foreground opacity-0 outline-none hover:bg-muted hover:text-foreground group-hover/tab:opacity-100",
                  selected && !dirty && "opacity-100",
                )}
              >
                <X className="size-3" />
              </button>
            </span>
          </div>
        )
      })}
    </div>
  )
}

interface PathBarProps {
  file: WorkflowFile
  pro: boolean
  readOnly: boolean
  dirty: boolean
  saving: boolean
  wrap: boolean
  /** Text assets: Save and Revert. Generated files are applied from the changes bar. */
  onSave?: () => void
  onRevert?: () => void
  onShowChanges?: () => void
  onFind?: () => void
  onToggleWrap: () => void
  onCopy?: () => void
  onDownload: () => void
  onCopyPath: () => void
  onExport: () => void
  onSwitchToPro: () => void
}

/** Breadcrumb of the active file, its edit state and file actions. */
export function PathBar({
  file,
  pro,
  readOnly,
  dirty,
  saving,
  wrap,
  onSave,
  onRevert,
  onShowChanges,
  onFind,
  onToggleWrap,
  onCopy,
  onDownload,
  onCopyPath,
  onExport,
  onSwitchToPro,
}: PathBarProps) {
  const segments = file.path.split("/")
  const reason = readOnlyReason(file)
  return (
    <div className="flex h-7 shrink-0 items-center gap-2 border-b pr-1 pl-3 text-xs">
      <nav aria-label="File path" className="flex min-w-0 items-center gap-0.5 text-muted-foreground">
        {segments.map((s, i) => (
          <span key={i} className="flex min-w-0 items-center gap-0.5">
            {i > 0 && <ChevronRight aria-hidden className="size-3 shrink-0 opacity-60" />}
            <span className={cn("truncate", i === segments.length - 1 && "font-medium text-foreground")}>{s}</span>
          </span>
        ))}
      </nav>

      {readOnly ? (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="flex shrink-0 items-center gap-1 rounded-sm border px-1.5 py-px text-[10px] font-medium text-muted-foreground">
              <Lock className="size-2.5" />
              Read-only
            </span>
          </TooltipTrigger>
          <TooltipContent className="max-w-64">
            {file.editable && !pro
              ? file.generated
                ? "Generated from your diagram. Switch to Pro mode to edit the code directly."
                : "Switch to Pro mode to edit this file."
              : reason}
          </TooltipContent>
        </Tooltip>
      ) : (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="flex shrink-0 items-center gap-1 rounded-sm border border-primary/40 bg-primary/10 px-1.5 py-px text-[10px] font-medium text-primary">
              <Pencil className="size-2.5" />
              Editable
            </span>
          </TooltipTrigger>
          <TooltipContent className="max-w-64">
            {file.generated
              ? "Edits are re-imported into the diagram when you apply them (⌘S)."
              : "Saved into the workflow's files (⌘S); exports and deploys use it."}
          </TooltipContent>
        </Tooltip>
      )}
      {readOnly && file.editable && !pro && (
        <button
          type="button"
          onClick={onSwitchToPro}
          className="hidden shrink-0 truncate text-[11px] text-primary underline-offset-2 hover:underline @[26rem]:inline"
        >
          Switch to Pro to edit
        </button>
      )}

      <div className="ml-auto flex shrink-0 items-center gap-0.5">
        <span className="mr-1.5 hidden text-[11px] text-muted-foreground tabular-nums @[40rem]:inline">
          {LANGUAGE_LABEL[file.language]} · {formatBytes(file.size)}
        </span>
        {dirty && onSave && (
          <>
            {onShowChanges && (
              <IconButton label="Show changes" onClick={onShowChanges}>
                <GitCompare />
              </IconButton>
            )}
            <IconButton label="Revert changes" onClick={onRevert}>
              <RotateCcw />
            </IconButton>
            <Button size="xs" className="mx-1" onClick={onSave} disabled={saving}>
              {saving ? <Spinner /> : <Save />} Save
            </Button>
          </>
        )}
        {onFind && (
          <IconButton label="Find" kbd="⌘F" onClick={onFind}>
            <Search />
          </IconButton>
        )}
        {onCopy && (
          <IconButton label="Copy contents" onClick={onCopy}>
            <Copy />
          </IconButton>
        )}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="Wrap lines"
              aria-pressed={wrap}
              onClick={onToggleWrap}
              disabled={!onFind}
              className={cn(wrap && "bg-primary/15 text-primary hover:bg-primary/20 hover:text-primary")}
            >
              <TextWrap />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{wrap ? "Don't wrap lines" : "Wrap lines"}</TooltipContent>
        </Tooltip>
        <IconButton label="Download file" onClick={onDownload}>
          <Download />
        </IconButton>
        <DropdownMenu>
          <Tooltip>
            <TooltipTrigger asChild>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-xs" aria-label="More file actions">
                  <Ellipsis />
                </Button>
              </DropdownMenuTrigger>
            </TooltipTrigger>
            <TooltipContent>More</TooltipContent>
          </Tooltip>
          <DropdownMenuContent align="end" className="w-56">
            <DropdownMenuItem onSelect={onCopyPath}>
              <Link2 /> Copy path in export
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onDownload}>
              <Download /> Download file
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={onExport}>
              <FolderDown /> Export all files…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  )
}

function IconButton({
  label,
  kbd,
  onClick,
  children,
}: {
  label: string
  kbd?: string
  onClick?: () => void
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon-xs" aria-label={label} onClick={onClick}>
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        {label}
        {kbd && <Kbd>{kbd}</Kbd>}
      </TooltipContent>
    </Tooltip>
  )
}
