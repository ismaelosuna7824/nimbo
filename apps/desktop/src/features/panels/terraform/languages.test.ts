import { describe, expect, test } from "bun:test"
import { StreamLanguage, type StreamParser, StringStream } from "@codemirror/language"
import type { FileLanguage } from "@/lib/api/types"
import { LANGUAGE_LABEL, languageOf } from "./file-model"
import { loadLanguage } from "./languages"
import { makefile } from "./makefile-language"
import { php } from "./php-language"

const CASES: Record<string, FileLanguage> = {
  "backend/src/main/java/A.java": "java",
  "app/Main.kt": "kotlin",
  "build.gradle.kts": "kotlin",
  "Program.cs": "csharp",
  "api/api.csproj": "xml",
  "src/main.rs": "rust",
  "public/index.php": "php",
  "Gemfile": "ruby",
  "lib/tasks/db.rake": "ruby",
  "db/init.sql": "sql",
  "Cargo.toml": "toml",
  "setup.cfg": "ini",
  "mysql/my.cnf": "ini",
  "php.ini": "ini",
  ".editorconfig": "ini",
  "supervisord.conf": "ini",
  "src/main/resources/app.properties": "properties",
  "pom.xml": "xml",
  "Info.plist": "xml",
  "web/index.html": "html",
  "styles/style.scss": "css",
  "styles/site.css": "css",
  "proxy/nginx.conf": "nginx",
  "nginx/conf.d/default.conf": "nginx",
  "proxy/nginx/default.conf": "nginx",
  "conf.d/x.conf": "nginx",
  "site.nginx": "nginx",
  "Makefile": "makefile",
  "GNUmakefile": "makefile",
  "build/rules.mk": "makefile",
  "proto/api.proto": "protobuf",
  // Existing rules still win.
  "main.tf.json": "json",
  ".env.production": "shell",
  "Dockerfile.dev": "dockerfile",
  "logo.svg": "text",
}

/** [text, style] of every styled token on a line. */
function tokens<S>(parser: StreamParser<S>, line: string): [string, string][] {
  const state = parser.startState!(2)
  const stream = new StringStream(line, 2, 2)
  const out: [string, string][] = []
  while (!stream.eol()) {
    stream.start = stream.pos
    const style = parser.token(stream, state)
    if (style) out.push([stream.current(), style])
  }
  return out
}

describe("languageOf", () => {
  for (const [path, lang] of Object.entries(CASES)) test(`${path} → ${lang}`, () => expect(languageOf(path)).toBe(lang))
})

describe("loadLanguage", () => {
  const ids = Object.keys(LANGUAGE_LABEL).filter((l) => l !== "hcl" && l !== "text") as FileLanguage[]
  for (const id of ids)
    test(`${id} loads a stream language`, async () => {
      const ext = await loadLanguage(id)
      expect(ext).toBeInstanceOf(StreamLanguage)
      expect(await loadLanguage(id)).toBe(ext)
    })
})

describe("php", () => {
  test("highlights tags, keywords, variables and comments", () => {
    expect(tokens(php, "<?php")).toEqual([["<?php", "meta"]])
    const line = tokens(php, "function hi($name) { return $name; } # done")
    expect(line).toContainEqual(["function", "keyword"])
    expect(line).toContainEqual(["$name", "variableName.special"])
    expect(line).toContainEqual(["# done", "comment"])
    expect(tokens(php, "// note")).toEqual([["// note", "comment"]])
  })
})

describe("makefile", () => {
  test("highlights directives, assignments, targets, variables and comments", () => {
    expect(tokens(makefile, ".PHONY: build")[0]).toEqual([".PHONY", "keyword"])
    expect(tokens(makefile, "ifeq ($(ENV),prod)")).toEqual([
      ["ifeq", "keyword"],
      ["$(ENV)", "variableName.special"],
    ])
    expect(tokens(makefile, "CC ?= gcc")[0]).toEqual(["CC", "def"])
    expect(tokens(makefile, "build: deps # all")).toEqual([
      ["build", "propertyName"],
      [":", "operator"],
      ["# all", "comment"],
    ])
    expect(tokens(makefile, '\tgo build -o $@ "./cmd"')).toEqual([
      ["$@", "variableName.special"],
      ['"./cmd"', "string"],
    ])
  })
})
