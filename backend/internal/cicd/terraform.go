package cicd

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// cloudSettings are the credentials of each cloud tool: OIDC federation
// where the cloud supports it, so no long-lived key is stored in CI.
var cloudSettings = map[tool][]setting{
	toolAWS: {
		{name: "AWS_ROLE_ARN", required: true, about: "IAM role assumed through OIDC (trust the CI provider's OIDC issuer for this repository)"},
		{name: "AWS_REGION", required: true, about: "AWS region of the provider and the S3 state bucket"},
	},
	toolAzure: {
		{name: "AZURE_CLIENT_ID", required: true, about: "Client ID of the app registration / managed identity with a federated credential for this repository"},
		{name: "AZURE_TENANT_ID", required: true, about: "Microsoft Entra tenant ID"},
		{name: "AZURE_SUBSCRIPTION_ID", required: true, about: "Subscription the resources go to"},
	},
	toolGCP: {
		{name: "GCP_WORKLOAD_IDENTITY_PROVIDER", required: true, about: "Workload identity provider (projects/<number>/locations/global/workloadIdentityPools/<pool>/providers/<provider>) trusting this repository"},
		{name: "GCP_SERVICE_ACCOUNT", required: true, about: "Service account the pipeline impersonates"},
	},
	toolOCI: {
		{name: "OCI_TENANCY_OCID", required: true, about: "Tenancy OCID"},
		{name: "OCI_USER_OCID", required: true, about: "OCID of the user owning the API key"},
		{name: "OCI_FINGERPRINT", required: true, about: "Fingerprint of the API key"},
		{name: "OCI_PRIVATE_KEY", secret: true, required: true, about: "API private key (PEM contents); OCI has no OIDC federation for Terraform"},
		{name: "OCI_REGION", required: true, about: "OCI region"},
	},
}

// cloudTools are the credential tools of the workflow's clouds.
func (g *gen) cloudTools() []tool {
	var out []tool
	for _, c := range g.wf.Clouds {
		var t tool
		switch c {
		case model.CloudAWS:
			t = toolAWS
		case model.CloudAzure:
			t = toolAzure
		case model.CloudGCP:
			t = toolGCP
		case model.CloudOCI:
			t = toolOCI
		default:
			continue
		}
		for _, s := range cloudSettings[t] {
			g.use(s)
		}
		out = append(out, t)
	}
	return out
}

// tfTarget is one Terraform environment of the pipeline.
type tfTarget struct {
	target
	// dir is the root module the commands run in.
	dir string
	// varFile is the single layout's variable file of the environment
	// (passed when committed: .gitignore keeps *.tfvars out by default).
	varFile string
	// workspace: the environments of a single root keep their states in
	// one workspace each.
	workspace bool
	// partialBackend: the root's backend.tf leaves the storage to
	// backend.hcl (environments layout default backend).
	partialBackend bool
}

