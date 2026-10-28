package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// An apply plans first and applies exactly that saved plan. When the plan
// would delete or replace resource instances that exist in the sandbox, the
// run ends RunNeedsConfirmation instead and keeps the plan as
// pendingPlanFile(run ID); a later apply with RunRequest.ConfirmDestroy
// applies that file as is, so the user gets what they reviewed. The plan
// file holds variable values (secrets included): it lives in the
// daemon-owned .nimbo directory and is removed once applied or superseded
// (the next apply or destroy, a sandbox reset).

// errNeedsConfirmation ends an apply whose plan is destructive.
var errNeedsConfirmation = errors.New("the plan deletes or replaces deployed resources")

// ErrNotPending is returned for a confirmation that names a run which is
// not an apply waiting for confirmation.
var ErrNotPending = errors.New("run is not an apply waiting for confirmation")

// driftCheckTimeout bounds the post-deploy check plan.
const driftCheckTimeout = 5 * time.Minute

// driftPlanFile is where the post-deploy check saves its plan.
const driftPlanFile = ".nimbo/check.tfplan"

func pendingPlanFile(runID string) string { return ".nimbo/pending-" + runID + ".tfplan" }

// pendingConfigFile holds configDigest of the run a pending plan was made
// from.
func pendingConfigFile(runID string) string { return ".nimbo/pending-" + runID + ".config" }

// removePendingPlans deletes every plan waiting for confirmation in work.
func removePendingPlans(work string) {
	paths, _ := filepath.Glob(filepath.Join(work, ".nimbo", "pending-*"))
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

// configDigest identifies the configuration a run plans: the exported
// Terraform and the environment's variable values (the Floci wiring
// follows from them).
func configDigest(req RunRequest) string {
	h := sha256.New()
	for _, name := range slices.Sorted(maps.Keys(req.Files)) {
		h.Write([]byte(name + "\x00"))
		h.Write(req.Files[name])
		h.Write([]byte{0})
	}
	h.Write([]byte(tfvarsFile + "\x00"))
	h.Write(tfvarsFileContent(req.Environment))
	return hex.EncodeToString(h.Sum(nil))
}

// pendingRun resolves the run a confirmed apply refers to: id, or with id
// "" the workflow's newest apply or destroy run when it waits for
// confirmation (nil when it doesn't, so the apply plans again; validate
// and plan runs change nothing). Callers hold m.mu.
func (m *Manager) pendingRun(wfID, id string) (*model.Run, error) {
	if id != "" {
		rs, ok := m.runs[id]
		if !ok || rs.run.WorkflowID != wfID || rs.run.Action != model.ActionApply || rs.run.Status != model.RunNeedsConfirmation {
			return nil, fmt.Errorf("%w: %q", ErrNotPending, id)
		}
		r := rs.run
		return &r, nil
	}
	var newest *runState
	for _, rs := range m.runs {
		if rs.run.WorkflowID == wfID && (rs.run.Action == model.ActionApply || rs.run.Action == model.ActionDestroy) && (newest == nil || rs.run.CreatedAt.After(newest.run.CreatedAt)) {
			newest = rs
		}
	}
	if newest == nil || newest.run.Action != model.ActionApply || newest.run.Status != model.RunNeedsConfirmation {
		return nil, nil
	}
	r := newest.run
	return &r, nil
}

// staleConfirmation says why the confirmed run's plan can't be applied as
// is ("" when it can): the configuration or environment changed since (card
// moves and other layout edits don't count), or the plan is gone.
func staleConfirmation(confirmed *model.Run, req RunRequest, work string) string {
	env := ""
	if req.Environment != nil {
		env = req.Environment.Name
	}
	if confirmed.Environment != env {
		return "The environment differs from the plan you confirmed"
	}
	if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(pendingPlanFile(confirmed.ID)))); err != nil {
		return "The plan you confirmed is no longer available (another run replaced it)"
	}
	if digest, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(pendingConfigFile(confirmed.ID)))); err != nil || string(digest) != configDigest(req) {
		return "The workflow changed since the plan you confirmed"
	}
	return ""
}

