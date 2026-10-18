import { BoxIcon, icons, type LucideProps } from "lucide-react"

function toPascal(name: string) {
  return name
    .split(/[-_\s]+/)
    .filter(Boolean)
    .map((p) => p[0].toUpperCase() + p.slice(1))
    .join("")
}

/** Renders a lucide icon by name ("message-square" or "MessageSquare"); unknown names fall back to a box. */
export function ResourceIcon({ name, ...props }: { name?: string } & LucideProps) {
  const Icon = (name && (icons[name as keyof typeof icons] ?? icons[toPascal(name) as keyof typeof icons])) || BoxIcon
  return <Icon {...props} />
}
