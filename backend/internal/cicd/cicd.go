// Package cicd generates the CI/CD pipeline an export can include: a
// GitHub Actions workflow (.github/workflows/<name>.yml) or a GitLab CI
// configuration (.gitlab-ci.yml) that checks, plans/renders and deploys
// the exported files per environment, with the secrets and variables it
// needs listed in its header and in a README section.
//
// The pipeline drives the files exactly as the export writes them: the
// Makefile of the Terraform environments layout, plain terraform commands
// (with workspaces when several environments share one root) for the
// single layout, the compose files and their environment overrides, the
// Kustomize overlays, Helm values files and manifests of a Kubernetes
// workflow. Jobs are described once (job) and rendered per provider.
package cicd

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Providers (Options.Provider).
const (
	GitHub = "github"
	GitLab = "gitlab"
)

// Options select the pipeline of an export.
type Options struct {
	// Provider is GitHub, GitLab or "" (no pipeline).
	Provider string
	// Smoke adds a job after every deploy that checks the environment's
	// SMOKE_URL with HTTP requests.
	Smoke bool
	// Builds are what a Terraform export builds before Terraform runs
	// (hclcodec.DetectBuilds of the exported files): every Terraform job
	// builds the dists first; a build job pushes the images to
	// IMAGE_REGISTRY and the plans point the image variables at them.
	Builds hclcodec.Builds
}

// ParseProvider validates a provider choice: "" and "none" mean no pipeline.
func ParseProvider(s string) (string, error) {
	switch s {
	case "", "none":
		return "", nil
	case GitHub, GitLab:
		return s, nil
	}
	return "", fmt.Errorf("invalid ci %q: use none, %s or %s", s, GitHub, GitLab)
}

// Readme and notes files Files writes the documentation to.
const (
	readmeFile = "README.md"
	notesFile  = "CI.md"
)

// Files renders the pipeline of wf for an export: generated are the files
// the export generates (path -> content; a README.md there is Nimbo's and
// gets the pipeline section appended), own the workflow's files exported
// as they are. The result is merged over generated and replaces own files
// with the same path: the pipeline file, and README.md (the generated one
// with the section, or a new one when the export has none) or CI.md when
// the workflow has its own README.
func Files(wf *model.Workflow, generated map[string][]byte, own map[string]bool, opts Options) (map[string][]byte, error) {
	if opts.Provider == "" {
		return nil, nil
	}
	exported := map[string]bool{}
	for p := range generated {
		exported[p] = true
	}
	for p := range own {
		exported[p] = true
	}
	g := &gen{wf: wf, files: exported, opts: opts, github: opts.Provider == GitHub, settingIdx: map[string]int{}}
	var err error
	switch {
	case wf.IsDocker():
		err = g.compose()
	case wf.IsKubernetes():
		err = g.kubernetes()
	default:
		err = g.terraform()
	}
	if err != nil {
		return nil, err
	}
	heading := providerName(opts.Provider) + " pipeline"
	doc := readmeFile
	_, generatedReadme := generated[readmeFile]
	if !generatedReadme && own[readmeFile] {
		doc = notesFile
	}
	out := map[string][]byte{}
	file := ".gitlab-ci.yml"
	if g.github {
		file = ".github/workflows/" + slug(wf.Name) + ".yml"
		out[file] = encode(g.header(doc, heading), g.githubDoc())
	} else {
		out[file] = encode(g.header(doc, heading), g.gitlabDoc())
	}
	section := g.readme(file)
	switch {
	case generatedReadme:
		out[doc] = []byte(strings.TrimRight(string(generated[readmeFile]), "\n") + "\n\n## " + heading + "\n\n" + section)
	case doc == notesFile:
		out[doc] = []byte("# " + heading + "\n\n" + section)
	default:
		title := strings.TrimSpace(wf.Name)
		if title == "" {
			title = wf.ID
		}
		text := "# " + title + "\n\n"
		if d := strings.TrimSpace(wf.Description); d != "" {
			text += d + "\n\n"
		}
		out[doc] = []byte(text + "## " + heading + "\n\n" + section)
	}
	return out, nil
}

