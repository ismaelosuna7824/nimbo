// Pure logic of the service inspector's Hardening section: which runtime protections a service has (the checks of
// the dk_root_user, dk_hardening, dk_privileged, dk_docker_sock and dk_no_init findings in
// backend/internal/compose) and the patches that turn them on or off.

import type { ServiceFacts, ServicePatch } from "@/lib/api/types"

/** The user a service runs as is root: empty (the image's default, root unless its Dockerfile says otherwise), root or 0. */
export function isRootUser(user?: string): boolean {
  const u = (user ?? "").trim()
  return u === "" || u === "root" || u === "0" || u.startsWith("root:") || u.startsWith("0:")
}

/** User for "Run as non-root": the image's documented unprivileged user, else 1000:1000. */
export function nonRootUserFor(svc: Pick<ServiceFacts, "nonRootUser">): { user: string; documented: boolean } {
  return svc.nonRootUser ? { user: svc.nonRootUser, documented: true } : { user: "1000:1000", documented: false }
}

const NO_NEW_PRIVILEGES = "no-new-privileges:true"

export function dropsAllCapabilities(svc: Pick<ServiceFacts, "capDrop">): boolean {
  return (svc.capDrop ?? []).some((c) => c.toUpperCase() === "ALL")
}

/** security_opt has no-new-privileges (bare or :true), like the dk_hardening check. */
export function hasNoNewPrivileges(svc: Pick<ServiceFacts, "securityOpt">): boolean {
  return (svc.securityOpt ?? []).some((o) => o.startsWith("no-new-privileges") && !o.endsWith(":false"))
}

export function mountsDockerSocket(svc: Pick<ServiceFacts, "volumes">): boolean {
  return svc.volumes.some((v) => v.type === "bind" && (v.source ?? "").endsWith("docker.sock"))
}

export type HardeningCheck = "non-root" | "cap-drop" | "read-only" | "no-new-privileges" | "init" | "not-privileged" | "no-docker-socket"

export interface HardeningItem {
  id: HardeningCheck
  label: string
  ok: boolean
}

/** The protections a service has, in the order the section lists them. */
export function hardeningItems(svc: ServiceFacts): HardeningItem[] {
  return [
    { id: "non-root", label: "Runs as a non-root user", ok: !isRootUser(svc.user) },
    { id: "cap-drop", label: "Drops all Linux capabilities", ok: dropsAllCapabilities(svc) },
    { id: "read-only", label: "Read-only root filesystem", ok: !!svc.readOnly },
    { id: "no-new-privileges", label: "No privilege escalation (no-new-privileges)", ok: hasNoNewPrivileges(svc) },
    { id: "init", label: "Init process as PID 1", ok: !!svc.init },
    { id: "not-privileged", label: "Not privileged", ok: !svc.privileged },
    { id: "no-docker-socket", label: "No Docker socket mounted", ok: !mountsDockerSocket(svc) },
  ]
}

export function hardeningScore(svc: ServiceFacts): { passed: number; total: number } {
  const items = hardeningItems(svc)
  return { passed: items.filter((i) => i.ok).length, total: items.length }
}

/** Patch of the "Drop all capabilities" toggle: adds or removes ALL, keeping the other entries. */
export function capDropPatch(svc: Pick<ServiceFacts, "capDrop">, on: boolean): ServicePatch {
  const rest = (svc.capDrop ?? []).filter((c) => c.toUpperCase() !== "ALL")
  const next = on ? [...rest, "ALL"] : rest
  return { capDrop: next.length ? next : null }
}

/** Patch of the "No new privileges" toggle, keeping the other security options. */
export function noNewPrivilegesPatch(svc: Pick<ServiceFacts, "securityOpt">, on: boolean): ServicePatch {
  const rest = (svc.securityOpt ?? []).filter((o) => !o.startsWith("no-new-privileges"))
  const next = on ? [...rest, NO_NEW_PRIVILEGES] : rest
  return { securityOpt: next.length ? next : null }
}

/** Patch of the "Read-only filesystem" toggle: turning it on gives the app a tmpfs at /tmp unless it has one. */
export function readOnlyPatch(svc: Pick<ServiceFacts, "tmpfs">, on: boolean): ServicePatch {
  if (!on) return { readOnly: false }
  return (svc.tmpfs ?? []).length ? { readOnly: true } : { readOnly: true, tmpfs: ["/tmp"] }
}

/** A capability as compose writes it: upper case, without the CAP_ prefix ("net_bind_service" → NET_BIND_SERVICE). */
export function normalizeCapability(text: string): string {
  return text.trim().toUpperCase().replace(/^CAP_/, "")
}

/** Adds an item to a chip list (no duplicates, blank ignored). */
export function withItem(list: string[] | undefined, item: string): string[] {
  const v = item.trim()
  const cur = list ?? []
  return v && !cur.includes(v) ? [...cur, v] : cur
}

export function withoutItem(list: string[] | undefined, item: string): string[] | null {
  const next = (list ?? []).filter((x) => x !== item)
  return next.length ? next : null
}

/** A secret name for an environment variable: POSTGRES_PASSWORD → postgres_password. */
export function secretNameFor(key: string): string {
  const name = key
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "_")
    .replace(/^[^a-z0-9]+/, "")
  return name || "secret"
}

export const SECRET_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9._-]*$/

export interface SecretCandidate {
  key: string
  /** The image reads <key>_FILE (its documentation says so). */
  imageReadsFile: boolean
  secret: string
}

/**
 * Environment variables worth moving to a secret file: written in the compose file (not an env_file), looking like
 * credentials (`isSecretKey`, or redacted by the daemon), and not already a `_FILE` pointer.
 */
export function secretCandidates(svc: Pick<ServiceFacts, "environment" | "fileEnv">, isSecretKey: (key: string) => boolean): SecretCandidate[] {
  return svc.environment
    .filter((e) => !e.fromFile && !e.key.endsWith("_FILE") && (e.secret || isSecretKey(e.key)))
    .map((e) => ({ key: e.key, imageReadsFile: (svc.fileEnv ?? []).includes(e.key), secret: secretNameFor(e.key) }))
}
