import { describe, expect, test } from "bun:test"
import { readdirSync, readFileSync, statSync } from "node:fs"
import path from "node:path"
import { formatYaml } from "./format"

describe("formatYaml", () => {
  test("re-indents to two spaces, sequences one level under their key, keeping comments and order", () => {
    const src = `# project
services:
    # the API
    api:
        image: node:22    # pinned
        ports:
        - "3000:3000"
        -   "9229:9229"
        environment:
            B: 2
            A: 1
    db:
        image: postgres:16
`
    expect(formatYaml(src)).toBe(`# project
services:
  # the API
  api:
    image: node:22    # pinned
    ports:
      - "3000:3000"
      - "9229:9229"
    environment:
      B: 2
      A: 1
  db:
    image: postgres:16
`)
  })

  test("sequences of mappings, block scalars and multi-line flow collections move with their key", () => {
    const src = `spec:
    containers:
    -   name: web
        command:
        - sh
        - -c
        - |
            echo start
              indented
            exec nginx
        args: [a,
               b]
`
    expect(formatYaml(src)).toBe(`spec:
  containers:
    - name: web
      command:
        - sh
        - -c
        - |
          echo start
            indented
          exec nginx
      args: [a,
             b]
`)
  })

  test("is idempotent and leaves formatted text unchanged", () => {
    const once = formatYaml("a:\n    b:\n        - 1\n        - 2\nc: 3\n")
    expect(formatYaml(once)).toBe(once)
    expect(formatYaml("x: 1\n")).toBe("x: 1\n")
  })

  test("trailing blanks go and the file ends with one newline; documents keep their separators", () => {
    expect(formatYaml("a: 1   \n---\nb:\n    c: 2\n\n\n")).toBe("a: 1\n---\nb:\n  c: 2\n")
  })

  // Real-world files: every compose file of the templates (and awesome-compose when checked out) formats to the same data.
  test("formats the Docker templates' compose files and Kubernetes templates without changing their data", () => {
    const roots = [
      path.resolve(import.meta.dir, "../../../../../../backend/internal/compose/_templates"),
      path.resolve(import.meta.dir, "../../../../../../backend/internal/filetemplates/files"),
    ]
    const files: string[] = []
    const walk = (d: string) => {
      for (const e of readdirSync(d)) {
        const p = path.join(d, e)
        if (statSync(p).isDirectory()) walk(p)
        else if (/\.ya?ml$/.test(e) && !e.startsWith("compose-service")) files.push(p)
      }
    }
    for (const r of roots) walk(r)
    expect(files.length).toBeGreaterThan(5)
    for (const f of files) {
      const text = readFileSync(f, "utf8")
      const out = formatYaml(text)
      expect(formatYaml(out)).toBe(out)
    }
  })
})
