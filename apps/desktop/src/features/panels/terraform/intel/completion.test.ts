import { beforeAll, describe, expect, test } from "bun:test"
import type { CompletionResult } from "@codemirror/autocomplete"
import { api } from "@/lib/api/client"
import type { FunctionSignature, ResourceSchema, TypeInfo } from "@/lib/api/types"
import { complete, type TfCompletion } from "./completion"
import { definitionAt } from "./definition"
import { SymbolIndex } from "./symbols"

const TYPES: TypeInfo[] = [
  { kind: "resource", type: "aws_sqs_queue", cloud: "aws", label: "SQS Queue", local: true },
  { kind: "resource", type: "aws_sqs_queue_policy", cloud: "aws", label: "SQS Queue Policy", local: true },
  { kind: "resource", type: "aws_s3_bucket", cloud: "aws", label: "S3 Bucket", local: true },
  { kind: "resource", type: "google_pubsub_topic", cloud: "gcp", label: "Pub/Sub Topic" },
  { kind: "data", type: "aws_iam_policy_document", cloud: "aws", label: "IAM Policy Document", local: true },
]

const schema = (kind: "resource" | "data" | "provider", type: string, block: ResourceSchema["block"]): ResourceSchema =>
  ({ kind, type, cloud: "aws", category: "Messaging", label: type, icon: "box", provider: "hashicorp/aws", block, docsUrl: `https://registry/${type}` }) as ResourceSchema

const SCHEMAS: Record<string, ResourceSchema> = {
  "resource/aws_sqs_queue": schema("resource", "aws_sqs_queue", {
    attributes: [
      { name: "arn", type: "string", computed: true },
      { name: "name", type: "string", optional: true },
      { name: "visibility_timeout_seconds", type: "number", optional: true, description: "Visibility timeout." },
      { name: "redrive_policy", type: "string", optional: true, deprecated: true },
      { name: "tags", type: ["map", "string"], optional: true },
    ],
    blocks: [],
  }),
  "resource/aws_s3_bucket": schema("resource", "aws_s3_bucket", {
    attributes: [
      { name: "bucket", type: "string", optional: true },
      { name: "arn", type: "string", computed: true },
      { name: "storage_class", type: "string", optional: true, enum: ["STANDARD", "GLACIER"] },
    ],
    blocks: [
      {
        name: "versioning",
        nesting: "list",
        maxItems: 1,
        block: { attributes: [{ name: "enabled", type: "bool", required: true }], blocks: [] },
      },
      {
        name: "server_side_encryption",
        nesting: "list",
        minItems: 1,
        block: { attributes: [{ name: "algorithm", type: "string", required: true }], blocks: [] },
      },
    ],
  }),
  "resource/aws_sqs_queue_policy": schema("resource", "aws_sqs_queue_policy", {
    attributes: [
      { name: "queue_url", type: "string", required: true },
      { name: "policy", type: "string", required: true },
    ],
    blocks: [],
  }),
  "provider/aws": schema("provider", "aws", {
    attributes: [{ name: "region", type: "string", optional: true }],
    blocks: [{ name: "default_tags", nesting: "list", maxItems: 1, block: { attributes: [], blocks: [] } }],
  }),
}

const FUNCTIONS: FunctionSignature[] = [
  { name: "upper", returnType: "string", parameters: [{ name: "str", type: "string" }], description: "Uppercase." },
  { name: "join", returnType: "string", parameters: [{ name: "separator", type: "string" }, { name: "lists", type: ["list", "string"] }] },
]

beforeAll(() => {
  api.catalogTypes = async () => TYPES
  api.catalogFunctions = async () => FUNCTIONS
  api.catalogSchemas = async (keys: string[]) => Object.fromEntries(keys.filter((k) => SCHEMAS[k]).map((k) => [k, SCHEMAS[k]]))
})

const WORKSPACE: Record<string, string> = {
  "variables.tf": 'variable "prefix" {\n  type        = string\n  description = "Name prefix"\n}\n',
  "modules/net/variables.tf":
    'variable "cidr" {\n  type = string\n}\nvariable "azs" {\n  type    = list(string)\n  default = []\n}\n',
  "modules/net/outputs.tf": 'output "vpc_id" {\n  value = "x"\n}\n',
  "modules/net/main.tf": 'locals {\n  name = var.cidr\n}\n',
}

