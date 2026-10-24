package catalog

import (
	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Grant recipes: dragging an identity onto a resource (or the other way
// round) offers "What can it do? Read / Write / Manage", and the provider's
// granter writes least privilege in its own permission model. Granters
// register from their own files (access_aws.go, access_azure.go, ...).

// accessPlan is one grant level offered for an identity/resource pair.
type accessPlan struct {
	level model.AccessLevel
	// desc is the exact preview of what applying writes ("Allows
	// dynamodb:GetItem, dynamodb:Query on aws_dynamodb_table.orders in the
	// inline policy aws_iam_role_policy.reader").
	desc               string
	identity, resource *model.Node
}

// accessGranter offers grants for one provider.
type accessGranter interface {
	// plans lists the levels a can get on b or b on a (whichever is the
	// identity), in Read, Write, Manage order; nil when the pair is not an
	// identity and a resource of this provider.
	plans(c *Catalog, wf *model.Workflow, a, b *model.Node) []accessPlan
	// apply writes p. x.src is p.identity and x.dst is p.resource.
	apply(x *conn, p accessPlan) error
}

var accessGranters []accessGranter

func registerGranter(g accessGranter) { accessGranters = append(accessGranters, g) }

// accessPlans returns the plans of the first granter that handles the pair.
func (c *Catalog) accessPlans(wf *model.Workflow, a, b *model.Node) (accessGranter, []accessPlan) {
	for _, g := range accessGranters {
		if ps := g.plans(c, wf, a, b); len(ps) > 0 {
			return g, ps
		}
	}
	return nil, nil
}

// accessRecipes are the grant recipes between src and dst.
func (c *Catalog) accessRecipes(wf *model.Workflow, src, dst *model.Node) []model.ConnectionRecipe {
	_, plans := c.accessPlans(wf, src, dst)
	out := make([]model.ConnectionRecipe, 0, len(plans))
	for _, p := range plans {
		out = append(out, model.ConnectionRecipe{
			ID:          access.RecipeID(p.level),
			Label:       access.LevelLabel(p.level),
			Description: p.desc,
			SourceType:  src.Type,
			TargetType:  dst.Type,
			Curated:     true,
			Group:       model.AccessRecipeGroup,
			Level:       p.level,
		})
	}
	return out
}

// applyAccess applies the grant level between src and dst.
func (c *Catalog) applyAccess(b *builder, wf *model.Workflow, src, dst *model.Node, level model.AccessLevel) error {
	g, plans := c.accessPlans(wf, src, dst)
	for _, p := range plans {
		if p.level != level {
			continue
		}
		x := &conn{c: c, b: b, src: p.identity, dst: p.resource}
		x.mid = model.Position{X: (src.Position.X + dst.Position.X) / 2, Y: (src.Position.Y + dst.Position.Y) / 2}
		return g.apply(x, p)
	}
	return access.Editf("catalog: no %s grant between %s and %s", level, address(src), address(dst))
}
