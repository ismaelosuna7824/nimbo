package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/challenge"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/loadtest"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

// Learn challenges in a workspace: starting one (a workflow from its
// starter), "Check my solution" (the grading engine in internal/challenge)
// and the reference solution as a copy.

// newChallenges wires the challenge checker to the workflow runtimes, the
// load test engine and the Learn progress.
func (s *Server) newChallenges() *challenge.Checker {
	return challenge.New(challenge.Config{
		Runtime:   challengeRuntime{s},
		LoadTests: s.cfg.LoadTest,
		Dir:       func(wfID string) string { return filepath.Dir(s.cfg.Store.AssetsDir(wfID)) },
		Finished: func(wfID string, res model.ChallengeResult) {
			if s.cfg.Learn == nil || res.Status == model.ChallengeCanceled {
				return
			}
			if err := s.cfg.Learn.RecordAttempt(res.Challenge, wfID, res); err != nil {
				s.log.Warn("record challenge attempt", "challenge", res.Challenge, "workflow", wfID, "err", err)
			}
		},
		Logger: s.log,
	})
}

// challengeByID loads a challenge from the Learn database.
func (s *Server) challengeByID(id string) (model.Challenge, error) {
	if s.cfg.Learn == nil {
		return model.Challenge{}, errorf(http.StatusServiceUnavailable, "the Learn database is not available")
	}
	c, found, err := s.cfg.Learn.Challenge(id)
	if err != nil {
		return model.Challenge{}, err
	}
	if !found {
		return model.Challenge{}, errorf(http.StatusNotFound, "challenge %q not found", id)
	}
	return c, nil
}

// challengeWorkflow loads a challenge workflow and its challenge.
func (s *Server) challengeWorkflow(r *http.Request) (*model.Workflow, model.Challenge, error) {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return nil, model.Challenge{}, err
	}
	if wf.Challenge == "" {
		return nil, model.Challenge{}, errorf(http.StatusNotFound, "this workflow is not a challenge: start one from Learn")
	}
	c, err := s.challengeByID(wf.Challenge)
	return wf, c, err
}

// startChallenge is POST /learn/challenges/{id}/start: a new workflow from
// the challenge's starter, linked to it (201 with the workflow).
func (s *Server) startChallenge(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	c, err := s.challengeByID(r.PathValue("id"))
	if err != nil {
		return err
	}
	st, err := challenge.ResolveStarter(s.cfg.Catalog, c.Starter)
	if err != nil {
		return errorf(http.StatusInternalServerError, "challenge %s: %v", c.ID, err)
	}
	wf, err := s.createChallengeWorkflow(cmp.Or(strings.TrimSpace(req.Name), c.Title), c, st, st.Files)
	if err != nil {
		return err
	}
	if _, err := s.cfg.Learn.MarkStarted(c.ID, wf.ID); err != nil {
		s.log.Warn("mark challenge started", "challenge", c.ID, "err", err)
	}
	s.announceCreated(r, wf)
	return writeJSON(w, http.StatusCreated, wf)
}

// challengeSolution is POST /workflows/{id}/challenge/solution: a copy of
// the starter with the reference solution files written over it (201 with
// the new workflow). The learner's workflow is left as it is.
func (s *Server) challengeSolution(w http.ResponseWriter, r *http.Request) error {
	_, c, err := s.challengeWorkflow(r)
	if err != nil {
		return err
	}
	if len(c.SolutionFiles) == 0 {
		return errorf(http.StatusNotFound, "this challenge has no reference files: read its solution instead")
	}
	st, err := challenge.ResolveStarter(s.cfg.Catalog, c.Starter)
	if err != nil {
		return errorf(http.StatusInternalServerError, "challenge %s: %v", c.ID, err)
	}
	wf, err := s.createChallengeWorkflow(c.Title+" (solution)", c, st, st.WithSolution(c.SolutionFiles))
	if err != nil {
		return err
	}
	s.announceCreated(r, wf)
	return writeJSON(w, http.StatusCreated, wf)
}

