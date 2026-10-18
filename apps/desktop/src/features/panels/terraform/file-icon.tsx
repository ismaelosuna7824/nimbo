import {
  FileArchive,
  FileBraces,
  FileCode,
  FileImage,
  FileKey,
  FileLock,
  FileSliders,
  FileTerminal,
  FileText,
  Folder,
  FolderOpen,
  Package,
  type LucideIcon,
} from "lucide-react"
import type { WorkflowFile } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { baseName } from "./file-model"

const IMAGE_EXT = /\.(png|jpe?g|gif|svg|webp|ico)$/i
const ARCHIVE_EXT = /\.(zip|gz|tgz|jar|tar|7z)$/i

function iconFor(file: WorkflowFile): { icon: LucideIcon; className: string } {
  const name = baseName(file.path)
  if (name === ".terraform.lock.hcl") return { icon: FileLock, className: "text-muted-foreground" }
  if (IMAGE_EXT.test(name)) return { icon: FileImage, className: "text-pink-500 dark:text-pink-400" }
  if (ARCHIVE_EXT.test(name) || file.binary) return { icon: FileArchive, className: "text-muted-foreground" }
  if (file.kind === "tfvars") return { icon: FileSliders, className: "text-sky-600 dark:text-sky-400" }
  if (file.kind === "env") return { icon: FileKey, className: "text-amber-600 dark:text-amber-400" }
  switch (file.language) {
    case "hcl":
      return { icon: FileCode, className: "text-violet-600 dark:text-violet-400" }
    case "json":
      return { icon: FileBraces, className: "text-yellow-600 dark:text-yellow-400" }
    case "javascript":
      return { icon: FileCode, className: "text-yellow-600 dark:text-yellow-300" }
    case "typescript":
      return { icon: FileCode, className: "text-blue-600 dark:text-blue-400" }
    case "python":
      return { icon: FileCode, className: "text-emerald-600 dark:text-emerald-400" }
    case "go":
      return { icon: FileCode, className: "text-cyan-600 dark:text-cyan-400" }
    case "yaml":
      return { icon: FileCode, className: "text-rose-600 dark:text-rose-400" }
    case "markdown":
      return { icon: FileText, className: "text-sky-600 dark:text-sky-300" }
    case "shell":
      return { icon: FileTerminal, className: "text-green-600 dark:text-green-400" }
    default:
      return { icon: FileText, className: "text-muted-foreground" }
  }
}

export function FileIcon({ file, className }: { file: WorkflowFile; className?: string }) {
  const { icon: Icon, className: tint } = iconFor(file)
  return <Icon aria-hidden className={cn("size-3.5 shrink-0", tint, className)} />
}

/** Folders; a local module's folder (modules/<name>) is a package. */
export function FolderIcon({ path, open, className }: { path: string; open: boolean; className?: string }) {
  const module = /^modules\/[^/]+$/.test(path)
  const Icon = module ? Package : open ? FolderOpen : Folder
  return (
    <Icon
      aria-hidden
      className={cn("size-3.5 shrink-0", module ? "text-violet-600 dark:text-violet-400" : "text-muted-foreground", className)}
    />
  )
}