func (g *gen) terraform() error {
	wf := g.wf
	layout, err := hclcodec.ResolveLayout(wf)
	if err != nil {
		return err
	}
	envLayout := layout == model.LayoutEnvironments
	g.what = "Terraform checks, plans and applies"
	g.globals = [][2]string{{"TF_IN_AUTOMATION", "true"}, {"TF_INPUT", "false"}}
	g.tfVersion = terraformVersion(hclcodec.RequiredVersion(wf))
	clouds := g.cloudTools()

	var names []string
	if envLayout {
		names = hclcodec.EnvironmentNames(wf)
	} else {
		for _, e := range wf.Environments {
			names = append(names, e.Name)
		}
		if len(names) == 0 {
			names = []string{"default"}
		}
	}
	var tts []tfTarget
	for _, t := range targets(names) {
		tt := tfTarget{target: t, dir: "."}
		if envLayout {
			tt.dir = hclcodec.EnvironmentsDir + "/" + t.name
			tt.partialBackend = g.files[tt.dir+"/"+hclcodec.BackendExampleFile]
		} else {
			tt.workspace = len(wf.Environments) >= 2
			for _, e := range wf.Environments {
				if e.Name == t.name {
					tt.varFile = "environments/" + e.Name + ".tfvars"
					if e.File != "" {
						tt.varFile = e.File
					}
				}
			}
		}
		tts = append(tts, tt)
	}
	backendExample := !envLayout && g.files[hclcodec.BackendTfExampleFile]
	backendRequired := backendExample
	for _, t := range tts {
		backendRequired = backendRequired || t.partialBackend
	}
	about := "Contents of backend.hcl: the settings of the state storage that backend.tf leaves out (bucket, region, ...; see backend.hcl.example)"
	switch {
	case backendExample:
		about = "Contents of backend.hcl for the remote state of backend.tf.example (bucket, region, ...; see its comments). The pipeline copies backend.tf.example to backend.tf when backend.tf is not committed"
	case !backendRequired:
		about = "Contents of backend.hcl, when the backend block leaves settings to -backend-config"
	}
	g.use(setting{name: "TF_BACKEND_CONFIG", required: backendRequired, about: about})

	var sensitive []string
	for _, v := range wf.Variables {
		for _, a := range v.Body.Attrs {
			if a.Name == "sensitive" && strings.TrimSpace(a.Value.Expr) == "true" {
				sensitive = append(sensitive, g.use(setting{name: "TF_VAR_" + v.Name, secret: true, required: !hasAttr(v.Body, "default"),
					about: "Value of the sensitive variable " + v.Name + " (never written to a variable file)"}))
			}
		}
	}

	tools := []tool{toolTerraform}
	if envLayout {
		tools = append(tools, toolMake)
	}
	// build: the service builds the jobs run before Terraform. In the
	// environments layout `make plan` and `make apply` build first, so
	// only validate (which doesn't run them) needs the step.
	dists, images := g.opts.Builds.Dists, g.opts.Builds.Images
	var build, planBuild []step
	if len(dists) > 0 {
		tools = append(tools, toolDockerBuild)
		if envLayout {
			build = []step{{"Build services", "make build"}}
		} else {
			build = []step{{"Build services", hclcodec.ServiceBuildScript(dists)}}
			planBuild = build
		}
	}
	// imageVars points the image variables at the images the build job
	// (pushJob) pushes, when a registry is set (else the Terraform keeps
	// its defaults).
	imageVars := ""
	var pushJob *job
	if len(images) > 0 {
		g.use(setting{name: "IMAGE_REGISTRY", about: "Registry repository the container images are pushed to (e.g. <account>.dkr.ecr.<region>.amazonaws.com/<project>, <name>.azurecr.io/<project>, <region>-docker.pkg.dev/<project>/<repo>, <region>.ocir.io/<namespace>/<project>); unset: images are neither built for the cloud nor pushed, and the Terraform keeps its nimbo.local defaults"})
		g.use(setting{name: "REGISTRY_USERNAME", about: "User the build job logs in to the registry with (docker login); unset: no login"})
		g.use(setting{name: "REGISTRY_PASSWORD", secret: true, about: "Password or token of REGISTRY_USERNAME"})
		var push, vars strings.Builder
		push.WriteString("if [ -z \"${IMAGE_REGISTRY:-}\" ]; then\n  " + g.warn("IMAGE_REGISTRY is not set: the images are not pushed") + "\n  exit 0\nfi\n")
		push.WriteString("if [ -n \"${REGISTRY_USERNAME:-}\" ]; then\n  printf '%s' \"$REGISTRY_PASSWORD\" | docker login \"${IMAGE_REGISTRY%%/*}\" -u \"$REGISTRY_USERNAME\" --password-stdin\nfi\n")
		vars.WriteString("if [ -n \"${IMAGE_REGISTRY:-}\" ]; then\n")
		for _, img := range images {
			ref := "\"$IMAGE_REGISTRY/" + img.Name + ":" + g.sha() + "\""
			push.WriteString(hclcodec.ImageBuildCommand(img, ref) + "\ndocker push " + ref + "\n")
			vars.WriteString("  export TF_VAR_" + hclcodec.ImageVariable(img.Name) + "=" + ref + "\n")
		}
		vars.WriteString("fi\n")
		imageVars = vars.String()
		pushJob = &job{id: "images", name: "Build and push images", stage: stageBuild, on: release, needs: []string{"validate"},
			tools: []tool{toolDockerBuild}, settings: []string{"IMAGE_REGISTRY", "REGISTRY_USERNAME", "REGISTRY_PASSWORD"},
			steps: []step{{"Build and push", strings.TrimSuffix(push.String(), "\n")}}}
	}
	validate := append([]step{{"Format", "terraform fmt -check -recursive -diff"}}, build...)
	if envLayout {
		var b strings.Builder
		for _, t := range tts {
			b.WriteString("make validate ENV=" + shQuote(t.name) + "\n")
		}
		validate = append(validate, step{"Validate", strings.TrimSuffix(b.String(), "\n")})
	} else {
		validate = append(validate, step{"Validate", "terraform init -backend=false -input=false\nterraform validate"})
	}
	g.add(job{id: "validate", name: "Format and validate", stage: stageCheck, on: everyRun, tools: tools, steps: validate})
	if pushJob != nil {
		g.add(*pushJob)
	}

	settings := append([]string{"TF_BACKEND_CONFIG"}, sensitive...)
	if len(images) > 0 {
		settings = append(settings, "IMAGE_REGISTRY")
	}
	runTools := append(append([]tool(nil), tools...), clouds...)
	prev := ""
	for _, t := range tts {
		planFile := "plan-" + t.id + ".txt"
		plan := append(slices.Clone(planBuild), g.tfBackend(t, false), g.tfInit(t))
		plan = append(plan, step{"Plan", imageVars + g.tfPlan(t) + "\n" + g.tfShow(t, planFile) + g.summary("Terraform plan: "+t.name, planFile)})
		g.add(job{
			id: "plan-" + t.id, name: "Plan " + t.name, stage: stagePlan, on: review, needs: []string{"validate"},
			env: t.name, tools: runTools, settings: settings, steps: plan,
			artifacts: []string{planFile}, artifactName: "terraform-plan-" + t.id,
		})
		needs := []string{"validate"}
		if len(images) > 0 {
			needs = append(needs, "images")
		}
		if prev != "" {
			needs = append(needs, prev)
		}
		apply := append(slices.Clone(planBuild), g.tfBackend(t, true), g.tfInit(t), step{"Plan", imageVars + g.tfPlan(t)}, step{"Apply", g.tfApply(t)})
		id := "apply-" + t.id
		g.add(job{
			id: id, name: "Apply " + t.name, stage: stageDeploy, on: release, needs: needs,
			env: t.name, deploy: true, gated: model.ProdLike(t.name), lock: "terraform-" + t.id,
			tools: runTools, settings: settings, steps: apply, smokeURL: g.opts.Smoke,
		})
		if g.opts.Smoke {
			g.smokeJob(t.target, id)
		}
		prev = id
	}

	envs := make([]string, len(tts))
	for i, t := range tts {
		envs[i] = "`" + t.name + "`"
	}
	g.overview = append(g.overview, "- **Format and validate** (every push and pull request): `terraform fmt -check -recursive`, then `terraform validate` of every root without touching the state.")
	if len(dists) > 0 {
		how := ""
		if envLayout {
			how = ": `make build`, which `make plan` and `make apply` run first"
		}
		g.overview = append(g.overview, "- **Build services** (before Terraform in every job"+how+"): `docker build --target package --output type=local,dest=<service>/dist <service>` "+
			"exports the `package` stage of each service's Dockerfile ("+strings.Join(quoteAll(dists), ", ")+") into the `dist/` folder the Terraform packages, so a source change that doesn't build fails the pipeline before any plan.")
		g.notes = append(g.notes, g.ifGitHub("Service builds use the Docker Engine and Buildx of the GitHub-hosted runner (self-hosted runners need both).",
			"Service builds run in the Terraform jobs through a docker:dind service (the docker CLI and Buildx are installed from Alpine): the runner must allow privileged containers."))
	}
	if len(images) > 0 {
		var names []string
		for _, img := range images {
			names = append(names, "`"+img.Name+"` ("+img.Dir+", variable `"+hclcodec.ImageVariable(img.Name)+"`)")
		}
		g.overview = append(g.overview, "- **Build and push images** (pushes to the default branch, before the applies): builds each container image the Terraform runs ("+strings.Join(names, ", ")+
			") and pushes it as `$IMAGE_REGISTRY/<image>:<commit>`; every plan then sets `TF_VAR_<variable>` to that reference. Without `IMAGE_REGISTRY` nothing is pushed and the Terraform keeps its `nimbo.local/<image>:latest` defaults, which only the Nimbo sandbox can run.")
		g.notes = append(g.notes, "Container images: set IMAGE_REGISTRY to a registry your cloud pulls from (ECR, ACR, Artifact Registry, OCIR) and REGISTRY_USERNAME/REGISTRY_PASSWORD to credentials that may push; registries with short-lived tokens (ECR, Artifact Registry) need a token refreshed by your pipeline, e.g. aws ecr get-login-password.")
	}
	if envLayout {
		g.overview = append(g.overview,
			"- **Plan** (pull requests, one job per environment: "+strings.Join(envs, ", ")+"): `make init plan ENV=<env>`; the plan is attached to the run as an artifact"+g.ifGitHub(" and shown on its summary page", " linked from the merge request")+".",
			"- **Apply** (pushes to the default branch): `make init plan apply ENV=<env>`, one environment after the other, production-like ones last. Each environment root has its own state (`environments/<env>/backend.tf`).")
	} else {
		ws := ""
		if len(tts) > 1 {
			ws = " Every environment has its own Terraform workspace (`terraform workspace select -or-create`), so their states stay apart in the one backend, and its variable file (`-var-file`) when committed (.gitignore leaves `*.tfvars` out: `git add -f` the ones without secrets)."
		}
		g.overview = append(g.overview,
			"- **Plan** (pull requests, one job per environment: "+strings.Join(envs, ", ")+"): `terraform init`, `terraform plan`; the plan is attached to the run as an artifact"+g.ifGitHub(" and shown on its summary page", " linked from the merge request")+"."+ws,
			"- **Apply** (pushes to the default branch): plan and apply, one environment after the other, production-like ones last.")
	}
	g.smokeNotes()
	if !backendRequired && !hclcodec.HasBackend(wf) {
		g.notes = append(g.notes, "State: the workflow has no remote state backend, so every pipeline run starts from an empty local state. Add a backend block before applying from CI.")
	}
	if len(clouds) > 0 {
		g.notes = append(g.notes, "Credentials come from OIDC federation (AWS, Azure, Google Cloud) or API key variables (OCI): plan jobs need read access, apply jobs the rights to change the resources.")
	}
	if envLayout && !g.anyFile("/terraform.tfvars") {
		g.notes = append(g.notes, "Environment values: the export has no environments/<env>/terraform.tfvars. Export with environment values, or set TF_VAR_<name> for every input without a default.")
	}
	return nil
}

