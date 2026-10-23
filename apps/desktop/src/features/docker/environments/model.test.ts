import { describe, expect, test } from "bun:test"
import type { ComposeEnvironment, EnvironmentVariable } from "@/lib/api/types"
import {
  baseEnvFile,
  dotenvValue,
  envFileComposeFiles,
  envFileOfCompose,
  envFilePath,
  environmentOfFile,
  infoEnvFile,
  overridden,
  sortVariables,
  variableKeyError,
} from "./model"

const prod: ComposeEnvironment = { name: "prod", file: "compose.prod.yaml", envFile: ".env.prod", files: ["compose.prod.yaml", ".env.prod"] }
const dev: ComposeEnvironment = { name: "dev", file: "docker/docker-compose.dev.yml", files: ["docker/docker-compose.dev.yml"] }
const compose = { files: ["compose.yaml", "monitoring/compose.yaml"], environments: [prod, dev] }

describe("docker environments", () => {
  test("files of an environment", () => {
    expect(environmentOfFile(compose, ".env.prod")?.name).toBe("prod")
    expect(environmentOfFile(compose, "docker/.env.dev")?.name).toBe("dev") // not created yet
    expect(environmentOfFile(compose, "compose.yaml")).toBeUndefined()
    expect(envFilePath(dev)).toBe("docker/.env.dev")
    expect(envFileOfCompose(compose, "compose.prod.yaml")).toBe(".env.prod")
    expect(envFileOfCompose(compose, "compose.yaml")).toBeUndefined()
  })

  test("an env file alone is an environment, in either style", () => {
    const qa: ComposeEnvironment = { name: "qa", envFile: "qa.env", files: ["qa.env"] }
    const c = { files: ["compose.yaml"], environment: "qa", environments: [prod, qa] }
    expect(environmentOfFile(c, "qa.env")?.name).toBe("qa")
    expect(envFilePath(qa)).toBe("qa.env")
    expect(infoEnvFile({ name: "qa", files: ["qa.env"] })).toBe("qa.env")
    // Base files read the active environment's env file first; overrides their own.
    expect(envFileOfCompose(c, "compose.yaml")).toBe("qa.env")
    expect(envFileOfCompose(c, "compose.prod.yaml")).toBe(".env.prod")
    expect(envFileComposeFiles(c, "qa.env")).toEqual(["compose.yaml"])
    expect(envFileComposeFiles(c, ".env")).toEqual(["compose.yaml", "compose.prod.yaml"])
  })

  test("env files feed the base plus their environment's override", () => {
    expect(envFileComposeFiles(compose, ".env.prod")).toEqual(["compose.yaml", "monitoring/compose.yaml", "compose.prod.yaml"])
    expect(envFileComposeFiles(compose, ".env")).toEqual(["compose.yaml", "monitoring/compose.yaml", "compose.prod.yaml", "docker/docker-compose.dev.yml"])
    expect(envFileComposeFiles(compose, ".env.example")).toEqual(["compose.yaml", "monitoring/compose.yaml"])
  })

  test("overridden attributes", () => {
    const facts = { overrides: ["image", "deploy.replicas"] } as Parameters<typeof overridden>[0]
    expect(overridden(facts, "image")).toBe(true)
    expect(overridden(facts, "deploy")).toBe(true)
    expect(overridden(facts, "ports", "environment")).toBe(false)
    expect(overridden(undefined, "image")).toBe(false)
  })

  test("dotenv values", () => {
    const text = 'A=1\nexport B="two words"\nC=\'lit ${X}\'\nD=x # comment\nA=2\nE="say \\"hi\\""\n'
    expect(dotenvValue(text, "A")).toBe("2")
    expect(dotenvValue(text, "B")).toBe("two words")
    expect(dotenvValue(text, "C")).toBe("lit ${X}")
    expect(dotenvValue(text, "D")).toBe("x")
    expect(dotenvValue(text, "E")).toBe('say "hi"')
    expect(dotenvValue(text, "Z")).toBeUndefined()
  })

  test("variables: own first, then inherited", () => {
    const v = (key: string, own: boolean): EnvironmentVariable => ({ key, overridden: own, inBase: !own, secret: false, file: ".env.prod" })
    expect(sortVariables([v("B", false), v("Z", true), v("A", false), v("C", true)]).map((x) => x.key)).toEqual(["C", "Z", "A", "B"])
  })

  test("env files of an environment", () => {
    expect(infoEnvFile({ name: "prod", files: ["compose.prod.yaml", ".env.prod"] })).toBe(".env.prod")
    expect(infoEnvFile({ name: "dev", files: ["docker/docker-compose.dev.yml"] })).toBe("docker/.env.dev")
    expect(baseEnvFile("docker/.env.dev")).toBe("docker/.env")
    expect(baseEnvFile(".env.prod")).toBe(".env")
  })

  test("variable names", () => {
    expect(variableKeyError("", [])).toBe("Enter a name.")
    expect(variableKeyError("1X", [])).not.toBeNull()
    expect(variableKeyError("LOG_LEVEL", ["LOG_LEVEL"])).toContain("already")
    expect(variableKeyError("LOG_LEVEL", [])).toBeNull()
  })
})
