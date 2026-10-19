package catalog

import (
	"encoding/json"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Conventions of the Terraform Nimbo generates (AddNode, Connect). When the
// first resource of a cloud is added to a workflow, the workflow gets:
//
//	variable "project"     (string, described, validated; default: workflow name slug)
//	variable "environment" (string, described, validated; default "dev")
//	locals {
//	  name_prefix = "${var.project}-${var.environment}"
//	  common_tags = { Project = var.project, Environment = var.environment, ManagedBy = "nimbo" } # AWS, Azure
//	  common_labels = { project = …, environment = …, managed_by = "nimbo" }                       # GCP
//	}
//
// a region variable used by the provider block (aws_region, gcp_region,
// oci_region; Azure resource groups use var.location), an AWS provider
// default_tags block, and required_version in a fresh terraform block.
// Starters then name resources "${local.name_prefix}-<role>" (attrMeta.
// Naming) and tag Azure resources with local.common_tags and GCP resources
// with local.common_labels. Nothing is added when the workflow already
// declares a local of the same name, so imported projects keep their own
// scheme.

const (
	localNamePrefix   = "name_prefix"
	localCommonTags   = "common_tags"
	localCommonLabels = "common_labels"

	requiredVersion = ">= 1.6"

	commonTagsExpr = "{\n  Project     = var.project\n  Environment = var.environment\n  ManagedBy   = \"nimbo\"\n}"
	// GCP label keys and values must be lowercase.
	commonLabelsExpr = "{\n  project     = var.project\n  environment = var.environment\n  managed_by  = \"nimbo\"\n}"
)

func (b *builder) hasLocal(name string) bool {
	for _, l := range b.wf.Locals {
		if l.Name == name {
			return true
		}
	}
	return false
}

func (b *builder) hasProvider(name string) bool {
	for _, pb := range b.wf.Providers {
		if pb.Name == name {
			return true
		}
	}
	return false
}

// prepare applies the workflow conventions before the first node of typ's
// cloud is built, so starters can already use local.name_prefix.
func (b *builder) prepare(typ string) {
	p, ok := b.c.providerOf(typ)
	if !ok || p.cloud == "" || b.hasProvider(p.info.Name) {
		return
	}
	b.ensureConventions(p.cloud)
}

// ensureConventions adds the project/environment variables and the naming
// and tagging locals the cloud uses, each only when missing.
func (b *builder) ensureConventions(cloud model.Cloud) {
	if !b.hasLocal(localNamePrefix) {
		b.ensureProjectVariables()
		b.wf.Locals = append(b.wf.Locals, model.Attr{Name: localNamePrefix, Value: expr(`"${var.project}-${var.environment}"`)})
	}
	switch cloud {
	case model.CloudAWS, model.CloudAzure:
		if !b.hasLocal(localCommonTags) {
			b.ensureProjectVariables()
			b.wf.Locals = append(b.wf.Locals, model.Attr{Name: localCommonTags, Value: expr(commonTagsExpr)})
		}
	case model.CloudGCP:
		if !b.hasLocal(localCommonLabels) {
			b.ensureProjectVariables()
			b.wf.Locals = append(b.wf.Locals, model.Attr{Name: localCommonLabels, Value: expr(commonLabelsExpr)})
		}
	}
}

func (b *builder) hasVariable(name string) bool {
	for _, v := range b.wf.Variables {
		if v.Name == name {
			return true
		}
	}
	return false
}

func (b *builder) ensureProjectVariables() {
	if !b.hasVariable("project") {
		b.wf.Variables = append(b.wf.Variables, slugVariable("project",
			"Project name: the first part of every resource name and the Project tag.",
			projectSlug(b.wf.Name), "^[a-z][a-z0-9-]{1,19}$",
			"project must be 2-20 lowercase letters, digits or hyphens, starting with a letter."))
	}
	if !b.hasVariable("environment") {
		b.wf.Variables = append(b.wf.Variables, slugVariable("environment",
			"Deployment environment (dev, staging, prod, …): part of every resource name and the Environment tag.",
			"dev", "^[a-z][a-z0-9-]{0,11}$",
			"environment must be 1-12 lowercase letters, digits or hyphens, starting with a letter."))
	}
}

func slugVariable(name, description, def, pattern, message string) model.NamedBlock {
	body := emptyBody()
	setAttr(&body, "type", expr("string"))
	setAttr(&body, "default", lit(def))
	setAttr(&body, "description", lit(description))
	validation := model.Block{Type: "validation", Body: emptyBody()}
	setAttr(&validation.Body, "condition", expr("can(regex("+hclString(pattern)+", var."+name+"))"))
	setAttr(&validation.Body, "error_message", lit(message))
	body.Blocks = append(body.Blocks, validation)
	return model.NamedBlock{Name: name, Body: body}
}

// projectSlug derives the default of variable "project" from a workflow
// name: lowercase letters, digits and single hyphens, starting with a
// letter, at most 20 characters ("Orders API (v2)" -> "orders-api-v2").
func projectSlug(name string) string {
	var sb strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' && sb.Len() > 0:
			if dash && sb.Len() > 0 {
				sb.WriteByte('-')
			}
			sb.WriteRune(r)
			dash = false
		default:
			dash = true
		}
		if sb.Len() >= 20 {
			break
		}
	}
	s := strings.Trim(sb.String(), "-")
	if len(s) > 20 {
		s = strings.TrimRight(s[:20], "-")
	}
	if len(s) < 2 {
		return "app"
	}
	return s
}