// createChallengeWorkflow creates and saves a workflow holding files of the
// starter's kind, linked to challenge c. Terraform files keep their names
// (no re-layout), so solution files match the starter's.
func (s *Server) createChallengeWorkflow(name string, c model.Challenge, st challenge.Starter, files map[string][]byte) (*model.Workflow, error) {
	var (
		wf    *model.Workflow
		extra []model.Diagnostic
	)
	switch st.Kind {
	case model.WorkflowDocker:
		wf = &model.Workflow{ID: store.NewID(name), Kind: model.WorkflowDocker}
		composeFiles := compose.DefaultFiles(func(n string) bool { _, ok := files[n]; return ok })
		if len(composeFiles) == 0 {
			return nil, errorf(http.StatusInternalServerError, "challenge %s: the starter has no compose file", c.ID)
		}
		wf.Compose = &model.ComposeModel{Files: composeFiles}
	case model.WorkflowKubernetes:
		wf = &model.Workflow{ID: store.NewID(name), Kind: model.WorkflowKubernetes,
			Kubernetes: &model.KubernetesModel{Sources: slices.Clone(st.Kubernetes.Sources), Builds: slices.Clone(st.Kubernetes.Builds)}}
	default:
		var err error
		if wf, extra, err = hclcodec.Import(name, files); err != nil {
			return nil, fmt.Errorf("challenge %s: %w", c.ID, err)
		}
		wf.ID = store.NewID(name)
	}
	wf.Name, wf.Description, wf.Challenge = name, c.Summary, c.ID
	wf.Icon = s.randomIcon()
	if !wf.IsTerraform() {
		if err := s.writeAssets(wf.ID, files); err != nil {
			_ = s.cfg.Store.Delete(wf.ID)
			return nil, err
		}
	}
	if err := s.save(wf, extra); err != nil {
		_ = s.cfg.Store.Delete(wf.ID)
		return nil, err
	}
	return wf, nil
}

// getChallenge is GET /workflows/{id}/challenge: the challenge and its
// latest check.
func (s *Server) getChallenge(w http.ResponseWriter, r *http.Request) error {
	wf, c, err := s.challengeWorkflow(r)
	if err != nil {
		return err
	}
	return ok(w, model.ChallengeView{Challenge: c, Last: s.challenges.Last(wf.ID)})
}

// checkChallenge is POST /workflows/{id}/challenge/check: runs every check
// (deploying when one needs the runtime); 202 with the running check, poll
// GET /challenge.
func (s *Server) checkChallenge(w http.ResponseWriter, r *http.Request) error {
	wf, c, err := s.challengeWorkflow(r)
	if err != nil {
		return err
	}
	if err := refuseArchived(wf); err != nil {
		return err
	}
	res, err := s.challenges.Start(wf.ID, c)
	if errors.Is(err, challenge.ErrRunning) {
		return errorf(http.StatusConflict, "%v", err)
	}
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, res)
}

// cancelChallengeCheck is DELETE /workflows/{id}/challenge/check.
func (s *Server) cancelChallengeCheck(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := s.challenges.Cancel(wf.ID); err != nil {
		return errorf(http.StatusConflict, "%v", err)
	}
	return noContent(w)
}

// challengeRuntime is the runtime of a challenge check: compose projects,
// per-workflow Kubernetes clusters and the Floci sandbox.
type challengeRuntime struct{ s *Server }

// deployTimeout bounds a deploy (builds, image pulls, cluster start).
const deployTimeout = 15 * time.Minute

// workflow loads a workflow with its graph and findings refreshed.
func (c challengeRuntime) workflow(wfID string) (*model.Workflow, error) {
	wf, err := c.s.cfg.Store.Get(wfID)
	if err != nil {
		return nil, err
	}
	switch {
	case wf.IsDocker():
		c.s.deriveDocker(wf, nil)
	case wf.IsKubernetes():
		c.s.deriveKube(wf, nil)
	}
	return wf, nil
}

func (c challengeRuntime) Analyze(ctx context.Context, wfID, env string) (*challenge.Analysis, error) {
	wf, err := c.s.cfg.Store.Get(wfID)
	if err != nil {
		return nil, err
	}
	return challenge.Analyze(ctx, c.s.cfg.Catalog, wf, c.s.cfg.Store.AssetsDir(wfID), env)
}

func (c challengeRuntime) Environment(wfID string) string {
	wf, err := c.s.cfg.Store.Get(wfID)
	if err != nil {
		return ""
	}
	switch {
	case wf.IsDocker() && wf.Compose != nil:
		return wf.Compose.Environment
	case wf.IsKubernetes() && wf.Kubernetes != nil:
		return wf.Kubernetes.Environment
	}
	return wf.ActiveEnvironment
}

