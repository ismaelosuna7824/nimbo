package learn

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// filenameRE is YYYYMMDDHHMMSS_<provider>_<slug>.yaml.
var filenameRE = regexp.MustCompile(`^(\d{14})_([a-z]+)_([a-z0-9]+(?:[-_][a-z0-9]+)*)\.yaml$`)

// idRE is a lower-case kebab slug.
var idRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// tagRE is a lower-case tag: words joined by "-", "_" or ".".
var tagRE = regexp.MustCompile(`^[a-z0-9]+(?:[-._][a-z0-9]+)*$`)

// parseFilename checks a migration file name and returns its provider.
func parseFilename(name string) (string, error) {
	m := filenameRE.FindStringSubmatch(name)
	if m == nil {
		return "", fmt.Errorf("file name must be YYYYMMDDHHMMSS_<provider>_<slug>.yaml (lower-case slug)")
	}
	if _, err := time.Parse("20060102150405", m[1]); err != nil {
		return "", fmt.Errorf("file name timestamp %s is not a valid UTC date-time", m[1])
	}
	if !slices.Contains(model.LearnProviders, m[2]) {
		return "", fmt.Errorf("file name provider %q must be one of %s", m[2], strings.Join(model.LearnProviders, ", "))
	}
	return m[2], nil
}

// validateID checks an operation id: "<provider>-<slug>" for challenges,
// "<provider>-roadmap" for roadmaps, provider = the file's provider.
func validateID(kind, id, provider string) error {
	if !idRE.MatchString(id) {
		return fmt.Errorf("id %q must be a lower-case kebab-case slug", id)
	}
	if kind == KindRoadmap {
		if id != provider+"-roadmap" {
			return fmt.Errorf("roadmap id must be %q (one roadmap per provider; the provider comes from the file name)", provider+"-roadmap")
		}
		return nil
	}
	if !strings.HasPrefix(id, provider+"-") {
		return fmt.Errorf("challenge id must start with %q (the provider in the file name)", provider+"-")
	}
	if id == provider+"-roadmap" {
		return fmt.Errorf("challenge id %q is reserved for the roadmap", id)
	}
	return nil
}

// officialHosts are the documentation sites accepted in sources and
// docs: the vendors' and projects' own docs (see CONTRIBUTING-learn.md,
// Sourcing). A host matches itself and its subdomains.
var officialHosts = []string{
	// AWS
	"docs.aws.amazon.com", "aws.amazon.com", "skillbuilder.aws", "d1.awsstatic.com",
	// Microsoft Azure
	"learn.microsoft.com", "azure.microsoft.com",
	// Google Cloud
	"cloud.google.com", "docs.cloud.google.com", "services.google.com", "sre.google",
	// Oracle Cloud
	"docs.oracle.com", "www.oracle.com", "education.oracle.com", "mylearn.oracle.com", "learn.oracle.com",
	// Docker
	"docs.docker.com", "www.docker.com", "hub.docker.com",
	// Kubernetes, CNCF projects and curricula
	"kubernetes.io", "cncf.io", "training.linuxfoundation.org", "helm.sh", "kustomize.io",
	"opentelemetry.io", "prometheus.io",
	// HashiCorp Terraform
	"registry.terraform.io", "developer.hashicorp.com",
	// Software documented by its project
	"redis.io", "www.postgresql.org", "nginx.org", "docs.nginx.com", "www.rabbitmq.com", "dev.mysql.com",
	"docs.haproxy.org", "nodejs.org",
}

// officialRepos are GitHub organizations whose repositories are official
// sources (github.com/<org>/...).
var officialRepos = []string{"cncf", "kubernetes", "kubernetes-sigs", "docker", "hashicorp", "aws", "awslabs", "Azure", "GoogleCloudPlatform", "oracle"}

// checkSource validates a sources/docs entry: a title and an https URL on
// an official documentation site.
func checkSource(s model.LearnSource) error {
	if strings.TrimSpace(s.Title) == "" {
		return fmt.Errorf("%s: title is required", s.URL)
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%q: url must be an absolute https URL", s.URL)
	}
	host := strings.ToLower(u.Hostname())
	if host == "github.com" {
		org, _, _ := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
		for _, o := range officialRepos {
			if strings.EqualFold(org, o) {
				return nil
			}
		}
		return fmt.Errorf("%q: only official GitHub organizations (%s) are sources", s.URL, strings.Join(officialRepos, ", "))
	}
	for _, h := range officialHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return nil
		}
	}
	return fmt.Errorf("%q: %s is not an official documentation site (Sourcing in CONTRIBUTING-learn.md; ask to add an official host)", s.URL, host)
}

