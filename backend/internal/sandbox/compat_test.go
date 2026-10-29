//go:build compat

package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-version"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Provider compatibility matrix: every provider of the compat table
// (catalog.ProviderCompats) is deployed through the sandbox, exactly as a
// user's run, onto the Floci emulators at several versions: apply a small
// representative configuration, then require a second plan with no changes.
// It catches provider releases and Floci image updates that break local
// deploys (docs/PROVIDER_COMPAT.md).
//
//	go test -tags compat -timeout 3h -run TestProviderCompat -v ./internal/sandbox/
//
// Environment:
//
//	NIMBO_COMPAT_PROVIDERS  comma-separated table names (default: all)
//	NIMBO_COMPAT_VERSIONS   comma-separated selectors (default minimum,major,latest):
//	                        minimum = the table's localMinimum,
//	                        major   = newest release of the catalog's major,
//	                        latest  = newest release overall,
//	                        X.Y.Z or name@X.Y.Z = that release (of every / of one provider)
//	NIMBO_COMPAT_REPORT     file the markdown matrix is appended to
//
// Needs docker and network access (registry.terraform.io, Docker Hub).
// Every sandbox lives in a temporary home and is removed after its cell.

// compatConfig is a representative configuration of one or more providers.
// Its body has no provider blocks: the sandbox wiring generates them.
type compatConfig struct {
	name      string
	clouds    []model.Cloud
	providers []string // compat table names, in required_providers order
	body      string
}

