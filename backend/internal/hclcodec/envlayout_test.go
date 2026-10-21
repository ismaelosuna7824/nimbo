package hclcodec

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// templateWorkflow imports a catalog template the way a new workflow gets
// it (recommended layout).
func templateWorkflow(t *testing.T, id, name string) *model.Workflow {
	t.Helper()
	root := filepath.Join("..", "catalog", "templates", id)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = data
	}
	wf, _ := mustImport(t, files)
	UnpinFiles(wf, testResourceFile)
	wf.Name = name
	return wf
}

func strValue(s string) model.Value { return stringValue(s) }

// awsEnvWorkflow is aws-serverless-api with dev and prod, a sensitive input
// and an environment variable.
func awsEnvWorkflow(t *testing.T) *model.Workflow {
	t.Helper()
	wf := templateWorkflow(t, "aws-serverless-api", "Items API")
	wf.Variables = append(wf.Variables, model.NamedBlock{Name: "api_key", Body: model.Body{Attrs: []model.Attr{
		{Name: "type", Value: model.Value{Expr: "string"}},
		{Name: "description", Value: strValue("Key the API checks.")},
		{Name: "sensitive", Value: model.Value{Expr: "true"}},
	}, Blocks: []model.Block{}}})
	// The module reads the key, so the roots pass it.
	wf.Outputs = append(wf.Outputs, model.NamedBlock{Name: "api_key_set", Body: model.Body{Attrs: []model.Attr{
		{Name: "value", Value: model.Value{Expr: `var.api_key != ""`}},
		{Name: "sensitive", Value: model.Value{Expr: "true"}},
	}, Blocks: []model.Block{}}})
	wf.Environments = []model.Environment{
		{Name: "dev", Values: map[string]model.Value{
			"environment": strValue("dev"), "api_key": strValue("dev-secret-value"),
		}, EnvVars: []model.EnvVar{{Key: "AWS_PROFILE", Value: "dev"}}},
		{Name: "prod", Values: map[string]model.Value{
			"environment": strValue("prod"), "lambda_memory_mb": {Expr: "1024"}, "api_key": strValue("prod-secret-value"),
			"project": strValue("shop"),
		}, EnvVars: []model.EnvVar{{Key: "AWS_PROFILE", Value: "prod"}, {Key: "TOKEN", Value: "x", Secret: true}}},
	}
	Normalize(wf)
	return wf
}

// envTree is everything an export of wf writes in its layout (Terraform,
// values and scaffold).
func envTree(t *testing.T, wf *model.Workflow) map[string][]byte {
	t.Helper()
	files, err := ExportLayout(wf, ExportOptions{Environments: true, ResourceFile: testResourceFile})
	if err != nil {
		t.Fatalf("ExportLayout: %v", err)
	}
	for name, data := range Scaffold(wf, testResourceFile) {
		if _, taken := files[name]; !taken {
			files[name] = data
		}
	}
	if dir := os.Getenv("NIMBO_ENVLAYOUT_DUMP"); dir != "" {
		for name, data := range files {
			p := filepath.Join(dir, t.Name(), filepath.FromSlash(name))
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, data, 0o644)
		}
	}
	return files
}

func treeNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func TestEnvironmentsLayoutAWS(t *testing.T) {
	wf := awsEnvWorkflow(t)
	if got := wf.EffectiveLayout(); got != model.LayoutEnvironments {
		t.Fatalf("auto layout with 2 environments = %q", got)
	}
	files := envTree(t, wf)
	want := []string{
		".gitignore", "Makefile", "README.md",
		"environments/dev/.env.example", "environments/dev/backend.hcl.example", "environments/dev/backend.tf",
		"environments/dev/locals.tf", "environments/dev/main.tf", "environments/dev/outputs.tf",
		"environments/dev/providers.tf", "environments/dev/terraform.tfvars", "environments/dev/variables.tf", "environments/dev/versions.tf",
		"environments/prod/.env.example", "environments/prod/backend.hcl.example", "environments/prod/backend.tf",
		"environments/prod/locals.tf", "environments/prod/main.tf", "environments/prod/outputs.tf",
		"environments/prod/providers.tf", "environments/prod/terraform.tfvars", "environments/prod/variables.tf", "environments/prod/versions.tf",
		"modules/items_api/README.md", "modules/items_api/api.tf", "modules/items_api/compute.tf", "modules/items_api/data.tf",
		"modules/items_api/database.tf", "modules/items_api/iam.tf", "modules/items_api/locals.tf", "modules/items_api/main.tf",
		"modules/items_api/outputs.tf", "modules/items_api/variables.tf", "modules/items_api/versions.tf",
	}
	if got := treeNames(files); !slices.Equal(got, want) {
		t.Fatalf("tree:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	text := func(name string) string { return string(files[name]) }

	// One state per environment: S3 with native locking, partial config.
	for _, env := range []string{"dev", "prod"} {
		be := text("environments/" + env + "/backend.tf")
		for _, s := range []string{`backend "s3"`, `key          = "items_api/` + env + `/terraform.tfstate"`, "use_lockfile = true", "encrypt      = true"} {
			if !strings.Contains(be, s) {
				t.Errorf("%s backend.tf lacks %q:\n%s", env, s, be)
			}
		}
		if strings.Contains(be, "bucket") {
			t.Errorf("backend.tf must leave the bucket to backend.hcl:\n%s", be)
		}
		if !strings.Contains(text("environments/"+env+"/backend.hcl.example"), "bucket") {
			t.Errorf("backend.hcl.example lacks the bucket")
		}
		v := text("environments/" + env + "/versions.tf")
		if !strings.Contains(v, `required_version = ">= 1.10"`) || !strings.Contains(v, `version = "~> 6.0"`) {
			t.Errorf("root versions.tf must pin ~> and require use_lockfile's Terraform:\n%s", v)
		}
		main := text("environments/" + env + "/main.tf")
		for _, s := range []string{`module "items_api"`, `source = "../../modules/items_api"`, "environment      = var.environment",
			"lambda_memory_mb = var.lambda_memory_mb", "api_key          = var.api_key", "project          = var.project"} {
			if !strings.Contains(main, s) {
				t.Errorf("%s main.tf lacks %q:\n%s", env, s, main)
			}
		}
		// aws_region is read by the provider only; log_retention_days keeps its module default.
		if strings.Contains(main, "aws_region") || strings.Contains(main, "log_retention_days") {
			t.Errorf("%s main.tf passes an input the module does not need from the root:\n%s", env, main)
		}
		// prod overrides project: it differs between environments, so no root holds a default and dev states the old one.
		if blk := variableBlock(text("environments/"+env+"/variables.tf"), "project"); blk == "" || strings.Contains(blk, "default") {
			t.Errorf("project differs between environments; root variable:\n%s", blk)
		}
		if p := text("environments/" + env + "/providers.tf"); !strings.Contains(p, `provider "aws"`) || !strings.Contains(p, "local.common_tags") {
			t.Errorf("providers.tf:\n%s", p)
		}
		if l := text("environments/" + env + "/locals.tf"); !strings.Contains(l, "common_tags") || strings.Contains(l, "name_prefix") {
			t.Errorf("root locals must be only what providers read:\n%s", l)
		}
		vars := text("environments/" + env + "/variables.tf")
		for _, s := range []string{`variable "environment"`, `variable "api_key"`, `variable "project"`, `variable "aws_region"`, `variable "lambda_memory_mb"`} {
			if !strings.Contains(vars, s) {
				t.Errorf("%s variables.tf lacks %s:\n%s", env, s, vars)
			}
		}
		if out := text("environments/" + env + "/outputs.tf"); !strings.Contains(out, "module.items_api.") {
			t.Errorf("outputs not re-exported:\n%s", out)
		}
		tfvars := text("environments/" + env + "/terraform.tfvars")
		if strings.Contains(tfvars, "secret") || strings.Contains(tfvars, "api_key") {
			t.Errorf("secret written to %s terraform.tfvars:\n%s", env, tfvars)
		}
		if !strings.Contains(tfvars, `environment      = "`+env+`"`) {
			t.Errorf("%s terraform.tfvars:\n%s", env, tfvars)
		}
		dotenv := text("environments/" + env + "/.env.example")
		if !strings.Contains(dotenv, "TF_VAR_api_key=\n") || !strings.Contains(dotenv, "AWS_PROFILE="+env) {
			t.Errorf("%s .env.example:\n%s", env, dotenv)
		}
	}
	if s := text("environments/prod/terraform.tfvars"); !strings.Contains(s, "lambda_memory_mb = 1024") || !strings.Contains(s, `project          = "shop"`) {
		t.Errorf("prod values:\n%s", s)
	}
	if s := text("environments/dev/terraform.tfvars"); !strings.Contains(s, "lambda_memory_mb = 256") {
		t.Errorf("dev keeps the old default of an input prod overrides:\n%s", s)
	}
	if s := text("environments/prod/.env.example"); !strings.Contains(s, "TOKEN=\n") {
		t.Errorf("secret env var value written:\n%s", s)
	}

	// The service module: no provider configuration, minimum versions,
	// environment-specific inputs without default.
	for name, data := range files {
		if strings.HasPrefix(name, "modules/") && strings.HasSuffix(name, ".tf") && strings.Contains(string(data), "provider \"") {
			t.Errorf("%s configures a provider:\n%s", name, data)
		}
	}
	mv := text("modules/items_api/versions.tf")
	if !strings.Contains(mv, `version = ">= 6.0"`) || strings.Contains(mv, "~>") || strings.Contains(mv, "backend") {
		t.Errorf("module versions.tf:\n%s", mv)
	}
	mvars := text("modules/items_api/variables.tf")
	// project: the roots pass it (they read it for the tags) and hold its default.
	for _, name := range []string{"environment", "lambda_memory_mb", "project"} {
		if blk := variableBlock(mvars, name); blk == "" || strings.Contains(blk, "default") {
			t.Errorf("module input %s must exist without a default:\n%s", name, blk)
		}
	}
	if blk := variableBlock(mvars, "log_retention_days"); !strings.Contains(blk, "default     = 14") {
		t.Errorf("module-only input lost its default:\n%s", blk)
	}
	if strings.Contains(mvars, `variable "aws_region"`) {
		t.Errorf("the module declares aws_region, which only the provider reads:\n%s", mvars)
	}
	if l := text("modules/items_api/locals.tf"); strings.Contains(l, "common_tags") {
		t.Errorf("the module keeps common_tags, which only the provider reads:\n%s", l)
	}
	// A project override in an environment reaches the resource names.
	if !strings.Contains(text("modules/items_api/locals.tf"), "${var.project}-${var.environment}") {
		t.Errorf("module locals:\n%s", text("modules/items_api/locals.tf"))
	}
	if !strings.Contains(text("modules/items_api/data.tf"), "${path.module}/build/api.zip") {
		t.Errorf("module data.tf:\n%s", text("modules/items_api/data.tf"))
	}
	readme := text("README.md")
	for _, s := range []string{"terraform -chdir=environments/dev init -backend-config=backend.hcl", "make plan ENV=<env>", "`items_api/prod/terraform.tfstate`", "TF_VAR_api_key"} {
		if !strings.Contains(readme, s) {
			t.Errorf("README lacks %q:\n%s", s, readme)
		}
	}
	mk := text("Makefile")
	for _, s := range []string{"ENV ?= dev", "ENVS := dev prod", "\tterraform", "init:", "plan:", "apply:", "fmt:", "validate:"} {
		if s == "\tterraform" {
			s = "\t$(TERRAFORM) -chdir=$(DIR) init $(BACKEND_CONFIG)"
		}
		if !strings.Contains(mk, s) {
			t.Errorf("Makefile lacks %q:\n%s", s, mk)
		}
	}
	if !IsScaffold("Makefile", files["Makefile"]) || !IsScaffold("modules/items_api/README.md", files["modules/items_api/README.md"]) ||
		!IsScaffold("environments/dev/backend.hcl.example", files["environments/dev/backend.hcl.example"]) {
		t.Error("scaffold files must carry their marker")
	}
	if g := text(".gitignore"); !strings.Contains(g, ".terraform.lock.hcl") || strings.Contains(g, "\n*.tfvars\n") {
		t.Errorf(".gitignore must commit terraform.tfvars and the lock file:\n%s", g)
	}
}

// variableBlock returns the source of one variable block.
func variableBlock(src, name string) string {
	i := strings.Index(src, `variable "`+name+`"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(src[i:], "\n}\n")
	if j < 0 {
		return src[i:]
	}
	return src[i : i+j+3]
}

func TestEnvironmentsLayoutGCP(t *testing.T) {
	wf := templateWorkflow(t, "gcp-pubsub-storage", "Orders")
	wf.Layout = model.LayoutEnvironments
	wf.Environments = []model.Environment{
		{Name: "staging", Values: map[string]model.Value{"environment": strValue("staging"), "gcp_project": strValue("orders-staging")}},
		{Name: "prod", Values: map[string]model.Value{"environment": strValue("prod"), "gcp_project": strValue("orders-prod")}},
	}
	Normalize(wf)
	files := envTree(t, wf)
	for _, env := range []string{"staging", "prod"} {
		be := string(files["environments/"+env+"/backend.tf"])
		if !strings.Contains(be, `backend "gcs"`) || !strings.Contains(be, `prefix = "orders/`+env+`"`) {
			t.Errorf("%s backend.tf:\n%s", env, be)
		}
		if !strings.Contains(string(files["environments/"+env+"/backend.hcl.example"]), "bucket") {
			t.Errorf("%s backend.hcl.example lacks the bucket", env)
		}
		if p := string(files["environments/"+env+"/providers.tf"]); !strings.Contains(p, `provider "google"`) {
			t.Errorf("providers.tf:\n%s", p)
		}
		if v := string(files["environments/"+env+"/versions.tf"]); !strings.Contains(v, `required_version = ">= 1.6"`) {
			t.Errorf("gcs needs no newer Terraform:\n%s", v)
		}
		if s := string(files["environments/"+env+"/terraform.tfvars"]); !strings.Contains(s, `gcp_project = "orders-`+env+`"`) {
			t.Errorf("%s terraform.tfvars:\n%s", env, s)
		}
	}
	if _, ok := files["modules/orders/main.tf"]; !ok {
		t.Errorf("service module missing: %v", treeNames(files))
	}
}

// TestEnvironmentsLayoutRoundTrip: importing the generated tree gives one
// workflow that exports the same tree, byte for byte.
func TestEnvironmentsLayoutRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		wf   func(t *testing.T) *model.Workflow
	}{
		{"aws", awsEnvWorkflow},
		{"gcp", func(t *testing.T) *model.Workflow {
			wf := templateWorkflow(t, "gcp-pubsub-storage", "Orders")
			wf.Environments = []model.Environment{
				{Name: "dev", Values: map[string]model.Value{"environment": strValue("dev")}},
				{Name: "prod", Values: map[string]model.Value{"environment": strValue("prod")}},
			}
			return wf
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := tc.wf(t)
			first := envTree(t, wf)
			tree := map[string][]byte{}
			for name, data := range first {
				if !IsScaffold(name, data) {
					tree[name] = data
				}
			}
			back, diags, err := ImportEnvironments(wf.Name, tree)
			if err != nil {
				t.Fatalf("ImportEnvironments: %v", err)
			}
			for _, d := range diags {
				if d.Severity != model.SeverityInfo {
					t.Errorf("diagnostic: %+v", d)
				}
			}
			if back.Layout != model.LayoutEnvironments || len(back.Environments) != 2 {
				t.Fatalf("layout %q, environments %+v", back.Layout, back.Environments)
			}
			second := envTree(t, back)
			for _, name := range treeNames(first) {
				if string(first[name]) != string(second[name]) {
					t.Errorf("%s differs after import:\n--- first\n%s\n--- second\n%s", name, first[name], second[name])
				}
			}
			if a, b := treeNames(first), treeNames(second); !slices.Equal(a, b) {
				t.Errorf("trees differ:\n%v\n%v", a, b)
			}
		})
	}
}

// TestEnvironmentsLayoutNoDeadInputs: across every catalog template (all
// four clouds), the service module declares only variables and locals it
// reads, and every root passes each module input that it also reads.
func TestEnvironmentsLayoutNoDeadInputs(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "catalog", "templates"))
	if err != nil {
		t.Fatal(err)
	}
	ref := regexp.MustCompile(`\b(var|local)\.([A-Za-z_][A-Za-z0-9_-]*)`)
	decl := regexp.MustCompile(`(?m)^variable "([^"]+)"`)
	clouds := map[model.Cloud]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			wf := templateWorkflow(t, e.Name(), "Svc")
			wf.Environments = []model.Environment{{Name: "dev", Values: map[string]model.Value{}}, {Name: "prod", Values: map[string]model.Value{}}}
			Normalize(wf)
			for _, c := range wf.Clouds {
				clouds[c] = true
			}
			files := envTree(t, wf)
			used := map[string]bool{}
			for name, data := range files {
				if strings.HasPrefix(name, "modules/svc/") && strings.HasSuffix(name, ".tf") && name != "modules/svc/variables.tf" {
					for _, m := range ref.FindAllStringSubmatch(string(data), -1) {
						used[m[1]+"."+m[2]] = true
					}
				}
			}
			for _, m := range decl.FindAllStringSubmatch(string(files["modules/svc/variables.tf"]), -1) {
				if !used["var."+m[1]] {
					t.Errorf("modules/svc declares var.%s, which it never reads", m[1])
				}
			}
			for _, m := range regexp.MustCompile(`(?m)^  ([A-Za-z_][A-Za-z0-9_]*) +=`).FindAllStringSubmatch(string(files["modules/svc/locals.tf"]), -1) {
				if !used["local."+m[1]] {
					t.Errorf("modules/svc declares local.%s, which it never reads", m[1])
				}
			}
			// Every module input the root also reads is passed on.
			rootReads := map[string]bool{}
			for _, f := range []string{"providers.tf", "locals.tf"} {
				for _, m := range ref.FindAllStringSubmatch(string(files["environments/dev/"+f]), -1) {
					rootReads[m[1]+"."+m[2]] = true
				}
			}
			main := string(files["environments/dev/main.tf"])
			for _, m := range decl.FindAllStringSubmatch(string(files["modules/svc/variables.tf"]), -1) {
				if rootReads["var."+m[1]] && !regexp.MustCompile(`(?m)^\s+`+m[1]+` += var\.`+m[1]+`$`).MatchString(main) {
					t.Errorf("the root reads var.%s but does not pass it to the module:\n%s", m[1], main)
				}
				// One place holds the default: the root, for inputs it always passes.
				if rootReads["var."+m[1]] && strings.Contains(variableBlock(string(files["modules/svc/variables.tf"]), m[1]), "default") {
					t.Errorf("var.%s has a default in the module although every root passes it", m[1])
				}
			}
		})
	}
	if len(clouds) != 4 {
		t.Errorf("templates cover clouds %v, want all four", clouds)
	}
}
