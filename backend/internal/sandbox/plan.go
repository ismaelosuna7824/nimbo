package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// planFile is where plan runs save the plan for `terraform show -json`
// (relative to the workdir). .nimbo is daemon-owned, so workdir syncs never
// touch it; the file is removed after the run because it holds variable
// values, secrets included.
const planFile = ".nimbo/plan.tfplan"

// readPlanFile maps the plan a run saved as file (relative to the
// workdir) onto wf's nodes.
func (m *Manager) readPlanFile(ctx context.Context, wf *model.Workflow, file string) ([]model.PlannedChange, error) {
	raw, err := m.showJSON(ctx, wf.ID, file)
	if err != nil {
		return nil, err
	}
	return parsePlan(raw, wf)
}

type tfResourceChange struct {
	Address       string `json:"address"`
	ModuleAddress string `json:"module_address"`
	Mode          string `json:"mode"`
	Type          string `json:"type"`
	Name          string `json:"name"`
	Index         any    `json:"index"`
	ActionReason  string `json:"action_reason"`
	Change        struct {
		Actions         []string       `json:"actions"`
		Before          map[string]any `json:"before"`
		After           map[string]any `json:"after"`
		AfterUnknown    any            `json:"after_unknown"`
		BeforeSensitive any            `json:"before_sensitive"`
		AfterSensitive  any            `json:"after_sensitive"`
		ReplacePaths    [][]any        `json:"replace_paths"`
	} `json:"change"`
}

// parsePlan maps the `resource_changes` of `terraform show -json <planfile>`
// onto wf's nodes, in Terraform's (address) order.
func parsePlan(data []byte, wf *model.Workflow) ([]model.PlannedChange, error) {
	var doc struct {
		ResourceChanges []tfResourceChange `json:"resource_changes"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse terraform plan: %w", err)
	}
	nodes := newNodeIndex(wf)
	out := make([]model.PlannedChange, 0, len(doc.ResourceChanges))
	for _, rc := range doc.ResourceChanges {
		pc := model.PlannedChange{
			Address: rc.Address,
			NodeID:  nodes.of(rc.ModuleAddress, rc.Mode, rc.Type, rc.Name),
			Mode:    rc.Mode, Type: rc.Type, Name: rc.Name, Index: rc.Index,
			Action: planAction(rc.Change.Actions),
			Reason: rc.ActionReason,
		}
		switch pc.Action {
		case model.PlanCreate, model.PlanUpdate, model.PlanReplace:
			pc.Attributes = attributeChanges(rc)
		}
		out = append(out, pc)
	}
	return out, nil
}

// planAction folds Terraform's action list into one action.
func planAction(actions []string) model.PlanAction {
	switch {
	case slices.Equal(actions, []string{"create"}):
		return model.PlanCreate
	case slices.Equal(actions, []string{"update"}):
		return model.PlanUpdate
	case slices.Equal(actions, []string{"delete"}):
		return model.PlanDelete
	case slices.Equal(actions, []string{"read"}):
		return model.PlanRead
	case len(actions) == 2 && slices.Contains(actions, "delete") && slices.Contains(actions, "create"):
		return model.PlanReplace
	default:
		// "no-op", and "forget" (removed blocks keep the object running).
		return model.PlanNoOp
	}
}

// attributeChanges lists the top-level attributes a create, update or
// replace sets: for a create every attribute with a value, otherwise those
// whose value differs or becomes unknown.
func attributeChanges(rc tfResourceChange) []model.AttributeChange {
	c := rc.Change
	names := map[string]bool{}
	for k := range c.After {
		names[k] = true
	}
	if unknown, ok := c.AfterUnknown.(map[string]any); ok {
		for k := range unknown {
			names[k] = true
		}
	}
	for k := range c.Before {
		names[k] = true
	}
	forces := map[string]bool{}
	for _, p := range c.ReplacePaths {
		if len(p) > 0 {
			if k, ok := p[0].(string); ok {
				forces[k] = true
			}
		}
	}
	var out []model.AttributeChange
	for _, k := range slices.Sorted(maps.Keys(names)) {
		before, after := c.Before[k], c.After[k]
		unknown := maskAt(c.AfterUnknown, k)
		if !containsTrue(unknown) && reflect.DeepEqual(before, after) {
			continue
		}
		if c.Before == nil && isEmpty(after) && !containsTrue(unknown) {
			continue // a create leaves the attribute unset (empty nested blocks too, as Terraform's own diff does)
		}
		ac := model.AttributeChange{Name: k, ForcesReplacement: forces[k]}
		switch {
		case containsTrue(maskAt(c.BeforeSensitive, k)) || containsTrue(maskAt(c.AfterSensitive, k)):
			ac.Sensitive = true
		case unknown == true:
			ac.Before, ac.Unknown = before, true
		default:
			ac.Before, ac.After = before, fillUnknown(after, unknown)
		}
		out = append(out, ac)
	}
	return out
}

func isEmpty(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

// maskAt returns the part of a sensitivity/unknown mask for attribute k: a
// mask is true for the whole object, or mirrors its structure.
func maskAt(mask any, k string) any {
	switch m := mask.(type) {
	case bool:
		return m
	case map[string]any:
		return m[k]
	}
	return nil
}

func containsTrue(mask any) bool {
	switch m := mask.(type) {
	case bool:
		return m
	case map[string]any:
		for _, v := range m {
			if containsTrue(v) {
				return true
			}
		}
	case []any:
		for _, v := range m {
			if containsTrue(v) {
				return true
			}
		}
	}
	return false
}

// fillUnknown puts model.UnknownValue where the unknown mask marks parts of
// v that Terraform leaves out of the planned value.
func fillUnknown(v, mask any) any {
	switch m := mask.(type) {
	case bool:
		if m {
			return model.UnknownValue
		}
	case map[string]any:
		if !containsTrue(m) {
			return v
		}
		obj, _ := v.(map[string]any)
		out := maps.Clone(obj)
		if out == nil {
			out = map[string]any{}
		}
		for k, sub := range m {
			if containsTrue(sub) {
				out[k] = fillUnknown(obj[k], sub)
			}
		}
		return out
	case []any:
		if !containsTrue(m) {
			return v
		}
		list, _ := v.([]any)
		out := make([]any, max(len(list), len(m)))
		copy(out, list)
		for i, sub := range m {
			if containsTrue(sub) {
				out[i] = fillUnknown(out[i], sub)
			}
		}
		return out
	}
	return v
}