// compatConfigs: the first config listing a provider tests it; the other
// providers of that config stay at their recommended constraint.
var compatConfigs = []compatConfig{
	{
		name:      "aws",
		clouds:    []model.Cloud{model.CloudAWS},
		providers: []string{"aws", "archive", "random"},
		body: `
resource "random_id" "suffix" {
  byte_length = 4
}

resource "random_password" "secret" {
  length  = 20
  special = false
}

resource "aws_s3_bucket" "b" {
  bucket        = "nimbo-compat-${random_id.suffix.hex}"
  force_destroy = true
}

resource "aws_s3_bucket_versioning" "b" {
  bucket = aws_s3_bucket.b.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_sqs_queue" "q" {
  name = "nimbo-compat-queue"
}

resource "aws_sns_topic" "t" {
  name = "nimbo-compat-topic"
}

resource "aws_dynamodb_table" "t" {
  name         = "nimbo-compat-table"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"
  attribute {
    name = "id"
    type = "S"
  }
}

resource "aws_secretsmanager_secret" "s" {
  name = "nimbo-compat-secret"
}

resource "aws_secretsmanager_secret_version" "s" {
  secret_id     = aws_secretsmanager_secret.s.id
  secret_string = random_password.secret.result
}

resource "aws_cloudwatch_log_group" "fn" {
  name              = "/aws/lambda/nimbo-compat-fn"
  retention_in_days = 7
}

data "archive_file" "fn" {
  type        = "zip"
  output_path = "${path.module}/build/fn.zip"
  source {
    content  = "exports.handler = async () => ({ statusCode: 200, body: 'ok' })\n"
    filename = "index.js"
  }
}

resource "aws_iam_role" "fn" {
  name = "nimbo-compat-fn"
  assume_role_policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Action = "sts:AssumeRole", Principal = { Service = "lambda.amazonaws.com" } }]
  })
}

resource "aws_lambda_function" "fn" {
  function_name    = "nimbo-compat-fn"
  role             = aws_iam_role.fn.arn
  handler          = "index.handler"
  runtime          = "nodejs22.x"
  filename         = data.archive_file.fn.output_path
  source_code_hash = data.archive_file.fn.output_base64sha256
  depends_on       = [aws_cloudwatch_log_group.fn]
}

resource "aws_lambda_event_source_mapping" "q" {
  event_source_arn = aws_sqs_queue.q.arn
  function_name    = aws_lambda_function.fn.arn
}
`,
	},
	{
		name:      "google",
		clouds:    []model.Cloud{model.CloudGCP},
		providers: []string{"google"},
		body: `
resource "google_storage_bucket" "b" {
  name                        = "nimbo-compat-bucket"
  location                    = "US"
  force_destroy               = true
  uniform_bucket_level_access = true
}

resource "google_pubsub_topic" "t" {
  name = "nimbo-compat-topic"
}

resource "google_pubsub_subscription" "s" {
  name  = "nimbo-compat-sub"
  topic = google_pubsub_topic.t.id
}

resource "google_secret_manager_secret" "s" {
  secret_id = "nimbo-compat-secret"
  replication {
    auto {}
  }
}

resource "google_project_service" "sql" {
  service            = "sqladmin.googleapis.com"
  disable_on_destroy = false
}

resource "google_service_account" "app" {
  account_id   = "nimbo-compat"
  display_name = "nimbo compat"
}

resource "google_project_iam_member" "app" {
  project = "floci-local"
  role    = "roles/pubsub.subscriber"
  member  = "serviceAccount:${google_service_account.app.email}"
}

resource "google_compute_network" "vpc" {
  name                    = "nimbo-compat-vpc"
  auto_create_subnetworks = false
}

resource "google_sql_database_instance" "db" {
  name                = "nimbo-compat-db"
  database_version    = "POSTGRES_16"
  region              = "us-central1"
  deletion_protection = false
  settings {
    tier = "db-f1-micro"
  }
  depends_on = [google_project_service.sql]
}

resource "google_sql_database" "app" {
  name     = "app"
  instance = google_sql_database_instance.db.name
}

resource "google_sql_user" "app" {
  name     = "app"
  instance = google_sql_database_instance.db.name
  password = "nimbo-compat"
}
`,
	},
	{
		name:      "azurerm",
		clouds:    []model.Cloud{model.CloudAzure},
		providers: []string{"azurerm"},
		body: `
resource "azurerm_resource_group" "rg" {
  name     = "nimbo-compat-rg"
  location = "eastus"
}

resource "azurerm_storage_account" "sa" {
  name                     = "nimbocompatstorage"
  resource_group_name      = azurerm_resource_group.rg.name
  location                 = azurerm_resource_group.rg.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
}

resource "azurerm_virtual_network" "vnet" {
  name                = "nimbo-compat-vnet"
  resource_group_name = azurerm_resource_group.rg.name
  location            = azurerm_resource_group.rg.location
  address_space       = ["10.20.0.0/16"]
}

resource "azurerm_subnet" "app" {
  name                 = "app"
  resource_group_name  = azurerm_resource_group.rg.name
  virtual_network_name = azurerm_virtual_network.vnet.name
  address_prefixes     = ["10.20.1.0/24"]
}

resource "azurerm_network_security_group" "app" {
  name                = "nimbo-compat-nsg"
  resource_group_name = azurerm_resource_group.rg.name
  location            = azurerm_resource_group.rg.location
}

resource "azurerm_subnet_network_security_group_association" "app" {
  subnet_id                 = azurerm_subnet.app.id
  network_security_group_id = azurerm_network_security_group.app.id
}
`,
	},
	{
		name:      "oci",
		clouds:    []model.Cloud{model.CloudOCI},
		providers: []string{"oci"},
		body: `
resource "oci_identity_compartment" "app" {
  compartment_id = "` + ociTenancy + `"
  name           = "nimbo-compat"
  description    = "nimbo compat"
  enable_delete  = true
}

resource "oci_objectstorage_bucket" "b" {
  compartment_id = oci_identity_compartment.app.id
  namespace      = "floci-local"
  name           = "nimbo-compat-bucket"
}

resource "oci_queue_queue" "q" {
  compartment_id = oci_identity_compartment.app.id
  display_name   = "nimbo-compat-queue"
}

resource "oci_streaming_stream" "events" {
  compartment_id = oci_identity_compartment.app.id
  name           = "nimbo-compat-events"
  partitions     = 1
}

resource "oci_kms_vault" "v" {
  compartment_id = oci_identity_compartment.app.id
  display_name   = "nimbo-compat-vault"
  vault_type     = "DEFAULT"
}

resource "oci_kms_key" "k" {
  compartment_id      = oci_identity_compartment.app.id
  display_name        = "nimbo-compat-key"
  management_endpoint = oci_kms_vault.v.management_endpoint
  key_shape {
    algorithm = "AES"
    length    = 32
  }
}

resource "oci_vault_secret" "s" {
  compartment_id = oci_identity_compartment.app.id
  vault_id       = oci_kms_vault.v.id
  key_id         = oci_kms_key.k.id
  secret_name    = "nimbo-compat-secret"
  secret_content {
    content_type = "BASE64"
    content      = base64encode("nimbo-compat")
  }
}
`,
	},
}