func (g *gen) ifGitHub(github, gitlab string) string {
	if g.github {
		return github
	}
	return gitlab
}

func (g *gen) anyFile(suffix string) bool {
	for f := range g.files {
		if strings.HasSuffix(f, suffix) {
			return true
		}
	}
	return false
}

func hasAttr(b model.Body, name string) bool {
	for _, a := range b.Attrs {
		if a.Name == name {
			return true
		}
	}
	return false
}

// tfBackend writes backend.hcl from TF_BACKEND_CONFIG. Without it a
// partial backend cannot initialize, and an apply without a remote state
// would forget what it created: both fail with what to set.
func (g *gen) tfBackend(t tfTarget, apply bool) step {
	hcl := "backend.hcl"
	if t.dir != "." {
		hcl = t.dir + "/backend.hcl"
	}
	var b strings.Builder
	b.WriteString("if [ -n \"${TF_BACKEND_CONFIG:-}\" ]; then\n")
	b.WriteString("  printf '%s\\n' \"$TF_BACKEND_CONFIG\" > " + shQuote(hcl) + "\n")
	single := t.dir == "." && g.files[hclcodec.BackendTfExampleFile]
	if single {
		b.WriteString("  [ -f backend.tf ] || cp backend.tf.example backend.tf\n")
	}
	switch {
	case t.partialBackend:
		b.WriteString("elif [ ! -f " + shQuote(hcl) + " ]; then\n")
		b.WriteString("  " + g.fail("Set TF_BACKEND_CONFIG (the contents of backend.hcl) for "+t.name+": its backend.tf leaves the state storage to it") + "\n")
	case single && apply:
		b.WriteString("elif [ ! -f backend.tf ]; then\n")
		b.WriteString("  " + g.fail("Set TF_BACKEND_CONFIG (or commit backend.tf): without a remote state the apply would forget what it created") + "\n")
	case single:
		b.WriteString("elif [ ! -f backend.tf ]; then\n")
		b.WriteString("  " + g.warn("No remote state (TF_BACKEND_CONFIG is not set): planning against an empty local state") + "\n")
	}
	b.WriteString("fi")
	return step{"State backend", b.String()}
}

