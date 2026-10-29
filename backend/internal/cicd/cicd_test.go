package cicd

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const tfSource = `terraform {
  required_version = "~> 1.11.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region = var.region
}

variable "region" {
  type    = string
  default = "us-east-1"
}

variable "api_key" {
  type      = string
  sensitive = true
}

resource "aws_s3_bucket" "orders" {
  bucket = "orders-${var.region}"
}

resource "aws_ssm_parameter" "key" {
  name  = "/orders/api-key"
  type  = "SecureString"
  value = var.api_key
}
`

// terraformWorkflow imports tfSource (plus extra files) with envs.
func terraformWorkflow(t *testing.T, layout string, extra map[string]string, envs ...string) *model.Workflow {
	t.Helper()
	files := map[string][]byte{"main.tf": []byte(tfSource)}
	for n, c := range extra {
		files[n] = []byte(c)
	}
	wf, _, err := hclcodec.Import("Orders API", files)
	if err != nil {
		t.Fatal(err)
	}
	wf.Layout = layout
	for _, e := range envs {
		wf.Environments = append(wf.Environments, model.Environment{Name: e, Values: map[string]model.Value{"region": {Expr: `"eu-west-1"`}}})
	}
	hclcodec.Normalize(wf)
	return wf
}

// exportOf is what an export of a Terraform workflow generates.
func exportOf(t *testing.T, wf *model.Workflow) map[string][]byte {
	t.Helper()
	files, err := hclcodec.ExportLayout(wf, hclcodec.ExportOptions{Environments: true})
	if err != nil {
		t.Fatal(err)
	}
	for n, d := range hclcodec.Scaffold(wf, nil, hclcodec.Builds{}) {
		files[n] = d
	}
	return files
}

func dockerWorkflow() *model.Workflow {
	return &model.Workflow{
		ID: "shop", Name: "Shop", Kind: model.WorkflowDocker,
		Environments: []model.Environment{{Name: "prod"}, {Name: "staging"}, {Name: "dev"}},
		Compose: &model.ComposeModel{
			Files: []string{"compose.yaml"},
			Environments: []model.ComposeEnvironment{
				{Name: "dev", EnvFile: ".env.dev"},
				{Name: "prod", File: "compose.prod.yaml", EnvFile: ".env.prod"},
				{Name: "staging", File: "compose.staging.yaml"},
			},
			Graph: model.ComposeGraph{Nodes: []model.ComposeNode{
				{ID: "service:api", Kind: model.ComposeService, Name: "api", File: "compose.yaml", Service: &model.ServiceFacts{Build: &model.ServiceBuild{Context: "./api", Dockerfile: "api/Dockerfile"}}},
				{ID: "service:web", Kind: model.ComposeService, Name: "Web", File: "compose.yaml", Service: &model.ServiceFacts{Build: &model.ServiceBuild{Context: "./web", Dockerfile: "web/Dockerfile"}}},
				{ID: "service:db", Kind: model.ComposeService, Name: "db", File: "compose.yaml", Service: &model.ServiceFacts{Image: &model.ImageRef{Ref: "postgres:16"}}},
				{ID: "service:debug", Kind: model.ComposeService, Name: "debug", File: "compose.staging.yaml", Service: &model.ServiceFacts{Build: &model.ServiceBuild{Context: "./debug"}}},
			}},
		},
	}
}

func kubeWorkflow() *model.Workflow {
	return &model.Workflow{
		ID: "shop", Name: "Shop K8s", Kind: model.WorkflowKubernetes,
		Environments: []model.Environment{{Name: "dev"}, {Name: "prod"}},
		Kubernetes: &model.KubernetesModel{
			Sources: []model.KubeSource{
				{Kind: model.KubeKustomize, Path: "base", EnvPaths: map[string]string{"dev": "overlays/dev", "prod": "overlays/prod"}},
				{Kind: model.KubeHelm, Path: "charts/api", Namespace: "shop", ValuesFiles: []string{"charts/api/values-common.yaml"},
					EnvPaths: map[string]string{"prod": "charts/api/values-prod.yaml"}, Chart: &model.KubeChart{Name: "api"}},
				{Kind: model.KubeManifests, Path: "extra", Namespace: "tools"},
			},
			Builds: []model.KubeBuild{{Image: "api:dev", Context: "api"}},
		},
	}
}

