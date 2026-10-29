// Package catalog serves the embedded Terraform provider schemas with curated
// metadata on top: palette search, Floci support, node creation with sensible
// defaults, connection recipes, schema validation and starter templates.
package catalog

//go:generate go run ./gen -out data

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog/schemafile"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

//go:embed data/*.json.gz
var dataFS embed.FS

//go:embed curated/*.json
var curatedFS embed.FS

// Catalog is safe for concurrent use.
type Catalog struct {
	providers []*provider
	byName    map[string]*provider
	cur       *curated
	clouds    map[model.Cloud]*cloudServices

	summariesOnce sync.Once
	summaries     []summary
	summaryIdx    map[schemaKey]int // summaries index by kind and type

	// schemas memoizes decoded schema entries with curated metadata
	// applied: both are immutable, and decoding the entry's JSON dominated
	// every lint/validate pass (one lookup per node and rule).
	schemaMu sync.RWMutex
	schemas  map[schemaKey]*cachedSchema

	mu        sync.RWMutex
	live      map[model.Cloud]map[string]bool
	awsRegion string // region of new aws provider blocks; "" = us-east-1
}

type schemaKey struct {
	kind model.NodeKind
	typ  string
}

// cachedSchema is the immutable part of a ResourceSchema. Its Block is
// shared by every Schema result, so callers must treat it as read-only.
type cachedSchema struct {
	description string
	docsURL     string
	block       model.SchemaBlock
}

// provider is one embedded provider schema, decompressed on first use.
type provider struct {
	cloud     model.Cloud // "" for utility providers (archive, random)
	info      model.ProviderInfo
	resources []string
	data      []string

	once    sync.Once
	file    *rawFile
	loadErr error
}

type rawFile struct {
	Provider  *schemafile.Entry          `json:"provider"`
	Resources map[string]json.RawMessage `json:"resources"`
	Data      map[string]json.RawMessage `json:"data"`
}

var providerMeta = []struct {
	name, label, emulator string
	cloud                 model.Cloud
	port                  int
}{
	{"aws", "Amazon Web Services", "floci/floci:latest", model.CloudAWS, 4566},
	{"azurerm", "Microsoft Azure", "floci/floci-az:latest", model.CloudAzure, 4577},
	{"google", "Google Cloud", "floci/floci-gcp:latest", model.CloudGCP, 4588},
	{"oci", "Oracle Cloud Infrastructure", "floci/floci-oci:latest", model.CloudOCI, 4599},
	{"archive", "Archive", "", "", 0},
	{"random", "Random", "", "", 0},
}

// Load reads the embedded index and curated metadata. Full provider schemas
// are decompressed lazily on first use.
func Load() (*Catalog, error) {
	var idx schemafile.Index
	if err := readGzipJSON("data/index.json.gz", &idx); err != nil {
		return nil, err
	}
	cur, err := loadCurated()
	if err != nil {
		return nil, err
	}
	c := &Catalog{
		byName:  map[string]*provider{},
		cur:     cur,
		clouds:  buildCloudServices(cur),
		live:    map[model.Cloud]map[string]bool{},
		schemas: map[schemaKey]*cachedSchema{},
	}
	for _, m := range providerMeta {
		var ip *schemafile.IndexProvider
		for i := range idx.Providers {
			if idx.Providers[i].Name == m.name {
				ip = &idx.Providers[i]
			}
		}
		if ip == nil {
			return nil, fmt.Errorf("catalog: provider %s missing from embedded index", m.name)
		}
		p := &provider{
			cloud:     m.cloud,
			resources: ip.Resources,
			data:      ip.Data,
			info: model.ProviderInfo{
				Cloud:         m.cloud,
				Name:          m.name,
				Source:        ip.Source,
				Version:       ip.Version,
				Label:         m.label,
				Emulator:      m.emulator,
				EmulatorPort:  m.port,
				ResourceCount: len(ip.Resources),
				DataCount:     len(ip.Data),
			},
		}
		c.providers = append(c.providers, p)
		c.byName[m.name] = p
	}
	return c, nil
}