// compatCell is one provider × version result.
type compatCell struct {
	provider, version string
	selectors         []string
	config            string
	result            string // ok, fail, known (failure inside a known issue range)
	detail            string
}

func TestProviderCompat(t *testing.T) {
	c, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	compats := map[string]catalog.ProviderCompat{}
	for _, pc := range c.ProviderCompats() {
		compats[pc.Name] = pc
	}
	providers := envList("NIMBO_COMPAT_PROVIDERS")
	if len(providers) == 0 {
		for name := range compats {
			providers = append(providers, name)
		}
		sort.Strings(providers)
	}
	selectors := envList("NIMBO_COMPAT_VERSIONS")
	if len(selectors) == 0 {
		selectors = []string{"minimum", "major", "latest"}
	}

	var cells []compatCell
	images := map[model.Cloud]string{}
	t.Cleanup(func() { reportMatrix(t, cells, images) })

	for _, name := range providers {
		pc, ok := compats[name]
		if !ok {
			t.Errorf("%s is not in the compat table (catalog/curated/provider_compat.json)", name)
			continue
		}
		cfg, ok := configFor(name)
		if !ok {
			cells = append(cells, compatCell{provider: name, result: "fail",
				detail: "no representative config uses this provider: add one to compatConfigs in sandbox/compat_test.go"})
			t.Errorf("%s: no representative config", name)
			continue
		}
		versions, err := resolveVersions(pc, selectors)
		if err != nil {
			cells = append(cells, compatCell{provider: name, config: cfg.name, result: "fail", detail: err.Error()})
			t.Errorf("%s: %v", name, err)
			continue
		}
		for _, v := range versions {
			cell := compatCell{provider: name, version: v.version, selectors: v.selectors, config: cfg.name}
			t.Run(name+"@"+v.version, func(t *testing.T) {
				cell.result, cell.detail = runCompatCell(t, cfg, compats, name, v.version, images)
				if issues := pc.CheckVersion(v.version, cfg.resourceTypes()).Issues; len(issues) > 0 && cell.result == "fail" {
					cell.result = "known"
					cell.detail += " (known issue " + issues[0].Versions + ")"
				}
				switch cell.result {
				case "fail":
					t.Errorf("%s %s: %s", name, v.version, cell.detail)
				default:
					t.Logf("%s %s: %s %s", name, v.version, cell.result, cell.detail)
				}
			})
			cells = append(cells, cell)
		}
	}
}