func generate(t *testing.T, wf *model.Workflow, generated map[string][]byte, own map[string]bool, opts Options) map[string][]byte {
	t.Helper()
	out, err := Files(wf, generated, own, opts)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// pipeline parses the pipeline file of out and checks it with actionlint
// (GitHub) when the machine has it.
func pipeline(t *testing.T, out map[string][]byte) (string, map[string]any) {
	t.Helper()
	var file string
	for n := range out {
		if strings.HasPrefix(n, ".github/") || n == ".gitlab-ci.yml" {
			file = n
		}
	}
	if file == "" {
		t.Fatalf("no pipeline in %v", keys(out))
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out[file], &doc); err != nil {
		t.Fatalf("%s does not parse: %v\n%s", file, err, out[file])
	}
	if strings.HasPrefix(file, ".github/") {
		actionlint(t, file, out[file])
	}
	return file, doc
}

func actionlint(t *testing.T, file string, data []byte) {
	t.Helper()
	bin, err := exec.LookPath("actionlint")
	if err != nil {
		t.Log("actionlint is not installed: the GitHub workflow is only parsed")
		return
	}
	dir := t.TempDir()
	p := filepath.Join(dir, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-no-color", p)
	cmd.Dir = dir
	if res, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actionlint %s: %v\n%s\n%s", file, err, res, data)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func jobOf(t *testing.T, doc map[string]any, provider, id string) map[string]any {
	t.Helper()
	root := doc
	if provider == GitHub {
		root, _ = doc["jobs"].(map[string]any)
	}
	j, ok := root[id].(map[string]any)
	if !ok {
		t.Fatalf("no job %q", id)
	}
	return j
}

func TestTerraformEnvironmentsLayout(t *testing.T) {
	wf := terraformWorkflow(t, model.LayoutEnvironments, nil, "dev", "prod", "staging")
	generated := exportOf(t, wf)
	if _, ok := generated["environments/prod/"+hclcodec.BackendExampleFile]; !ok {
		t.Fatalf("export has no backend.hcl.example: %v", keys(generated))
	}

	out := generate(t, wf, generated, nil, Options{Provider: GitHub, Smoke: true})
	file, doc := pipeline(t, out)
	if file != ".github/workflows/orders-api.yml" {
		t.Errorf("file = %s", file)
	}
	text := string(out[file])
	for _, want := range []string{"make validate ENV=dev", "make plan ENV=staging", "make apply ENV=prod",
		"terraform_version: \"1.11\"", "role-to-assume: ${{ vars.AWS_ROLE_ARN }}", "TF_VAR_api_key: ${{ secrets.TF_VAR_api_key }}",
		"environments/prod/backend.hcl", "AWS_ROLE_ARN (variable, required)", "TF_BACKEND_CONFIG (variable, required)"} {
		if !strings.Contains(text, want) {
			t.Errorf("pipeline lacks %q", want)
		}
	}
	// Promotion: dev, then the production-like ones (staging, prod) in order.
	if needs := jobOf(t, doc, GitHub, "apply-prod")["needs"]; !slices.Equal(toStrings(needs), []string{"validate", "apply-staging"}) {
		t.Errorf("apply-prod needs %v", needs)
	}
	if needs := jobOf(t, doc, GitHub, "apply-staging")["needs"]; !slices.Equal(toStrings(needs), []string{"validate", "apply-dev"}) {
		t.Errorf("apply-staging needs %v", needs)
	}
	apply := jobOf(t, doc, GitHub, "apply-prod")
	if apply["environment"] != "prod" || apply["permissions"].(map[string]any)["id-token"] != "write" {
		t.Errorf("apply-prod = %v", apply)
	}
	if plan := jobOf(t, doc, GitHub, "plan-prod"); plan["environment"] != nil || plan["if"] != ghReview {
		t.Errorf("plan-prod runs on %v in %v", plan["if"], plan["environment"])
	}
	if smoke := jobOf(t, doc, GitHub, "smoke-prod"); !strings.Contains(smoke["env"].(map[string]any)["SMOKE_URL"].(string), "needs.apply-prod.outputs.smoke_url") {
		t.Errorf("smoke-prod env = %v", smoke["env"])
	}
	readme := string(out[readmeFile])
	if !hclcodec.IsScaffold(readmeFile, out[readmeFile]) || !strings.Contains(readme, "## GitHub Actions pipeline") || !strings.Contains(readme, "| `TF_BACKEND_CONFIG` |") {
		t.Errorf("README:\n%s", readme)
	}

	out = generate(t, wf, generated, nil, Options{Provider: GitLab})
	_, doc = pipeline(t, out)
	prod := jobOf(t, doc, GitLab, "apply-prod")
	rule := prod["rules"].([]any)[0].(map[string]any)
	if rule["when"] != "manual" || prod["allow_failure"] != false || prod["resource_group"] != "terraform-prod" {
		t.Errorf("apply-prod = %v", prod)
	}
	if rule := jobOf(t, doc, GitLab, "apply-dev")["rules"].([]any)[0].(map[string]any); rule["when"] != nil {
		t.Errorf("apply-dev is gated: %v", rule)
	}
	if img := jobOf(t, doc, GitLab, "plan-dev")["image"].(map[string]any)["name"]; img != "hashicorp/terraform:1.11" {
		t.Errorf("plan image = %v", img)
	}
	if ids := jobOf(t, doc, GitLab, "plan-dev")["id_tokens"].(map[string]any); ids["AWS_ID_TOKEN"] == nil {
		t.Errorf("plan id_tokens = %v", ids)
	}
	if _, ok := doc["smoke-prod"]; ok {
		t.Error("smoke job without the smoke option")
	}
}

func TestTerraformSingleLayout(t *testing.T) {
	gcp := `resource "google_storage_bucket" "assets" {
  name     = "assets"
  location = "EU"
}
`
	wf := terraformWorkflow(t, model.LayoutSingle, map[string]string{"gcs.tf": gcp}, "dev", "prod")
	if !slices.Contains(wf.Clouds, model.CloudGCP) || !slices.Contains(wf.Clouds, model.CloudAWS) {
		t.Fatalf("clouds = %v", wf.Clouds)
	}
	generated := exportOf(t, wf)
	for _, provider := range []string{GitHub, GitLab} {
		out := generate(t, wf, generated, nil, Options{Provider: provider, Smoke: true})
		file, doc := pipeline(t, out)
		text := string(out[file])
		for _, want := range []string{"terraform workspace select -or-create=true prod", "-var-file=environments/prod.tfvars",
			"cp backend.tf.example backend.tf", "GCP_WORKLOAD_IDENTITY_PROVIDER", "terraform init -backend=false"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: pipeline lacks %q", provider, want)
			}
		}
		if strings.Contains(text, "make ") {
			t.Errorf("%s: the single layout has no Makefile:\n%s", provider, text)
		}
		jobOf(t, doc, provider, "smoke-dev")
	}
}

func TestTerraformWithoutEnvironments(t *testing.T) {
	wf := terraformWorkflow(t, "", nil)
	out := generate(t, wf, exportOf(t, wf), nil, Options{Provider: GitHub})
	file, doc := pipeline(t, out)
	if strings.Contains(string(out[file]), "workspace") {
		t.Error("one environment uses the default workspace")
	}
	jobOf(t, doc, GitHub, "apply-default")
}

func TestCompose(t *testing.T) {
	wf := dockerWorkflow()
	own := map[string]bool{"compose.yaml": true, "compose.prod.yaml": true, "README.md": true}
	for _, provider := range []string{GitHub, GitLab} {
		out := generate(t, wf, map[string][]byte{}, own, Options{Provider: provider, Smoke: true})
		file, doc := pipeline(t, out)
		text := string(out[file])
		if !strings.Contains(text, "build Web api\n") {
			t.Errorf("%s: built services:\n%s", provider, text)
		}
		for _, want := range []string{"-f compose.yaml -f compose.prod.yaml", "--env-file .env.prod", "${IMAGE_PREFIX}/web:${IMAGE_TAG}", "up -d --no-build"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: pipeline lacks %q", provider, want)
			}
		}
		// dev first, then the production-like environments.
		if needs := toStrings(jobOf(t, doc, provider, "deploy-staging")["needs"]); !slices.Equal(needs, []string{"build", "deploy-dev"}) {
			t.Errorf("%s: deploy-staging needs %v", provider, needs)
		}
		// The workflow has its own README: the pipeline is documented in CI.md.
		if _, ok := out[readmeFile]; ok || !strings.Contains(string(out[notesFile]), "DEPLOY_SSH_KEY") {
			t.Errorf("%s: docs = %v", provider, keys(out))
		}
		if provider == GitHub {
			if p := jobOf(t, doc, provider, "build")["permissions"].(map[string]any); p["packages"] != "write" {
				t.Errorf("build permissions = %v", p)
			}
		} else if img := jobOf(t, doc, provider, "build")["services"]; !slices.Equal(toStrings(img), []string{imageDockerDind}) {
			t.Errorf("build services = %v", img)
		}
	}
}

