package cicd

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// imagesOverride is the compose file the pipeline writes next to the
// project's: it names the images of the services that build, so the build
// job pushes them and the deploy host pulls them instead of building.
const imagesOverride = ".ci-images.compose.yaml"

// composeTarget is one environment of a docker workflow (name "default"
// with an empty env: the base files alone).
type composeTarget struct {
	target
	env model.ComposeEnvironment
}

func (g *gen) compose() error {
	c := g.wf.Compose
	if c == nil || len(c.Files) == 0 {
		return fmt.Errorf("the docker workflow has no compose file")
	}
	g.what = "Docker Compose checks, image builds and deploys"
	byName := map[string]model.ComposeEnvironment{}
	var names []string
	for _, e := range c.Environments {
		byName[e.Name] = e
		names = append(names, e.Name)
	}
	if len(names) == 0 {
		names = []string{"default"}
	}
	var cts []composeTarget
	for _, t := range targets(names) {
		cts = append(cts, composeTarget{target: t, env: byName[t.name]})
	}

	// Services building an image, declared in the base files (a service
	// only an override declares has no image to build for the others).
	overrides := map[string]bool{}
	for _, e := range c.Environments {
		if e.File != "" {
			overrides[e.File] = true
		}
	}
	var built []string
	for _, n := range c.Graph.Nodes {
		if n.Kind == model.ComposeService && n.Service != nil && n.Service.Build != nil && !overrides[n.File] && !slices.Contains(built, n.Name) {
			built = append(built, n.Name)
		}
	}
	slices.Sort(built)

	var validate strings.Builder
	for i, t := range cts {
		if i > 0 {
			validate.WriteString("\n")
		}
		validate.WriteString("echo " + shQuote("== "+t.name) + "\n")
		validate.WriteString(g.composeArgs(t, nil, false) + "\n")
		validate.WriteString("docker compose \"$@\" config --quiet\n")
	}
	g.add(job{id: "validate", name: "Validate compose files", stage: stageCheck, on: everyRun, tools: []tool{toolCompose},
		steps: []step{{"docker compose config", strings.TrimSuffix(validate.String(), "\n")}}})

	deployNeeds := []string{"validate"}
	if len(built) > 0 {
		base := g.composeArgs(composeTarget{}, []string{imagesOverride}, false)
		build := g.imageEnv() + "\n" + g.imagesFile(built) + "\n" + base + "\ndocker compose \"$@\" build " + shWords(built)
		push := "if " + g.onDefaultBranch() + "; then\n  docker compose \"$@\" push " + shWords(built) + "\nelse\n  echo \"Not the default branch: images are built, not pushed\"\nfi"
		j := job{id: "build", name: "Build images", stage: stageBuild, on: everyRun, needs: []string{"validate"},
			tools: []tool{toolCompose, toolDockerBuild, toolRegistryPush}, steps: []step{{"Build and push", build + "\n" + push}}}
		if g.github {
			j.vars = append(j.vars, [2]string{"DEFAULT_BRANCH", "${{ github.event.repository.default_branch }}"})
		}
		g.add(j)
		deployNeeds = []string{"build"}
	}

	g.use(setting{name: "DEPLOY_HOST", secret: true, about: "SSH destination of the deploy (user@host) with Docker installed; unset: the deploy is skipped"})
	g.use(setting{name: "DEPLOY_SSH_KEY", secret: true, about: "Private SSH key the deploy logs in with"})
	g.use(setting{name: "DEPLOY_KNOWN_HOSTS", about: "known_hosts line(s) of the host (ssh-keyscan); unset: the key is trusted on first use"})
	g.use(setting{name: "DEPLOY_PATH", about: "Project directory on the host (default " + slug(g.wf.Name) + ", relative to the SSH user's home)"})
	settings := []string{"DEPLOY_HOST", "DEPLOY_SSH_KEY", "DEPLOY_KNOWN_HOSTS", "DEPLOY_PATH"}
	prev := ""
	for _, t := range cts {
		needs := slices.Clone(deployNeeds)
		if prev != "" {
			needs = append(needs, prev)
		}
		tools := []tool{toolSSH}
		if len(built) > 0 {
			tools = append(tools, toolRegistryPull)
		}
		id := "deploy-" + t.id
		g.add(job{
			id: id, name: "Deploy " + t.name, stage: stageDeploy, on: release, needs: needs,
			env: t.name, deploy: true, gated: model.ProdLike(t.name), lock: "deploy-" + t.id,
			tools: tools, settings: settings, smokeURL: g.opts.Smoke,
			steps: []step{{"Deploy over SSH", g.composeDeploy(t, built)}},
		})
		if g.opts.Smoke {
			g.smokeJob(t.target, id)
		}
		prev = id
	}

	envs := make([]string, len(cts))
	for i, t := range cts {
		envs[i] = "`" + t.name + "`"
	}
	g.overview = append(g.overview, "- **Validate** (every push and pull request): `docker compose config` of the base files and of every environment ("+strings.Join(envs, ", ")+") with its override and env file.")
	if len(built) > 0 {
		registry := g.ifGitHub("GitHub Container Registry (`ghcr.io/<owner>/<repo>/<service>`)", "project's GitLab container registry (`$CI_REGISTRY_IMAGE/<service>`)")
		g.overview = append(g.overview, "- **Build images** (every push and pull request): `docker compose build` of the services that build ("+strings.Join(quoteAll(built), ", ")+"), tagged with the commit SHA through `"+imagesOverride+"`; pushed to the "+registry+" from the default branch only.")
	}
	g.overview = append(g.overview, "- **Deploy** (pushes to the default branch, one environment after the other, production-like ones last): copies the commit (`git archive`) to `DEPLOY_PATH` on `DEPLOY_HOST` over SSH and runs `docker compose -f <files> -f compose.<env>.yaml up -d` there"+g.ifThen(len(built) > 0, " with the pushed images (`pull`, then `up --no-build`)")+". Skipped while `DEPLOY_HOST` is unset. Env files kept out of git (secrets) must already be on the host, next to the compose files.")
	g.smokeNotes()
	return nil
}