// TestProviderMajors fails when the registry has a newer major release than
// the schemas the catalog embeds: regenerate them and review the compat
// table.
func TestProviderMajors(t *testing.T) {
	c, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("### Catalog vs registry\n\n| provider | catalog | newest in major | newest | status |\n|---|---|---|---|---|\n")
	for _, pc := range c.ProviderCompats() {
		major := pc.Major()
		all, err := registryVersions(pc.Source)
		if err != nil {
			t.Errorf("%s: %v", pc.Name, err)
			fmt.Fprintf(&b, "| %s | %s | ? | ? | registry error: %s |\n", pc.Name, pc.Catalog, mdCell(err.Error()))
			continue
		}
		newestMajor := newestInMajor(all, major)
		latest := all[len(all)-1]
		status := "ok"
		if latest.Segments()[0] > major {
			status = fmt.Sprintf("**new major %s**: regenerate schemas (`go generate ./internal/catalog/...`), run the matrix, update provider_compat.json and the templates' constraints", latest)
			t.Errorf("%s %s is out (catalog embeds %s)", pc.Source, latest, pc.Catalog)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", pc.Name, pc.Catalog, verString(newestMajor), latest, status)
	}
	t.Log("\n" + b.String())
	appendReport(t, b.String()+"\n")
}

func envList(key string) []string {
	var out []string
	for _, s := range strings.Split(os.Getenv(key), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func configFor(provider string) (compatConfig, bool) {
	for _, cfg := range compatConfigs {
		if slices.Contains(cfg.providers, provider) {
			return cfg, true
		}
	}
	return compatConfig{}, false
}

type selectedVersion struct {
	version   string
	selectors []string
}

// resolveVersions turns the selectors into distinct releases, oldest first.
func resolveVersions(pc catalog.ProviderCompat, selectors []string) ([]selectedVersion, error) {
	var all []*version.Version
	registry := func() ([]*version.Version, error) {
		if all != nil {
			return all, nil
		}
		var err error
		all, err = registryVersions(pc.Source)
		return all, err
	}
	byVersion := map[string]*selectedVersion{}
	add := func(v, sel string) {
		if s, ok := byVersion[v]; ok {
			s.selectors = append(s.selectors, sel)
			return
		}
		byVersion[v] = &selectedVersion{version: v, selectors: []string{sel}}
	}
	for _, sel := range selectors {
		switch sel {
		case "minimum":
			if pc.LocalMinimum == "" {
				return nil, fmt.Errorf("localMinimum is not set")
			}
			add(pc.LocalMinimum, sel)
		case "major", "latest":
			vs, err := registry()
			if err != nil {
				return nil, err
			}
			if sel == "latest" {
				add(vs[len(vs)-1].String(), sel)
				continue
			}
			v := newestInMajor(vs, pc.Major())
			if v == nil {
				return nil, fmt.Errorf("no %d.x release in the registry", pc.Major())
			}
			add(v.String(), sel)
		default:
			name, v, scoped := strings.Cut(sel, "@")
			if !scoped {
				v = sel
			} else if name != pc.Name {
				continue
			}
			if _, err := version.NewVersion(v); err != nil {
				return nil, fmt.Errorf("NIMBO_COMPAT_VERSIONS: %q is not minimum, major, latest or a version", sel)
			}
			add(v, "explicit")
		}
	}
	out := make([]selectedVersion, 0, len(byVersion))
	for _, s := range byVersion {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		return version.Must(version.NewVersion(out[i].version)).LessThan(version.Must(version.NewVersion(out[j].version)))
	})
	return out, nil
}

// registryVersions lists the stable releases of source (namespace/type),
// oldest first.
func registryVersions(source string) ([]*version.Version, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://registry.terraform.io/v1/providers/"+source+"/versions", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry %s: %w", source, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry %s: HTTP %d", source, resp.StatusCode)
	}
	var body struct {
		Versions []struct {
			Version string `json:"version"`
		} `json:"versions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("registry %s: %w", source, err)
	}
	var out []*version.Version
	for _, v := range body.Versions {
		if pv, err := version.NewVersion(v.Version); err == nil && pv.Prerelease() == "" {
			out = append(out, pv)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("registry %s: no releases", source)
	}
	sort.Sort(version.Collection(out))
	return out, nil
}

func newestInMajor(sorted []*version.Version, major int) *version.Version {
	for i := len(sorted) - 1; i >= 0; i-- {
		if sorted[i].Segments()[0] == major {
			return sorted[i]
		}
	}
	return nil
}

func verString(v *version.Version) string {
	if v == nil {
		return "-"
	}
	return v.String()
}

var resourceTypeRe = regexp.MustCompile(`(?m)^(?:resource|data) "([a-z0-9_]+)"`)

// resourceTypes are the resource and data source types cfg declares.
func (cfg compatConfig) resourceTypes() []string {
	var out []string
	for _, m := range resourceTypeRe.FindAllStringSubmatch(cfg.body, -1) {
		out = append(out, m[1])
	}
	return out
}

// compatMain is cfg with provider pinned to v and the config's other
// providers at their recommended constraint.
func compatMain(cfg compatConfig, compats map[string]catalog.ProviderCompat, provider, v string) string {
	var b strings.Builder
	b.WriteString("terraform {\n  required_providers {\n")
	for _, name := range cfg.providers {
		pc := compats[name]
		constraint := pc.Recommended
		if name == provider {
			constraint = "= " + v
		}
		source := pc.Source
		if source == "" {
			source = "hashicorp/" + name
		}
		fmt.Fprintf(&b, "    %s = { source = %q, version = %q }\n", name, source, constraint)
	}
	b.WriteString("  }\n}\n")
	b.WriteString(cfg.body)
	return b.String()
}

// runCompatCell applies cfg with provider at v in a fresh sandbox, then
// plans again; it returns the cell's result and detail.
func runCompatCell(t *testing.T, cfg compatConfig, compats map[string]catalog.ProviderCompat, provider, v string, images map[model.Cloud]string) (string, string) {
	m, err := New(Config{Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.DockerAvailable(context.Background()); err != nil {
		t.Fatalf("docker not available: %v", err)
	}
	var b [3]byte
	_, _ = rand.Read(b[:])
	wfID := "compat-" + provider + "-" + strings.ReplaceAll(v, ".", "-") + "-" + hex.EncodeToString(b[:])
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := m.Remove(ctx, wfID); err != nil {
			t.Errorf("remove sandbox %s: %v", wfID, err)
		}
	}()
	wf := &model.Workflow{ID: wfID, Clouds: cfg.clouds}
	files := map[string][]byte{"main.tf": []byte(compatMain(cfg, compats, provider, v))}

	start := time.Now()
	apply, err := compatRun(m, RunRequest{Workflow: wf, Action: model.ActionApply, Files: files})
	for _, c := range cfg.clouds {
		if _, seen := images[c]; seen {
			continue
		}
		if base, ok := m.EmulatorURL(wfID, c); ok {
			if ver, _, ok := probeHealth(context.Background(), emulators[c], base); ok {
				images[c] = m.emulatorImage(c) + " " + ver
			}
		}
	}
	if err != nil {
		return "fail", "apply: " + err.Error()
	}
	if apply.Status != model.RunSucceeded {
		return "fail", "apply: " + firstErrorLine(apply)
	}
	if got := readLockVersions(m.WorkDir(wfID))[provider]; got != v {
		return "fail", fmt.Sprintf("lock file selected %s %q, want %s", provider, got, v)
	}
	// The sandbox's own post-deploy check (Run.Drift) is what users see;
	// the plan below names the attributes.
	var drift []string
	for _, d := range apply.Drift {
		drift = append(drift, fmt.Sprintf("%s (%s)", d.Address, d.Action))
	}
	prefix := ""
	if len(drift) > 0 {
		prefix = "post-deploy drift: " + strings.Join(drift, ", ") + "; "
	}
	plan, err := compatRun(m, RunRequest{Workflow: wf, Action: model.ActionPlan, Files: files})
	if err != nil {
		return "fail", "plan: " + err.Error()
	}
	if plan.Status != model.RunSucceeded {
		return "fail", prefix + "plan after apply: " + firstErrorLine(plan)
	}
	if p := plan.Plan; p == nil || p.Add+p.Change+p.Destroy > 0 {
		var changes []string
		for _, ch := range plan.Changes {
			if ch.Action == model.PlanNoOp || ch.Action == model.PlanRead {
				continue
			}
			var attrs []string
			for _, a := range ch.Attributes {
				if ch.Action != model.PlanReplace || a.ForcesReplacement {
					if a.Sensitive || a.Unknown {
						attrs = append(attrs, a.Name)
					} else {
						attrs = append(attrs, fmt.Sprintf("%s %v → %v", a.Name, a.Before, a.After))
					}
				}
			}
			changes = append(changes, fmt.Sprintf("%s (%s: %s)", ch.Address, ch.Action, strings.Join(attrs, ", ")))
		}
		summary := "no plan summary"
		if p != nil {
			summary = fmt.Sprintf("%d to add, %d to change, %d to destroy", p.Add, p.Change, p.Destroy)
		}
		return "fail", prefix + "plan after apply not empty: " + summary + ": " + strings.Join(changes, "; ")
	}
	if prefix != "" {
		return "fail", strings.TrimSuffix(prefix, "; ")
	}
	return "ok", time.Since(start).Round(time.Second).String()
}

// compatRun starts a run and waits for it to end.
func compatRun(m *Manager, req RunRequest) (*model.Run, error) {
	run, err := m.Run(context.Background(), req)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		r, ok := m.GetRun(run.ID)
		if !ok {
			return nil, fmt.Errorf("run %s disappeared", run.ID)
		}
		if r.EndedAt != nil {
			return r, nil
		}
		time.Sleep(time.Second)
	}
	_ = m.Cancel(run.ID)
	last := ""
	if r, ok := m.GetRun(run.ID); ok && len(r.Log) > 0 {
		last = ": " + strings.TrimSpace(r.Log[len(r.Log)-1].Text)
	}
	return nil, fmt.Errorf("%s run did not finish in 15m (cancelled)%s", req.Action, last)
}

var (
	errorLine   = regexp.MustCompile(`^[│|]?\s*Error: `)
	snippetLine = regexp.MustCompile(`^\d+: `)
)

// firstErrorLine is the first Terraform error of a run (summary and its
// first detail line), else the run error.
func firstErrorLine(r *model.Run) string {
	for i, l := range r.Log {
		text := strings.TrimSpace(strings.TrimLeft(l.Text, "│| "))
		if !errorLine.MatchString(l.Text) {
			continue
		}
		// Skip the "with"/"on" location and the source snippet to the
		// first detail line, if the error has one.
		for _, next := range r.Log[i+1:] {
			if next.Stream != l.Stream {
				continue
			}
			d := strings.TrimSpace(strings.TrimLeft(next.Text, "│| "))
			if strings.HasPrefix(d, "╵") || errorLine.MatchString(next.Text) {
				break
			}
			if d == "" || strings.HasPrefix(d, "with ") || strings.HasPrefix(d, "on ") || strings.HasPrefix(d, "├") || snippetLine.MatchString(d) {
				continue
			}
			text += " — " + d
			break
		}
		return text
	}
	if r.Error != "" {
		return r.Error
	}
	return string(r.Status)
}

func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func reportMatrix(t *testing.T, cells []compatCell, images map[model.Cloud]string) {
	if len(cells) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("### Provider × version on Floci\n\n")
	clouds := make([]string, 0, len(images))
	for c, img := range images {
		clouds = append(clouds, string(c)+": `"+img+"`")
	}
	sort.Strings(clouds)
	if len(clouds) > 0 {
		b.WriteString("Emulators: " + strings.Join(clouds, ", ") + "\n\n")
	}
	b.WriteString("| provider | version | selected as | config | result | detail |\n|---|---|---|---|---|---|\n")
	for _, c := range cells {
		result := c.result
		if result == "fail" {
			result = "**fail**"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", c.provider, c.version, strings.Join(c.selectors, ", "), c.config, result, mdCell(c.detail))
	}
	t.Log("\n" + b.String())
	appendReport(t, b.String()+"\n")
}

func appendReport(t *testing.T, s string) {
	path := os.Getenv("NIMBO_COMPAT_REPORT")
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Errorf("report: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Errorf("report: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Errorf("report: %v", err)
	}
}
