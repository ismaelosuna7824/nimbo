package challenge

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/netcheck"
)

// Analysis is a workflow opened for grading: its graph and every finding
// (all rules on, whatever the user's settings), in one environment.
type Analysis struct {
	Workflow    *model.Workflow
	Diagnostics []model.Diagnostic
	// Compose is the effective compose project (docker; nil when the files
	// don't load); Kube the rendered objects (Kubernetes).
	Compose *types.Project
	Kube    *kube.Project
}

// allRules turns on every best-practice rule, opt-in ones included.
func allRules() model.BestPracticeSettings {
	var on []string
	for _, r := range slices.Concat(lint.Rules(), compose.Rules(), kuberules.Rules()) {
		if r.DefaultOff {
			on = append(on, r.Code)
		}
	}
	return model.BestPracticeSettings{EnabledRules: on}
}

// Analyze opens wf (files in dir) for grading in environment env ("" =
// the active one; Terraform workflows have one). wf is not modified.
// Terraform workflows must carry the diagnostics of their last save
// (parse, schema and reference findings): the rules are added here.
func Analyze(ctx context.Context, cat *catalog.Catalog, wf *model.Workflow, dir, env string) (*Analysis, error) {
	cp := *wf
	if env != "" {
		cp.ActiveEnvironment = env
	}
	a := &Analysis{Workflow: &cp}
	switch {
	case wf.IsDocker():
		c := model.ComposeModel{}
		if wf.Compose != nil {
			c = *wf.Compose
		}
		cp.Compose = &c
		a.Diagnostics = compose.Derive(ctx, dir, &cp, allRules())
		if env != "" && cp.Compose.Environment != env {
			return nil, fmt.Errorf("the workflow has no %q environment (add compose.%s.yaml or .env.%s)", env, env, env)
		}
		p := compose.Open(ctx, dir, cp.Compose.Files, cp.Compose.Profiles, compose.Options{Environment: cp.Compose.Environment})
		a.Compose = p.Model
	case wf.IsKubernetes():
		k := model.KubernetesModel{}
		if wf.Kubernetes != nil {
			k = *wf.Kubernetes
			k.Sources = slices.Clone(k.Sources)
		}
		cp.Kubernetes = &k
		p, diags := kube.Derive(ctx, dir, &cp)
		if env != "" && cp.Kubernetes.Environment != env {
			return nil, fmt.Errorf("the workflow has no %q environment (no overlay or values file maps it)", env)
		}
		a.Diagnostics = append(diags, kuberules.Check(p, &cp, allRules())...)
		a.Kube = p
	default:
		if cat == nil {
			var err error
			if cat, err = defaultCatalog(); err != nil {
				return nil, err
			}
		}
		diags := netcheck.Strip(lint.Strip(wf.Diagnostics))
		diags = append(diags, lint.Check(&cp, cat, allRules(), nil)...)
		a.Diagnostics = append(diags, netcheck.Check(&cp)...)
	}
	cp.Diagnostics = a.Diagnostics
	return a, nil
}

// ImportTerraform builds a Terraform workflow from files as a new
// workflow would be (no re-layout), with its parse and schema findings.
func ImportTerraform(cat *catalog.Catalog, name string, files map[string][]byte) (*model.Workflow, error) {
	wf, extra, err := hclcodec.Import(name, files)
	if err != nil {
		return nil, err
	}
	if cat == nil {
		if cat, err = defaultCatalog(); err != nil {
			return nil, err
		}
	}
	diags := hclcodec.Normalize(wf)
	diags = append(diags, cat.Validate(wf)...)
	wf.Diagnostics = append(diags, extra...)
	return wf, nil
}

var severityRank = map[model.Severity]int{model.SeverityInfo: 0, model.SeverityWarning: 1, model.SeverityError: 2}

// GradeStatic grades a validate, rules_clean or has check on a.
func GradeStatic(c model.ChallengeCheck, a *Analysis) model.ChallengeGoalResult {
	res := model.ChallengeGoalResult{ID: c.ID, Label: c.Label, Type: c.Type, Checks: []model.ChallengeCheckResult{}}
	var r model.ChallengeCheckResult
	switch c.Type {
	case model.CheckValidate:
		r = validateCheck(a)
	case model.CheckRulesClean:
		r = rulesCheck(c.RulesClean, a)
	case model.CheckHas:
		r = hasCheck(c.Has, a)
	default:
		r = model.ChallengeCheckResult{Label: c.Label, Actual: "not a static check", Expected: c.Type}
	}
	res.Checks = append(res.Checks, r)
	res.Passed = r.Passed
	res.Status = model.ChallengeFailed
	res.Detail = fmt.Sprintf("Not yet: %s (found %s).", r.Expected, r.Actual)
	if r.Passed {
		res.Status, res.Detail = model.ChallengePassed, "Passed."
	}
	return res
}

