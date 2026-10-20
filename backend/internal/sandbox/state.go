package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func emptyState(wfID string) *model.DeployedState {
	return &model.DeployedState{
		WorkflowID: wfID,
		Resources:  []model.DeployedResource{},
		Outputs:    map[string]any{},
		UpdatedAt:  time.Now().UTC(),
	}
}

// State reads the sandbox Terraform state with `terraform show -json`.
func (m *Manager) State(ctx context.Context, wf *model.Workflow) (*model.DeployedState, error) {
	if wf == nil {
		return nil, errors.New("state: workflow is required")
	}
	if err := checkID(wf.ID); err != nil {
		return nil, err
	}
	work := m.WorkDir(wf.ID)
	info, err := os.Stat(filepath.Join(work, "terraform.tfstate"))
	if os.IsNotExist(err) {
		return emptyState(wf.ID), nil
	} else if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(work, ".terraform")); err != nil {
		return emptyState(wf.ID), nil
	}
	key := stateKey{mod: info.ModTime(), size: info.Size()}
	m.mu.Lock()
	cached, hit := m.stateCache[wf.ID]
	m.mu.Unlock()
	if hit && cached.key == key {
		return m.stateFrom(cached.raw, wf, info)
	}
	image := m.terraformImage()
	if err := m.ensureImage(ctx, image, func(string) {}); err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	for attempt := 0; ; attempt++ {
		name := "nimbo-" + wf.ID + "-tf-show-" + newRunID()
		args := m.terraformArgs(name, wf.ID, "none")
		args = append(args, image, "show", "-json", "-no-color")
		cmd := exec.CommandContext(ctx, m.dockerBin(), args...)
		var stderr bytes.Buffer
		stdout.Reset()
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		pluginCacheMu.RLock()
		err := cmd.Run()
		pluginCacheMu.RUnlock()
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String() + " " + stdout.String())
		// A provider being written into the shared plugin cache by another
		// process fails checksum verification until the write completes.
		if attempt == 0 && strings.Contains(msg, "does not match any of the checksums") {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(3 * time.Second):
			}
			continue
		}
		return nil, fmt.Errorf("terraform show: %s", msg)
	}
	raw := bytes.Clone(stdout.Bytes())
	st, err := m.stateFrom(raw, wf, info)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.stateCache[wf.ID] = stateEntry{key: key, raw: raw}
	m.mu.Unlock()
	return st, nil
}

// stateKey identifies a version of terraform.tfstate.
type stateKey struct {
	mod  time.Time
	size int64
}

// stateEntry caches `terraform show -json` output, which only changes with
// the state file, so repeated reads (endpoints, "Try it") skip the
// Terraform container. Node mapping is redone per call.
type stateEntry struct {
	key stateKey
	raw []byte
}

func (m *Manager) stateFrom(raw []byte, wf *model.Workflow, info os.FileInfo) (*model.DeployedState, error) {
	st, err := parseState(raw, wf)
	if err != nil {
		return nil, err
	}
	st.UpdatedAt = info.ModTime().UTC()
	return st, nil
}

type tfModule struct {
	Address      string       `json:"address"`
	Resources    []tfResource `json:"resources"`
	ChildModules []tfModule   `json:"child_modules"`
}

type tfResource struct {
	Address string         `json:"address"`
	Mode    string         `json:"mode"`
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	Index   any            `json:"index"`
	Values  map[string]any `json:"values"`
}

// ParseState maps recorded `terraform show -json` output onto wf's nodes,
// exactly like State (used by tests replaying recorded sandbox states).
func ParseState(data []byte, wf *model.Workflow) (*model.DeployedState, error) {
	return parseState(data, wf)
}

// parseState maps `terraform show -json` output onto workflow nodes. Root
// resources match the node with the same mode, type and name; resources in
// module calls belong to the root module node.
func parseState(data []byte, wf *model.Workflow) (*model.DeployedState, error) {
	var doc struct {
		Values *struct {
			Outputs map[string]struct {
				Sensitive bool `json:"sensitive"`
				Value     any  `json:"value"`
			} `json:"outputs"`
			RootModule tfModule `json:"root_module"`
		} `json:"values"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse terraform show output: %w", err)
	}
	st := emptyState(wf.ID)
	if doc.Values == nil {
		return st, nil
	}
	for name, o := range doc.Values.Outputs {
		if o.Sensitive {
			st.Outputs[name] = "(sensitive)"
		} else {
			st.Outputs[name] = o.Value
		}
	}

	nodes := map[string]string{} // "resource.type.name" / "data.type.name" / "module.name" -> node ID
	for _, n := range wf.Nodes {
		switch n.Kind {
		case model.KindResource:
			nodes["managed."+n.Type+"."+n.Name] = n.ID
		case model.KindData:
			nodes["data."+n.Type+"."+n.Name] = n.ID
		case model.KindModule:
			nodes["module."+n.Name] = n.ID
		}
	}

	var walk func(mod tfModule)
	walk = func(mod tfModule) {
		for _, r := range mod.Resources {
			dr := model.DeployedResource{
				Address: r.Address, Mode: r.Mode, Type: r.Type, Name: r.Name, Index: r.Index, Values: r.Values,
			}
			if dr.Values == nil {
				dr.Values = map[string]any{}
			}
			if mod.Address == "" {
				dr.NodeID = nodes[r.Mode+"."+r.Type+"."+r.Name]
			} else {
				dr.NodeID = nodes[rootModuleCall(mod.Address)]
			}
			st.Resources = append(st.Resources, dr)
		}
		for _, child := range mod.ChildModules {
			walk(child)
		}
	}
	walk(doc.Values.RootModule)
	return st, nil
}

// rootModuleCall returns "module.<name>" for a module instance address such
// as `module.vpc["a"].module.subnets[0]`.
func rootModuleCall(addr string) string {
	rest := strings.TrimPrefix(addr, "module.")
	end := strings.IndexAny(rest, ".[")
	if end >= 0 {
		rest = rest[:end]
	}
	return "module." + rest
}
