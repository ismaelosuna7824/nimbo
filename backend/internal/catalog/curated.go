package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// curated is the hand-written layer in curated/*.json.
type curated struct {
	Categories       []string                     `json:"categories"`
	CategoryIcons    map[string]string            `json:"categoryIcons"`
	CategoryKeywords []categoryKeywords           `json:"categoryKeywords"`
	Clouds           map[model.Cloud]cloudCurated `json:"clouds"`

	Variables map[string]curatedVariable `json:"variables"`
	Resources map[string]*curatedType    `json:"resources"`
	Data      map[string]*curatedType    `json:"data"`
}

type categoryKeywords struct {
	Category string   `json:"category"`
	Words    []string `json:"words"`
}

type cloudCurated struct {
	FlociKeys []string         `json:"flociKeys"`
	Local     []string         `json:"local"`
	Services  []curatedService `json:"services"`
}

type curatedService struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Category string   `json:"category"`
	Icon     string   `json:"icon"`
	Floci    bool     `json:"floci"`
	Prefixes []string `json:"prefixes"`
}

type curatedVariable struct {
	Type        string          `json:"type"`
	Default     json.RawMessage `json:"default"`
	Description string          `json:"description"`
}

// curatedType is the metadata and starter body of one resource/data type.
type curatedType struct {
	Label       string                `json:"label"`
	Icon        string                `json:"icon"`
	Popular     bool                  `json:"popular"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Attrs       *orderedMap[attrMeta] `json:"attrs"`
	Blocks      []initBlock           `json:"blocks"`
	Needs       []need                `json:"needs"`
	Companions  []companion           `json:"companions"`
}

// attrMeta: Basic/Default/Enum annotate the schema; Init (constant) or Expr
// (HCL source) is written by AddNode. Strings support placeholders, see
// placeholders.render. Naming marks the attribute that names the cloud
// resource: its value is the content of an HCL string template written
// instead of Init when the workflow has local.name_prefix (e.g.
// "${local.name_prefix}-{{slug}}"); the bp_name_prefix check reads it too.
type attrMeta struct {
	Basic   bool            `json:"basic"`
	Default json.RawMessage `json:"default"`
	Enum    []string        `json:"enum"`
	Init    json.RawMessage `json:"init"`
	Expr    string          `json:"expr"`
	Naming  string          `json:"naming"`
}

// companion is a node AddNode creates after a curated starter that refers
// to it (e.g. a bucket's encryption configuration, a function's log group).
// Init strings may use {{self}} (the starter's address), the starter's
// {{name}}/{{slug}} and {{need:<type>}} for its needs and earlier companions.
type companion struct {
	Kind model.NodeKind `json:"kind"`
	Type string         `json:"type"`
	Name string         `json:"name"`
	Init *initOverride  `json:"init"`
}

type initBlock struct {
	Type   string                       `json:"type"`
	Attrs  *orderedMap[json.RawMessage] `json:"attrs"`
	Exprs  *orderedMap[string]          `json:"exprs"`
	Blocks []initBlock                  `json:"blocks"`
}

// need is a dependency AddNode satisfies by referencing an existing node of
// Type (or creating one), or a workflow variable when Var is set. Refs maps
// attributes of the new node to attributes of the needed node.
type need struct {
	Kind     model.NodeKind      `json:"kind"`
	Type     string              `json:"type"`
	Var      string              `json:"var"`
	New      bool                `json:"new"`
	Optional bool                `json:"optional"`
	Name     string              `json:"name"`
	Refs     *orderedMap[string] `json:"refs"`
	Init     *initOverride       `json:"init"`
}

type initOverride struct {
	Attrs  *orderedMap[json.RawMessage] `json:"attrs"`
	Exprs  *orderedMap[string]          `json:"exprs"`
	Blocks []initBlock                  `json:"blocks"`
}

func (n need) kind() model.NodeKind {
	if n.Kind == "" {
		return model.KindResource
	}
	return n.Kind
}

func loadCurated() (*curated, error) {
	var cur curated
	for _, name := range []string{"curated/services.json", "curated/resources.json"} {
		raw, err := curatedFS.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("catalog: %w", err)
		}
		if err := json.Unmarshal(raw, &cur); err != nil {
			return nil, fmt.Errorf("catalog: %s: %w", name, err)
		}
	}
	return &cur, nil
}

func (cur *curated) lookup(kind model.NodeKind, typ string) *curatedType {
	if kind == model.KindData {
		return cur.Data[typ]
	}
	return cur.Resources[typ]
}

// orderedMap is a JSON object that keeps key order, so starter bodies are
// written in the order they are curated.
type orderedMap[V any] struct {
	keys   []string
	values map[string]V
}

func (m *orderedMap[V]) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("expected object")
	}
	m.values = map[string]V{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key := tok.(string)
		var v V
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		if _, dup := m.values[key]; !dup {
			m.keys = append(m.keys, key)
		}
		m.values[key] = v
	}
	_, err = dec.Token()
	return err
}

func (m *orderedMap[V]) get(key string) (V, bool) {
	var zero V
	if m == nil {
		return zero, false
	}
	v, ok := m.values[key]
	return v, ok
}

func (m *orderedMap[V]) len() int {
	if m == nil {
		return 0
	}
	return len(m.keys)
}

func (m *orderedMap[V]) each(fn func(key string, v V)) {
	if m == nil {
		return
	}
	for _, k := range m.keys {
		fn(k, m.values[k])
	}
}

// ---------------------------------------------------------------------------
// Services, categories and Floci support

type cloudServices struct {
	cloud     model.Cloud
	prefix    string // provider type prefix incl. "_"
	flociKeys map[string]bool
	local     map[string]bool
	services  []curatedService
	byPrefix  []prefixEntry // sorted longest first
}

type prefixEntry struct {
	prefix string
	svc    *curatedService
}

func buildCloudServices(cur *curated) map[model.Cloud]*cloudServices {
	prefixes := map[model.Cloud]string{
		model.CloudAWS: "aws_", model.CloudAzure: "azurerm_", model.CloudGCP: "google_", model.CloudOCI: "oci_",
	}
	out := map[model.Cloud]*cloudServices{}
	for cloud, cc := range cur.Clouds {
		cs := &cloudServices{
			cloud:     cloud,
			prefix:    prefixes[cloud],
			flociKeys: map[string]bool{},
			local:     map[string]bool{},
			services:  cc.Services,
		}
		for _, k := range cc.FlociKeys {
			cs.flociKeys[k] = true
		}
		for _, t := range cc.Local {
			cs.local[t] = true
		}
		for i := range cs.services {
			for _, p := range cs.services[i].Prefixes {
				cs.byPrefix = append(cs.byPrefix, prefixEntry{p, &cs.services[i]})
			}
		}
		// Longest prefix wins: aws_route53_ beats aws_route.
		slices.SortFunc(cs.byPrefix, func(a, b prefixEntry) int {
			if len(a.prefix) != len(b.prefix) {
				return len(b.prefix) - len(a.prefix)
			}
			return strings.Compare(a.prefix, b.prefix)
		})
		out[cloud] = cs
	}
	return out
}

// serviceOf resolves the curated service of typ, if any.
func (cs *cloudServices) serviceOf(typ string) *curatedService {
	for _, e := range cs.byPrefix {
		if strings.HasPrefix(typ, e.prefix) {
			return e.svc
		}
	}
	return nil
}

// serviceInfo classifies typ: Floci service key, category, icon and support.
type serviceInfo struct {
	key      string
	category string
	icon     string
	floci    bool
}

func (c *Catalog) serviceInfo(kind model.NodeKind, typ string, cloud model.Cloud) serviceInfo {
	cs := c.clouds[cloud]
	if cs == nil {
		return serviceInfo{category: "Other"}
	}
	var si serviceInfo
	if svc := cs.serviceOf(typ); svc != nil {
		si = serviceInfo{key: svc.Key, category: svc.Category, icon: svc.Icon, floci: svc.Floci}
	} else {
		tail := strings.TrimPrefix(typ, cs.prefix)
		first, _, _ := strings.Cut(tail, "_")
		si.key = first
		si.category = c.categoryByKeywords(tail)
	}
	if cloud == model.CloudAWS {
		si.floci = c.awsKeySupported(si.key)
	} else if !si.floci {
		c.mu.RLock()
		si.floci = c.live[cloud][si.key]
		c.mu.RUnlock()
	}
	// Local data sources (caller identity, policy documents…) need no service.
	if kind == model.KindData && cs.local[typ] {
		si.floci = true
	}
	return si
}

func (c *Catalog) awsKeySupported(key string) bool {
	c.mu.RLock()
	live, ok := c.live[model.CloudAWS]
	c.mu.RUnlock()
	if ok && len(live) > 0 {
		return live[key]
	}
	return c.clouds[model.CloudAWS].flociKeys[key]
}

func (c *Catalog) categoryByKeywords(tail string) string {
	tokens := strings.Split(tail, "_")
	for _, ck := range c.cur.CategoryKeywords {
		for _, w := range ck.Words {
			if strings.Contains(w, "_") {
				if strings.HasPrefix(tail, w) {
					return ck.Category
				}
				continue
			}
			for _, t := range tokens {
				if t == w {
					return ck.Category
				}
			}
		}
	}
	return "Other"
}