func describe(d model.Diagnostic) string {
	s := d.Message
	if d.File != "" {
		s += fmt.Sprintf(" (%s:%d)", d.File, d.Line)
	}
	return s
}

// schemaCodes are provider-schema findings `terraform validate` rejects,
// whatever severity the canvas shows them with.
var schemaCodes = []string{catalog.CodeUnknownType, catalog.CodeUnknownAttribute, catalog.CodeUnknownBlock,
	catalog.CodeMissingAttribute, catalog.CodeMissingBlock, catalog.CodeTooManyBlocks, catalog.CodeReadOnlyAttribute}

func validateCheck(a *Analysis) model.ChallengeCheckResult {
	r := model.ChallengeCheckResult{Label: "No errors", Expected: "no error findings"}
	var errs []string
	for _, d := range a.Diagnostics {
		if d.Severity == model.SeverityError || slices.Contains(schemaCodes, d.Code) {
			errs = append(errs, describe(d))
		}
	}
	r.Passed = len(errs) == 0
	r.Actual = findings(errs)
	return r
}

func rulesCheck(p *model.ChallengeRulesClean, a *Analysis) model.ChallengeCheckResult {
	r := model.ChallengeCheckResult{Label: "No matching findings"}
	scope := "findings"
	if len(p.Codes)+len(p.Prefixes) > 0 {
		scope = strings.Join(slices.Concat(p.Codes, mapSlice(p.Prefixes, func(s string) string { return s + "*" })), ", ")
	}
	r.Expected = fmt.Sprintf("no %s at %s or above", scope, p.MinSeverity)
	var hits []string
	for _, d := range a.Diagnostics {
		if severityRank[d.Severity] < severityRank[p.MinSeverity] {
			continue
		}
		match := len(p.Codes)+len(p.Prefixes) == 0 || slices.Contains(p.Codes, d.Code) ||
			slices.ContainsFunc(p.Prefixes, func(pre string) bool { return strings.HasPrefix(d.Code, pre) })
		if match {
			hits = append(hits, d.Code+": "+describe(d))
		}
	}
	r.Passed = len(hits) == 0
	r.Actual = findings(hits)
	return r
}

func findings(list []string) string {
	switch len(list) {
	case 0:
		return "none"
	case 1:
		return list[0]
	}
	return fmt.Sprintf("%d findings, e.g. %s", len(list), list[0])
}

