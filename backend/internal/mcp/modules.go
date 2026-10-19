package mcp

import (
	"context"
	"errors"
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

type extractModuleIn struct {
	Workflow string   `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Nodes    []string `json:"nodes" jsonschema:"addresses or IDs of the nodes to move into the module, e.g. [\"aws_lambda_function.api\", \"aws_iam_role.api\", \"aws_cloudwatch_log_group.api\"]"`
	Name     string   `json:"name" jsonschema:"module name in snake_case; becomes module.<name> and the directory modules/<name>"`
	DryRun   bool     `json:"dry_run,omitempty" jsonschema:"only report the inputs, outputs, moved blocks and files that would be created"`
}

func (t *tools) extractModule(ctx context.Context, in extractModuleIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if len(in.Nodes) == 0 {
		return nil, errors.New("nodes is required: list the addresses to move into the module")
	}
	ids := make([]string, 0, len(in.Nodes))
	for _, ref := range in.Nodes {
		n, err := findNode(wf, ref)
		if err != nil {
			return nil, err
		}
		ids = append(ids, n.ID)
	}
	req := model.ModuleExtractRequest{NodeIDs: ids, Name: in.Name}
	base := "/workflows/" + pathEscape(wf.ID) + "/modules/extract"
	var preview model.ModuleExtraction
	if err := t.c.do(ctx, http.MethodPost, base+"/preview", req, &preview); err != nil {
		return nil, err
	}
	if in.DryRun {
		return preview, nil
	}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodPost, base, req, &out); err != nil {
		return nil, err
	}
	call, err := findNode(&out, "module."+in.Name)
	if err != nil {
		return nil, err
	}
	return mutationResult(wf, &out, map[string]any{
		"module":     viewNode(&out, call),
		"inputs":     preview.Inputs,
		"outputs":    preview.Outputs,
		"moved":      preview.Moved,
		"providers":  preview.Providers,
		"depends_on": preview.DependsOn,
		"files":      preview.Files,
		"note":       "The root now has moved blocks: a plan of an already deployed workflow should only move resources into module." + in.Name + " (0 to add, 0 to change, 0 to destroy). Edit the module files with get_terraform/export_terraform or in the app's Code tab.",
	}), nil
}

type inlineModuleIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Node     string `json:"node" jsonschema:"module call address (module.vpc) or ID"`
}

func (t *tools) inlineModule(ctx context.Context, in inlineModuleIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	node, err := findNode(wf, in.Node)
	if err != nil {
		return nil, err
	}
	before := map[string]bool{}
	for _, n := range wf.Nodes {
		before[n.ID] = true
	}
	movedBefore := map[string]bool{}
	for _, m := range movedOf(wf) {
		movedBefore[m.From+"->"+m.To] = true
	}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/modules/"+pathEscape(node.ID)+"/inline", nil, &out); err != nil {
		return nil, err
	}
	var added []nodeView
	for i := range out.Nodes {
		if n := &out.Nodes[i]; !before[n.ID] {
			added = append(added, viewNode(&out, n))
		}
	}
	moved := []model.MovedAddress{}
	for _, m := range movedOf(&out) {
		if !movedBefore[m.From+"->"+m.To] {
			moved = append(moved, m)
		}
	}
	return mutationResult(wf, &out, map[string]any{
		"removed":     address(node),
		"added_nodes": added,
		"moved":       moved,
	}), nil
}

// movedOf lists the workflow's root moved blocks.
func movedOf(wf *model.Workflow) []model.MovedAddress {
	var out []model.MovedAddress
	for _, b := range wf.Other {
		if b.Type != "moved" {
			continue
		}
		var m model.MovedAddress
		for _, a := range b.Body.Attrs {
			switch a.Name {
			case "from":
				m.From = a.Value.Expr
			case "to":
				m.To = a.Value.Expr
			}
		}
		out = append(out, m)
	}
	return out
}