// applySteps is the apply after `terraform init`.
func (m *Manager) applySteps(ctx context.Context, rs *runState, req RunRequest, work string, env, userEnv []string) error {
	wf := req.Workflow
	m.mu.Lock()
	id, confirmed := rs.run.ID, rs.confirmed
	m.mu.Unlock()

	if confirmed != nil {
		if why := staleConfirmation(confirmed, req, work); why != "" {
			m.logLine(rs, "system", why+": planning again. Review the changes before confirming.")
		} else {
			m.mu.Lock()
			rs.run.PlanRunID = confirmed.ID
			m.mu.Unlock()
			defer removePendingPlans(work)
			m.logLine(rs, "system", "Applying the plan confirmed in run "+confirmed.ID)
			return m.applyPlan(ctx, rs, req, work, env, userEnv, pendingPlanFile(confirmed.ID))
		}
	} else if req.ConfirmDestroy {
		m.logLine(rs, "system", "No plan is waiting for confirmation: planning again.")
	}

	removePendingPlans(work)
	file := pendingPlanFile(id)
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(filepath.Join(work, filepath.FromSlash(file)))
		}
	}()
	args := []string{"plan", "-input=false", "-no-color", "-out=" + file}
	m.logLine(rs, "system", "$ terraform "+strings.Join(args, " "))
	code, err := m.terraform(ctx, rs, "plan", env, userEnv, args...)
	if err != nil {
		return err
	}
	if code != 0 {
		m.setExit(rs, code)
		return fmt.Errorf("terraform plan exited with code %d", code)
	}
	changes, err := m.readPlanFile(ctx, wf, file)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Without the per-resource changes only the summary tells whether
		// anything is destroyed.
		m.mu.Lock()
		p := rs.run.Plan
		m.mu.Unlock()
		if p == nil || p.Destroy > 0 {
			return fmt.Errorf("could not read the plan to check it for destroyed resources: %w", err)
		}
		m.logLine(rs, "system", "Warning: could not read the planned changes: "+err.Error())
	}
	if d := destructiveChanges(changes); len(d) > 0 {
		if err := writeFile(filepath.Join(work, filepath.FromSlash(pendingConfigFile(id))), []byte(configDigest(req))); err != nil {
			return fmt.Errorf("save the plan to confirm: %w", err)
		}
		keep = true
		m.mu.Lock()
		rs.run.Changes, rs.run.Destructive = changes, d
		m.mu.Unlock()
		return errNeedsConfirmation
	}
	m.mu.Lock()
	rs.run.Changes = changes
	m.mu.Unlock()
	return m.applyPlan(ctx, rs, req, work, env, userEnv, file)
}

// applyPlan applies a saved plan, removes it and runs the post-deploy check.
func (m *Manager) applyPlan(ctx context.Context, rs *runState, req RunRequest, work string, env, userEnv []string, file string) error {
	defer os.Remove(filepath.Join(work, filepath.FromSlash(file)))
	args := []string{"apply", "-input=false", "-no-color", file}
	m.logLine(rs, "system", "$ terraform "+strings.Join(args, " "))
	code, err := m.terraform(ctx, rs, "apply", env, userEnv, args...)
	if err != nil {
		return err
	}
	m.setExit(rs, code)
	if code != 0 {
		return fmt.Errorf("terraform apply exited with code %d", code)
	}
	m.mu.Lock()
	rs.applied = true
	p := rs.run.Plan
	m.mu.Unlock()
	if p == nil || p.Add+p.Change+p.Destroy == 0 {
		return nil // nothing changed: nothing new to check
	}
	m.checkDrift(ctx, rs, req.Workflow, work, env, userEnv)
	return nil
}

// checkDrift plans again right after a successful apply and records what
// would still change (Run.Drift). It never fails the run: its Terraform
// output stays out of the log, which gets one summary line.
func (m *Manager) checkDrift(ctx context.Context, rs *runState, wf *model.Workflow, work string, env, userEnv []string) {
	cctx, cancel := context.WithTimeout(ctx, driftCheckTimeout)
	defer cancel()
	defer os.Remove(filepath.Join(work, filepath.FromSlash(driftPlanFile)))
	var mu sync.Mutex
	var last []string // the last output lines, for a failure
	code, err := m.terraformTo(cctx, rs, "check", env, userEnv, func(_, line string) {
		if line = strings.TrimSpace(line); line != "" {
			mu.Lock()
			last = append(last[max(0, len(last)-2):], line)
			mu.Unlock()
		}
	}, "plan", "-input=false", "-no-color", "-detailed-exitcode", "-out="+driftPlanFile)
	skip := func(why string) { m.logLine(rs, "system", "Post-deploy check skipped: "+why) }
	switch {
	case ctx.Err() != nil:
		skip("canceled.")
		return
	case cctx.Err() != nil:
		skip("the plan took longer than " + driftCheckTimeout.String() + ".")
		return
	case err != nil:
		skip(err.Error())
		return
	case code == 0:
		m.logLine(rs, "system", "Post-deploy check: a new plan has no changes.")
		return
	case code != 2:
		skip("terraform plan exited with code " + strconv.Itoa(code) + ": " + strings.Join(last, " "))
		return
	}
	raw, err := m.showJSON(cctx, wf.ID, driftPlanFile)
	if err != nil {
		skip(err.Error())
		return
	}
	drift, err := driftChanges(raw, wf)
	if err != nil {
		skip(err.Error())
		return
	}
	if len(drift) == 0 {
		m.logLine(rs, "system", "Post-deploy check: a new plan changes no resources.")
		return
	}
	m.mu.Lock()
	rs.run.Drift = drift
	m.mu.Unlock()
	items := make([]string, len(drift))
	for i, d := range drift {
		items[i] = d.Address
		if len(d.Attributes) > 0 {
			items[i] += " (" + strings.Join(d.Attributes, ", ") + ")"
		}
	}
	m.logLine(rs, "system", fmt.Sprintf("Warning: post-deploy check: %d resource(s) still change after deploy, so the next deploy would change them again: %s", len(drift), strings.Join(items, "; ")))
}