func TestComposeWithoutBuilds(t *testing.T) {
	wf := dockerWorkflow()
	wf.Compose.Graph.Nodes = wf.Compose.Graph.Nodes[2:3]
	wf.Compose.Environments, wf.Environments = nil, nil
	out := generate(t, wf, map[string][]byte{}, nil, Options{Provider: GitHub})
	file, doc := pipeline(t, out)
	if _, ok := doc["jobs"].(map[string]any)["build"]; ok {
		t.Error("build job without services that build")
	}
	if !strings.Contains(string(out[file]), "up -d --build") {
		t.Errorf("deploy:\n%s", out[file])
	}
	// No README in the export: one is written.
	if !strings.HasPrefix(string(out[readmeFile]), "# Shop\n") {
		t.Errorf("README:\n%s", out[readmeFile])
	}
}

func TestKubernetes(t *testing.T) {
	wf := kubeWorkflow()
	for _, provider := range []string{GitHub, GitLab} {
		out := generate(t, wf, map[string][]byte{}, nil, Options{Provider: provider, Smoke: true})
		file, doc := pipeline(t, out)
		text := string(out[file])
		for _, want := range []string{"kubectl kustomize overlays/prod", "kubectl apply -k overlays/dev",
			"helm upgrade --install api charts/api --namespace shop -f charts/api/values-common.yaml -f charts/api/values-prod.yaml",
			"kubeconform -ignore-missing-schemas", "kubectl apply -n tools -f extra", "api:dev"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: pipeline lacks %q", provider, want)
			}
		}
		// dev's Helm release has no values-dev.yaml.
		if strings.Contains(text, "values-dev") {
			t.Errorf("%s: dev renders a values file it lacks", provider)
		}
		deploy := jobOf(t, doc, provider, "deploy-prod")
		if provider == GitHub {
			if deploy["environment"] != "prod" || deploy["env"].(map[string]any)["KUBECONFIG_DATA"] != "${{ secrets.KUBECONFIG }}" {
				t.Errorf("deploy-prod = %v", deploy)
			}
		} else if deploy["image"] != imageKube {
			t.Errorf("deploy-prod image = %v", deploy["image"])
		}
	}
}