func (g *gen) tfInit(t tfTarget) step {
	if t.dir != "." {
		return step{"Init", "make init ENV=" + shQuote(t.name)}
	}
	s := "if [ -f backend.hcl ]; then\n  terraform init -input=false -backend-config=backend.hcl\nelse\n  terraform init -input=false\nfi"
	if t.workspace {
		s += "\nterraform workspace select -or-create=true " + shQuote(t.name)
	}
	return step{"Init", s}
}

func (g *gen) tfPlan(t tfTarget) string {
	if t.dir != "." {
		return "make plan ENV=" + shQuote(t.name)
	}
	if t.varFile == "" {
		return "terraform plan -input=false -out=tfplan"
	}
	f := shQuote(t.varFile)
	return "if [ -f " + f + " ]; then\n  terraform plan -input=false -out=tfplan -var-file=" + f + "\nelse\n  terraform plan -input=false -out=tfplan\nfi"
}

func (g *gen) tfShow(t tfTarget, out string) string {
	if t.dir != "." {
		return "terraform -chdir=" + shQuote(t.dir) + " show -no-color terraform.tfplan > " + shQuote(out)
	}
	return "terraform show -no-color tfplan > " + shQuote(out)
}

func (g *gen) tfApply(t tfTarget) string {
	if t.dir != "." {
		return "make apply ENV=" + shQuote(t.name)
	}
	return "terraform apply -input=false tfplan"
}

