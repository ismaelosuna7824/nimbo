// The caption of each step: who handles the request, in plain words a beginner can follow.
import type { Chip, Describer, ItemInfo, Role, Where } from "./describe"
import type { Walk } from "./paths"

export interface Caption {
  name: string
  type: string
  /** What happens here, in one or two short sentences. */
  text: string
  /** The catalog's description of the component. */
  about?: string
  /** How this hop is connected ("environment variable DATABASE_URL") and the ports it states. */
  via?: string
  facts: string[]
  where?: Where
  also: Chip[]
  /** Set on queues and topics: the request's answer does not wait for what comes after. */
  async?: boolean
}

const UNKNOWN: ItemInfo = { id: "", name: "", type: "", role: "other", facts: [], also: [], nodeIds: [], async: false, supporting: false }

/** How sentences name an item: with its service when its own name is ambiguous. */
const said = (i: ItemInfo) => i.ref ?? i.name

/** What a component does with a request that goes on to `next`. */
function onward(info: ItemInfo, next: ItemInfo | undefined): string {
  if (!next) return ""
  switch (next.role) {
    case "database":
      return ` It reads or writes data in ${said(next)}.`
    case "cache":
      return ` It checks the cache ${said(next)} first.`
    case "storage":
      return ` It stores or reads files in ${said(next)}.`
    case "messaging":
      return ` It publishes a message to ${said(next)}.`
    case "internet":
      return " It calls out to the internet."
    case "network":
      return ` It reaches into ${said(next)}.`
    default:
      return info.role === "front" ? ` It passes the request on to ${said(next)}.` : ` It calls ${said(next)}.`
  }
}

const DOES: Record<Role, (entry: boolean) => string> = {
  users: () => "A user opens your app or calls your API: the request starts here.",
  internet: () => "Traffic comes in from the public internet.",
  front: (entry) => (entry ? "It is the front door: it receives the request from the internet and routes it." : "It routes the request."),
  compute: (entry) => (entry ? "It receives the request and runs your code to handle it." : "It runs your code to handle the request."),
  database: () => "It stores the data and answers the query.",
  cache: () => "It answers from memory, fast, so the database is asked less often.",
  storage: () => "It keeps the files (objects) the request reads or writes.",
  messaging: () => "It holds the message until a consumer picks it up.",
  identity: () => "It checks who is calling (authenticates) before the request goes on.",
  network: () => "The traffic enters this network.",
  other: () => "It takes part in handling the request.",
}

/**
 * The caption of `walk.steps[index]`. The onward sentence names the step that follows on the same way
 * (none at an end, a fork, or when the following step resumes another way).
 */
export function captionOf(walk: Walk, index: number, d: Describer): Caption {
  const step = walk.steps[index]
  const info = d.info.get(step.at) ?? { ...UNKNOWN, id: step.at, name: step.at }
  const prev = step.from ? d.info.get(step.from) : undefined
  const following = walk.steps[index + 1]
  const next = following && following.from === step.at && !step.branches ? d.info.get(following.at) : undefined
  // The front door: right after Users/Internet, or the first step of a walk without actors.
  const entry = prev ? prev.role === "users" || prev.role === "internet" : index === 0 && info.role !== "users"

  let text: string
  if (info.role === "internet" && step.from) text = "The request leaves your design for the public internet."
  else if (step.joins) text = "The request continues from here the way shown before."
  else if (step.afterAsync && prev?.async && !info.async) text = `Later, it picks the message up from ${said(prev)} and processes it on its own.${onward(info, next)}`
  // A queue or topic holds the message for its consumer, or hands it on to another queue.
  else if (info.async) text = !next ? DOES.messaging(false) : next.async ? `It holds the message and passes it on to ${said(next)}.` : `It holds the message until ${said(next)} picks it up.`
  else text = `${DOES[info.role](entry)}${info.role === "users" ? "" : onward(info, next)}`
  if (step.resumes && prev) text = `Back at ${said(prev)}, the other way leads here. ${text}`
  if (!step.joins && step.end && !step.afterAsync && (info.role === "database" || info.role === "storage" || info.role === "cache" || info.role === "compute")) {
    text += " The answer travels back the way it came."
  }
  if (step.branches && !step.joins) text += ` From here the flow splits ${step.branches.length} ways.`

  const via = step.from ? (step.reversed ? d.relation(step.at, step.from) : d.relation(step.from, step.at)) : undefined
  return {
    name: info.name,
    type: info.type,
    text,
    ...(info.about ? { about: info.about } : {}),
    ...(via ? { via } : {}),
    facts: info.facts,
    ...(info.where ? { where: info.where } : {}),
    also: info.also,
    ...(step.async ? { async: true } : {}),
  }
}

/** "The request passed through 5 components; 2 continued asynchronously." */
export function summaryText({ components, async }: { components: number; async: number }): string {
  const passed = `The request passed through ${components} component${components === 1 ? "" : "s"}`
  return async ? `${passed}; ${async} continued asynchronously.` : `${passed}.`
}

/** The empty state's hint per kind of workspace. */
export const NO_ENTRY_HINT: Record<"terraform" | "docker" | "kubernetes", string> = {
  terraform: "Add a public front door — an API gateway, a load balancer, a public function URL or a container app with external ingress — so users can reach the design.",
  docker: "Publish a port on a service (for example ports: \"8080:80\") so requests can come in.",
  kubernetes: "Add an Ingress or a Service of type LoadBalancer so requests can reach the workloads.",
}
