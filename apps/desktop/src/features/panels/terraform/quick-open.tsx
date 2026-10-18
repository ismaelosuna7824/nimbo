import {
  CommandDialog,
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import type { WorkflowFile } from "@/lib/api/types"
import { FileIcon } from "./file-icon"
import { baseName, dirName } from "./file-model"

interface QuickOpenProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  files: WorkflowFile[]
  /** Open tabs, most recent first: listed first. */
  recent: string[]
  onPick: (path: string) => void
}

/** ⌘P: fuzzy file picker over everything the export writes. */
export function QuickOpen({ open, onOpenChange, files, recent, onPick }: QuickOpenProps) {
  const rank = (f: WorkflowFile) => {
    const i = recent.indexOf(f.path)
    return i < 0 ? recent.length : i
  }
  const sorted = [...files].sort((a, b) => rank(a) - rank(b) || a.path.localeCompare(b.path))
  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Go to file"
      description="Search the workflow's files by name or path"
      className="sm:max-w-lg"
    >
      <Command>
        <CommandInput placeholder="Go to file…" />
        <CommandList className="max-h-[360px]">
          <CommandEmpty>No matching files.</CommandEmpty>
          <CommandGroup heading="Files">
            {sorted.map((f) => (
              <CommandItem
                key={f.path}
                value={f.path}
                keywords={[baseName(f.path)]}
                onSelect={() => {
                  onOpenChange(false)
                  onPick(f.path)
                }}
                className="gap-2"
              >
                <FileIcon file={f} />
                <span className="truncate font-medium">{baseName(f.path)}</span>
                <span className="min-w-0 truncate text-xs text-muted-foreground">{dirName(f.path)}</span>
                {f.generated && (
                  <span className="ml-auto shrink-0 text-[10px] tracking-wide text-muted-foreground uppercase">
                    generated
                  </span>
                )}
              </CommandItem>
            ))}
          </CommandGroup>
        </CommandList>
      </Command>
    </CommandDialog>
  )
}
