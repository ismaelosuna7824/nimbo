import { describe, expect, test } from "bun:test"
import type { CompletionResult } from "@codemirror/autocomplete"
import { keysAt } from "@/features/code-intel/yaml/schema"
import { schemaCompletions } from "@/features/code-intel/yaml/schema-intel"
import { parseYaml } from "@/features/code-intel/yaml/tree"
import { validateYaml } from "@/features/code-intel/yaml/validate"
import { CHART_SCHEMA, commentAbove, valuesSchema } from "./helm-schema"
import { actionAt, dotAt, namedTemplates, templateCompletions, templateDefinition, templateHover, type ChartEnv } from "./helm-template"

const VALUES = `# -- Number of pods
replicaCount: 2

image:
  # The image repository
  repository: ghcr.io/acme/web
  tag: ""
  pullPolicy: IfNotPresent

service:
  type: ClusterIP
  port: 80

ingress:
  enabled: false
  hosts:
    - host: web.local
      paths:
        - path: /
`

const HELPERS = `{{/*
Expand the name of the chart.
*/}}
{{- define "web.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "web.labels" -}}
app.kubernetes.io/name: {{ include "web.name" . }}
{{- end }}
`

const env = (): ChartEnv => ({
  valuesPath: "chart/values.yaml",
  valuesText: VALUES,
  valuesSchema: valuesSchema(VALUES, undefined),
  templates: { "chart/templates/_helpers.tpl": HELPERS },
})

const labels = (r: CompletionResult | null) => r?.options.map((o) => o.label) ?? []

describe("Chart.yaml", () => {
  test("validates against the chart schema", () => {
    const ok = "apiVersion: v2\nname: web\nversion: 0.1.0\ntype: application\ndependencies:\n  - name: postgresql\n    version: 15.x\n    repository: oci://registry-1.docker.io/bitnamicharts\n"
    expect(validateYaml(parseYaml(ok)[0].root, CHART_SCHEMA)).toEqual([])
    const bad = "apiVersion: v3\nname: web\ntyp: application\n"
    const msgs = validateYaml(parseYaml(bad)[0].root, CHART_SCHEMA).map((p) => p.message)
    expect(msgs.some((m) => /“v3” is not one of/.test(m))).toBe(true)
    expect(msgs.some((m) => /Unknown key “typ”/.test(m))).toBe(true)
    expect(msgs.some((m) => /Missing required key “version”/.test(m))).toBe(true)
  })

  test("completes its keys", () => {
    const text = "apiVersion: v2\nname: web\nappV"
    expect(labels(schemaCompletions(text, text.length, false, CHART_SCHEMA))).toContain("appVersion")
  })
})

describe("values files", () => {
  test("keys come from the chart's values.yaml with defaults and comments", () => {
    const s = valuesSchema(VALUES, undefined)
    const img = keysAt(s, ["image"])
    expect(img.map((k) => k.name)).toEqual(["repository", "tag", "pullPolicy"])
    expect(img[0].description).toContain("The image repository")
    expect(img[0].description).toContain("Default: `ghcr.io/acme/web`")
    expect(keysAt(s, []).find((k) => k.name === "replicaCount")?.description).toContain("Number of pods")
    const text = "image:\n  ta"
    expect(labels(schemaCompletions(text, text.length, false, s))).toEqual(["repository", "tag", "pullPolicy"])
  })

  test("values files may add keys and change types (Helm merges them): nothing is judged", () => {
    const s = valuesSchema(VALUES, undefined)
    expect(validateYaml(parseYaml("replicaCount: three\nextra: {a: 1}\nimage:\n  tag: 1.2.3\n")[0].root, s)).toEqual([])
  })

  test("values.schema.json rules apply, merged with the inferred keys", () => {
    const schemaJson = JSON.stringify({
      type: "object",
      properties: { replicaCount: { type: "integer", minimum: 1 }, service: { type: "object", properties: { type: { enum: ["ClusterIP", "NodePort"] } } } },
    })
    const s = valuesSchema(VALUES, schemaJson)
    const msgs = validateYaml(parseYaml("replicaCount: three\nservice:\n  type: LoadBalancer\n")[0].root, s).map((p) => p.message)
    expect(msgs.some((m) => /Expected number/.test(m))).toBe(true)
    expect(msgs.some((m) => /“LoadBalancer” is not one of/.test(m))).toBe(true)
    expect(keysAt(s, ["service"]).map((k) => k.name)).toEqual(["type", "port"])
  })

  test("a broken values.schema.json falls back to the inferred schema", () => {
    expect(keysAt(valuesSchema(VALUES, "{nope"), []).map((k) => k.name)).toContain("ingress")
  })

  test("comment blocks above keys", () => {
    expect(commentAbove(VALUES.split("\n"), 2)).toBe("Number of pods")
    expect(commentAbove(VALUES.split("\n"), 4)).toBeUndefined()
  })
})