func providerName(p string) string {
	if p == GitHub {
		return "GitHub Actions"
	}
	return "GitLab CI"
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slug is a file or job name made of a-z, 0-9 and '-'.
func slug(s string) string {
	s = strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if s == "" {
		return "nimbo"
	}
	return s
}

// ---------------------------------------------------------------------------
// Jobs

// trigger is when a job runs.
type trigger int

const (
	everyRun trigger = iota // every push and pull/merge request
	review                  // pull/merge requests
	release                 // pushes to the default branch (and manual runs there)
)

// Stages, in pipeline order.
const (
	stageCheck  = "check"
	stagePlan   = "plan"
	stageBuild  = "build"
	stageDeploy = "deploy"
	stageSmoke  = "smoke"
)

var stages = []string{stageCheck, stagePlan, stageBuild, stageDeploy, stageSmoke}

// tool is something a job needs installed or configured before its steps.
type tool int

const (
	toolTerraform tool = iota
	toolMake           // GNU make (the environments layout's Makefile)
	toolAWS            // OIDC role
	toolAzure          // OIDC federated credential
	toolGCP            // workload identity federation
	toolOCI            // API key (environment variables)
	toolKubectl
	toolHelm
	toolKubeconform
	toolCompose      // docker compose CLI
	toolDockerBuild  // a Docker daemon that builds images
	toolRegistryPush // logged in to the registry with push rights
	toolRegistryPull // a token the deploy host pulls with
	toolSSH          // ssh and git
	toolCurl
)

type step struct {
	name, run string
}

// job is one provider-neutral pipeline job.
type job struct {
	id, name string
	stage    string
	on       trigger
	needs    []string
	// env is the environment whose settings the job reads ("" = none).
	env string
	// deploy: the job changes env (GitHub environment with its protection
	// rules; GitLab environment, manual when gated).
	deploy, gated bool
	// verify: the job checks a deployed env (GitLab environment action).
	verify   bool
	tools    []tool
	settings []string
	// vars are fixed environment variables of the job.
	vars      [][2]string
	steps     []step
	artifacts []string
	// artifactName names the artifact (GitHub) / its merge request link (GitLab).
	artifactName string
	// lock serializes runs per environment (GitHub concurrency group,
	// GitLab resource_group).
	lock string
	// smokeURL: the job hands its environment's SMOKE_URL to smokeOf jobs
	// (GitHub: an environment-scoped variable is only readable by a job
	// of that environment, and a second job there would ask for a second
	// approval).
	smokeURL bool
	smokeOf  string
}

func (j *job) uses(t tool) bool { return slices.Contains(j.tools, t) }

// target is one environment the pipeline plans and deploys.
type target struct {
	name string
	id   string // name usable in job ids
}

// targets orders environments for promotion, each deploy waiting for the
// previous one: the others first, then the production-like ones, staging
// (staging, stage, stg, uat, preprod) before production.
func targets(names []string) []target {
	var out []target
	used := map[string]bool{}
	for _, n := range names {
		id := slug(n)
		for base, i := id, 2; used[id]; i++ {
			id = fmt.Sprintf("%s-%d", base, i)
		}
		used[id] = true
		out = append(out, target{name: n, id: id})
	}
	slices.SortStableFunc(out, func(a, b target) int { return promotionRank(a.name) - promotionRank(b.name) })
	return out
}

var stagingName = regexp.MustCompile(`(^|[-_.])(staging|stage|stg|uat|pre-?prod)([-_.]|$)`)

// promotionRank: 0 for development-like environments, 1 for staging, 2
// for production.
func promotionRank(env string) int {
	switch e := strings.ToLower(env); {
	case stagingName.MatchString(e):
		return 1
	case model.ProdLike(e):
		return 2
	}
	return 0
}

// ---------------------------------------------------------------------------
// Settings: the secrets and variables a pipeline reads

type setting struct {
	name   string
	secret bool
	// file: on GitLab a File variable (the job sees a path).
	file bool
	// githubEnv is the job environment variable a GitHub job reads it as
	// ("" = name).
	githubEnv string
	// required: the jobs fail without it (else they skip what needs it).
	required bool
	about    string
}

// gen accumulates one pipeline.
type gen struct {
	wf     *model.Workflow
	files  map[string]bool
	opts   Options
	github bool

	jobs       []job
	settings   []setting
	settingIdx map[string]int
	// globals are environment variables of every job.
	globals [][2]string
	// notes are extra header/README lines.
	notes []string
	// what describes the pipeline in a line ("Terraform", ...).
	what string
	// tfVersion is the Terraform release to install (terraformVersion).
	tfVersion string
	// overview explains the jobs (README).
	overview []string
}

// use registers a setting and returns its name.
func (g *gen) use(s setting) string {
	if i, ok := g.settingIdx[s.name]; ok {
		g.settings[i].required = g.settings[i].required || s.required
		return s.name
	}
	g.settingIdx[s.name] = len(g.settings)
	g.settings = append(g.settings, s)
	return s.name
}

func (g *gen) add(j job) { g.jobs = append(g.jobs, j) }

// ---------------------------------------------------------------------------
// Shell snippets that differ per provider

// warn prints a warning annotation.
func (g *gen) warn(msg string) string {
	if g.github {
		return `echo "::warning::` + msg + `"`
	}
	return `echo "WARNING: ` + msg + `"`
}

// fail prints an error annotation and fails the job.
func (g *gen) fail(msg string) string {
	if g.github {
		return `echo "::error::` + msg + `"; exit 1`
	}
	return `echo "ERROR: ` + msg + `"; exit 1`
}

// sha is the commit being built.
func (g *gen) sha() string {
	if g.github {
		return "$GITHUB_SHA"
	}
	return "$CI_COMMIT_SHA"
}

// summary appends a text file to the run's summary page (GitHub only;
// GitLab links the artifact from the merge request).
func (g *gen) summary(title, file string) string {
	if !g.github {
		return ""
	}
	return "\n{\n  echo \"### " + title + "\"\n  echo \"<details><summary>Show</summary>\"\n  echo\n  echo '```'\n  cat " + shQuote(file) + "\n  echo '```'\n  echo \"</details>\"\n} >> \"$GITHUB_STEP_SUMMARY\""
}

// shQuote quotes s for sh when it holds anything but safe characters.
func shQuote(s string) string {
	if s != "" && safeShell.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var safeShell = regexp.MustCompile(`^[A-Za-z0-9_./@%+=:,-]+$`)

// smokeJob checks env's SMOKE_URL after the deploy job dep.
func (g *gen) smokeJob(t target, dep string) {
	g.use(setting{name: "SMOKE_URL", about: "Space-separated URLs the smoke check requests after each deploy (per environment); unset: the check is skipped"})
	script := `if [ -z "${SMOKE_URL:-}" ]; then
  ` + g.warn("SMOKE_URL is not set for "+t.name+": skipping the smoke check") + `
  exit 0
fi
for url in $SMOKE_URL; do
  echo "GET $url"
  curl --fail --silent --show-error --location --output /dev/null \
    --max-time 10 --retry 12 --retry-delay 5 --retry-all-errors "$url"
done`
	g.add(job{
		id: "smoke-" + t.id, name: "Smoke check " + t.name, stage: stageSmoke, on: release,
		needs: []string{dep}, env: t.name, verify: true, smokeOf: dep, tools: []tool{toolCurl},
		settings: []string{"SMOKE_URL"},
		steps:    []step{{"HTTP check", script}},
	})
}

// smokeNotes documents the smoke check.
func (g *gen) smokeNotes() {
	if !g.opts.Smoke {
		return
	}
	g.overview = append(g.overview, "- **Smoke check** (after each deploy): GET every URL of the environment's `SMOKE_URL` with retries (about a minute); a URL that keeps failing fails the pipeline. Nimbo's load tests need the Nimbo daemon (they drive traffic from your machine and read the sandbox's state), so the full load test stays in the app: this is only a reachability check.")
}

// ---------------------------------------------------------------------------
// Header and README

// settingWhere says where a setting is defined.
func (g *gen) settingWhere(s setting) string {
	kind := "variable"
	if s.secret {
		kind = "secret"
	}
	if !g.github && s.file {
		kind = "File variable"
	}
	if !g.github && s.secret {
		kind = "masked variable"
	}
	return kind
}

// header is the comment block of the pipeline file; doc and heading name
// the documentation section.
func (g *gen) header(doc, heading string) []string {
	lines := []string{
		"Generated by Nimbo from the workflow \"" + oneLine(g.wf.Name) + "\" (" + g.what + "): rewritten",
		"whenever the project is exported with a " + providerName(g.opts.Provider) + " pipeline.",
		"",
	}
	if g.github {
		lines = append(lines,
			"Set these under Settings > Secrets and variables > Actions (repository level),",
			"and override them per environment under Settings > Environments where an",
			"environment uses another account. Deploy jobs run in a GitHub environment named",
			"after the Nimbo environment: add required reviewers there to gate production.",
			"Pull request jobs (plans, renders) read the repository-level values.",
		)
	} else {
		lines = append(lines,
			"Set these under Settings > CI/CD > Variables, scoped to an environment where an",
			"environment uses another account (jobs run with environment:<name>). Deploys of",
			"production-like environments (prod, staging, live, ...) wait for a manual start;",
			"protect the environments to restrict who may run them.",
		)
	}
	lines = append(lines, "")
	for _, s := range g.settings {
		req := "optional"
		if s.required {
			req = "required"
		}
		lines = append(lines, fmt.Sprintf("  %s (%s, %s): %s", s.name, g.settingWhere(s), req, s.about))
	}
	if len(g.settings) == 0 {
		lines = append(lines, "  No secrets or variables are needed.")
	}
	for _, n := range g.notes {
		lines = append(lines, "", n)
	}
	lines = append(lines, "", "What each job does: the \""+heading+"\" section of "+doc+".")
	return lines
}

func (g *gen) readme(file string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "`%s` runs the %s of this project on %s:\n\n", file, g.what, providerName(g.opts.Provider))
	for _, l := range g.overview {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n")
	if g.github {
		b.WriteString("Deploy jobs run in a GitHub environment named after each environment (Settings > Environments): add required reviewers to the production ones so a deploy waits for approval. Pull request jobs read repository-level values; deploy jobs read the environment's own values first.\n\n")
	} else {
		b.WriteString("Deploy jobs run in a GitLab environment named after each environment; production-like ones (prod, staging, live, ...) wait for a manual start. Scope variables to an environment where it uses another account.\n\n")
	}
	if len(g.settings) > 0 {
		b.WriteString("| Name | Kind | Needed | Purpose |\n|------|------|--------|---------|\n")
		for _, s := range g.settings {
			req := "optional"
			if s.required {
				req = "required"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", s.name, g.settingWhere(s), req, strings.ReplaceAll(s.about, "|", `\|`))
		}
		b.WriteString("\n")
	}
	for _, n := range g.notes {
		b.WriteString(n + "\n\n")
	}
	return b.String()
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, `"`, "'")), " ")
}