/** Completions with the cursor at "|" in file path. */
async function at(src: string, path = "main.tf", explicit = false): Promise<CompletionResult | null> {
  const pos = src.indexOf("|")
  const text = src.slice(0, pos) + src.slice(pos + 1)
  const symbols = new SymbolIndex({ ...WORKSPACE, [path]: text }, [])
  return complete(text, pos, explicit, { path, symbols })
}

const labels = (r: CompletionResult | null) => r?.options.map((o) => o.label) ?? []

describe("complete", () => {
  test("top-level keywords", async () => {
    expect(labels(await at("res|"))).toContain("resource")
    expect(labels(await at("res|"))).toEqual(expect.arrayContaining(["module", "variable", "output", "locals", "moved", "import", "check", "removed"]))
  })

  test('resource types after resource "aws_sqs', async () => {
    const r = await at('resource "aws_sqs|')
    expect(labels(r)).toEqual(["aws_sqs_queue", "aws_sqs_queue_policy"])
    const q = r!.options[0] as TfCompletion
    expect(q.local).toBe(true)
    expect(q.detail).toBe("SQS Queue · AWS")
    expect(r!.from).toBe('resource "'.length)
  })

  test("types of the workflow's clouds rank first", async () => {
    const r = await at('resource "aws_s3_bucket" "b" {}\nresource "|')
    const google = r!.options.find((o) => o.label === "google_pubsub_topic")!
    const s3 = r!.options.find((o) => o.label === "aws_s3_bucket")!
    expect(s3.boost!).toBeGreaterThan(google.boost ?? 0)
  })

  test("data source types", async () => {
    expect(labels(await at('data "aws_|'))).toEqual(["aws_iam_policy_document"])
  })

  test("arguments of a resource, required first, without those already set", async () => {
    const r = await at('resource "aws_sqs_queue" "q" {\n  name = "jobs"\n  visibility|\n}\n')
    const names = labels(r)
    expect(names).toContain("visibility_timeout_seconds")
    expect(names).not.toContain("name")
    expect(names).not.toContain("arn")
    expect(names).toEqual(expect.arrayContaining(["count", "for_each", "depends_on", "lifecycle", "provider"]))
    const deprecated = r!.options.find((o) => o.label === "redrive_policy") as TfCompletion
    expect(deprecated.deprecated).toBe(true)
    const vis = r!.options.find((o) => o.label === "visibility_timeout_seconds")!
    expect(vis.detail).toBe("number")
  })

  test("required arguments and blocks are in the Required section", async () => {
    const r = await at('resource "aws_sqs_queue_policy" "p" {\n  |\n}\n', "main.tf", true)
    const sec = (l: string) => {
      const s = r!.options.find((o) => o.label === l)!.section
      return typeof s === "string" ? s : s?.name
    }
    expect(sec("queue_url")).toBe("Required")
    expect(sec("count")).toBe("Meta-arguments")
  })

  test("nested block arguments and single blocks already present", async () => {
    expect(labels(await at('resource "aws_s3_bucket" "b" {\n  versioning {\n    ena|\n  }\n}\n'))).toEqual(["enabled"])
    expect(labels(await at('resource "aws_s3_bucket" "b" {\n  versioning {}\n  ver|\n}\n'))).not.toContain("versioning")
  })

  test("lifecycle arguments", async () => {
    expect(labels(await at('resource "aws_s3_bucket" "b" {\n  lifecycle {\n    pre|\n  }\n}\n'))).toEqual(
      expect.arrayContaining(["prevent_destroy", "create_before_destroy", "ignore_changes", "precondition"]),
    )
  })

  test("provider arguments", async () => {
    expect(labels(await at('provider "aws" {\n  reg|\n}\n'))).toEqual(expect.arrayContaining(["region", "alias", "default_tags"]))
  })

  test("enum values inside a string", async () => {
    const r = await at('resource "aws_s3_bucket" "b" {\n  storage_class = "GL|"\n}\n')
    expect(labels(r)).toEqual(["STANDARD", "GLACIER"])
  })

  test("references to a resource and then its attributes", async () => {
    const names = await at('resource "aws_sqs_queue" "queue" {}\noutput "o" {\n  value = aws_sqs_queue.|\n}\n')
    expect(labels(names)).toEqual(["queue"])
    const attrs = await at('resource "aws_sqs_queue" "queue" {}\noutput "o" {\n  value = aws_sqs_queue.queue.|\n}\n')
    expect(labels(attrs)).toEqual(expect.arrayContaining(["arn", "id", "name", "visibility_timeout_seconds"]))
  })

  test("nested attributes after a block reference", async () => {
    const r = await at('resource "aws_s3_bucket" "b" {}\noutput "o" {\n  value = aws_s3_bucket.b.versioning[0].|\n}\n')
    expect(labels(r)).toEqual(["enabled"])
  })

  test("roots, declared resource types and functions in an expression", async () => {
    const r = await at('resource "aws_sqs_queue" "queue" {}\nlocals {\n  a = u|\n}\n')
    expect(labels(r)).toEqual(expect.arrayContaining(["var", "local", "module", "data", "path", "aws_sqs_queue", "upper", "join"]))
    expect(labels(r)).not.toContain("each")
    const fe = await at('resource "aws_sqs_queue" "q" {\n  for_each = toset([])\n  name = e|\n}\n')
    expect(labels(fe)).toContain("each")
    expect(labels(await at('resource "aws_sqs_queue" "q" {\n  for_each = toset([])\n  name = each.|\n}\n'))).toEqual(["key", "value"])
  })

  test("var. in the root and inside a module file", async () => {
    expect(labels(await at("locals {\n  a = var.|\n}\n"))).toEqual(["prefix"])
    expect(labels(await at("resource \"aws_vpc\" \"this\" {\n  cidr_block = var.|\n}\n", "modules/net/vpc.tf"))).toEqual(["cidr", "azs"])
    expect(labels(await at("output \"x\" {\n  value = local.|\n}\n", "modules/net/out.tf"))).toEqual(["name"])
  })

  test("inside string interpolation", async () => {
    expect(labels(await at('resource "aws_s3_bucket" "b" {\n  bucket = "${var.|}-logs"\n}\n'))).toEqual(["prefix"])
  })

  test("module inputs and outputs", async () => {
    const call = 'module "net" {\n  source = "./modules/net"\n  |\n}\n'
    const r = await at(call, "main.tf", true)
    expect(labels(r)).toEqual(expect.arrayContaining(["cidr", "azs", "version", "providers", "count", "for_each", "depends_on"]))
    const cidr = r!.options.find((o) => o.label === "cidr")!
    expect(cidr.detail).toContain("required")
    expect(labels(await at('module "net" {\n  source = "./modules/net"\n}\noutput "o" {\n  value = module.|\n}\n'))).toEqual(["net"])
    expect(labels(await at('module "net" {\n  source = "./modules/net"\n}\noutput "o" {\n  value = module.net.|\n}\n'))).toEqual(["vpc_id"])
  })

  test("local module sources", async () => {
    expect(labels(await at('module "x" {\n  source = "./|"\n}\n'))).toEqual(["./modules/net"])
  })

  test("variable types", async () => {
    expect(labels(await at('variable "v" {\n  type = |\n}\n', "main.tf", true))).toEqual(
      expect.arrayContaining(["string", "number", "bool", "list", "map", "object", "any"]),
    )
    expect(labels(await at('variable "v" {\n  sen|\n}\n'))).toContain("sensitive")
    expect(labels(await at('variable "v" {\n  val|\n}\n'))).toContain("validation")
  })
})

describe("definitionAt", () => {
  test("references across files and into modules", () => {
    const text = 'module "net" {\n  source = "./modules/net"\n  cidr   = var.prefix\n}\n'
    const symbols = new SymbolIndex({ ...WORKSPACE, "main.tf": text }, [])
    const env = { path: "main.tf", symbols }
    expect(definitionAt(text, text.indexOf("prefix"), env)).toEqual({ file: "variables.tf", from: 0, line: 1 })
    expect(definitionAt(text, text.indexOf("cidr") + 1, env)).toMatchObject({ file: "modules/net/variables.tf", line: 1 })
    const out = 'output "o" {\n  value = module.net.vpc_id\n}\n'
    const s2 = new SymbolIndex({ ...WORKSPACE, "main.tf": text, "outputs.tf": out }, [])
    expect(definitionAt(out, out.indexOf("vpc_id") + 1, { path: "outputs.tf", symbols: s2 })).toMatchObject({ file: "modules/net/outputs.tf" })
    expect(definitionAt(out, out.indexOf("net") + 1, { path: "outputs.tf", symbols: s2 })).toMatchObject({ file: "main.tf", line: 1 })
  })
})