func (g *gen) ifThen(cond bool, s string) string {
	if cond {
		return s
	}
	return ""
}

func quoteAll(items []string) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = "`" + s + "`"
	}
	return out
}

// shWords quotes each word for sh.
func shWords(items []string) string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = shQuote(s)
	}
	return strings.Join(out, " ")
}

// composeArgs sets the positional parameters to the -f/--env-file
// arguments of t (the zero target: the base files), extra files last.
// The environment's env file is read after .env when they exist (git may
// leave secret env files out). esc escapes $ for an unquoted heredoc.
func (g *gen) composeArgs(t composeTarget, extra []string, esc bool) string {
	c := g.wf.Compose
	at := "\"$@\""
	if esc {
		at = "\"\\$@\""
	}
	var files []string
	for _, f := range c.Files {
		files = append(files, "-f", shQuote(f))
	}
	if t.env.File != "" {
		files = append(files, "-f", shQuote(t.env.File))
	}
	for _, f := range extra {
		files = append(files, "-f", shQuote(f))
	}
	s := "set -- " + strings.Join(files, " ")
	if t.env.EnvFile != "" {
		dotenv := shQuote(path.Join(path.Dir(c.Files[0]), ".env"))
		ef := shQuote(t.env.EnvFile)
		s += "\nif [ -f " + ef + " ]; then\n  if [ -f " + dotenv + " ]; then set -- " + at + " --env-file " + dotenv + "; fi\n  set -- " + at + " --env-file " + ef + "\nfi"
	}
	return s
}

