package netcheck

import (
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// deployedEntities returns the root module's managed resources of st (the
// sandbox's `terraform show -json`), mapped onto wf's nodes. Resources
// inside module calls are not analyzed.
func deployedEntities(wf *model.Workflow, st *model.DeployedState) []*entity {
	nodes := map[string]*model.Node{}
	for i := range wf.Nodes {
		nodes[wf.Nodes[i].ID] = &wf.Nodes[i]
	}
	var out []*entity
	for _, r := range st.Resources {
		if r.Mode != "managed" || strings.HasPrefix(r.Address, "module.") {
			continue
		}
		attrs := r.Values
		if attrs == nil {
			attrs = map[string]any{}
		}
		e := &entity{node: nodes[r.NodeID], typ: r.Type, name: r.Name, addr: r.Address, attrs: attrs}
		if r.Index != nil {
			e.key = fmt.Sprint(r.Index)
		}
		if id, ok := attrs["id"].(string); ok {
			e.id = id
		}
		out = append(out, e)
	}
	return out
}