// awsDefaultTags reports whether the aws provider configuration a node uses
// has default_tags.
func (b *builder) awsDefaultTags(n *model.Node) bool {
	alias := ""
	if a := getAttr(&n.Body, "provider"); a != nil {
		_, alias, _ = strings.Cut(strings.TrimSpace(valueSource(a.Value)), ".")
	}
	for _, pb := range b.wf.Providers {
		if pb.Name != "aws" {
			continue
		}
		pa := ""
		if a := getAttr(&pb.Body, "alias"); a != nil {
			_ = json.Unmarshal(a.Value.Literal, &pa)
		}
		if pa == alias {
			return getBlock(&pb.Body, "default_tags") != nil
		}
	}
	return false
}

// applyTags tags a new resource with the workflow's common tags/labels when
// its cloud has no provider-level default for it.
func (b *builder) applyTags(n *model.Node) {
	if n.Kind != model.KindResource {
		return
	}
	cloud, ok := b.c.CloudOf(n.Type)
	if !ok {
		return
	}
	attr, local := "tags", localCommonTags
	switch cloud {
	case model.CloudAWS:
		if b.awsDefaultTags(n) {
			return
		}
	case model.CloudAzure:
	case model.CloudGCP:
		attr, local = "labels", localCommonLabels
	default:
		return
	}
	if !b.hasLocal(local) || getAttr(&n.Body, attr) != nil || !b.c.hasSettableAttr(n, attr) {
		return
	}
	setAttr(&n.Body, attr, expr("local."+local))
}

// nameValue is the name a recipe gives a new resource of typ: derived from
// local.name_prefix when the workflow has it and typ has a naming attribute,
// else the plain role.
func (b *builder) nameValue(typ, role string) model.Value {
	if b.hasLocal(localNamePrefix) && b.c.NamingAttr(typ) != "" {
		return expr(`"${local.` + localNamePrefix + `}-` + role + `"`)
	}
	return lit(role)
}

// render returns a copy of o with placeholders rendered by p.
func (o *initOverride) render(p placeholders) *initOverride {
	if o == nil {
		return nil
	}
	out := &initOverride{}
	if o.Attrs != nil {
		out.Attrs = &orderedMap[json.RawMessage]{values: map[string]json.RawMessage{}}
		o.Attrs.each(func(k string, raw json.RawMessage) {
			out.Attrs.keys = append(out.Attrs.keys, k)
			out.Attrs.values[k] = p.renderJSON(raw)
		})
	}
	if o.Exprs != nil {
		out.Exprs = &orderedMap[string]{values: map[string]string{}}
		o.Exprs.each(func(k, src string) {
			out.Exprs.keys = append(out.Exprs.keys, k)
			out.Exprs.values[k] = p.render(src)
		})
	}
	for _, blk := range o.Blocks {
		out.Blocks = append(out.Blocks, blk.render(p))
	}
	return out
}

func (ib initBlock) render(p placeholders) initBlock {
	o := (&initOverride{Attrs: ib.Attrs, Exprs: ib.Exprs, Blocks: ib.Blocks}).render(p)
	return initBlock{Type: ib.Type, Attrs: o.Attrs, Exprs: o.Exprs, Blocks: o.Blocks}
}

func (c companion) kind() model.NodeKind {
	if c.Kind == "" {
		return model.KindResource
	}
	return c.Kind
}
