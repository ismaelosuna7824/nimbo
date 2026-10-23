package kuberules

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"go.yaml.in/yaml/v4"
)

// Schema validation against the OpenAPI definitions of the pinned
// Kubernetes release (gen/ reduces the release's swagger.json), like
// kubeconform: known kinds, known fields, scalar types, required fields.
// Custom resources are validated against the openAPIV3Schema of a
// CustomResourceDefinition in the same workflow; other custom kinds are
// skipped (their CRD lives in the cluster).

//go:generate go run ./gen -version 1.36.5 -out schema

// SchemaVersion is the Kubernetes minor the schema comes from (the local
// cluster runs the same release).
const SchemaVersion = "1.36"

//go:embed schema/*.json.gz
var schemaFS embed.FS

// schemaNode is one OpenAPI schema: a swagger definition or a CRD's
// openAPIV3Schema (the extra keywords).
type schemaNode struct {
	Type        string                 `json:"type,omitempty" yaml:"type"`
	Format      string                 `json:"format,omitempty" yaml:"format"`
	Ref         string                 `json:"$ref,omitempty" yaml:"$ref"`
	Description string                 `json:"description,omitempty" yaml:"description"`
	Properties  map[string]*schemaNode `json:"properties,omitempty" yaml:"properties"`
	Items       *schemaNode            `json:"items,omitempty" yaml:"items"`
	Required    []string               `json:"required,omitempty" yaml:"required"`
	// AdditionalProperties is a schema, or true/false.
	AdditionalProperties *addl  `json:"additionalProperties,omitempty" yaml:"additionalProperties"`
	GVK                  []gvk  `json:"x-kubernetes-group-version-kind,omitempty" yaml:"-"`
	Enum                 []any  `json:"-" yaml:"enum"`
	PreserveUnknown      bool   `json:"-" yaml:"x-kubernetes-preserve-unknown-fields"`
	IntOrString          bool   `json:"-" yaml:"x-kubernetes-int-or-string"`
	EmbeddedResource     bool   `json:"-" yaml:"x-kubernetes-embedded-resource"`
	AnyOf                []any  `json:"-" yaml:"anyOf"`
	OneOf                []any  `json:"-" yaml:"oneOf"`
	AllOf                []any  `json:"-" yaml:"allOf"`
	Not                  any    `json:"-" yaml:"not"`
	Pattern              string `json:"-" yaml:"pattern"`
}