// destructiveChanges picks the planned deletes and replacements of managed
// resource instances: a plan only deletes or replaces what the (refreshed)
// state holds, so each is a deployed instance.
func destructiveChanges(changes []model.PlannedChange) []model.DestructiveChange {
	var out []model.DestructiveChange
	for _, c := range changes {
		if c.Mode != "managed" || (c.Action != model.PlanDelete && c.Action != model.PlanReplace) {
			continue
		}
		d := model.DestructiveChange{Address: c.Address, NodeID: c.NodeID, Action: c.Action, Cause: c.Reason}
		for _, a := range c.Attributes {
			if a.ForcesReplacement {
				d.Reasons = append(d.Reasons, a.Name)
			}
		}
		out = append(out, d)
	}
	return out
}

// driftChanges lists the resources the plan saved by the post-deploy check
// still changes, each with its likely cause.
func driftChanges(data []byte, wf *model.Workflow) ([]model.DriftChange, error) {
	changes, err := parsePlan(data, wf)
	if err != nil {
		return nil, err
	}
	defaultTags := false
	if src, err := json.Marshal(wf.Providers); err == nil {
		defaultTags = strings.Contains(string(src), "default_tags")
	}
	var out []model.DriftChange
	for _, c := range changes {
		if c.Mode != "managed" || c.Action == model.PlanNoOp || c.Action == model.PlanRead {
			continue
		}
		d := model.DriftChange{Address: c.Address, NodeID: c.NodeID, Action: c.Action, Attributes: []string{}}
		if c.Action == model.PlanUpdate || c.Action == model.PlanReplace {
			for _, a := range c.Attributes {
				d.Attributes = append(d.Attributes, a.Name)
			}
		}
		d.Cause, d.Hint = driftCause(c, nodeUsesDynamicValue(wf, c.NodeID), defaultTags)
		out = append(out, d)
	}
	return out, nil
}

// dynamicFuncs return a new value on every plan.
var dynamicFuncs = []string{"timestamp()", "uuid()", "bcrypt("}

func nodeUsesDynamicValue(wf *model.Workflow, nodeID string) bool {
	if nodeID == "" {
		return false
	}
	for i := range wf.Nodes {
		if wf.Nodes[i].ID != nodeID {
			continue
		}
		src, err := json.Marshal(wf.Nodes[i].Body)
		if err != nil {
			return false
		}
		return slices.ContainsFunc(dynamicFuncs, func(f string) bool { return strings.Contains(string(src), f) })
	}
	return false
}

// driftCause guesses why c keeps changing: the common perpetual diffs of
// providers, else a generic fix.
func driftCause(c model.PlannedChange, dynamic, defaultTags bool) (cause, hint string) {
	switch c.Action {
	case model.PlanCreate:
		return model.DriftOther, "Terraform no longer finds the resource it just created: the sandbox may not keep this resource type, or something else deletes it."
	case model.PlanDelete:
		return model.DriftOther, "Terraform plans to delete the resource it just applied: check for duplicate or conflicting blocks managing it."
	}
	var names, jsonDocs, unknown, tags []string
	for _, a := range c.Attributes {
		names = append(names, a.Name)
		if a.Unknown {
			unknown = append(unknown, a.Name)
		}
		if isJSONDoc(a.Before) || isJSONDoc(a.After) {
			jsonDocs = append(jsonDocs, a.Name)
		}
		if a.Name == "tags" || a.Name == "tags_all" {
			tags = append(tags, a.Name)
		}
	}
	list := func(s []string) string { return strings.Join(s, ", ") }
	switch {
	case len(names) == 0:
		return model.DriftOther, "The provider reads this resource back differently from what was applied: compare its configuration with the deployed values, or add lifecycle { ignore_changes } for the attributes that differ."
	case len(jsonDocs) > 0:
		return model.DriftJSONNormalization, "The provider normalizes the JSON in " + list(jsonDocs) +
			": build it with jsonencode() (or an aws_iam_policy_document data source for IAM policies) instead of a literal string, so it matches what the cloud returns."
	case dynamic || len(unknown) > 0:
		attrs := list(orElse(unknown, names))
		return model.DriftDynamicValue, "A value that is new on every plan (timestamp(), uuid(), …) feeds " + attrs +
			": pass it in as a variable, or add lifecycle { ignore_changes = [" + attrs + "] }."
	case defaultTags && len(tags) > 0:
		return model.DriftDefaultTags, "The resource's tags repeat the provider's default_tags: set each tag in one place (drop it from the resource's tags or from default_tags)."
	}
	return model.DriftOther, "The provider stores " + list(names) + " differently from the configuration: set the value the provider returns, or add lifecycle { ignore_changes = [" + list(names) + "] } if the difference doesn't matter."
}

// orElse returns a when it is not empty, else b.
func orElse(a, b []string) []string {
	if len(a) > 0 {
		return a
	}
	return b
}

// isJSONDoc reports a string holding a JSON object or array.
func isJSONDoc(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
		return false
	}
	return json.Valid([]byte(s))
}
