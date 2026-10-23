import { describe, expect, test } from "bun:test"
import type { JsonSchema } from "./schema"
import { parseYaml } from "./tree"
import { validateYaml } from "./validate"

// A Kubernetes-like schema: properties listed, additionalProperties unset (unknown keys are warnings).
const SCHEMA: JsonSchema = {
  type: "object",
  required: ["apiVersion", "kind"],
  properties: {
    apiVersion: { type: "string" },
    kind: { type: "string", enum: ["Deployment", "Service"] },
    metadata: { $ref: "#/definitions/meta" },
    spec: {
      type: "object",
      properties: {
        replicas: { type: "integer" },
        paused: { type: "boolean" },
        ports: { type: "array", items: { type: "object", required: ["port"], properties: { port: { "x-kubernetes-int-or-string": true }, name: { type: "string" } } } },
      },
    },
  },
  definitions: {
    meta: { type: "object", properties: { name: { type: "string" }, labels: { type: "object", additionalProperties: { type: "string" } } } },
  },
}

const problems = (text: string, schema = SCHEMA) =>
  parseYaml(text).flatMap((d) => validateYaml(d.root, schema).map((p) => ({ ...p, at: text.slice(p.from, p.to) })))

describe("validateYaml", () => {
  test("a valid document has no problems (labels take any key, int-or-string takes both)", () => {
    expect(problems(`apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  labels:\n    app: web\nspec:\n  replicas: 2\n  ports:\n    - port: 80\n    - port: http\n`)).toEqual([])
  })

  test("wrong types, enum values, unknown keys (with a suggestion) and missing required keys", () => {
    const p = problems(`kind: Deploymnt\nmetadata:\n  nme: web\nspec:\n  replicas: two\n  paused: yes please\n  ports:\n    - name: x\n`)
    expect(p.map((x) => [x.message, x.at])).toEqual([
      ["“Deploymnt” is not one of: Deployment, Service", "Deploymnt"],
      ["Unknown key “nme” in metadata — did you mean “name”?", "nme"],
      ["Expected number, found a string", "two"],
      ["Expected boolean, found a string", "yes please"],
      ["Missing required key “port” in spec.ports", "name"],
      ["Missing required key “apiVersion”", "kind"],
    ])
    expect(p.find((x) => x.at === "nme")?.severity).toBe("warning")
  })

  test("aliases, merge keys and templated values are not judged", () => {
    expect(problems(`apiVersion: v1\nkind: Service\nspec:\n  replicas: {{ .Values.replicas }}\n  paused: \${PAUSED}\n`)).toEqual([])
    expect(problems(`apiVersion: v1\nkind: Service\nspec:\n  <<: *base\n  replicas: *n\n`)).toEqual([])
  })

  test("every document of a stream is checked on its own", () => {
    const p = problems(`apiVersion: v1\nkind: Service\n---\nkind: Pod\n`)
    expect(p.map((x) => x.message)).toEqual(["“Pod” is not one of: Deployment, Service", "Missing required key “apiVersion”"])
  })
})
