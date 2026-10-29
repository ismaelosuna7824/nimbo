# Provider compatibility with the local sandbox

Nimbo deploys Terraform onto the Floci emulators. A Terraform provider release
or a Floci image update can break that without any Nimbo change: for example
hashicorp/google before 7.26 built doubled paths against the sandbox's custom
endpoints (`/sql/v1beta4/sql/v1beta4/...`), so Cloud SQL, IAM and GKE failed
locally. Two things keep that from happening silently:

- **The compat table**, `backend/internal/catalog/curated/provider_compat.json`
  (`catalog.ProviderCompats()`): per provider, the lowest version verified on
  Floci (`localMinimum`) and the known broken ranges (`issues`). The constraint
  Nimbo writes (`Recommended`) and the embedded schema version (`Catalog`) are
  derived from the embedded index. Field semantics are in
  [ARCHITECTURE.md](ARCHITECTURE.md).
- **The compat matrix**, a test that deploys a small representative config per
  provider through the real sandbox at several versions, run weekly by
  [`.github/workflows/provider-compat.yml`](../.github/workflows/provider-compat.yml).

## The matrix

`TestProviderCompat` (`backend/internal/sandbox/compat_test.go`, build tag
`compat`) runs, for every provider of the table and each selected version:

1. a fresh sandbox (temporary home, its own pod and emulators) and an apply of
   the provider's representative config with `version = "= <version>"`; the
   config's other providers stay at their recommended constraint;
2. a check that the lock file selected that version;
3. no post-deploy drift (the apply run's own check, `Run.Drift`, which users
   see) and a second plan reporting no changes;
4. removal of the sandbox (containers, network, volumes).

Versions, oldest first, duplicates merged:

| selector | version |
|---|---|
| `minimum` | the table's `localMinimum` |
| `major` | newest release of the catalog's major (what `~> X.Y` resolves to) |
| `latest` | newest release overall (catches a new major) |
| `X.Y.Z` / `name@X.Y.Z` | that release, for every / one provider |

| config | tests | resources |
|---|---|---|
| `aws` | aws, archive, random | S3 (+versioning), SQS, SNS, DynamoDB, Secrets Manager, CloudWatch Logs, IAM, Lambda from an `archive_file` zip, SQS event source, `random_id`, `random_password` |
| `google` | google | Storage, Pub/Sub, Secret Manager, Service Usage, service account + project IAM member, VPC, Cloud SQL instance + database + user |
| `azurerm` | azurerm | resource group, storage account, VNet, subnet, NSG + association |
| `oci` | oci | compartment, Object Storage bucket, Queue, Streaming pool + stream, KMS vault + key, Vault secret |

Configs only use services the emulator implements (OCI networking such as VCNs
isn't emulated: floci-oci serves the clients listed in `ociClients`,
`sandbox/wiring.go`). Keep them small: they guard the provider ↔ emulator
contract, not every resource.

A provider in the table without a config fails the matrix ("no representative
config"): add it to `compatConfigs` in `compat_test.go`.

Each cell ends `ok` (with its duration), `fail` (with the first Terraform error)
or `known`: it failed inside a range of the provider's `issues` that affects
one of the config's resources, which doesn't fail the run. The matrix is
logged and, with `NIMBO_COMPAT_REPORT`, appended to a markdown file.

`TestProviderMajors` (same file, no Docker) compares each provider's embedded
schema version with the registry and fails when a newer major is out.

### Running it

Needs Docker and network access (registry.terraform.io, Docker Hub). The
sandbox only pulls an image that is missing, so pull the current emulators
first to test what users get:

```sh
for i in floci floci-gcp; do docker pull floci/$i:latest; done   # emulators of the providers you test
cd backend
NIMBO_COMPAT_PROVIDERS=aws,google \
  go test -tags compat -timeout 3h -run '^TestProviderCompat$' -v ./internal/sandbox/
go test -tags compat -run '^TestProviderMajors$' -v ./internal/sandbox/
```

| variable | default | meaning |
|---|---|---|
| `NIMBO_COMPAT_PROVIDERS` | every provider of the table | comma-separated table names |
| `NIMBO_COMPAT_VERSIONS` | `minimum,major,latest` | selectors above |
| `NIMBO_COMPAT_REPORT` | none | markdown file the matrix is appended to |

Each cell takes one to a few minutes (emulator start, provider download into
the shared `nimbo-tf-plugins` cache, apply, plan). Nothing outside the test's
own `nimbo-compat-*` sandboxes is touched.

### The weekly workflow

Mondays and on demand (*Actions → Provider compat → Run workflow*, optional
providers and versions): one job per provider runs the matrix on an Ubuntu
runner, which pulls the current `:latest` Floci images, and one job runs
`TestProviderMajors`. When anything fails, the workflow opens (or updates) a
single issue labelled `provider-compat` with the matrices and a link to the run;
when a later run is green it comments the result and closes the issue. The
per-provider reports are also kept as `compat-*` artifacts for 30 days.

## When it fails

Start from the failing cell's error and the run log (`-v` prints every
Terraform line of the run).

**A new major is out** (`TestProviderMajors` fails; the `latest` cell already
tests it with the current representative config):

1. `cd backend && go generate ./internal/catalog/...` to embed the new
   schemas; `Recommended` follows the new catalog version.
2. Fix what the new major broke: templates' `version` constraints, curated
   attributes, the sandbox wiring for that provider
   (`backend/internal/sandbox/wiring.go`), the provider's config in
   `compatConfigs`.
3. Run the matrix for the provider.

`TestProviderMajors` keeps reporting the new major (and the issue stays open)
until the schemas are regenerated. If the new major is broken on Floci, record
it in `issues` (affected resources, message, fix) so the `latest` cell reports
`known` and users who lock it get the sandbox's warning, and track the fix
upstream.

**`major` or `latest` fails within a major Nimbo already uses** (a provider
release or a Floci image regressed):

- Fixable in Nimbo (wiring, a version-specific endpoint like
  `gcpLegacyEndpoints`): fix it, rerun the matrix.
- Not fixable in Nimbo: add an `issues` entry with the broken range (a
  go-version constraint, e.g. `">= 7.50.0, < 7.52.0"`), the affected resource
  types, a message and the fix users should apply, and report it upstream
  (Floci or the provider). The cell then shows `known`.

**`minimum` fails** (usually a Floci image change): either fix the wiring for
older versions or raise `localMinimum` to the lowest version that passes
(`NIMBO_COMPAT_VERSIONS=X.Y.Z` tries one) and record the range below it in
`issues`.

**A config resource fails on every version**: the emulator stopped supporting
it; check the Floci release notes, then fix or drop the resource from the
config (and from Nimbo's local-support flags if it is gone for good).

After editing the table, run `go test ./internal/catalog/` and the matrix for
the affected provider.