type gvk struct {
	Group   string `json:"group"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

// addl is additionalProperties: a schema or a boolean.
type addl struct {
	Schema *schemaNode
	Allow  bool
}

func (a *addl) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("true")) || bytes.Equal(b, []byte("false")) {
		a.Allow = string(b) == "true"
		return nil
	}
	a.Allow = true
	return json.Unmarshal(b, &a.Schema)
}

func (a *addl) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.ShortTag() == "!!bool" {
		a.Allow = n.Value == "true"
		return nil
	}
	a.Allow = true
	return n.Decode(&a.Schema)
}

// apiSchema is the loaded definitions.
type apiSchema struct {
	Version     string                 `json:"version"`
	Namespaced  []string               `json:"namespaced"`
	Definitions map[string]*schemaNode `json:"definitions"`

	byGVK      map[string]string // "apps/v1/Deployment" -> definition
	kindsOf    map[string][]string
	groups     map[string]bool
	namespaced map[string]bool
}

var (
	schemaOnce sync.Once
	schemaData []byte
	loaded     *apiSchema
)

// SchemaJSON is the reduced OpenAPI document (GET /catalog/kubernetes/schema):
// {version, namespaced, definitions}.
func SchemaJSON() []byte {
	loadSchema()
	return schemaData
}

func loadSchema() *apiSchema {
	schemaOnce.Do(func() {
		gz, err := schemaFS.ReadFile("schema/kubernetes-" + SchemaVersion + ".json.gz")
		if err != nil {
			panic(err)
		}
		zr, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			panic(err)
		}
		if schemaData, err = io.ReadAll(zr); err != nil {
			panic(err)
		}
		s := &apiSchema{}
		if err := json.Unmarshal(schemaData, s); err != nil {
			panic(err)
		}
		s.byGVK, s.kindsOf, s.groups, s.namespaced = map[string]string{}, map[string][]string{}, map[string]bool{}, map[string]bool{}
		for name, d := range s.Definitions {
			for _, g := range d.GVK {
				gv := groupVersion(g.Group, g.Version)
				s.byGVK[gv+"/"+g.Kind] = name
				s.kindsOf[g.Kind] = append(s.kindsOf[g.Kind], gv)
				s.groups[g.Group] = true
			}
		}
		for _, k := range s.kindsOf {
			sort.Strings(k)
		}
		for _, k := range s.Namespaced {
			s.namespaced[k] = true
		}
		loaded = s
	})
	return loaded
}

func groupVersion(group, version string) string {
	if group == "" {
		return version
	}
	return group + "/" + version
}

// splitAPIVersion splits "apps/v1" into "apps", "v1" ("v1" into "", "v1").
func splitAPIVersion(apiVersion string) (string, string) {
	if g, v, ok := strings.Cut(apiVersion, "/"); ok {
		return g, v
	}
	return "", apiVersion
}

// builtinGroup reports whether group is served by Kubernetes itself.
func builtinGroup(group string) bool {
	return loadSchema().groups[group]
}

// definition is the schema of apiVersion/kind (nil for unknown kinds).
func (s *apiSchema) definition(apiVersion, kind string) *schemaNode {
	if name, ok := s.byGVK[apiVersion+"/"+kind]; ok {
		return s.Definitions[name]
	}
	return nil
}

// schemaIssue is one schema violation at a field path.
type schemaIssue struct {
	path string
	node *yaml.Node
	msg  string
}

// validator walks an object's node against a schema.
type validator struct {
	defs   map[string]*schemaNode
	issues []schemaIssue
}

// Free-form definitions: any JSON value.
var freeForm = map[string]bool{
	"io.k8s.apiextensions-apiserver.pkg.apis.apiextensions.v1.JSON":                         true,
	"io.k8s.apiextensions-apiserver.pkg.apis.apiextensions.v1.JSONSchemaPropsOrArray":       true,
	"io.k8s.apiextensions-apiserver.pkg.apis.apiextensions.v1.JSONSchemaPropsOrBool":        true,
	"io.k8s.apiextensions-apiserver.pkg.apis.apiextensions.v1.JSONSchemaPropsOrStringArray": true,
	"io.k8s.apimachinery.pkg.runtime.RawExtension":                                          true,
	"io.k8s.apimachinery.pkg.apis.meta.v1.FieldsV1":                                         true,
}

const quantityDef = "io.k8s.apimachinery.pkg.api.resource.Quantity"

// quantityRe is the Kubernetes quantity syntax: a signed decimal number with
// a binary (Ki…Ei), decimal (n, u, m, k, M…E) or exponent suffix.
var quantityRe = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+|Ki|Mi|Gi|Ti|Pi|Ei|n|u|m|k|M|G|T|P|E)?$`)

func (v *validator) add(path string, n *yaml.Node, format string, args ...any) {
	v.issues = append(v.issues, schemaIssue{path, n, fmt.Sprintf(format, args...)})
}

// tagOf is the YAML type of a scalar: str, int, float, bool or null.
func tagOf(n *yaml.Node) string {
	return strings.TrimPrefix(n.ShortTag(), "!!")
}

func kindName(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "an object"
	case yaml.SequenceNode:
		return "a list"
	}
	switch tagOf(n) {
	case "int", "float":
		return "a number (" + n.Value + ")"
	case "bool":
		return "a boolean (" + n.Value + ")"
	}
	return "a string (" + quoteShort(n.Value) + ")"
}

func quoteShort(s string) string {
	if len(s) > 40 {
		s = s[:37] + "…"
	}
	return fmt.Sprintf("%q", s)
}

