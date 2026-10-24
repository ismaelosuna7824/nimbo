package challenge

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

var (
	validID       = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	scenarios     = []string{model.ScenarioKillReplica, model.ScenarioDependencyDown, model.ScenarioDependencyLatency, model.ScenarioCacheFlush, model.ScenarioTrafficSpike}
	metrics       = []string{model.MetricP50, model.MetricP90, model.MetricP95, model.MetricP99, model.MetricMax, model.MetricErrorRate, model.MetricRPS, model.MetricReplicasMax}
	ops           = []string{"lt", "lte", "gt", "gte"}
	runtimeChecks = []string{model.CheckDeploy, model.CheckHTTP, model.CheckLoad, model.CheckScenario}
)

// Scenario timing the verdicts need (see the scenario package): a 10 s
// baseline before the fault and room to observe the recovery after it.
const (
	minBaselineSec = 10
	minRecoverySec = 15
)

// Validate checks a challenge's content: identity, level, provider and
// runtime, texts, sources (https), the starter (resolvable) and every check
// (well-formed for its type and possible on the starter's kind). Roadmap and
// prerequisite references are the Learn store's.
func Validate(c model.Challenge) error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if !validID.MatchString(c.ID) || !strings.HasPrefix(c.ID, c.Provider+"-") {
		bad("id %q: use <provider>-<kebab-case>", c.ID)
	}
	if !slices.Contains(model.LearnProviders, c.Provider) {
		bad("provider %q: use one of %s", c.Provider, strings.Join(model.LearnProviders, ", "))
	}
	if !slices.Contains(model.LearnLevels, c.Level) {
		bad("level %q: use one of %s", c.Level, strings.Join(model.LearnLevels, ", "))
	}
	if !slices.Contains(model.LearnRuntimes, c.Runtime) {
		bad("runtime %q: use one of %s", c.Runtime, strings.Join(model.LearnRuntimes, ", "))
	}
	for name, v := range map[string]string{"title": c.Title, "summary": c.Summary, "problem": c.Problem, "solution": c.Solution} {
		if strings.TrimSpace(v) == "" {
			bad("%s is required", name)
		}
	}
	if c.Minutes < 5 || c.Minutes > 240 {
		bad("minutes %d: between 5 and 240", c.Minutes)
	}
	if len(c.Requirements) == 0 {
		bad("at least one requirement")
	}
	if len(c.Hints) < 2 {
		bad("at least two hints")
	}
	if len(c.Sources) == 0 {
		bad("at least one official source")
	}
	for _, s := range c.Sources {
		if u, err := url.Parse(s.URL); err != nil || u.Scheme != "https" || u.Host == "" || strings.TrimSpace(s.Title) == "" {
			bad("source %q: needs a title and an https URL", s.URL)
		}
	}
	if c.Provider == model.LearnProviderGlobal {
		if len(c.Adaptations) == 0 {
			bad("global challenges need adaptations")
		}
	} else if len(c.Adaptations) > 0 {
		bad("adaptations are for global challenges")
	}
	for p := range c.Adaptations {
		if p == model.LearnProviderGlobal || !slices.Contains(model.LearnProviders, p) {
			bad("adaptation for unknown provider %q", p)
		}
	}
	st, err := ResolveStarter(nil, c.Starter)
	if err != nil {
		bad("%v", err)
	}
	kind := st.Kind
	for rel := range c.SolutionFiles {
		if rel == "" || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
			bad("solution file %q: use a clean relative slash path", rel)
		}
	}
	if len(c.Checks) == 0 {
		bad("at least one check")
	}
	seen := map[string]bool{}
	for i, ck := range c.Checks {
		if err := validCheck(c.Runtime, kind, ck); err != nil {
			bad("check %d (%s): %v", i+1, ck.ID, err)
		}
		if seen[ck.ID] {
			bad("check %d: duplicate id %q", i+1, ck.ID)
		}
		seen[ck.ID] = true
	}
	return errors.Join(errs...)
}

