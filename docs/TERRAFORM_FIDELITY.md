# Terraform fidelity

Nimbo imports existing Terraform, lets you edit it on the canvas, and exports
it back. This page states what the round trip guarantees, how it is tested,
what the tests found, and what is still not covered.

## Guarantees

When you import a Terraform root (folder import, template, or code editor)
and export it, the exported folder:

1. **Means the same thing.** Every top-level block survives with its type,
   labels, attributes and nested blocks: resources, data sources, modules
   (including `providers = {}` mappings and `depends_on`), variables (with
   `validation`, `nullable`, `sensitive`, `ephemeral`), outputs (with
   `precondition`, `depends_on`), providers and their aliases, `locals`,
   `terraform` (`required_providers`, `backend`, `cloud`), and every other
   block as is (`moved`, `import` with `for_each`, `removed`, `check`, …).
   Expressions are copied token for token: heredocs (`<<EOT`, `<<-EOT`),
   template directives (`%{ if }`, `%{ for }`), `for` expressions, splats,
   conditionals, `dynamic` blocks, provider-defined functions
   (`provider::aws::arn_parse`), Unicode and escapes.
2. **Is formatted.** Generated files pass `terraform fmt -check`.
3. **Validates** with `terraform init -backend=false && terraform validate`
   whenever the original does.
4. **Plans no changes** against infrastructure the original created (checked
   on the Floci emulator).
5. **Keeps its comments.** `#`, `//` and `/* */` comments stay with the
   attribute or block they belong to: lines above it (file headers included),
   the comment at the end of its line, comments after a block's `{` or `}`,
   commented-out code at the end of a block, and end-of-file comments.
   Comments inside an expression are part of the expression.
6. **Keeps its files.** Everything else in the project folder is copied
   unchanged to the same relative path: Lambda sources, zips, templates read
   by `file()`/`templatefile()`, `.terraform.lock.hcl`, override files and
   `*.tf.json`. Local modules (nested too) are copied to `modules/<name>`
   with their `source` rewritten to match.
7. **Keeps its variable files.** `terraform.tfvars`, `*.auto.tfvars` and other
   root `*.tfvars` are written back under their own name, so Terraform loads
   them exactly as before; `environments/<name>.tfvars` stay there.
8. **Is stable.** Exporting twice gives byte-identical files, and importing
   an export gives back the same workflow.

What may differ, by design:

- **Layout.** Blocks are regrouped into `versions.tf`, `providers.tf`,
  `variables.tf`, `main.tf` and `outputs.tf`; inside a block, attributes come
  before nested blocks; whitespace follows `terraform fmt`. Comments move
  with their block. Several `locals` blocks become one, and so do several
  `terraform` blocks (Terraform merges them the same way).
- **Line endings.** CRLF files are read as LF. Inside a heredoc this turns a
  `\r\n` line ending of the value into `\n`, as git's `autocrlf` does on
  checkout.
- **`.env.example`** values are rewritten as `KEY=VALUE`, quoted only when
  needed.

## What Nimbo reports instead of guessing

| Situation | Diagnostic | What happens |
|---|---|---|
| `override.tf` / `*_override.tf` | `unsupported_file` (warning) | Not shown on the canvas; kept as a file, exported unchanged. |
| `*.tf.json`, `*.tfvars.json` | `unsupported_file` (warning) | Same. A folder with only JSON configuration is listed as skipped by the scan. |
| A path leaving the folder: `"${path.module}/../x"`, `file("../x")` | `path_outside_folder` (warning) | The file is not copied; fix the path or copy the file into the folder. |
| A local module outside the imported folder | `module_outside_folder` (warning) | Its `source` is kept as is. |

## How it is tested

The harness lives in `backend/internal/tfqa`. Every root goes through the
real API: `POST /import` (the folder import, with local module copies) or
`POST /workflows` for templates, then `POST /workflows/{id}/export` with
environments. Checks per root:

| Check | What it verifies |
|---|---|
| `semantic` | Both folders reduced to a canonical form (`tfqa.CompareDirs`): top-level blocks by type and labels, attributes by name with expressions compared token by token, nested blocks in order, `locals` and `terraform` merged; local module calls followed into both copies and compared the same way; override and JSON files compared too. It ignores only file split, top-level block order, attribute order and whitespace. |
| `comments` | The multiset of comments of every `.tf` file (root and modules) is unchanged. |
| `reimport` | Importing the export gives the same workflow (nodes, variables, outputs, locals, providers, terraform, other blocks, environments, edges, comments). |
| `stable` | Two exports are byte-identical. |
| `files` | Every non-Terraform file of the root and its modules is in the export, byte-identical, at the same relative path. |
| `tfvars` | Every root and `environments/` variable file is in the export at the same path with the same values. |
| `fmt` | `terraform fmt -check -recursive` passes on the export (copied module files only need to be as formatted as their originals). |
| `validate` | `terraform init -backend=false` + `validate` pass for the export whenever they pass for the original. |
| `deploy` | Apply the original on Floci, swap in the export in the same working directory, `terraform plan -detailed-exitcode` must report no changes, or exactly the changes the original itself plans right after its apply (attributes the emulator does not round-trip). |

`tfqa.CompareDirs` has its own tests showing it catches changed values,
missing blocks, nested changes, duplicate locals, module content changes,
lost comments and stray override files.

```sh
cd backend
# Always runs (part of go test ./...): templates, existing fixtures, the
# vendored corpus subset and the edge-case root (+ CRLF, tab and 1,500-block
# variants). fmt runs when terraform is on PATH; TFQA_VALIDATE=1 adds validate.
go test ./internal/tfqa/

# Full corpus: clones the pinned repositories, runs every check including
# validate (downloads providers), then the Floci deployment check (docker).
TFQA_PLUGIN_CACHE=/tmp/tf-plugins go test -tags corpus -timeout 4h ./internal/tfqa/ -v
```

Environment: `TFQA_CORPUS` (clone cache), `TFQA_PLUGIN_CACHE` (provider
cache kept across runs; by default each test run uses a temporary one),
`TFQA_VALIDATE=0|1`, `TFQA_REPORT`, `TFQA_REPORT_LOCAL`,
`TFQA_DEPLOY_REPORT` (result tables), `TFQA_KEEP` (keep exports).

## Corpus

Run on 2026-10-18 with Terraform 1.14.8 (darwin/arm64) and `floci/floci:latest`.

**Local roots** (always run): the 10 catalog templates; the codec fixtures
`hclcodec/testdata/{serverless,network,multicloud}` and the folder-import
fixture `api/testdata/repo` (`envs/dev`, `envs/prod`, nested local modules);
a vendored subset of the corpus in `tfqa/testdata/vendor` (licenses kept);
and `tfqa/testdata/edge`, a hand-written root with every edge case above
(comments everywhere, a comment-only file, heredocs and template directives,
`dynamic`, provisioners with `connection`, `lifecycle` with
`ignore_changes`/`replace_triggered_by`/pre- and postconditions, `check`,
`import` with `for_each`, `moved`, `removed`, `terraform_data`,
`null_resource`, Unicode, two `locals` and two `terraform` blocks,
validations, `nullable`/`ephemeral` variables, output `depends_on` and
`precondition`, `provider::aws::arn_parse`, an override file, a `.tf.json`
file, a nested local module with a provider alias mapping, Lambda sources, a
zip, `templatefile()` templates, `terraform.tfvars`, `prod.auto.tfvars`,
`environments/staging.tfvars` + `.env.example`), plus three generated
variants of it: CRLF line endings, tab indentation, and a 1,500-block file.

**Public roots** (`-tags corpus`), pinned in `tfqa/corpus_test.go`:

| Repository | Commit | Roots |
|---|---|---|
| [terraform-aws-modules/terraform-aws-vpc](https://github.com/terraform-aws-modules/terraform-aws-vpc) | `b3abd6d` | `examples/simple`, `complete`, `ipv6-dualstack` |
| [terraform-aws-modules/terraform-aws-s3-bucket](https://github.com/terraform-aws-modules/terraform-aws-s3-bucket) | `9afa4fc` | `examples/complete`, `object`, `notification` |
| [terraform-aws-modules/terraform-aws-lambda](https://github.com/terraform-aws-modules/terraform-aws-lambda) | `851d209` | `examples/simple`, `complete`, `triggers`, `alias` |
| [terraform-aws-modules/terraform-aws-sqs](https://github.com/terraform-aws-modules/terraform-aws-sqs) | `a68b4d5` | `examples/complete` |
| [terraform-aws-modules/terraform-aws-dynamodb-table](https://github.com/terraform-aws-modules/terraform-aws-dynamodb-table) | `b6cc515` | `examples/basic`, `autoscaling`, `s3-import` |
| [terraform-aws-modules/terraform-aws-sns](https://github.com/terraform-aws-modules/terraform-aws-sns) | `4538b7e` | `examples/complete` |
| [terraform-aws-modules/terraform-aws-eventbridge](https://github.com/terraform-aws-modules/terraform-aws-eventbridge) | `f993472` | `examples/complete`, `with-schedules` (+ `default-bus` for deploy) |
| [terraform-aws-modules/terraform-aws-apigateway-v2](https://github.com/terraform-aws-modules/terraform-aws-apigateway-v2) | `95e6a1d` | `examples/complete-http`, `websocket` |
| [terraform-aws-modules/terraform-aws-rds](https://github.com/terraform-aws-modules/terraform-aws-rds) | `175da04` | `examples/complete-postgres`, `s3-import-mysql` |
| [terraform-aws-modules/terraform-aws-ec2-instance](https://github.com/terraform-aws-modules/terraform-aws-ec2-instance) | `4dfedcd` | `examples/complete` |
| [terraform-aws-modules/terraform-aws-iam](https://github.com/terraform-aws-modules/terraform-aws-iam) | `da8e6a8` | `examples/iam-role`, `iam-user` |
| [terraform-aws-modules/terraform-aws-security-group](https://github.com/terraform-aws-modules/terraform-aws-security-group) | `b3c1b2e` | `examples/complete` |
| [hashicorp-education/learn-terraform-lambda-api-gateway](https://github.com/hashicorp-education/learn-terraform-lambda-api-gateway) | `9fa856d` | root |
| [hashicorp-education/learn-terraform-provision-eks-cluster](https://github.com/hashicorp-education/learn-terraform-provision-eks-cluster) | `20816e4` | root |
| [hashicorp-education/learn-terraform-data-sources-vpc](https://github.com/hashicorp-education/learn-terraform-data-sources-vpc) | `5204ab4` | root |
| [hashicorp-education/learn-terraform-data-sources-app](https://github.com/hashicorp-education/learn-terraform-data-sources-app) | `bf30acb` | root |
| [hashicorp-education/learn-terraform-variables](https://github.com/hashicorp-education/learn-terraform-variables) | `543b5e5` | root |
| [hashicorp-education/learn-terraform-outputs](https://github.com/hashicorp-education/learn-terraform-outputs) | `ef89e0b` | root |
| [hashicorp-education/learn-terraform-modules](https://github.com/hashicorp-education/learn-terraform-modules) | `7d1154e` | root |
| [hashicorp-education/learn-terraform-provisioning](https://github.com/hashicorp-education/learn-terraform-provisioning) | `d29c1e0` | `instances` |
| [terraform-google-modules/terraform-google-cloud-storage](https://github.com/terraform-google-modules/terraform-google-cloud-storage) | `c7effc0` | `examples/simple_bucket`, `multiple_buckets` |
| [terraform-google-modules/terraform-google-pubsub](https://github.com/terraform-google-modules/terraform-google-pubsub) | `6cf1860` | `examples/simple`, `cloud_storage` |
| [terraform-google-modules/terraform-google-network](https://github.com/terraform-google-modules/terraform-google-network) | `91352be` | `examples/basic_custom_mode`, `submodule_firewall`, `simple_project` |
| [hashicorp/terraform-provider-azurerm](https://github.com/hashicorp/terraform-provider-azurerm) (`examples/`) | `bb0454f` | `storage/storage-account`, `servicebus/basic`, `virtual-networks/basic`, `app-service/linux-web-app-zip-deploy`, `container-registry` |
| [oracle/terraform-provider-oci](https://github.com/oracle/terraform-provider-oci) (`examples/`) | `3b4efe9` | `object_storage`, `functions`, `networking/vcn` |

## Results

`orig-fails`: the original itself does not validate, so neither does the
export (same errors): `hclcodec/testdata/network` is a codec fixture with a
deliberately unknown value in an output description, and
`terraform-google-cloud-storage/examples/multiple_buckets` passes `ip_filter`,
which its module at that commit does not declare.

### Local roots

| root | semantic | comments | reimport | stable | files | tfvars | fmt | validate |
|---|---|---|---|---|---|---|---|---|
| template/aws-serverless-api | pass | pass | pass | pass | pass | pass | pass | pass |
| template/aws-queue-worker | pass | pass | pass | pass | pass | pass | pass | pass |
| template/aws-event-fanout | pass | pass | pass | pass | pass | pass | pass | pass |
| template/aws-static-site | pass | pass | pass | pass | pass | pass | pass | pass |
| template/aws-scheduled-job | pass | pass | pass | pass | pass | pass | pass | pass |
| template/aws-vpc-basics | pass | pass | pass | pass | pass | pass | pass | pass |
| template/aws-step-functions | pass | pass | pass | pass | pass | pass | pass | pass |
| template/gcp-pubsub-storage | pass | pass | pass | pass | pass | pass | pass | pass |
| template/azure-storage-queue | pass | pass | pass | pass | pass | pass | pass | pass |
| template/oci-object-storage | pass | pass | pass | pass | pass | pass | pass | pass |
| hclcodec/testdata/serverless | pass | pass | pass | pass | pass | pass | pass | pass |
| hclcodec/testdata/network | pass | pass | pass | pass | pass | pass | pass | orig-fails |
| hclcodec/testdata/multicloud | pass | pass | pass | pass | pass | pass | pass | pass |
| api/testdata/repo/envs/dev | pass | pass | pass | pass | pass | pass | pass | pass |
| api/testdata/repo/envs/prod | pass | pass | pass | pass | pass | pass | pass | pass |
| vendor/learn-terraform-modules | pass | pass | pass | pass | pass | pass | pass | pass |
| vendor/learn-terraform-lambda-api-gateway | pass | pass | pass | pass | pass | pass | pass | pass |
| vendor/terraform-aws-sqs/examples/complete | pass | pass | pass | pass | pass | pass | pass | pass |
| edge | pass | pass | pass | pass | pass | pass | pass | pass |
| edge-crlf | pass | pass | pass | pass | pass | pass | pass | pass |
| edge-tabs | pass | pass | pass | pass | pass | pass | pass | pass |
| edge-long | pass | pass | pass | pass | pass | pass | pass | pass |
| **pass count** | 22/22 | 22/22 | 22/22 | 22/22 | 22/22 | 22/22 | 22/22 | 21/22 (1 orig-fails) |

### Public roots

| root | semantic | comments | reimport | stable | files | tfvars | fmt | validate |
|---|---|---|---|---|---|---|---|---|
| terraform-aws-vpc/examples/simple | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-vpc/examples/complete | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-vpc/examples/ipv6-dualstack | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-s3-bucket/examples/complete | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-s3-bucket/examples/object | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-s3-bucket/examples/notification | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-lambda/examples/simple | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-lambda/examples/complete | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-lambda/examples/triggers | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-lambda/examples/alias | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-sqs/examples/complete | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-dynamodb-table/examples/basic | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-dynamodb-table/examples/autoscaling | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-dynamodb-table/examples/s3-import | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-sns/examples/complete | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-eventbridge/examples/complete | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-eventbridge/examples/with-schedules | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-apigateway-v2/examples/complete-http | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-apigateway-v2/examples/websocket | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-rds/examples/complete-postgres | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-rds/examples/s3-import-mysql | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-ec2-instance/examples/complete | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-iam/examples/iam-role | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-iam/examples/iam-user | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-aws-security-group/examples/complete | pass | pass | pass | pass | pass | pass | pass | pass |
| learn-terraform-lambda-api-gateway | pass | pass | pass | pass | pass | pass | pass | pass |
| learn-terraform-provision-eks-cluster | pass | pass | pass | pass | pass | pass | pass | pass |
| learn-terraform-data-sources-vpc | pass | pass | pass | pass | pass | pass | pass | pass |
| learn-terraform-data-sources-app | pass | pass | pass | pass | pass | pass | pass | pass |
| learn-terraform-variables | pass | pass | pass | pass | pass | pass | pass | pass |
| learn-terraform-outputs | pass | pass | pass | pass | pass | pass | pass | pass |
| learn-terraform-modules | pass | pass | pass | pass | pass | pass | pass | pass |
| learn-terraform-provisioning/instances | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-google-cloud-storage/examples/simple_bucket | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-google-cloud-storage/examples/multiple_buckets | pass | pass | pass | pass | pass | pass | pass | orig-fails |
| terraform-google-pubsub/examples/simple | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-google-pubsub/examples/cloud_storage | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-google-network/examples/basic_custom_mode | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-google-network/examples/submodule_firewall | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-google-network/examples/simple_project | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-provider-azurerm/examples/storage/storage-account | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-provider-azurerm/examples/servicebus/basic | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-provider-azurerm/examples/virtual-networks/basic | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-provider-azurerm/examples/app-service/linux-web-app-zip-deploy | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-provider-azurerm/examples/container-registry | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-provider-oci/examples/object_storage | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-provider-oci/examples/functions | pass | pass | pass | pass | pass | pass | pass | pass |
| terraform-provider-oci/examples/networking/vcn | pass | pass | pass | pass | pass | pass | pass | pass |
| **pass count** | 48/48 | 48/48 | 48/48 | 48/48 | 48/48 | 48/48 | 48/48 | 47/48 (1 orig-fails) |

### Deployment equivalence on Floci

| root | original apply | plan after swapping in the export | result |
|---|---|---|---|
| template/aws-serverless-api | ok | exit 0 (no changes) | pass |
| template/aws-queue-worker | ok | exit 0 (no changes) | pass |
| template/aws-event-fanout | ok | exit 0 (no changes) | pass |
| template/aws-static-site | ok | exit 0 (no changes) | pass |
| template/aws-scheduled-job | ok | exit 0 (no changes) | pass |
| template/aws-vpc-basics | ok | exit 0 (no changes) | pass |
| template/aws-step-functions | ok | exit 0 (no changes) | pass |
| terraform-aws-sqs/examples/complete | ok | exit 0 (no changes) | pass |
| terraform-aws-dynamodb-table/examples/basic | ok | exit 0 (no changes) | pass |
| learn-terraform-lambda-api-gateway | ok | exit 0 (no changes) | pass |
| terraform-aws-s3-bucket/examples/object | ok | exit 2: the same 1 change the original plans right after its own apply | pass |
| terraform-aws-lambda/examples/simple | ok | exit 2: the same 1 change the original plans right after its own apply | pass |
| terraform-aws-sns/examples/complete | fails on Floci (`PutDataProtectionPolicy` not supported) | – | n/a |
| terraform-aws-eventbridge/examples/default-bus | fails on Floci (CloudWatch Logs delivery source not supported) | – | n/a |

12/12 roots that deploy on Floci are equivalent. The two drift cases are
emulator gaps (S3 object attributes such as `website_redirect`, and the
Lambda module's empty `kms_key_arn` output), present before the swap.

## Bugs found and fixed

| Bug | Before | After |
|---|---|---|
| Comments dropped | Import dropped every comment: 600 comments lost across 45 of the 48 public roots (only 3 roots had none to lose). | Kept on attributes, blocks, `locals`, `terraform` and file ends (`model.Comments`); 0 lost. Mirrored in the desktop types; canvas edits keep them. |
| Dependency lock file dropped | The folder import skipped `.terraform.lock.hcl` (8/48 roots), so the export resolved newer providers. | Kept as an asset and exported (the sandbox still ignores it). |
| Variable files moved | `terraform.tfvars` and `prod.auto.tfvars` were exported as `environments/default.tfvars` / `environments/prod.tfvars`, which Terraform does not load automatically: a plan of the export saw different inputs. `terraform.tfvars` + `terraform.auto.tfvars` were merged into one environment. | Each root variable file is its own environment remembering its file (`Environment.File`) and is written back under that name; name clashes get the file name. |
| Override files merged as normal blocks | `override.tf` was imported as a second `resource "aws_security_group" "web"`: a `duplicate_address` error and an export with two copies of the block, which does not validate. | Not interpreted (`unsupported_file` warning), kept as a file and exported unchanged, so Terraform applies it as before. |
| JSON configuration silent | Folder imports copied `*.tf.json` without saying anything; folders with only JSON configuration vanished from the scan. | `unsupported_file` warning on the scan and the workflow; JSON-only folders listed as skipped. |
| Mixed line endings | CRLF files kept `\r\n` inside multi-line expressions and heredocs (35 CRs in the edge root's `main.tf`) while the rest became LF. | CRLF is read as LF everywhere. |
| Files outside the folder | References such as `"${path.module}/../fixtures/app"` silently broke in the export. | `path_outside_folder` warning on the scan and the workflow. |
| Module problems hidden after import | Diagnostics of copied local modules were shown by the scan only. | Also on the imported workflow. |

## Known limitations

- Comments inside a `*.tfvars` file and in `.env.example` files are not kept
  (environment values are a map).
- Comments between an attribute name and its value (`a = # x` + newline + value)
  are dropped.
- Comments move with their block into Nimbo's file layout; a comment that
  described a group of blocks ("# --- Networking ---") stays above the first
  of them only.
- Files outside the imported folder are reported, not copied.
- Override files and `*.tf.json` are kept and exported, but not shown or
  editable on the canvas; deleting a block on the canvas that an override
  file still targets makes the configuration invalid.
- A local module whose directory name clashes with an asset folder under
  `modules/` of the root shares that folder.
- Deployment equivalence covers AWS on Floci only; Azure, GCP and OCI roots
  are covered by the semantic, `fmt` and `validate` checks.
