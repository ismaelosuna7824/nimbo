package capacity

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The latest plan of each environment of a workflow lives in
// <home>/workflows/<id>/capacity/<environment>.json (the base: _base.json),
// next to the workflow, so deleting the workflow deletes its plans.

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// baseFile names the plan of the base ("" environment); environment names
// are DNS labels, so they never start with "_".
const baseFile = "_base"

func planPath(home, wfID, env string) (string, bool) {
	if !safeID.MatchString(wfID) || env != "" && !safeID.MatchString(env) {
		return "", false
	}
	name := env
	if name == "" {
		name = baseFile
	}
	return filepath.Join(home, "workflows", wfID, "capacity", name+".json"), true
}

// SavePlan stores p as the latest plan of its workflow and environment.
func SavePlan(home string, p *model.CapacityPlan) error {
	path, ok := planPath(home, p.WorkflowID, p.Environment)
	if !ok {
		return errors.New("invalid workflow or environment name")
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LastPlan returns the latest plan of a workflow's environment ("" = the
// base); ok is false when there is none.
func LastPlan(home, wfID, env string) (*model.CapacityPlan, bool) {
	path, ok := planPath(home, wfID, env)
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var p model.CapacityPlan
	if json.Unmarshal(data, &p) != nil {
		return nil, false
	}
	return &p, true
}