// validCheck checks one check against the starter's kind ("" = Terraform)
// and the challenge runtime.
func validCheck(runtime, kind string, c model.ChallengeCheck) error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if !validID.MatchString(c.ID) {
		bad("id %q: use kebab-case", c.ID)
	}
	if strings.TrimSpace(c.Label) == "" {
		bad("label is required")
	}
	params := map[string]bool{
		model.CheckRulesClean: c.RulesClean != nil, model.CheckHas: c.Has != nil, model.CheckHTTP: c.HTTP != nil,
		model.CheckLoad: c.Load != nil, model.CheckScenario: c.Scenario != nil, model.CheckReadiness: c.Readiness != nil,
	}
	for typ, set := range params {
		if set && typ != c.Type {
			bad("%s parameters on a %s check", typ, c.Type)
		}
	}
	if c.Environment != "" && kind == "" {
		bad("environment is for docker and Kubernetes checks")
	}
	if slices.Contains(runtimeChecks, c.Type) && runtime == model.LearnRuntimeDesign {
		bad("%s needs a runtime: design challenges use validate, rules_clean, has and readiness", c.Type)
	}
	if slices.Contains(runtimeChecks, c.Type) && kind == "" && runtime != model.LearnRuntimeEmulated {
		bad("%s on a Terraform starter needs runtime emulated", c.Type)
	}
	switch c.Type {
	case model.CheckValidate, model.CheckDeploy:
	case model.CheckRulesClean:
		if c.RulesClean == nil {
			bad("rules_clean parameters missing")
		} else if _, ok := severityRank[c.RulesClean.MinSeverity]; !ok {
			bad("minSeverity %q: use info, warning or error", c.RulesClean.MinSeverity)
		}
	case model.CheckHas:
		if c.Has == nil {
			bad("has parameters missing")
		} else if err := validHas(kind, c.Has); err != nil {
			bad("%v", err)
		}
	case model.CheckHTTP:
		if c.HTTP == nil {
			bad("http parameters missing")
		} else {
			if err := validTarget(kind, c.HTTP.ChallengeTarget); err != nil {
				bad("%v", err)
			}
			if c.HTTP.Status != 0 && (c.HTTP.Status < 100 || c.HTTP.Status > 599) {
				bad("status %d", c.HTTP.Status)
			}
		}
	case model.CheckLoad:
		if c.Load == nil {
			bad("load parameters missing")
			break
		}
		if err := validTarget(kind, c.Load.Target); err != nil {
			bad("%v", err)
		}
		if _, err := trafficLength(c.Load.Spec); err != nil {
			bad("%v", err)
		}
		if len(c.Load.Expect) == 0 {
			bad("at least one expected metric")
		}
		if err := validMetrics(kind, c.Load.Expect); err != nil {
			bad("%v", err)
		}
	case model.CheckScenario:
		s := c.Scenario
		if s == nil {
			bad("scenario parameters missing")
			break
		}
		if kind != model.WorkflowDocker && kind != model.WorkflowKubernetes {
			bad("scenarios run on docker and Kubernetes workflows")
		}
		if err := validTarget(kind, s.Target); err != nil {
			bad("%v", err)
		}
		length, err := trafficLength(s.Spec)
		if err != nil {
			bad("%v", err)
		}
		sc := s.Scenario
		if !slices.Contains(scenarios, sc.ID) {
			bad("unknown scenario %q", sc.ID)
		}
		if sc.ID != model.ScenarioTrafficSpike && sc.Target == "" {
			bad("scenario %s needs a target (service or workload name)", sc.ID)
		}
		if sc.AtSec < minBaselineSec {
			bad("scenario atSec %d: at least %d s of baseline first", sc.AtSec, minBaselineSec)
		}
		if sc.DurationSec <= 0 || sc.AtSec+sc.DurationSec+minRecoverySec > length {
			bad("scenario ends at %d s: the traffic (%d s) must last %d s more to see the recovery", sc.AtSec+sc.DurationSec, length, minRecoverySec)
		}
		if s.Expect.Verdict != model.ScenarioSurvived && s.Expect.Verdict != model.ScenarioDegraded {
			bad("expect.verdict %q: use survived or degraded", s.Expect.Verdict)
		}
		if err := validMetrics(kind, s.Expect.Metrics); err != nil {
			bad("%v", err)
		}
	case model.CheckReadiness:
		if c.Readiness == nil {
			bad("readiness parameters missing")
		} else if c.Readiness.MinScore < 1 || c.Readiness.MinScore > 100 {
			bad("minScore %d: between 1 and 100", c.Readiness.MinScore)
		}
	default:
		bad("unknown type %q", c.Type)
	}
	return errors.Join(errs...)
}