func TestTerraformVersion(t *testing.T) {
	for constraint, want := range map[string]string{
		"":                    "",
		">= 1.5":              "",
		"~> 1.9":              "",
		"~> 1.9.0":            "1.9",
		"1.5.7":               "1.5.7",
		"= 1.6":               "1.6.0",
		">= 1.5, < 1.8":       "1.7",
		"~> 1.9.0, < 1.9.5":   "1.9.4",
		"~> 1.10.0, >= 1.10":  "1.10",
		"<= 1.8.2":            "1.8.2",
		"not a version, ~> x": "",
	} {
		if got := terraformVersion(constraint); got != want {
			t.Errorf("terraformVersion(%q) = %q, want %q", constraint, got, want)
		}
	}
}

func TestParseProvider(t *testing.T) {
	for in, want := range map[string]string{"": "", "none": "", "github": GitHub, "gitlab": GitLab} {
		if got, err := ParseProvider(in); err != nil || got != want {
			t.Errorf("ParseProvider(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseProvider("jenkins"); err == nil {
		t.Error("jenkins accepted")
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// stepNames lists the names of a GitHub job's steps.
func stepNames(j map[string]any) []string {
	var out []string
	for _, s := range j["steps"].([]any) {
		if n, ok := s.(map[string]any)["name"].(string); ok {
			out = append(out, n)
		}
	}
	return out
}

// Exports with service builds build them before Terraform in every job;
// exports without any never mention them.
func TestTerraformServiceBuilds(t *testing.T) {
	build := "rm -rf services/api/dist\ndocker build --target package --output type=local,dest=services/api/dist services/api"
	single := terraformWorkflow(t, model.LayoutSingle, nil, "dev")
	generated := exportOf(t, single)

	out := generate(t, single, generated, nil, Options{Provider: GitHub, Builds: hclcodec.Builds{Dists: []string{"services/api"}}})
	file, doc := pipeline(t, out)
	for id, before := range map[string]string{"validate": "Validate", "plan-dev": "State backend", "apply-dev": "State backend"} {
		names := stepNames(jobOf(t, doc, GitHub, id))
		if i := slices.Index(names, "Build services"); i < 0 || i > slices.Index(names, before) {
			t.Errorf("%s steps %v: want Build services before %s", id, names, before)
		}
	}
	if text := string(out[file]); !strings.Contains(text, "rm -rf services/api/dist") || !strings.Contains(text, "--output type=local,dest=services/api/dist services/api") {
		t.Errorf("pipeline lacks the build:\n%s", text)
	}
	if readme := string(out[readmeFile]); !strings.Contains(readme, "**Build services**") || !strings.Contains(readme, "`services/api`") {
		t.Errorf("README:\n%s", readme)
	}

	out = generate(t, single, generated, nil, Options{Provider: GitLab, Builds: hclcodec.Builds{Dists: []string{"services/api"}}})
	_, doc = pipeline(t, out)
	for _, id := range []string{"validate", "plan-dev", "apply-dev"} {
		j := jobOf(t, doc, GitLab, id)
		vars, _ := j["variables"].(map[string]any)
		if !slices.Equal(toStrings(j["services"]), []string{imageDockerDind}) || vars["DOCKER_HOST"] != "tcp://docker:2376" || vars["DOCKER_CERT_PATH"] != "/certs/client" ||
			!slices.Contains(toStrings(j["before_script"]), "apk add --no-cache docker-cli docker-cli-buildx") {
			t.Errorf("%s = %v", id, j)
		}
		if !slices.Contains(toStrings(j["script"]), build) {
			t.Errorf("%s script = %v", id, j["script"])
		}
	}

	env := terraformWorkflow(t, model.LayoutEnvironments, nil, "dev", "prod")
	out = generate(t, env, exportOf(t, env), nil, Options{Provider: GitHub, Builds: hclcodec.Builds{Dists: []string{"modules/orders-api/services/api"}}})
	_, doc = pipeline(t, out)
	if names := stepNames(jobOf(t, doc, GitHub, "validate")); !slices.Contains(names, "Build services") {
		t.Errorf("validate steps %v", names)
	}
	if names := stepNames(jobOf(t, doc, GitHub, "plan-dev")); slices.Contains(names, "Build services") {
		t.Errorf("plan-dev builds twice (make plan builds): %v", names)
	}
	if text := string(out[".github/workflows/orders-api.yml"]); !strings.Contains(text, "run: make build") {
		t.Errorf("environments pipeline lacks make build:\n%s", text)
	}

	for _, p := range []string{GitHub, GitLab} {
		for _, wf := range []*model.Workflow{single, env} {
			out := generate(t, wf, exportOf(t, wf), nil, Options{Provider: p})
			for name, data := range out {
				if s := string(data); strings.Contains(s, "docker build") || strings.Contains(s, "Build services") || strings.Contains(s, "dind") || strings.Contains(s, "IMAGE_REGISTRY") {
					t.Errorf("%s %s mentions service builds without any:\n%s", p, name, s)
				}
			}
		}
	}
}

// Container images are built and pushed to IMAGE_REGISTRY before the
// applies, and every plan points the image variables at them; nothing is
// pushed while the registry is unset.
func TestTerraformImages(t *testing.T) {
	wf := terraformWorkflow(t, model.LayoutSingle, nil, "dev")
	images := []hclcodec.ServiceImage{{Name: "orders", Dir: "services/orders", Target: "runtime"}, {Name: "web-ui", Dir: "services/web-ui"}}
	for _, p := range []string{GitHub, GitLab} {
		out := generate(t, wf, exportOf(t, wf), nil, Options{Provider: p, Builds: hclcodec.Builds{Images: images}})
		file, doc := pipeline(t, out)
		sha := map[string]string{GitHub: "$GITHUB_SHA", GitLab: "$CI_COMMIT_SHA"}[p]
		push := jobOf(t, doc, p, "images")
		var script string
		if p == GitHub {
			script = strings.Join(stepRuns(push), "\n")
		} else {
			script = strings.Join(toStrings(push["script"]), "\n")
		}
		for _, s := range []string{"IMAGE_REGISTRY is not set: the images are not pushed", "exit 0",
			`docker login "${IMAGE_REGISTRY%%/*}" -u "$REGISTRY_USERNAME" --password-stdin`,
			`docker build --target runtime -t "$IMAGE_REGISTRY/orders:` + sha + `" services/orders`,
			`docker push "$IMAGE_REGISTRY/orders:` + sha + `"`,
			`docker build -t "$IMAGE_REGISTRY/web-ui:` + sha + `" services/web-ui`} {
			if !strings.Contains(script, s) {
				t.Errorf("%s images job lacks %q:\n%s", p, s, script)
			}
		}
		if needs := toStrings(jobOf(t, doc, p, "apply-dev")["needs"]); !slices.Contains(needs, "images") {
			t.Errorf("%s apply-dev needs %v", p, needs)
		}
		text := string(out[file])
		for _, s := range []string{`export TF_VAR_orders_image="$IMAGE_REGISTRY/orders:` + sha + `"`, `export TF_VAR_web_ui_image="$IMAGE_REGISTRY/web-ui:` + sha + `"`} {
			if strings.Count(text, s) != 2 { // the plan of plan-dev and of apply-dev
				t.Errorf("%s pipeline sets %q %d times:\n%s", p, s, strings.Count(text, s), text)
			}
		}
		if readme := string(out[readmeFile]); !strings.Contains(readme, "| `IMAGE_REGISTRY` |") || !strings.Contains(readme, "**Build and push images**") {
			t.Errorf("%s README:\n%s", p, readme)
		}
		if p == GitLab && !slices.Equal(toStrings(push["services"]), []string{imageDockerDind}) {
			t.Errorf("gitlab images job = %v", push)
		}
	}
}

// stepRuns lists the run scripts of a GitHub job's steps.
func stepRuns(j map[string]any) []string {
	var out []string
	for _, s := range j["steps"].([]any) {
		if r, ok := s.(map[string]any)["run"].(string); ok {
			out = append(out, r)
		}
	}
	return out
}