// imageEnv exports IMAGE_PREFIX (the registry repository images go under)
// and IMAGE_TAG (the commit).
func (g *gen) imageEnv() string {
	if g.github {
		return "IMAGE_PREFIX=\"ghcr.io/$(printf '%s' \"$GITHUB_REPOSITORY\" | tr '[:upper:]' '[:lower:]')\"\nexport IMAGE_PREFIX IMAGE_TAG=\"" + g.sha() + "\""
	}
	return "export IMAGE_PREFIX=\"$CI_REGISTRY_IMAGE\" IMAGE_TAG=\"" + g.sha() + "\""
}

// imagesFile writes imagesOverride; compose interpolates the variables.
func (g *gen) imagesFile(built []string) string {
	var b strings.Builder
	b.WriteString("cat > " + imagesOverride + " <<'EOF'\nservices:\n")
	for _, s := range built {
		fmt.Fprintf(&b, "  %s:\n    image: ${IMAGE_PREFIX}/%s:${IMAGE_TAG}\n", s, strings.ToLower(s))
	}
	b.WriteString("EOF")
	return b.String()
}

// onDefaultBranch is a sh condition: the run builds the default branch.
func (g *gen) onDefaultBranch() string {
	if g.github {
		return "[ \"$GITHUB_EVENT_NAME\" != pull_request ] && [ \"$GITHUB_REF_NAME\" = \"$DEFAULT_BRANCH\" ]"
	}
	return "[ \"${CI_COMMIT_BRANCH:-}\" = \"$CI_DEFAULT_BRANCH\" ]"
}

func (g *gen) composeDeploy(t composeTarget, built []string) string {
	var b strings.Builder
	b.WriteString("if [ -z \"${DEPLOY_HOST:-}\" ] || [ -z \"${DEPLOY_SSH_KEY:-}\" ]; then\n")
	b.WriteString("  " + g.warn("DEPLOY_HOST and DEPLOY_SSH_KEY are not set for "+t.name+": skipping the deploy") + "\n  exit 0\nfi\n")
	b.WriteString(`mkdir -p ~/.ssh && chmod 700 ~/.ssh
printf '%s\n' "$DEPLOY_SSH_KEY" > ~/.ssh/deploy_key && chmod 600 ~/.ssh/deploy_key
check=accept-new
if [ -n "${DEPLOY_KNOWN_HOSTS:-}" ]; then
  printf '%s\n' "$DEPLOY_KNOWN_HOSTS" >> ~/.ssh/known_hosts
  check=yes
fi
remote() { ssh -i ~/.ssh/deploy_key -o StrictHostKeyChecking="$check" "$DEPLOY_HOST" "$@"; }
`)
	b.WriteString("dir=\"${DEPLOY_PATH:-" + slug(g.wf.Name) + "}\"\n")
	b.WriteString("git archive --format=tar HEAD | remote \"mkdir -p '$dir' && tar -xf - -C '$dir'\"\n")
	if len(built) > 0 {
		b.WriteString(g.imageEnv() + "\n" + g.imagesFile(built) + "\n")
		b.WriteString("remote \"cat > '$dir/" + imagesOverride + "'\" < " + imagesOverride + "\n")
		b.WriteString("printf '%s\\n' \"$REGISTRY_PASSWORD\" | remote \"docker login '$REGISTRY' -u '$REGISTRY_USER' --password-stdin\"\n")
	}
	var extra []string
	if len(built) > 0 {
		extra = []string{imagesOverride}
	}
	b.WriteString("remote sh -s <<EOF\nset -eu\ncd '$dir'\n")
	if len(built) > 0 {
		b.WriteString("export IMAGE_PREFIX='$IMAGE_PREFIX' IMAGE_TAG='$IMAGE_TAG'\n")
	}
	b.WriteString(g.composeArgs(t, extra, true) + "\n")
	if len(built) > 0 {
		b.WriteString("docker compose \"\\$@\" pull --quiet\ndocker compose \"\\$@\" up -d --no-build --remove-orphans\n")
	} else {
		b.WriteString("docker compose \"\\$@\" up -d --build --remove-orphans\n")
	}
	b.WriteString("EOF")
	return b.String()
}