describe("templates", () => {
  const at = (text: string, marker = "|") => ({ text: text.replace(marker, ""), pos: text.indexOf(marker) })

  test("actions", () => {
    const { text, pos } = at("name: {{ .Values.| }}\n")
    expect(actionAt(text, pos)).toEqual({ from: 8, to: text.indexOf("}}") })
    expect(actionAt("a: {{ x }} b|".replace("|", ""), 12)).toBeNull()
    expect(actionAt("{{/* comment */}}", 6)).toBeNull()
  })

  test(".Values completes from values.yaml, nested", () => {
    let c = at("image: {{ .Values.| }}")
    expect(labels(templateCompletions(c.text, c.pos, false, env()))).toEqual(["replicaCount", "image", "service", "ingress"])
    c = at("image: {{ .Values.image.re| }}")
    const r = templateCompletions(c.text, c.pos, false, env())!
    expect(labels(r)).toEqual(["repository", "tag", "pullPolicy"])
    expect(r.from).toBe(c.pos - 2)
    c = at("x: {{ $.Values.service.| }}")
    expect(labels(templateCompletions(c.text, c.pos, false, env()))).toEqual(["type", "port"])
  })

  test("with and range move the dot", () => {
    let c = at("{{- with .Values.image }}\nimage: {{ .| }}\n{{- end }}")
    expect(dotAt(c.text, c.pos)).toEqual(["Values", "image"])
    expect(labels(templateCompletions(c.text, c.pos, false, env()))).toContain("repository")
    c = at("{{- range .Values.ingress.hosts }}\n- host: {{ .| }}\n{{- end }}")
    expect(labels(templateCompletions(c.text, c.pos, false, env()))).toEqual(["host", "paths"])
    c = at("{{- with .Values.image }}{{ end }}\n{{ .| }}")
    expect(dotAt(c.text, c.pos)).toEqual([])
    c = at("{{- range $k, $v := .Values.ingress.hosts }}{{ $.Values.| }}{{ end }}")
    expect(labels(templateCompletions(c.text, c.pos, false, env()))).toContain("replicaCount")
  })

  test("built-in objects", () => {
    let c = at("{{ .| }}")
    expect(labels(templateCompletions(c.text, c.pos, true, env()))).toEqual(["Values", "Release", "Chart", "Capabilities", "Files", "Template", "Subcharts"])
    c = at("{{ .Release.N| }}")
    expect(labels(templateCompletions(c.text, c.pos, false, env()))).toContain("Namespace")
    c = at("{{ .Capabilities.KubeVersion.| }}")
    expect(labels(templateCompletions(c.text, c.pos, false, env()))).toContain("Version")
    c = at("{{ .Files.| }}")
    expect(labels(templateCompletions(c.text, c.pos, false, env()))).toContain("Glob")
  })

  test("functions and actions", () => {
    const c = at("{{ toY| .Values.x }}")
    const r = templateCompletions(c.text, c.pos, false, env())!
    expect(labels(r)).toContain("toYaml")
    expect(labels(r)).toContain("nindent")
    expect(labels(r)).toContain("semverCompare")
    expect(labels(r)).toContain("range")
    expect(r.options.find((o) => o.label === "default")?.detail).toBe("default DEFAULT VALUE")
    expect(templateCompletions(...Object.values(at("{{ $x| }}")) as [string, number], false, env())).toBeNull()
  })

  test("include names come from define", () => {
    expect(namedTemplates({ "chart/templates/_helpers.tpl": HELPERS }).map((t) => [t.name, t.line, t.description])).toEqual([
      ["web.name", 4, "Expand the name of the chart."],
      ["web.labels", 8, undefined],
    ])
    const c = at('labels:\n  {{- include "web.|" . | nindent 4 }}')
    const r = templateCompletions(c.text, c.pos, false, env())!
    expect(labels(r)).toEqual(["web.name", "web.labels"])
    expect(r.from).toBe(c.text.indexOf('"') + 1)
  })

  test("hover: functions, objects, values with their default", () => {
    const text = "image: {{ .Values.image.repository | default .Chart.AppVersion | quote }}"
    expect(templateHover(text, text.indexOf("quote") + 2, env())?.title).toBe("quote VALUE...")
    const v = templateHover(text, text.indexOf("repository") + 3, env())!
    expect(v.title).toBe(".Values.image.repository")
    expect(v.extra).toBe("ghcr.io/acme/web")
    expect(v.description).toBe("The image repository")
    expect(v.badges).toEqual(["chart/values.yaml:6"])
    expect(templateHover(text, text.indexOf("AppVersion") + 1, env())?.title).toBe(".Chart.AppVersion")
    const missing = templateHover("{{ .Values.nope }}", 12, env())!
    expect(missing.badges).toEqual(["not in values.yaml"])
    expect(templateHover(text, 2, env())).toBeNull()
  })

  test("go to definition: values keys and named templates", () => {
    let text = "port: {{ .Values.service.port }}"
    expect(templateDefinition(text, text.indexOf("port }}") + 1, env())).toEqual({ file: "chart/values.yaml", line: 12 })
    expect(templateDefinition(text, text.indexOf("service") + 1, env())).toEqual({ file: "chart/values.yaml", line: 10 })
    text = '{{ include "web.labels" . }}'
    expect(templateDefinition(text, text.indexOf("labels") + 1, env())).toEqual({ file: "chart/templates/_helpers.tpl", line: 8 })
    text = "{{- with .Values.image }}{{ .tag }}{{ end }}"
    expect(templateDefinition(text, text.indexOf("tag") + 1, env())).toEqual({ file: "chart/values.yaml", line: 7 })
  })
})
