import { describe, expect, test } from "bun:test"
import type { CompletionResult } from "@codemirror/autocomplete"
import type { DockerImage } from "@/lib/api/types"
import { dockerfileCompletions, dockerfileSymbols, INSTRUCTIONS } from "./dockerfile"

const IMAGES: DockerImage[] = [
  { id: "node", image: "node", tag: "22-alpine", label: "Node.js", description: "JavaScript runtime", category: "language", icon: "node", ports: [] },
]

function complete(src: string, explicit = true): (CompletionResult & { text: string; pos: number }) | null {
  const pos = src.indexOf("‸")
  const text = src.slice(0, pos) + src.slice(pos + 1)
  const r = dockerfileCompletions(text, pos, explicit, IMAGES)
  return r && { ...r, text, pos }
}

const labels = (r: CompletionResult | null) => (r?.options ?? []).map((o) => o.label)

const MULTI = `ARG NODE_VERSION=22
FROM node:\${NODE_VERSION}-alpine AS deps
WORKDIR /app
COPY package*.json ./
RUN npm ci

FROM deps AS build
ENV NODE_ENV=production PORT=8080
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
`

describe("Dockerfile completion: instructions", () => {
  test("every instruction at a line start, replacing the typed word", () => {
    const r = complete(`FROM node\nCO‸`)
    expect(labels(r)).toEqual(INSTRUCTIONS.map((i) => i.name))
    expect(r?.from).toBe(r!.pos - 2)
  })

  test("lowercase typing still matches (the range covers it)", () => {
    const r = complete(`FROM node\nhea‸`)
    expect(r?.from).toBe(r!.pos - 3)
    expect(r?.validFor instanceof RegExp && r.validFor.test("hea")).toBe(true)
  })

  test("deprecated MAINTAINER ranks last", () => {
    const r = complete(`FROM node\n‸`)
    const boost = Object.fromEntries(r!.options.map((o) => [o.label, o.boost ?? 0]))
    expect(boost.MAINTAINER).toBeLessThan(boost.RUN)
  })

  test("an empty line pops up only when asked", () => {
    expect(complete(`FROM node\n‸`, false)).toBeNull()
  })

  test("a continuation line is an argument, not an instruction", () => {
    expect(labels(complete(`FROM node\nRUN apt-get update && \\\n    ap‸`))).not.toContain("RUN")
  })

  test("nothing in comments", () => {
    expect(complete(`# FR‸`)).toBeNull()
  })
})

describe("Dockerfile completion: flags", () => {
  test("COPY flags after --", () => {
    const r = complete(`FROM node\nCOPY --‸`)
    expect(labels(r)).toEqual(expect.arrayContaining(["--from=", "--chown=", "--chmod=", "--link"]))
    expect(r?.from).toBe(r!.pos - 2)
  })

  test("HEALTHCHECK and RUN flags are their own", () => {
    expect(labels(complete(`FROM node\nHEALTHCHECK --int‸`))).toContain("--interval=")
    const run = labels(complete(`FROM node\nRUN --‸`))
    expect(run).toContain("--mount=type=cache,target=")
    expect(run).not.toContain("--chown=")
  })

  test("instructions without flags offer none", () => {
    expect(complete(`FROM node\nWORKDIR --‸`)).toBeNull()
  })

  test("FROM --platform", () => {
    expect(labels(complete(`FROM --pl‸`))).toEqual(["--platform="])
  })
})

describe("Dockerfile completion: stages", () => {
  test("COPY --from= offers the stages declared above, then catalog images", () => {
    const r = complete(`${MULTI}COPY --from=‸`)
    expect(labels(r)).toEqual(["deps", "build", "node:22-alpine"])
    expect(r?.from).toBe(r!.pos)
  })

  test("a partial stage name is replaced", () => {
    const r = complete(`${MULTI}COPY --from=bu‸`)
    expect(r?.from).toBe(r!.pos - 2)
    expect(labels(r)).toContain("build")
  })

  test("stages declared below the cursor are not offered", () => {
    const src = `FROM node AS deps\nCOPY --from=‸ /a /b\nFROM deps AS later\n`
    expect(labels(complete(src))).toEqual(["deps", "node:22-alpine"])
  })

  test("FROM offers earlier stages and images", () => {
    expect(labels(complete(`FROM node AS base\nFROM ‸`))).toEqual(["base", "node:22-alpine"])
  })

  test("FROM <image> then AS", () => {
    expect(labels(complete(`FROM node:22 A‸`))).toEqual(["AS"])
  })

  test("a stage named with flags or lowercase 'as' is found", () => {
    expect(dockerfileSymbols(`FROM --platform=$BUILDPLATFORM golang:1.23 as builder\n`).stages).toEqual(["builder"])
  })
})

describe("Dockerfile completion: ARG and ENV", () => {
  test("$ offers the ARG and ENV names declared above", () => {
    const r = complete(`${MULTI}RUN echo $‸`)
    expect(labels(r)).toEqual(["NODE_VERSION", "NODE_ENV", "PORT"])
    expect(r?.options.find((o) => o.label === "NODE_VERSION")?.detail).toBe("ARG")
  })

  test("${ with a partial name replaces only the name", () => {
    const r = complete(`${MULTI}RUN echo \${NODE_‸`)
    expect(r?.from).toBe(r!.pos - 5)
  })

  test("variables declared later are not offered", () => {
    expect(labels(complete(`FROM node\nRUN echo $‸\nARG LATER=1\n`))).toEqual([])
  })

  test("the legacy 'ENV KEY value' form and multi-line ENV are read", () => {
    const s = dockerfileSymbols(`FROM x\nENV LEGACY some value\nENV A=1 \\\n    B=2\n`)
    expect(s.vars.map((v) => v.name)).toEqual(["LEGACY", "A", "B"])
  })

  test("a name declared twice is offered once", () => {
    expect(labels(complete(`ARG V=1\nFROM x\nARG V\nRUN echo $‸`))).toEqual(["V"])
  })
})
