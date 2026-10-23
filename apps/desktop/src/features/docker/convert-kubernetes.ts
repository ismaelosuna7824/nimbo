/** A Kubernetes name: a DNS-1123 label starting with a letter (what the converter accepts). */
export const DNS_LABEL = /^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$/

/** A workflow name as a DNS label: the server's default application name, shown as the placeholder. */
export function suggestKubeName(name: string): string {
  const s = name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63)
    .replace(/-+$/, "")
  if (!s) return "app"
  return /^[0-9]/.test(s) ? `s-${s}`.slice(0, 63).replace(/-+$/, "") : s
}
