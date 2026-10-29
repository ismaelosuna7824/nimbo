package catalog

import (
	"fmt"
	"slices"
	"sync"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog/schemafile"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// KindProvider names provider configuration schemas in SchemaOf
// ("provider", "aws"); it is not a node kind.
const KindProvider model.NodeKind = "provider"

// Types lists every resource and data source type of every provider,
// utility providers included, in provider order then by name. Local
// reflects the current (live) Floci support; utility types always run
// locally.
func (c *Catalog) Types() []model.TypeInfo {
	all := c.allSummaries()
	n := 0
	for _, p := range c.providers {
		n += len(p.resources) + len(p.data)
	}
	out := make([]model.TypeInfo, 0, n)
	for _, p := range c.providers {
		for _, kind := range []model.NodeKind{model.KindResource, model.KindData} {
			names := p.resources
			if kind == model.KindData {
				names = p.data
			}
			for _, typ := range names {
				if p.cloud == "" {
					label := autoLabel(p, typ)
					if cr := c.cur.lookup(kind, typ); cr != nil && cr.Label != "" {
						label = cr.Label
					}
					out = append(out, model.TypeInfo{Kind: kind, Type: typ, Label: label, Local: true})
					continue
				}
				s := &all[c.summaryIdx[schemaKey{kind, typ}]]
				out = append(out, model.TypeInfo{
					Kind:  kind,
					Type:  typ,
					Cloud: p.cloud,
					Label: s.label,
					Local: c.local(p.cloud, typ, s.svc),
				})
			}
		}
	}
	return out
}

// SchemaOf is Schema extended to provider configuration blocks: kind
// KindProvider with the provider's local name ("aws", "google", ...).
func (c *Catalog) SchemaOf(kind model.NodeKind, name string) (*model.ResourceSchema, bool) {
	if kind != KindProvider {
		return c.Schema(kind, name)
	}
	p, ok := c.byName[name]
	if !ok {
		return nil, false
	}
	f, err := p.load()
	if err != nil || f.Provider == nil {
		return nil, false
	}
	return &model.ResourceSchema{
		ResourceSummary: model.ResourceSummary{Kind: KindProvider, Type: name, Cloud: p.cloud, Category: "Other", Label: p.info.Label, Icon: "box"},
		Provider:        p.info.Source,
		Description:     f.Provider.Description,
		DocsURL:         fmt.Sprintf("https://registry.terraform.io/providers/%s/%s/docs", p.info.Source, p.info.Version),
		Block:           f.Provider.Block,
	}, true
}

// HasType reports whether typ is a known resource or data source type,
// without decoding its schema.
func (c *Catalog) HasType(kind model.NodeKind, typ string) bool {
	p, ok := c.providerOf(typ)
	if !ok {
		return false
	}
	names := p.resources
	if kind == model.KindData {
		names = p.data
	}
	_, found := slices.BinarySearch(names, typ)
	return found
}

// providerMetaAttrs are the meta-arguments of a provider block.
var providerMetaAttrs = map[string]bool{"alias": true, "version": true}

// ValidateProvider checks the body of a `provider "<name>"` block against
// the provider's configuration schema like Validate checks resources
// (diagnostics carry NodeID "provider.<name>"). Providers without an
// embedded schema are not checked.
func (c *Catalog) ValidateProvider(name string, b *model.Body) []model.Diagnostic {
	s, ok := c.SchemaOf(KindProvider, name)
	if !ok {
		return nil
	}
	body := model.Body{Blocks: b.Blocks}
	for _, a := range b.Attrs {
		if !providerMetaAttrs[a.Name] {
			body.Attrs = append(body.Attrs, a)
		}
	}
	var out []model.Diagnostic
	validator{node: &model.Node{ID: "provider." + name}, out: &out}.body(&body, &s.Block, "", false)
	return out
}

var loadFunctions = sync.OnceValues(func() ([]model.FunctionSignature, error) {
	var f schemafile.Functions
	if err := readGzipJSON("data/functions.json.gz", &f); err != nil {
		return nil, err
	}
	return f.Functions, nil
})

// Functions lists Terraform's built-in functions, sorted by name.
func (c *Catalog) Functions() ([]model.FunctionSignature, error) {
	return loadFunctions()
}