func checkSources(field string, list []model.LearnSource, required bool) []error {
	var errs []error
	if required && len(list) == 0 {
		errs = append(errs, fmt.Errorf("%s: at least one official source is required", field))
	}
	for i, s := range list {
		if err := checkSource(s); err != nil {
			errs = append(errs, fmt.Errorf("%s[%d]: %w", field, i, err))
		}
	}
	return errs
}

// checkChallenge validates a challenge's fields; extra (check parameters,
// starters) is the grading engine's validation.
func checkChallenge(c model.Challenge, extra func(model.Challenge) error) error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if !slices.Contains(model.LearnProviders, c.Provider) {
		add("provider must be one of %s", strings.Join(model.LearnProviders, ", "))
	} else if !strings.HasPrefix(c.ID, c.Provider+"-") {
		add("provider %q does not match the id prefix", c.Provider)
	}
	if !slices.Contains(model.LearnLevels, c.Level) {
		add("level must be one of %s", strings.Join(model.LearnLevels, ", "))
	}
	if !slices.Contains(model.LearnRuntimes, c.Runtime) {
		add("runtime must be one of %s", strings.Join(model.LearnRuntimes, ", "))
	}
	if strings.TrimSpace(c.Title) == "" {
		add("title is required")
	}
	if s := strings.TrimSpace(c.Summary); s == "" || strings.Contains(s, "\n") {
		add("summary is required and must be one line")
	}
	if c.Minutes <= 0 {
		add("minutes must be a positive estimate")
	}
	for _, t := range c.Tags {
		if !tagRE.MatchString(t) {
			add("tag %q must be lower-case kebab-case", t)
		}
	}
	for _, p := range c.Prerequisites {
		if p == c.ID {
			add("a challenge cannot be its own prerequisite")
		}
	}
	if strings.TrimSpace(c.Problem) == "" {
		add("problem is required")
	}
	if len(c.Requirements) == 0 {
		add("requirements: at least one is required")
	}
	for i, r := range c.Requirements {
		if strings.TrimSpace(r) == "" {
			add("requirements[%d] is empty", i)
		}
	}
	if len(c.Hints) == 0 {
		add("hints: at least one is required")
	}
	for i, h := range c.Hints {
		if strings.TrimSpace(h) == "" {
			add("hints[%d] is empty", i)
		}
	}
	if strings.TrimSpace(c.Solution) == "" {
		add("solution is required")
	}
	for p := range c.SolutionFiles {
		if !validRelPath(p) {
			add("solutionFiles: %q must be a relative path inside the workflow", p)
		}
	}
	for p := range c.Starter.Files {
		if !validRelPath(p) {
			add("starter.files: %q must be a relative path inside the workflow", p)
		}
	}
	switch {
	case c.Starter.TemplateID != "" && (c.Starter.Kind != "" || len(c.Starter.Files) > 0):
		add("starter: use either templateId or kind + files, not both")
	case c.Starter.TemplateID == "" && c.Starter.Kind == "":
		add("starter: templateId or kind is required")
	}
	if len(c.Checks) == 0 {
		add("checks: at least one is required")
	}
	ids := map[string]bool{}
	for i, ch := range c.Checks {
		if !idRE.MatchString(ch.ID) {
			add("checks[%d]: id %q must be a lower-case kebab-case slug", i, ch.ID)
		}
		if ids[ch.ID] {
			add("checks[%d]: id %q is used twice", i, ch.ID)
		}
		ids[ch.ID] = true
		if strings.TrimSpace(ch.Label) == "" {
			add("checks[%d]: label is required", i)
		}
	}
	if c.Provider == model.LearnProviderGlobal {
		if len(c.Adaptations) == 0 {
			add("adaptations: global challenges explain how to solve them on each provider")
		}
		for k, v := range c.Adaptations {
			if k == model.LearnProviderGlobal || !slices.Contains(model.LearnProviders, k) {
				add("adaptations: %q is not a provider", k)
			}
			if strings.TrimSpace(v) == "" {
				add("adaptations.%s is empty", k)
			}
		}
	} else if len(c.Adaptations) > 0 {
		add("adaptations are only for global challenges")
	}
	errs = append(errs, checkSources("sources", c.Sources, true)...)
	if extra != nil {
		if err := extra(c); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// validRelPath reports whether p is a clean relative slash path.
func validRelPath(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && !strings.Contains(p, `\`) && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}

// checkRoadmap validates a roadmap's fields (challenge references are
// checked on the whole content).
func checkRoadmap(r model.Roadmap) error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if r.Provider == "" || r.ID != r.Provider+"-roadmap" {
		add("provider must match the roadmap id (%s)", strings.TrimSuffix(r.ID, "-roadmap"))
	}
	if strings.TrimSpace(r.Title) == "" {
		add("title is required")
	}
	if strings.TrimSpace(r.Summary) == "" {
		add("summary is required")
	}
	if len(r.Levels) == 0 {
		add("levels: at least one is required")
	}
	last := -1
	for i, lvl := range r.Levels {
		at := slices.Index(model.LearnLevels, lvl.Level)
		switch {
		case at < 0:
			add("levels[%d]: level must be one of %s", i, strings.Join(model.LearnLevels, ", "))
		case at <= last:
			add("levels[%d]: %s is repeated or out of order (beginner → expert)", i, lvl.Level)
		default:
			last = at
		}
		if strings.TrimSpace(lvl.Title) == "" {
			add("levels[%d]: title is required", i)
		}
		if strings.TrimSpace(lvl.Goals) == "" {
			add("levels[%d]: goals are required", i)
		}
		if len(lvl.Steps) == 0 {
			add("levels[%d]: steps: at least one is required", i)
		}
		for j, st := range lvl.Steps {
			if strings.TrimSpace(st.Title) == "" {
				add("levels[%d].steps[%d]: title is required", i, j)
			}
			if strings.TrimSpace(st.Description) == "" {
				add("levels[%d].steps[%d]: description is required", i, j)
			}
			errs = append(errs, checkSources(fmt.Sprintf("levels[%d].steps[%d].docs", i, j), st.Docs, true)...)
		}
	}
	errs = append(errs, checkSources("sources", r.Sources, true)...)
	return errors.Join(errs...)
}

// references checks what crosses content items: roadmap steps and
// prerequisites name existing challenges, prerequisites have no cycles,
// and every challenge declares solvableWithoutCode.
func (st state) references() []error {
	var errs []error
	ids := make([]string, 0, len(st.roadmaps))
	for id := range st.roadmaps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := st.roadmaps[id]
		for _, lvl := range r.Levels {
			for _, step := range lvl.Steps {
				for _, cid := range step.Challenges {
					if _, ok := st.challenges[cid]; !ok {
						errs = append(errs, fmt.Errorf("roadmap %s, %s step %q: challenge %s does not exist", id, lvl.Level, step.Title, cid))
					}
				}
			}
		}
	}
	ids = ids[:0]
	for id := range st.challenges {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, p := range st.challenges[id].Prerequisites {
			if _, ok := st.challenges[p]; !ok {
				errs = append(errs, fmt.Errorf("challenge %s: prerequisite %s does not exist", id, p))
			}
		}
	}
	errs = append(errs, st.undeclared(ids)...)
	// Cycles: depth-first search over prerequisites.
	const (
		visiting = 1
		done     = 2
	)
	mark := map[string]int{}
	var visit func(id string, path []string)
	visit = func(id string, path []string) {
		switch mark[id] {
		case visiting:
			errs = append(errs, fmt.Errorf("prerequisite cycle: %s", strings.Join(slices.Concat(path, []string{id}), " → ")))
			return
		case done:
			return
		}
		mark[id] = visiting
		for _, p := range st.challenges[id].Prerequisites {
			if _, ok := st.challenges[p]; ok {
				visit(p, slices.Concat(path, []string{id}))
			}
		}
		mark[id] = done
	}
	for _, id := range ids {
		visit(id, nil)
	}
	return errs
}

// undeclared reports the challenges (ids, sorted) that do not declare
// solvableWithoutCode.
func (st state) undeclared(ids []string) []error {
	var errs []error
	for _, id := range ids {
		if st.challenges[id].SolvableWithoutCode == nil {
			errs = append(errs, fmt.Errorf("challenge %s: solvableWithoutCode must be declared (true: every step can be done in the canvas and inspectors; false: some step needs editing code or files)", id))
		}
	}
	return errs
}