var constraintRe = regexp.MustCompile(`^\s*(~>|>=|<=|!=|=|>|<)?\s*v?(\d+(?:\.\d+){0,2})\s*$`)

// terraformVersion picks the Terraform release a required_version
// constraint allows, as a setup-terraform version and a hashicorp/terraform
// image tag: "" (the latest; also without a constraint) when only lower
// bounds or a major-version pin (~> 1.9) apply, "X.Y" (its latest patch)
// for ~> X.Y.Z or an upper bound below a minor, "X.Y.Z" for an exact pin.
func terraformVersion(constraint string) string {
	best := ""
	var bestNum []int
	// A minor ("1.9") stands for its latest patch: above every 1.9.Z.
	consider := func(v string) {
		n := versionNums(v)
		if strings.Count(v, ".") == 1 {
			n[2] = 1 << 30
		}
		if best == "" || compareNums(n, bestNum) < 0 {
			best, bestNum = v, n
		}
	}
	for _, part := range strings.Split(constraint, ",") {
		m := constraintRe.FindStringSubmatch(part)
		if m == nil {
			continue
		}
		op, v := m[1], m[2]
		n := versionNums(v)
		patch := func(z int) string { return strconv.Itoa(n[0]) + "." + strconv.Itoa(n[1]) + "." + strconv.Itoa(z) }
		minor := func(y int) string { return strconv.Itoa(n[0]) + "." + strconv.Itoa(y) }
		switch op {
		case "", "=":
			return patch(n[2])
		case "~>":
			if strings.Count(v, ".") == 2 {
				consider(minor(n[1]))
			}
		case "<=":
			consider(patch(n[2]))
		case "<":
			switch {
			case n[2] > 0:
				consider(patch(n[2] - 1))
			case n[1] > 0:
				consider(minor(n[1] - 1))
			}
		}
	}
	return best
}

func versionNums(v string) []int {
	out := []int{0, 0, 0}
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

func compareNums(a, b []int) int {
	for i := range a {
		if a[i] != b[i] {
			return a[i] - b[i]
		}
	}
	return 0
}