func validHas(kind string, h *model.ChallengeHas) error {
	n := 0
	for _, set := range []bool{h.Terraform != nil, h.Compose != nil, h.Kubernetes != nil, h.Connected != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("has needs exactly one of terraform, compose, kubernetes, connected")
	}
	count := func(c *model.Count) error {
		if c != nil && c.Min != nil && c.Max != nil && *c.Min > *c.Max {
			return fmt.Errorf("count min > max")
		}
		return nil
	}
	switch {
	case h.Terraform != nil:
		if kind != "" {
			return fmt.Errorf("has.terraform on a %s starter", kind)
		}
		if h.Terraform.Type == "" {
			return fmt.Errorf("has.terraform needs a type")
		}
		return errors.Join(validWhere(h.Terraform.Where), count(h.Terraform.Count))
	case h.Compose != nil:
		if kind != model.WorkflowDocker {
			return fmt.Errorf("has.compose on a non-docker starter")
		}
		return errors.Join(validWhere(h.Compose.Where), count(h.Compose.Count))
	case h.Kubernetes != nil:
		if kind != model.WorkflowKubernetes {
			return fmt.Errorf("has.kubernetes on a non-Kubernetes starter")
		}
		if h.Kubernetes.Kind == "" {
			return fmt.Errorf("has.kubernetes needs a kind")
		}
		return errors.Join(validWhere(h.Kubernetes.Where), count(h.Kubernetes.Count))
	}
	if h.Connected.From == "" || h.Connected.To == "" {
		return fmt.Errorf("connected needs from and to")
	}
	return nil
}

func validTarget(kind string, t model.ChallengeTarget) error {
	if t.Service == "" || !strings.HasPrefix(t.Path, "/") {
		return fmt.Errorf("target needs service and a path starting with /")
	}
	if t.Ingress && kind != model.WorkflowKubernetes {
		return fmt.Errorf("ingress targets are for Kubernetes")
	}
	if !t.Ingress && kind != "" && (t.Port <= 0 || t.Port > 65535) {
		return fmt.Errorf("target needs a port (container port for docker, Service port for Kubernetes)")
	}
	if t.Method != "" && !slices.Contains([]string{"GET", "POST", "PUT", "DELETE", "HEAD"}, t.Method) {
		return fmt.Errorf("method %q", t.Method)
	}
	return nil
}

// trafficLength validates a load spec and returns its length in seconds.
func trafficLength(s model.ChallengeLoadSpec) (int, error) {
	if (s.Preset == "") == (len(s.Stages) == 0) {
		return 0, fmt.Errorf("spec needs a preset or stages (not both)")
	}
	if s.Mode != "" && s.Mode != model.ModeRPS && s.Mode != model.ModeConcurrency {
		return 0, fmt.Errorf("mode %q", s.Mode)
	}
	stages := s.Stages
	if s.Preset != "" {
		p, ok := preset(s.Preset)
		if !ok {
			return 0, fmt.Errorf("unknown preset %q", s.Preset)
		}
		stages = p.Stages
	}
	length := 0
	for _, st := range stages {
		if st.DurationSec < 0 || st.Target < 0 {
			return 0, fmt.Errorf("stages must be non-negative")
		}
		length += st.DurationSec
	}
	if length < 10 || length > 600 {
		return 0, fmt.Errorf("traffic lasts %d s: between 10 and 600", length)
	}
	return length, nil
}

func validMetrics(kind string, ms []model.ChallengeMetric) error {
	var errs []error
	for _, m := range ms {
		if !slices.Contains(metrics, m.Metric) {
			errs = append(errs, fmt.Errorf("unknown metric %q", m.Metric))
		}
		if !slices.Contains(ops, m.Op) {
			errs = append(errs, fmt.Errorf("%s: op %q (use lt, lte, gt, gte)", m.Metric, m.Op))
		}
		if m.Metric == model.MetricErrorRate && (m.Value < 0 || m.Value > 1) {
			errs = append(errs, fmt.Errorf("error_rate is a fraction (0..1)"))
		}
		if m.Metric == model.MetricReplicasMax && kind != model.WorkflowKubernetes {
			errs = append(errs, fmt.Errorf("replicas_max is for Kubernetes"))
		}
	}
	return errors.Join(errs...)
}
