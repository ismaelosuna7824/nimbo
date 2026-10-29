package catalog

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Diagnostic codes produced by Validate.
const (
	CodeUnknownType       = "unknown_type"
	CodeUnknownAttribute  = "unknown_attribute"
	CodeUnknownBlock      = "unknown_block"
	CodeReadOnlyAttribute = "readonly_attribute"
	CodeMissingAttribute  = "missing_attribute"
	CodeMissingBlock      = "missing_block"
	CodeTooManyBlocks     = "too_many_blocks"
	CodeDeprecated        = "deprecated_attribute"
	CodeFlociUnsupported  = "floci_unsupported"
)

// Meta-arguments valid in every resource/data body.
var (
	metaAttrs  = map[string]bool{"count": true, "for_each": true, "provider": true, "depends_on": true}
	metaBlocks = map[string]bool{"lifecycle": true, "provisioner": true, "connection": true, "precondition": true, "postcondition": true}
)

// Validate checks every resource and data node against its provider schema
// and reports Floci support. Module nodes are skipped.
func (c *Catalog) Validate(wf *model.Workflow) []model.Diagnostic {
	var out []model.Diagnostic
	// Schema decodes the provider JSON on every call; workflows repeat a few
	// types across many nodes, so decode each type once per validation.
	type typeKey struct {
		kind model.NodeKind
		typ  string
	}
	schemas := map[typeKey]*model.ResourceSchema{}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.Kind != model.KindResource && n.Kind != model.KindData {
			continue
		}
		k := typeKey{n.Kind, n.Type}
		schema, ok := schemas[k]
		if !ok {
			schema, _ = c.Schema(n.Kind, n.Type)
			schemas[k] = schema
		}
		if schema == nil {
			out = append(out, model.Diagnostic{
				Severity: model.SeverityError,
				Code:     CodeUnknownType,
				Message:  fmt.Sprintf("Unknown %s type %q", n.Kind, n.Type),
				Detail:   "The type is not part of the aws, azurerm, google, oci, archive or random providers known to Nimbo.",
				NodeID:   n.ID,
			})
			continue
		}
		v := validator{node: n, out: &out}
		v.body(&n.Body, &schema.Block, "", true)
		if schema.Cloud != "" && !schema.FlociSupported {
			svc := schema.Service
			if svc == "" {
				svc = n.Type
			}
			d := model.Diagnostic{
				Severity: model.SeverityWarning,
				Code:     CodeFlociUnsupported,
				Message:  fmt.Sprintf("%s is not supported by the local Floci emulator (service %q)", n.Type, svc),
				Detail:   "Sandbox deploys of this resource will likely fail. It still exports to valid Terraform for the real cloud.",
				NodeID:   n.ID,
			}
			if is, ok := c.catalogGap(n.Type); ok {
				d.Message = fmt.Sprintf("%s doesn't deploy on the local Floci emulator with the provider release Nimbo targets", n.Type)
				d.Detail = is.Message + ". " + is.Fix + ". It still exports to valid Terraform for the real cloud."
			}
			out = append(out, d)
		}
	}
	return out
}

type validator struct {
	node *model.Node
	out  *[]model.Diagnostic
}

func (v validator) add(sev model.Severity, code, attr, msg string) {
	*v.out = append(*v.out, model.Diagnostic{Severity: sev, Code: code, Message: msg, NodeID: v.node.ID, Attribute: attr})
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func (v validator) body(b *model.Body, sb *model.SchemaBlock, path string, top bool) {
	attrs := make(map[string]*model.SchemaAttribute, len(sb.Attributes))
	for i := range sb.Attributes {
		attrs[sb.Attributes[i].Name] = &sb.Attributes[i]
	}
	blocks := make(map[string]*model.SchemaNestedBlock, len(sb.Blocks))
	for i := range sb.Blocks {
		blocks[sb.Blocks[i].Name] = &sb.Blocks[i]
	}

	for _, a := range b.Attrs {
		p := join(path, a.Name)
		if top && metaAttrs[a.Name] {
			continue
		}
		sa, ok := attrs[a.Name]
		if !ok {
			// Some providers accept nested blocks in attribute syntax.
			if _, isBlock := blocks[a.Name]; isBlock {
				continue
			}
			v.add(model.SeverityError, CodeUnknownAttribute, p, fmt.Sprintf("Unsupported argument %q", a.Name))
			continue
		}
		if !sa.Required && !sa.Optional {
			v.add(model.SeverityError, CodeReadOnlyAttribute, p, fmt.Sprintf("%q is computed by the provider and cannot be set", a.Name))
			continue
		}
		if sa.Deprecated {
			v.add(model.SeverityWarning, CodeDeprecated, p, fmt.Sprintf("%q is deprecated", a.Name))
		}
	}

	counts := map[string]int{}
	dynamic := map[string]bool{}
	for i := range b.Blocks {
		blk := &b.Blocks[i]
		if blk.Type == "dynamic" {
			if len(blk.Labels) > 0 {
				dynamic[blk.Labels[0]] = true
				if _, ok := blocks[blk.Labels[0]]; !ok && !(attrs[blk.Labels[0]] != nil && isObjectCollection(attrs[blk.Labels[0]].Type)) {
					v.add(model.SeverityError, CodeUnknownBlock, join(path, blk.Labels[0]), fmt.Sprintf("Unsupported block type %q", blk.Labels[0]))
				}
			}
			continue
		}
		if top && metaBlocks[blk.Type] {
			continue
		}
		nb, ok := blocks[blk.Type]
		if !ok {
			// Legacy "attributes as blocks" (e.g. aws_security_group ingress).
			if sa, isAttr := attrs[blk.Type]; isAttr && isObjectCollection(sa.Type) {
				continue
			}
			v.add(model.SeverityError, CodeUnknownBlock, join(path, blk.Type), fmt.Sprintf("Unsupported block type %q", blk.Type))
			continue
		}
		idx := counts[blk.Type]
		counts[blk.Type]++
		elem := join(path, blk.Type)
		if nb.Nesting != "single" && nb.Nesting != "group" {
			elem += "[" + strconv.Itoa(idx) + "]"
		}
		v.body(&blk.Body, &nb.Block, elem, false)
	}

	for _, sa := range sb.Attributes {
		if sa.Required && getAttr(b, sa.Name) == nil && !dynamic[sa.Name] {
			v.add(model.SeverityError, CodeMissingAttribute, join(path, sa.Name), fmt.Sprintf("Missing required argument %q", sa.Name))
		}
	}
	for _, nb := range sb.Blocks {
		if dynamic[nb.Name] {
			continue
		}
		have := counts[nb.Name]
		if have < nb.MinItems {
			v.add(model.SeverityError, CodeMissingBlock, join(path, nb.Name),
				fmt.Sprintf("At least %d %q block(s) required", nb.MinItems, nb.Name))
		}
		if nb.MaxItems > 0 && have > nb.MaxItems {
			v.add(model.SeverityError, CodeTooManyBlocks, join(path, nb.Name),
				fmt.Sprintf("At most %d %q block(s) allowed", nb.MaxItems, nb.Name))
		}
	}
}

// isObjectCollection reports a list/set of objects type, which Terraform also
// accepts in nested block syntax.
func isObjectCollection(t json.RawMessage) bool {
	var v []json.RawMessage
	if json.Unmarshal(t, &v) != nil || len(v) != 2 || !(typeIs(v[0], "list") || typeIs(v[0], "set")) {
		return false
	}
	var inner []json.RawMessage
	return json.Unmarshal(v[1], &inner) == nil && len(inner) == 2 && typeIs(inner[0], "object")
}