func readGzipJSON(name string, v any) error {
	raw, err := dataFS.ReadFile(name)
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("catalog: %s: %w", name, err)
	}
	defer zr.Close()
	plain, err := io.ReadAll(zr)
	if err != nil {
		return fmt.Errorf("catalog: %s: %w", name, err)
	}
	if err := json.Unmarshal(plain, v); err != nil {
		return fmt.Errorf("catalog: %s: %w", name, err)
	}
	return nil
}

func (p *provider) load() (*rawFile, error) {
	p.once.Do(func() {
		var f rawFile
		p.loadErr = readGzipJSON("data/"+p.info.Name+".json.gz", &f)
		p.file = &f
	})
	return p.file, p.loadErr
}

// entry decodes the schema entry of one type.
func (p *provider) entry(kind model.NodeKind, typ string) (*schemafile.Entry, bool) {
	f, err := p.load()
	if err != nil {
		return nil, false
	}
	m := f.Resources
	if kind == model.KindData {
		m = f.Data
	}
	raw, ok := m[typ]
	if !ok {
		return nil, false
	}
	var e schemafile.Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, false
	}
	return &e, true
}

// providerOf returns the provider owning typ (by its name prefix).
func (c *Catalog) providerOf(typ string) (*provider, bool) {
	name, _, ok := strings.Cut(typ, "_")
	if !ok {
		return nil, false
	}
	p, ok := c.byName[name]
	return p, ok
}

// Providers lists the cloud providers (utility providers excluded).
func (c *Catalog) Providers() []model.ProviderInfo {
	var out []model.ProviderInfo
	for _, p := range c.providers {
		if p.cloud != "" {
			out = append(out, p.info)
		}
	}
	return out
}

// CloudOf maps a resource or data type to its cloud by provider prefix.
func (c *Catalog) CloudOf(typ string) (model.Cloud, bool) {
	p, ok := c.providerOf(typ)
	if !ok || p.cloud == "" {
		return "", false
	}
	return p.cloud, true
}

// categoryFiles names the recommended Terraform file of each category.
// "Other" and unknown types go to main.tf (ResourceFile returns "").
var categoryFiles = map[string]string{
	"Storage":            "storage.tf",
	"Databases":          "database.tf",
	"Compute":            "compute.tf",
	"Containers":         "containers.tf",
	"Networking":         "network.tf",
	"Messaging & Events": "messaging.tf",
	"Identity":           "iam.tf",
	"Security":           "security.tf",
	"Observability":      "monitoring.tf",
	"Analytics":          "analytics.tf",
	"AI & ML":            "ai.tf",
	"Developer Tools":    "devtools.tf",
	"Cost":               "cost.tf",
}

// apiServices are the service keys whose resources go to api.tf instead of
// their category's file (API gateways are "Networking" in the palette).
var apiServices = map[string]bool{
	"apigateway": true, "apigatewayv2": true, "appsync": true, "apim": true, "api": true,
}

// ResourceFile names the recommended Terraform file of a resource type,
// from its catalog category ("aws_vpc" -> "network.tf", "aws_sqs_queue" ->
// "messaging.tf", API gateways -> "api.tf"); "" for types without a cloud
// category (main.tf). It is hclcodec.ExportOptions.ResourceFile.
func (c *Catalog) ResourceFile(typ string) string {
	p, ok := c.providerOf(typ)
	if !ok || p.cloud == "" {
		return ""
	}
	cs := c.clouds[p.cloud]
	if cs == nil {
		return ""
	}
	var key, category string
	if svc := cs.serviceOf(typ); svc != nil {
		key, category = svc.Key, svc.Category
	} else {
		tail := strings.TrimPrefix(typ, cs.prefix)
		key, _, _ = strings.Cut(tail, "_")
		category = c.categoryByKeywords(tail)
	}
	if apiServices[key] {
		return "api.tf"
	}
	return categoryFiles[category]
}

// Schema returns the full schema of a type with curated metadata applied.
// The returned Block is shared between calls and must not be modified.
func (c *Catalog) Schema(kind model.NodeKind, typ string) (*model.ResourceSchema, bool) {
	if kind != model.KindResource && kind != model.KindData {
		return nil, false
	}
	p, ok := c.providerOf(typ)
	if !ok {
		return nil, false
	}
	cs, ok := c.cachedSchema(p, kind, typ)
	if !ok {
		return nil, false
	}
	// The summary reflects live emulator services, so it is built per call.
	return &model.ResourceSchema{
		ResourceSummary: c.summarize(kind, typ, p),
		Provider:        p.info.Source,
		Description:     cs.description,
		DocsURL:         cs.docsURL,
		Block:           cs.block,
	}, true
}