func (c challengeRuntime) Readiness(ctx context.Context, wfID, env string) (int, error) {
	wf, err := c.workflow(wfID)
	if err != nil {
		return 0, err
	}
	if !wf.IsDocker() && !wf.IsKubernetes() {
		return 0, fmt.Errorf("production readiness is for docker and Kubernetes workflows")
	}
	if env == "" {
		env = c.Environment(wfID)
	}
	if !readinessEnvironment(wf, env) {
		return 0, fmt.Errorf("environment %q not found", env)
	}
	rep := c.s.readinessReport(ctx, wf, &env)
	for _, e := range rep.Environments {
		if e.Name == env {
			return e.Score, nil
		}
	}
	return 0, fmt.Errorf("no readiness report for environment %q", env)
}

// Deploy runs up (docker: remapping fixed host ports another program
// holds; Terraform: starting the Floci sandbox, then apply) and waits.
func (c challengeRuntime) Deploy(ctx context.Context, wfID string) (string, error) {
	wf, err := c.workflow(wfID)
	if err != nil {
		return "", err
	}
	if err := refuseArchived(wf); err != nil {
		return "", err
	}
	var run *model.Run
	bg := context.WithoutCancel(ctx)
	switch {
	case wf.IsDocker():
		req := c.s.composeRunRequest(wf, runRequest{Action: model.ActionUp, RemapPorts: true, TimeoutSec: int(deployTimeout / time.Second)})
		run, err = c.s.cfg.Sandbox.ComposeRun(bg, req)
		err = composeError(err)
	case wf.IsKubernetes():
		kreq := sandbox.KubeRunRequest{Workflow: wf, Action: model.ActionUp, SourceDir: c.s.cfg.Store.AssetsDir(wf.ID), Timeout: deployTimeout}
		if err = c.s.prepareKubeUp(ctx, wf, "", &kreq); err == nil {
			run, err = c.s.cfg.Sandbox.KubeRun(bg, kreq)
			err = kubeError(err)
		}
	default:
		run, err = c.applyTerraform(bg, wf)
	}
	if err != nil {
		var he *httpError
		if errors.As(err, &he) {
			msg := he.msg
			for _, d := range he.diagnostics {
				msg += "; " + d.Message
			}
			return "", errors.New(msg)
		}
		return "", err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for run.Status == model.RunQueued || run.Status == model.RunRunning {
		select {
		case <-ctx.Done():
			return run.ID, ctx.Err()
		case <-ticker.C:
		}
		if r, found := c.s.cfg.Sandbox.GetRun(run.ID); found {
			run = r
		}
	}
	if run.Status == model.RunNeedsConfirmation {
		return run.ID, fmt.Errorf("the deploy would destroy %d deployed resource(s); review and confirm it in the Deploy tab", len(run.Destructive))
	}
	if run.Status != model.RunSucceeded {
		return run.ID, fmt.Errorf("%s (see the Deploy tab)", cmp.Or(run.Error, string(run.Action)+" "+string(run.Status)))
	}
	return run.ID, nil
}

// applyTerraform starts the workflow's Floci sandbox and applies it, as
// the Deploy tab does.
func (c challengeRuntime) applyTerraform(ctx context.Context, wf *model.Workflow) (*model.Run, error) {
	for _, d := range wf.Diagnostics {
		if d.Severity == model.SeverityError {
			return nil, fmt.Errorf("the workflow has errors; fix them before deploying: %s", d.Message)
		}
	}
	if _, err := c.s.cfg.Sandbox.Start(ctx, wf.ID, sandboxClouds(wf)); err != nil {
		return nil, fmt.Errorf("start the sandbox: %w", err)
	}
	files, err := c.s.exportTerraform(wf, false)
	if err != nil {
		return nil, err
	}
	env, err := environment(wf, "")
	if err != nil {
		return nil, err
	}
	return c.s.cfg.Sandbox.Run(ctx, sandbox.RunRequest{Workflow: wf, Action: model.ActionApply, Files: files, Environment: env})
}

// Target finds the host address of a compose service's container port, a
// Kubernetes Service port (its port-forward, or the Ingress rule routing
// to it) or a Terraform node's endpoint.
func (c challengeRuntime) Target(ctx context.Context, wfID string, t model.ChallengeTarget) (model.LoadTarget, error) {
	wf, err := c.workflow(wfID)
	if err != nil {
		return model.LoadTarget{}, err
	}
	method := cmp.Or(t.Method, http.MethodGet)
	target := func(nodeID, base string, headers map[string]string) model.LoadTarget {
		if u, err := url.Parse(base); err == nil && !wf.IsTerraform() {
			base = u.Scheme + "://" + u.Host
		}
		base = strings.TrimSuffix(base, "/")
		return model.LoadTarget{
			Kind: model.TargetHTTP, NodeID: nodeID,
			Label: fmt.Sprintf("%s %s · %s", method, t.Path, t.Service),
			HTTP:  &model.HTTPTarget{Method: method, URL: base + t.Path, Headers: headers},
		}
	}
	switch {
	case wf.IsDocker():
		rt, err := c.s.composeRuntime(ctx, wf)
		if err != nil {
			return model.LoadTarget{}, err
		}
		for _, svc := range rt.Status.Services {
			if svc.Name != t.Service {
				continue
			}
			for _, ct := range svc.Containers {
				for _, p := range ct.Ports {
					if ct.State == "running" && p.Container == t.Port && p.Host > 0 && p.URL != "" {
						return target(svc.NodeID, p.URL, nil), nil
					}
				}
			}
			return model.LoadTarget{}, fmt.Errorf("service %s does not publish port %d on a running container: publish it (ports: [\"<host>:%d\"]) and deploy", t.Service, t.Port, t.Port)
		}
		return model.LoadTarget{}, fmt.Errorf("service %s is not running: it must exist in the compose file and be deployed", t.Service)
	case wf.IsKubernetes():
		st, err := c.s.cfg.Sandbox.KubeStatus(ctx, wfID)
		if err != nil {
			return model.LoadTarget{}, err
		}
		if t.Ingress {
			for _, u := range st.Ingresses {
				if u.Service != t.Service || u.Address == "" {
					continue
				}
				var headers map[string]string
				if u.Host != "" {
					headers = map[string]string{"Host": u.Host}
				}
				return target(u.ServiceNodeID, u.Address, headers), nil
			}
			return model.LoadTarget{}, fmt.Errorf("no Ingress rule routes to Service %s (or the ingress controller isn't ready): keep the Ingress and deploy", t.Service)
		}
		for _, f := range st.Forwards {
			if f.Service == t.Service && f.Port == t.Port && f.URL != "" {
				return target(f.NodeID, f.URL, nil), nil
			}
		}
		return model.LoadTarget{}, fmt.Errorf("Service %s port %d is not forwarded (no ready pod?): check the Deploy tab", t.Service, t.Port)
	}
	node, err := findNode(wf, t.Service)
	if err != nil {
		return model.LoadTarget{}, err
	}
	st, err := c.s.cfg.Sandbox.State(ctx, wf)
	if err != nil {
		return model.LoadTarget{}, err
	}
	for _, lt := range loadtest.Suggest(wf, st, c.s.cfg.Sandbox, c.s.sandboxInstances(ctx, wf, st)) {
		if lt.NodeID == node.ID && lt.HTTP != nil {
			return target(node.ID, terraformBase(lt.HTTP.URL), lt.HTTP.Headers), nil
		}
	}
	return model.LoadTarget{}, fmt.Errorf("%s has no HTTP endpoint in the sandbox (the Load test tab lists none for it)", t.Service)
}

// NodeID resolves a compose service or Kubernetes workload name to its
// graph node.
func (c challengeRuntime) NodeID(ctx context.Context, wfID, name string) (string, error) {
	wf, err := c.workflow(wfID)
	if err != nil {
		return "", err
	}
	if wf.IsDocker() && wf.Compose != nil {
		for _, n := range wf.Compose.Graph.Nodes {
			if n.Kind == model.ComposeService && n.Name == name && !n.Inactive {
				return n.ID, nil
			}
		}
		return "", fmt.Errorf("no compose service named %s: keep its name so the scenario can find it", name)
	}
	if wf.IsKubernetes() && wf.Kubernetes != nil {
		for _, n := range wf.Kubernetes.Graph.Nodes {
			if (n.Kind == "Deployment" || n.Kind == "StatefulSet") && n.Name == name {
				return n.ID, nil
			}
		}
	}
	return "", fmt.Errorf("no Deployment or StatefulSet named %s: keep its name so the scenario can find it", name)
}

// apiGatewayBase matches an emulated API Gateway invoke URL's prefix
// (/execute-api/<api id>/<stage>).
var apiGatewayBase = regexp.MustCompile(`^(.*/execute-api/[^/]+/[^/]+)`)

// terraformBase is the base a check's path is appended to for a sandbox
// endpoint the Load test tab lists: the invoke URL up to its stage for an
// API Gateway, else the endpoint's folder (S3 website, function URL).
func terraformBase(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	if m := apiGatewayBase.FindStringSubmatch(u.Path); m != nil {
		u.Path = m[1]
	} else {
		u.Path = path.Dir(u.Path)
	}
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimSuffix(u.String(), "/")
}
