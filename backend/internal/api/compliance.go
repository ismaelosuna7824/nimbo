package api

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compliance"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/netcheck"
)

// workflowComplianceView is GET /workflows/{id}/compliance.
type workflowComplianceView struct {
	// Frameworks are the benchmarks for the workflow's platforms (its
	// Terraform clouds, docker or Kubernetes).
	Frameworks []compliance.FrameworkInfo `json:"frameworks"`
	// Results grades the workflow against each of them (only the one asked
	// for with ?framework=).
	Results    []compliance.Result `json:"results"`
	Disclaimer string              `json:"disclaimer"`
}

// workflowCompliance is GET /workflows/{id}/compliance: the frameworks that
// apply to the workflow and its grade against each (?framework=: just that
// one).
func (s *Server) workflowCompliance(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	all := compliance.ForWorkflow(wf)
	fws := all
	if id := r.URL.Query().Get("framework"); id != "" {
		f, err := workflowFramework(wf, id)
		if err != nil {
			return err
		}
		fws = []compliance.Framework{f}
	}
	results, err := s.complianceResults(r.Context(), wf, fws)
	if err != nil {
		return err
	}
	view := workflowComplianceView{Frameworks: []compliance.FrameworkInfo{}, Results: results, Disclaimer: compliance.Disclaimer}
	for _, f := range all {
		view.Frameworks = append(view.Frameworks, f.Info())
	}
	return ok(w, view)
}

// complianceReport is GET /workflows/{id}/compliance/report?framework=&format=md:
// the Markdown report of one framework (default: the first that applies).
func (s *Server) complianceReport(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if f := q.Get("format"); f != "" && f != "md" {
		return errorf(http.StatusBadRequest, "format %q is not supported (md)", f)
	}
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	var f compliance.Framework
	if id := q.Get("framework"); id != "" {
		if f, err = workflowFramework(wf, id); err != nil {
			return err
		}
	} else if fws := compliance.ForWorkflow(wf); len(fws) > 0 {
		f = fws[0]
	} else {
		return errorf(http.StatusNotFound, "no compliance framework applies to %s", wf.Name)
	}
	results, err := s.complianceResults(r.Context(), wf, []compliance.Framework{f})
	if err != nil {
		return err
	}
	now := time.Now()
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": reportFilename(wf.Name, f.ID, now)}))
	_, err = w.Write([]byte(compliance.Markdown(wf.Name, results[0], now)))
	return err
}

// reportFilename names an exported report: "<workflow>-<framework>-<date>.md".
func reportFilename(workflow, framework string, now time.Time) string {
	slug := strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' {
			return r
		}
		return '-'
	}, strings.ToLower(workflow)), "-")
	if slug == "" {
		slug = "workflow"
	}
	return fmt.Sprintf("%s-%s-%s.md", slug, framework, now.Format("2006-01-02"))
}

// attestControl is PUT /workflows/{id}/compliance/attestations/{framework}/{control}
// {note, by?}: the user marks a manual control as done.
func (s *Server) attestControl(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Note string `json:"note"`
		By   string `json:"by"`
	}
	if err := decode(w, r, maxBody, &in); err != nil {
		return err
	}
	if strings.TrimSpace(in.Note) == "" {
		return errorf(http.StatusBadRequest, "note is required: say how the control is met (it goes into the report)")
	}
	wf, key, err := s.manualControl(r)
	if err != nil {
		return err
	}
	a := compliance.Attestation{Note: in.Note, By: in.By, At: time.Now().UTC()}
	if err := compliance.Attest(s.cfg.Home, wf.ID, key, a); err != nil {
		return err
	}
	att, err := compliance.LoadAttestations(s.cfg.Home, wf.ID)
	if err != nil {
		return err
	}
	return ok(w, att[key])
}

// unattestControl is DELETE /workflows/{id}/compliance/attestations/{framework}/{control}.
func (s *Server) unattestControl(w http.ResponseWriter, r *http.Request) error {
	wf, key, err := s.manualControl(r)
	if err != nil {
		return err
	}
	if err := compliance.Unattest(s.cfg.Home, wf.ID, key); err != nil {
		return err
	}
	return noContent(w)
}

// manualControl resolves the workflow and the attestation key of the
// request's {framework}/{control}, which must be a manual control of a
// framework that applies to the workflow.
func (s *Server) manualControl(r *http.Request) (*model.Workflow, string, error) {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return nil, "", err
	}
	f, err := workflowFramework(wf, r.PathValue("framework"))
	if err != nil {
		return nil, "", err
	}
	id := r.PathValue("control")
	i := slices.IndexFunc(f.Controls, func(c compliance.Control) bool { return c.ID == id })
	if i < 0 {
		return nil, "", errorf(http.StatusNotFound, "%s has no control %q", f.Name, id)
	}
	if !f.Controls[i].Manual {
		return nil, "", errorf(http.StatusBadRequest, "control %s is checked on the design; only manual controls can be attested", id)
	}
	return wf, compliance.Key(f.ID, id), nil
}

// workflowFramework returns framework id when it applies to wf.
func workflowFramework(wf *model.Workflow, id string) (compliance.Framework, error) {
	f, ok := compliance.Lookup(id)
	if !ok {
		return f, errorf(http.StatusNotFound, "framework %q not found", id)
	}
	if !slices.Contains(compliance.Platforms(wf), f.Platform) {
		return f, errorf(http.StatusBadRequest, "%s is for %s; %s doesn't use it", f.Name, f.Platform, wf.Name)
	}
	return f, nil
}

// complianceResults grades wf against fws on fresh diagnostics: the files
// may have changed outside the app, and benchmark rules run even when they
// are opt-in (compliance.GradingSettings).
func (s *Server) complianceResults(ctx context.Context, wf *model.Workflow, fws []compliance.Framework) ([]compliance.Result, error) {
	att, err := compliance.LoadAttestations(s.cfg.Home, wf.ID)
	if errors.Is(err, compliance.ErrInvalidID) {
		return nil, errorf(http.StatusBadRequest, "%v", err)
	}
	if err != nil {
		return nil, err
	}
	out := []compliance.Result{}
	if len(fws) == 0 {
		return out, nil
	}
	cfg := s.bestPracticeSettings()
	diags := s.gradingDiagnostics(ctx, wf, compliance.GradingSettings(cfg, fws...))
	for _, f := range fws {
		out = append(out, compliance.Evaluate(f, wf, diags, cfg, att))
	}
	return out, nil
}

// gradingDiagnostics re-derives wf (docker: its compose graph, Kubernetes:
// its rendered objects) and returns its best-practice and network findings
// under cfg.
func (s *Server) gradingDiagnostics(ctx context.Context, wf *model.Workflow, cfg model.BestPracticeSettings) []model.Diagnostic {
	dir := s.cfg.Store.AssetsDir(wf.ID)
	switch {
	case wf.IsDocker():
		return compose.Derive(ctx, dir, wf, cfg)
	case wf.IsKubernetes():
		p, diags := kube.Derive(ctx, dir, wf)
		return append(diags, kuberules.Check(p, wf, cfg)...)
	}
	var diags []model.Diagnostic
	if !cfg.Disabled {
		diags = lint.Check(wf, s.cfg.Catalog, cfg, s.localModules(wf))
	}
	return append(diags, netcheck.Check(wf)...)
}