func (c *Catalog) cachedSchema(p *provider, kind model.NodeKind, typ string) (*cachedSchema, bool) {
	key := schemaKey{kind, typ}
	c.schemaMu.RLock()
	cs, hit := c.schemas[key]
	c.schemaMu.RUnlock()
	if hit {
		return cs, true
	}
	e, ok := p.entry(kind, typ)
	if !ok {
		return nil, false
	}
	cs = &cachedSchema{description: e.Description, docsURL: docsURL(p, kind, typ), block: e.Block}
	if cr := c.cur.lookup(kind, typ); cr != nil {
		if cr.Description != "" {
			cs.description = cr.Description
		}
		applyAttrMeta(&cs.block, cr.Attrs)
	}
	c.schemaMu.Lock()
	c.schemas[key] = cs
	c.schemaMu.Unlock()
	return cs, true
}

// NamingAttr returns the attribute that names the cloud resource of a
// curated resource type ("bucket" for aws_s3_bucket, "function_name" for
// aws_lambda_function), or "" when the type has none or its names have
// constraints a "<prefix>-<role>" name could break (e.g. storage account
// names without hyphens).
func (c *Catalog) NamingAttr(typ string) string {
	cr := c.cur.lookup(model.KindResource, typ)
	if cr == nil || cr.Attrs == nil {
		return ""
	}
	for _, key := range cr.Attrs.keys {
		if cr.Attrs.values[key].Naming != "" {
			return key
		}
	}
	return ""
}

func docsURL(p *provider, kind model.NodeKind, typ string) string {
	section := "resources"
	if kind == model.KindData {
		section = "data-sources"
	}
	return fmt.Sprintf("https://registry.terraform.io/providers/%s/%s/docs/%s/%s",
		p.info.Source, p.info.Version, section, strings.TrimPrefix(typ, p.info.Name+"_"))
}

// applyAttrMeta copies curated basic/default/enum onto schema attributes;
// keys are dotted paths through nested blocks ("attribute.type").
func applyAttrMeta(b *model.SchemaBlock, metas *orderedMap[attrMeta]) {
	if metas == nil {
		return
	}
	for _, key := range metas.keys {
		m := metas.values[key]
		path := strings.Split(key, ".")
		blk := b
		for _, seg := range path[:len(path)-1] {
			var next *model.SchemaBlock
			for i := range blk.Blocks {
				if blk.Blocks[i].Name == seg {
					next = &blk.Blocks[i].Block
				}
			}
			if next == nil {
				blk = nil
				break
			}
			blk = next
		}
		if blk == nil {
			continue
		}
		for i := range blk.Attributes {
			a := &blk.Attributes[i]
			if a.Name != path[len(path)-1] {
				continue
			}
			a.Basic = a.Basic || m.Basic
			if m.Default != nil {
				a.Default = m.Default
			}
			if len(m.Enum) > 0 {
				a.Enum = m.Enum
			}
		}
	}
}

// SetLiveServices refines Floci support with the service list reported by a
// running emulator (`/_floci/health` services map for AWS). For AWS the live
// keys replace the built-in key list; for Azure and OCI matching keys are
// added to the curated support; GCP keeps its curated support (see
// supported).
func (c *Catalog) SetLiveServices(cloud model.Cloud, services map[string]string) {
	set := make(map[string]bool, len(services))
	for k := range services {
		set[k] = true
	}
	c.mu.Lock()
	c.live[cloud] = set
	c.mu.Unlock()
}

// SetDefaultAWSRegion sets the region written into aws provider blocks that
// AddNode creates; "" restores us-east-1.
func (c *Catalog) SetDefaultAWSRegion(region string) {
	c.mu.Lock()
	c.awsRegion = region
	c.mu.Unlock()
}

func (c *Catalog) defaultAWSRegion() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.awsRegion == "" {
		return "us-east-1"
	}
	return c.awsRegion
}
