package lint

import (
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// untagged reports whether a taggable resource sets no tags/labels (a
// literal empty map counts as none).
func (p *pass) untagged(n *model.Node, attr string) bool {
	if !p.schemaHas(n, attr) {
		return false
	}
	a := getAttr(&n.Body, attr)
	if a == nil {
		return true
	}
	m, ok := literal(a.Value).(map[string]any)
	return ok && len(m) == 0
}

func checkMissingTags(p *pass) {
	// AWS and GCP have provider-level defaults: one finding per provider
	// configuration. Azure has none: one finding per resource.
	type group struct {
		key   string
		nodes []*model.Node
	}
	var groups []*group
	byKey := map[string]*group{}
	for _, n := range p.resources() {
		cloud, _ := p.cat.CloudOf(n.Type)
		switch cloud {
		case model.CloudAWS, model.CloudGCP:
			attr, defaults := "tags", "default_tags"
			if cloud == model.CloudGCP {
				attr, defaults = "labels", "default_labels"
			}
			if !p.untagged(n, attr) {
				continue
			}
			key := providerKey(n)
			if pb := p.providerBlock(key); pb != nil && (len(blocks(&pb.Body, defaults)) > 0 || getAttr(&pb.Body, defaults) != nil) {
				continue
			}
			g := byKey[key]
			if g == nil {
				g = &group{key: key}
				byKey[key] = g
				groups = append(groups, g)
			}
			g.nodes = append(g.nodes, n)
		case model.CloudAzure:
			if p.untagged(n, "tags") {
				p.report(finding{
					node: n, attribute: "tags",
					message: fmt.Sprintf("%s has no tags", addr(n)),
					why:     "Tags tell who owns a resource, which environment it belongs to and what it costs; untagged resources are hard to find, bill and clean up.",
					fix:     "Set tags = local.common_tags (Project, Environment, ManagedBy), merged with resource-specific tags if needed.",
				})
			}
		}
	}
	for _, g := range groups {
		name, _, _ := strings.Cut(g.key, ".")
		what, fix := "tags", fmt.Sprintf("Add default_tags { tags = local.common_tags } to provider %q so every resource gets Project, Environment and ManagedBy, or set tags on each resource.", g.key)
		if name == "google" {
			what, fix = "labels", fmt.Sprintf("Set default_labels = local.common_labels on provider %q, or labels = local.common_labels on each resource (keys and values in lowercase).", g.key)
		}
		noun := "resource has"
		if len(g.nodes) > 1 {
			noun = "resources have"
		}
		p.report(finding{
			node: g.nodes[0], attribute: what,
			message: fmt.Sprintf("%d %s no %s and provider %q sets no defaults: %s", len(g.nodes), noun, what, g.key, list(g.nodes)),
			why:     "Tags/labels tell who owns a resource, which environment it belongs to and what it costs; untagged resources are hard to find, bill and clean up.",
			fix:     fix,
		})
	}
}

func checkNamePrefix(p *pass) {
	if !p.hasLocal("name_prefix") {
		return
	}
	for _, n := range p.resources() {
		attr := p.cat.NamingAttr(n.Type)
		if attr == "" {
			continue
		}
		s, ok := attrString(&n.Body, attr)
		if !ok {
			continue
		}
		p.report(finding{
			node: n, attribute: attr,
			message: fmt.Sprintf("%s.%s is the fixed name %q instead of one derived from local.name_prefix", addr(n), attr, s),
			why:     "Names built from the shared prefix (project + environment) stay unique per environment and show what each resource belongs to.",
			fix:     fmt.Sprintf("Set %s = \"${local.name_prefix}-%s\".", attr, suggestRole(s, n)),
		})
	}
}

// suggestRole keeps the meaningful part of a fixed name.
func suggestRole(s string, n *model.Node) string {
	s = strings.Trim(strings.ToLower(nameSplit.ReplaceAllString(s, "-")), "-/")
	if s == "" {
		s = strings.ReplaceAll(n.Name, "_", "-")
	}
	return s
}
