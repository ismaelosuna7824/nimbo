import type { Cloud } from "@/lib/api/types"

export const CLOUD_META: Record<Cloud, { label: string; dot: string; text: string; border: string; accent: string }> = {
  aws: { label: "AWS", dot: "bg-orange-500", text: "text-orange-500", border: "border-orange-500/40", accent: "#f97316" },
  azure: { label: "Azure", dot: "bg-sky-500", text: "text-sky-500", border: "border-sky-500/40", accent: "#0ea5e9" },
  gcp: { label: "Google Cloud", dot: "bg-emerald-500", text: "text-emerald-500", border: "border-emerald-500/40", accent: "#10b981" },
  oci: { label: "Oracle Cloud", dot: "bg-red-500", text: "text-red-500", border: "border-red-500/40", accent: "#ef4444" },
}

/** Cloud of a Terraform type by provider prefix. */
export function cloudOfType(type: string): Cloud | undefined {
  if (type.startsWith("aws_")) return "aws"
  if (type.startsWith("azurerm_")) return "azure"
  if (type.startsWith("google_")) return "gcp"
  if (type.startsWith("oci_")) return "oci"
  return undefined
}
