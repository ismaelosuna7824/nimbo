// Package access reads and writes the permission models of Terraform
// nodes as structured documents (model.AccessView): AWS policy documents,
// Azure role assignments, GCP IAM members/bindings and OCI policy
// statements. Each provider registers itself (Register, RegisterCatalog)
// from its own file. Writes only touch what the editor represents and keep
// everything else verbatim; anything that can't be parsed is reported
// read-only (Editable=false) and never rewritten.
//
// Grants (the drag-identity-onto-resource recipes) live in package
// catalog, which builds on the parsers and writers here; Kubernetes RBAC
// lives in package kube.
package access

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ErrUnsupported is returned for nodes without an access editor.
var ErrUnsupported = errors.New("access: this node has no access editor")

// EditError is an invalid edit (HTTP 400).
type EditError struct{ Msg string }

func (e *EditError) Error() string { return e.Msg }

// Editf builds an EditError.
func Editf(format string, args ...any) error { return &EditError{Msg: fmt.Sprintf(format, args...)} }

// Provider is one provider's access editors.
type Provider interface {
	// Name is "aws", "azure", "gcp" or "oci".
	Name() string
	// Supports reports whether n has an access editor of this provider.
	Supports(n *model.Node) bool
	// View reads n's editors (Provider, NodeID and Warnings are filled by
	// the caller when empty).
	View(wf *model.Workflow, n *model.Node) (*model.AccessView, error)
	// Apply writes edit into n's body; it may read (not change) the rest of
	// wf. The caller normalizes and saves the workflow.
	Apply(wf *model.Workflow, n *model.Node, edit model.AccessEdit) error
}

// CatalogQuery filters GET /access/catalog/{provider}.
type CatalogQuery struct {
	Service      string // aws: actions of this service prefix
	ResourceType string // azure/gcp: suggest roles for this Terraform type
	Search       string // case-insensitive filter
}

// CatalogFunc serves one provider's reference data.
type CatalogFunc func(q CatalogQuery) (*model.AccessCatalog, error)

var (
	providers []Provider
	catalogs  = map[string]CatalogFunc{}
)

// Register adds a provider (called from init).
func Register(p Provider) { providers = append(providers, p) }

// RegisterCatalog adds a provider's reference data (called from init).
func RegisterCatalog(provider string, fn CatalogFunc) { catalogs[provider] = fn }

// For returns the provider editing n, or nil.
func For(n *model.Node) Provider {
	for _, p := range providers {
		if p.Supports(n) {
			return p
		}
	}
	return nil
}

// Supported reports whether n has an access editor.
func Supported(n *model.Node) bool { return For(n) != nil }

// View reads n's access editors.
func View(wf *model.Workflow, n *model.Node) (*model.AccessView, error) {
	p := For(n)
	if p == nil {
		return nil, ErrUnsupported
	}
	v, err := p.View(wf, n)
	if err != nil {
		return nil, err
	}
	if v.NodeID == "" {
		v.NodeID = n.ID
	}
	if v.Provider == "" {
		v.Provider = p.Name()
	}
	if v.Warnings == nil {
		v.Warnings = []model.AccessWarning{}
	}
	return v, nil
}

// Apply writes edit into n.
func Apply(wf *model.Workflow, n *model.Node, edit model.AccessEdit) error {
	p := For(n)
	if p == nil {
		return ErrUnsupported
	}
	return p.Apply(wf, n, edit)
}

// Catalog returns a provider's reference data.
func Catalog(provider string, q CatalogQuery) (*model.AccessCatalog, error) {
	fn, ok := catalogs[provider]
	if !ok {
		return nil, Editf("access: no reference data for provider %q (known: %s)", provider, strings.Join(CatalogProviders(), ", "))
	}
	return fn(q)
}

// CatalogProviders lists the providers with reference data.
func CatalogProviders() []string {
	out := make([]string, 0, len(catalogs))
	for k := range catalogs {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// RecipeID is the connect recipe ID of a grant level ("access:read").
func RecipeID(l model.AccessLevel) string { return model.AccessRecipePrefix + string(l) }

// ParseRecipeID returns the level of a grant recipe ID.
func ParseRecipeID(id string) (model.AccessLevel, bool) {
	rest, ok := strings.CutPrefix(id, model.AccessRecipePrefix)
	if !ok {
		return "", false
	}
	l := model.AccessLevel(rest)
	return l, slices.Contains(model.AccessLevels, l)
}

// LevelLabel is the display label of a level ("Read").
func LevelLabel(l model.AccessLevel) string {
	switch l {
	case model.AccessRead:
		return "Read"
	case model.AccessWrite:
		return "Write"
	case model.AccessManage:
		return "Manage"
	}
	return string(l)
}

// ---------------------------------------------------------------------------
// Body helpers shared by the providers.

// GetAttr returns the attribute name of b, or nil.
func GetAttr(b *model.Body, name string) *model.Attr {
	for i := range b.Attrs {
		if b.Attrs[i].Name == name {
			return &b.Attrs[i]
		}
	}
	return nil
}

// SetExpr sets attribute name of b to the HCL expression src (Literal is
// cleared; Normalize fills it), keeping its position and comments.
func SetExpr(b *model.Body, name, src string) {
	if a := GetAttr(b, name); a != nil {
		a.Value = model.Value{Expr: src}
		return
	}
	b.Attrs = append(b.Attrs, model.Attr{Name: name, Value: model.Value{Expr: src}})
}

// Unset removes attribute name of b.
func Unset(b *model.Body, name string) {
	b.Attrs = slices.DeleteFunc(b.Attrs, func(a model.Attr) bool { return a.Name == name })
}

// Source is the HCL source of v: Expr, or the literal rendered as HCL.
func Source(v model.Value) string {
	if strings.TrimSpace(v.Expr) != "" {
		return v.Expr
	}
	if len(v.Literal) == 0 {
		return ""
	}
	src, err := hclcodec.ExprFromLiteral(v.Literal)
	if err != nil {
		return ""
	}
	return src
}

// Blocks returns pointers to the nested blocks of type typ.
func Blocks(b *model.Body, typ string) []*model.Block {
	var out []*model.Block
	for i := range b.Blocks {
		if b.Blocks[i].Type == typ {
			out = append(out, &b.Blocks[i])
		}
	}
	return out
}

// Address is the Terraform address of n ("aws_iam_role.app", "data.x.y").
func Address(n *model.Node) string { return hclcodec.Address(n) }

// FindNode returns the node with id, or nil.
func FindNode(wf *model.Workflow, id string) *model.Node {
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == id {
			return &wf.Nodes[i]
		}
	}
	return nil
}