func mapSlice[T, U any](in []T, f func(T) U) []U {
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

// countOK checks n against c (default: at least one).
func countOK(n int, c *model.Count) (bool, string) {
	lo, hi := 1, -1
	if c != nil {
		lo = 0
		if c.Min != nil {
			lo = *c.Min
		}
		if c.Max != nil {
			hi = *c.Max
		}
	}
	want := fmt.Sprintf("at least %d", lo)
	switch {
	case hi >= 0 && lo == hi:
		want = fmt.Sprintf("exactly %d", lo)
	case hi >= 0 && lo == 0:
		want = fmt.Sprintf("at most %d", hi)
	case hi >= 0:
		want = fmt.Sprintf("%d to %d", lo, hi)
	}
	return n >= lo && (hi < 0 || n <= hi), want
}

func hasCheck(h *model.ChallengeHas, a *Analysis) model.ChallengeCheckResult {
	r := model.ChallengeCheckResult{Label: "Found"}
	var (
		n       int
		what    string
		count   *model.Count
		lastBad string
	)
	var where model.Where
	tally := func(obj any) {
		if ok, bad := matchWhere(obj, where); ok {
			n++
		} else {
			lastBad = bad
		}
	}
	switch {
	case h.Terraform != nil:
		t := h.Terraform
		what, count, where = t.Type, t.Count, t.Where
		if t.Name != "" {
			what += "." + t.Name
		}
		kind, typ := model.KindResource, t.Type
		if rest, ok := strings.CutPrefix(t.Type, "data."); ok {
			kind, typ = model.KindData, rest
		}
		for _, nd := range a.Workflow.Nodes {
			if nd.Kind == kind && nd.Type == typ && (t.Name == "" || nd.Name == t.Name) {
				tally(bodyValue(nd.Body))
			}
		}
	case h.Compose != nil:
		c := h.Compose
		count, where = c.Count, c.Where
		what = cmp.Or(c.Service, c.Image, c.Category, "the compose project")
		if a.Compose == nil {
			r.Actual, r.Expected = "the compose files don't load", what
			return r
		}
		if c.Service == "" && c.Image == "" && c.Category == "" {
			tally(toGeneric(a.Compose))
			break
		}
		for name, svc := range a.Compose.Services {
			if c.Service != "" && name != c.Service {
				continue
			}
			if c.Image != "" && !strings.HasPrefix(svc.Image, c.Image) {
				continue
			}
			if c.Category != "" && imageCategory(svc.Image) != c.Category {
				continue
			}
			tally(toGeneric(svc))
		}
	case h.Kubernetes != nil:
		k := h.Kubernetes
		what, count, where = k.Kind, k.Count, k.Where
		if k.Name != "" {
			what += "/" + k.Name
		}
		if a.Kube != nil {
			for _, o := range a.Kube.Objects {
				if o.Kind == k.Kind && (k.Name == "" || o.Name == k.Name) && (k.Namespace == "" || o.Namespace == k.Namespace) {
					tally(o.Obj)
				}
			}
		}
	case h.Connected != nil:
		ok := connected(a.Workflow, h.Connected.From, h.Connected.To)
		r.Label = "Connected"
		r.Expected = fmt.Sprintf("%s connected to %s", h.Connected.From, h.Connected.To)
		r.Passed, r.Actual = ok, "not connected"
		if ok {
			r.Actual = "connected"
		}
		return r
	}
	ok, want := countOK(n, count)
	r.Passed = ok
	r.Expected = fmt.Sprintf("%s %s", want, what)
	if len(where) > 0 {
		r.Expected += " matching the conditions"
	}
	r.Actual = fmt.Sprintf("%d found", n)
	if n == 0 && lastBad != "" {
		r.Actual += fmt.Sprintf(" (%s doesn't match)", lastBad)
	}
	return r
}

// imageCategory is the Nimbo palette category of an image reference.
func imageCategory(ref string) string {
	repo := ref
	if i := strings.LastIndex(repo, "@"); i >= 0 {
		repo = repo[:i]
	}
	if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}
	repo = strings.TrimPrefix(strings.TrimPrefix(repo, "docker.io/"), "library/")
	for _, im := range compose.Images() {
		if im.Image == repo {
			return im.Category
		}
	}
	return ""
}

// connected looks for a graph edge, either direction, between nodes
// matching from and to.
func connected(wf *model.Workflow, from, to string) bool {
	type edge struct{ a, b string }
	var edges []edge
	match := map[string][]string{}
	switch {
	case wf.IsDocker() && wf.Compose != nil:
		for _, n := range wf.Compose.Graph.Nodes {
			match[n.ID] = []string{n.Name}
		}
		for _, e := range wf.Compose.Graph.Edges {
			edges = append(edges, edge{e.Source, e.Target})
		}
	case wf.IsKubernetes() && wf.Kubernetes != nil:
		for _, n := range wf.Kubernetes.Graph.Nodes {
			match[n.ID] = []string{n.Name, n.Kind + "/" + n.Name}
		}
		for _, e := range wf.Kubernetes.Graph.Edges {
			edges = append(edges, edge{e.Source, e.Target})
		}
	default:
		for _, n := range wf.Nodes {
			addr := n.Type + "." + n.Name
			if n.Kind == model.KindData {
				addr = "data." + addr
			}
			match[n.ID] = []string{n.Type, addr}
		}
		for _, e := range wf.Edges {
			edges = append(edges, edge{e.Source, e.Target})
		}
	}
	is := func(id, sel string) bool { return slices.Contains(match[id], sel) }
	for _, e := range edges {
		if (is(e.a, from) && is(e.b, to)) || (is(e.a, to) && is(e.b, from)) {
			return true
		}
	}
	return false
}