// walk validates n against s at path.
func (v *validator) walk(path string, n *yaml.Node, s *schemaNode) {
	n = resolve(n)
	if n == nil || s == nil || isNull(n) {
		return
	}
	for s.Ref != "" {
		name := strings.TrimPrefix(s.Ref, "#/definitions/")
		if freeForm[name] {
			return
		}
		if name == quantityDef {
			v.quantity(path, n)
			return
		}
		next := v.defs[name]
		if next == nil {
			return
		}
		s = next
	}
	if len(s.AnyOf) > 0 || len(s.OneOf) > 0 || len(s.AllOf) > 0 || s.Not != nil {
		return // CRD schemas combining alternatives: too lenient to judge
	}
	if s.IntOrString || s.Format == "int-or-string" {
		if n.Kind != yaml.ScalarNode || tagOf(n) != "int" && tagOf(n) != "str" {
			v.add(path, n, "%s must be a number or a string, not %s", path, kindName(n))
		}
		return
	}
	switch s.Type {
	case "object":
		v.object(path, n, s)
	case "array":
		if n.Kind != yaml.SequenceNode {
			v.add(path, n, "%s must be a list, not %s", path, kindName(n))
			return
		}
		for i, c := range n.Content {
			v.walk(path+idx(i), c, s.Items)
		}
	case "string":
		if n.Kind != yaml.ScalarNode || tagOf(n) != "str" && tagOf(n) != "binary" && tagOf(n) != "timestamp" {
			hint := ""
			if n.Kind == yaml.ScalarNode {
				hint = fmt.Sprintf(" (quote it: \"%s\")", n.Value)
			}
			v.add(path, n, "%s must be a string, not %s%s", path, kindName(n), hint)
			return
		}
		if s.Format == "byte" && tagOf(n) == "str" {
			if _, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(n.Value), "")); err != nil {
				v.add(path, n, "%s must be base64-encoded (put plain text under stringData instead of data)", path)
			}
		}
		v.enum(path, n, s)
	case "integer":
		if n.Kind != yaml.ScalarNode || tagOf(n) != "int" {
			v.add(path, n, "%s must be an integer, not %s", path, kindName(n))
		}
	case "number":
		if n.Kind != yaml.ScalarNode || tagOf(n) != "int" && tagOf(n) != "float" {
			v.add(path, n, "%s must be a number, not %s", path, kindName(n))
		}
	case "boolean":
		if n.Kind != yaml.ScalarNode || tagOf(n) != "bool" {
			hint := ""
			if n.Kind == yaml.ScalarNode {
				hint = " (true or false, unquoted)"
			}
			v.add(path, n, "%s must be a boolean, not %s%s", path, kindName(n), hint)
		}
	default:
		if s.Properties != nil {
			v.object(path, n, s)
		}
	}
}

func (v *validator) enum(path string, n *yaml.Node, s *schemaNode) {
	if len(s.Enum) == 0 {
		return
	}
	for _, e := range s.Enum {
		if fmt.Sprint(e) == n.Value {
			return
		}
	}
	vals := make([]string, len(s.Enum))
	for i, e := range s.Enum {
		vals[i] = fmt.Sprint(e)
	}
	v.add(path, n, "%s must be one of %s, not %s", path, strings.Join(vals, ", "), quoteShort(n.Value))
}

func (v *validator) quantity(path string, n *yaml.Node) {
	if n.Kind != yaml.ScalarNode || !quantityRe.MatchString(strings.TrimSpace(n.Value)) {
		v.add(path, n, "%s must be a quantity like 250m, 0.5, 512Mi or 1Gi, not %s", path, kindName(n))
	}
}

func (v *validator) object(path string, n *yaml.Node, s *schemaNode) {
	if n.Kind != yaml.MappingNode {
		v.add(path, n, "%s must be an object, not %s", orRoot(path), kindName(n))
		return
	}
	if s.PreserveUnknown && s.Properties == nil {
		return
	}
	free := s.Properties == nil && s.AdditionalProperties == nil
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, val := n.Content[i], n.Content[i+1]
		if k.Value == "<<" {
			continue
		}
		p := field(path, k.Value)
		if ps, ok := s.Properties[k.Value]; ok {
			v.walk(p, val, ps)
			continue
		}
		switch {
		case s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil:
			v.walk(p, val, s.AdditionalProperties.Schema)
		case free, s.PreserveUnknown, s.AdditionalProperties != nil && s.AdditionalProperties.Allow:
		default:
			v.add(p, k, "unknown field %q in %s%s", k.Value, orRoot(path), suggest(k.Value, s.Properties))
		}
	}
	for _, r := range s.Required {
		if isNull(key(n, r)) {
			if r == "metadata" || r == "apiVersion" || r == "kind" {
				continue // checked by the renderer (k8s_object)
			}
			v.add(path, n, "%s is missing required field %q", orRoot(path), r)
		}
	}
}

func orRoot(path string) string {
	if path == "" {
		return "the object"
	}
	return path
}

// suggest names the known field closest to an unknown one (a typo or a
// wrong case: "contianers", "containerport").
func suggest(name string, props map[string]*schemaNode) string {
	best, bestD := "", 3
	for p := range props {
		if strings.EqualFold(p, name) {
			return fmt.Sprintf(" (did you mean %q?)", p)
		}
		if d := distance(strings.ToLower(name), strings.ToLower(p)); d < bestD || d == bestD && best != "" && p < best {
			best, bestD = p, d
		}
	}
	if best != "" && bestD <= 2 && len(name) > 3 {
		return fmt.Sprintf(" (did you mean %q?)", best)
	}
	known := make([]string, 0, len(props))
	for p := range props {
		known = append(known, p)
	}
	slices.Sort(known)
	if len(known) > 0 && len(known) <= 12 {
		return "; known fields: " + strings.Join(known, ", ")
	}
	return ""
}

// distance is the Levenshtein distance of a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
