package cicd

import (
	"strings"

	"go.yaml.in/yaml/v4"
)

// Actions the GitHub jobs use, by major version.
const (
	actionCheckout    = "actions/checkout@v7"
	actionUpload      = "actions/upload-artifact@v7"
	actionTerraform   = "hashicorp/setup-terraform@v4"
	actionAWS         = "aws-actions/configure-aws-credentials@v6"
	actionAzure       = "azure/login@v3"
	actionGCP         = "google-github-actions/auth@v3"
	actionKubectl     = "azure/setup-kubectl@v5"
	actionHelm        = "azure/setup-helm@v5"
	actionDockerLogin = "docker/login-action@v4"

	kubeconformVersion = "v0.8.0"
)

const (
	ghReview  = "github.event_name == 'pull_request'"
	ghRelease = "github.event_name != 'pull_request' && github.ref_name == github.event.repository.default_branch"
)

// ghExpr is the expression reading a setting.
func (g *gen) ghExpr(name string) string {
	if i, ok := g.settingIdx[name]; ok && g.settings[i].secret {
		return "${{ secrets." + name + " }}"
	}
	return "${{ vars." + name + " }}"
}

func (g *gen) githubDoc() *yaml.Node {
	name := strings.TrimSpace(g.wf.Name)
	if name == "" {
		name = g.wf.ID
	}
	doc := mapping(
		"name", name,
		"on", mapping("push", mapping(), "pull_request", mapping(), "workflow_dispatch", mapping()),
		"permissions", mapping("contents", "read"),
	)
	if len(g.globals) > 0 {
		env := mapping()
		for _, kv := range g.globals {
			set(env, kv[0], kv[1])
		}
		set(doc, "env", env)
	}
	jobs := mapping()
	for _, j := range g.jobs {
		set(jobs, j.id, g.githubJob(j))
	}
	set(doc, "jobs", jobs)
	return doc
}

func (g *gen) githubJob(j job) *yaml.Node {
	m := mapping("name", j.name)
	switch j.on {
	case review:
		set(m, "if", ghReview)
	case release:
		set(m, "if", ghRelease)
	}
	set(m, "needs", j.needs)
	set(m, "runs-on", "ubuntu-latest")
	if j.deploy {
		set(m, "environment", j.env)
	}
	perms := mapping("contents", "read")
	if j.uses(toolAWS) || j.uses(toolAzure) || j.uses(toolGCP) {
		set(perms, "id-token", "write")
	}
	switch {
	case j.uses(toolRegistryPush):
		set(perms, "packages", "write")
	case j.uses(toolRegistryPull):
		set(perms, "packages", "read")
	}
	if len(perms.Content) > 2 {
		set(m, "permissions", perms)
	}
	if j.lock != "" {
		set(m, "concurrency", mapping("group", slug(g.wf.Name)+"-"+j.lock, "cancel-in-progress", false))
	}
	if j.smokeURL {
		set(m, "outputs", mapping("smoke_url", "${{ steps.smoke-url.outputs.url }}"))
	}

	env := mapping()
	for _, kv := range j.vars {
		set(env, kv[0], kv[1])
	}
	for _, name := range j.settings {
		key := name
		if s := g.settings[g.settingIdx[name]]; s.githubEnv != "" {
			key = s.githubEnv
		}
		if name == "SMOKE_URL" && j.smokeOf != "" {
			set(env, key, "${{ needs."+j.smokeOf+".outputs.smoke_url }}")
			continue
		}
		set(env, key, g.ghExpr(name))
	}
	if j.uses(toolAzure) {
		set(env, "ARM_USE_OIDC", "true")
		set(env, "ARM_CLIENT_ID", g.ghExpr("AZURE_CLIENT_ID"))
		set(env, "ARM_TENANT_ID", g.ghExpr("AZURE_TENANT_ID"))
		set(env, "ARM_SUBSCRIPTION_ID", g.ghExpr("AZURE_SUBSCRIPTION_ID"))
	}
	if j.uses(toolOCI) {
		for _, s := range cloudSettings[toolOCI] {
			set(env, s.name, g.ghExpr(s.name))
		}
	}
	if j.uses(toolRegistryPull) {
		set(env, "REGISTRY", "ghcr.io")
		set(env, "REGISTRY_USER", "${{ github.actor }}")
		set(env, "REGISTRY_PASSWORD", "${{ secrets.GITHUB_TOKEN }}")
	}
	if len(env.Content) > 0 {
		set(m, "env", env)
	}

	var steps []*yaml.Node
	if j.smokeOf == "" { // the smoke check only requests URLs
		steps = append(steps, mapping("uses", actionCheckout))
	}
	uses := func(name, action string, with ...any) {
		s := mapping("name", name, "uses", action)
		if len(with) > 0 {
			set(s, "with", mapping(with...))
		}
		steps = append(steps, s)
	}
	for _, t := range j.tools {
		switch t {
		case toolTerraform:
			v := g.tfVersion
			if v == "" {
				v = "latest"
			}
			uses("Set up Terraform", actionTerraform, "terraform_version", v, "terraform_wrapper", false)
		case toolAWS:
			uses("AWS credentials (OIDC)", actionAWS, "role-to-assume", g.ghExpr("AWS_ROLE_ARN"), "aws-region", g.ghExpr("AWS_REGION"))
		case toolAzure:
			uses("Azure login (OIDC)", actionAzure, "client-id", g.ghExpr("AZURE_CLIENT_ID"), "tenant-id", g.ghExpr("AZURE_TENANT_ID"), "subscription-id", g.ghExpr("AZURE_SUBSCRIPTION_ID"))
		case toolGCP:
			uses("Google Cloud credentials (workload identity)", actionGCP, "workload_identity_provider", g.ghExpr("GCP_WORKLOAD_IDENTITY_PROVIDER"), "service_account", g.ghExpr("GCP_SERVICE_ACCOUNT"))
		case toolKubectl:
			uses("Set up kubectl", actionKubectl)
		case toolHelm:
			uses("Set up Helm", actionHelm)
		case toolKubeconform:
			steps = append(steps, mapping("name", "Install kubeconform", "run", kubeconformInstall("$RUNNER_TEMP/bin")+"\necho \"$RUNNER_TEMP/bin\" >> \"$GITHUB_PATH\""))
		case toolRegistryPush:
			uses("Log in to GitHub Container Registry", actionDockerLogin, "registry", "ghcr.io", "username", "${{ github.actor }}", "password", "${{ secrets.GITHUB_TOKEN }}")
		}
	}
	if j.smokeURL {
		steps = append(steps, mapping("name", "Smoke check URL", "id", "smoke-url",
			"env", mapping("SMOKE_URL", "${{ vars.SMOKE_URL }}"),
			"run", `echo "url=${SMOKE_URL:-}" >> "$GITHUB_OUTPUT"`))
	}
	for _, s := range j.steps {
		steps = append(steps, mapping("name", s.name, "run", s.run))
	}
	if len(j.artifacts) > 0 {
		steps = append(steps, mapping("name", "Upload "+j.artifactName, "uses", actionUpload,
			"with", mapping("name", j.artifactName, "path", strings.Join(j.artifacts, "\n"), "if-no-files-found", "warn")))
	}
	set(m, "steps", seq(steps...))
	return m
}

// kubeconformInstall downloads kubeconform into dir.
func kubeconformInstall(dir string) string {
	return "mkdir -p \"" + dir + "\"\ncurl -sSfL https://github.com/yannh/kubeconform/releases/download/" + kubeconformVersion +
		"/kubeconform-linux-amd64.tar.gz | tar -xz -C \"" + dir + "\" kubeconform"
}
