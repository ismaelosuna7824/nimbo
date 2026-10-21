// Command gen regenerates the embedded provider schemas in ../data from
// `terraform providers schema -json`. Run it from the backend module with
// `go generate ./internal/catalog/...`.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog/schemafile"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

type provider struct {
	name       string // local name and output file name
	source     string // registry source
	constraint string // version constraint ("" = latest)
}

var providers = []provider{
	{"aws", "hashicorp/aws", "~> 6.0"},
	{"azurerm", "hashicorp/azurerm", "~> 4.0"},
	{"google", "hashicorp/google", "~> 7.0"},
	{"oci", "oracle/oci", ""},
	{"archive", "hashicorp/archive", "~> 2.0"},
	{"random", "hashicorp/random", "~> 3.0"},
}

func main() {
	out := flag.String("out", "data", "output directory")
	pin := flag.Bool("pin", false, "keep the provider versions of the existing index (regenerate without upgrading)")
	flag.Parse()
	log.SetFlags(0)
	if err := run(*out, *pin); err != nil {
		log.Fatal(err)
	}
}

// pinnedVersions reads the provider versions of the index in out.
func pinnedVersions(out string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(out, "index.json.gz"))
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var idx schemafile.Index
	if err := json.NewDecoder(zr).Decode(&idx); err != nil {
		return nil, err
	}
	versions := map[string]string{}
	for _, p := range idx.Providers {
		versions[p.Name] = p.Version
	}
	return versions, nil
}

func run(out string, pin bool) error {
	work, err := os.MkdirTemp("", "nimbo-catalog-gen-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	var pinned map[string]string
	if pin {
		if pinned, err = pinnedVersions(out); err != nil {
			return fmt.Errorf("pin: %w", err)
		}
	}
	var tf strings.Builder
	tf.WriteString("terraform {\n  required_providers {\n")
	for _, p := range providers {
		fmt.Fprintf(&tf, "    %s = {\n      source = %q\n", p.name, p.source)
		constraint := p.constraint
		if v := pinned[p.name]; v != "" {
			constraint = "= " + v
		}
		if constraint != "" {
			fmt.Fprintf(&tf, "      version = %q\n", constraint)
		}
		tf.WriteString("    }\n")
	}
	tf.WriteString("  }\n}\n")
	if err := os.WriteFile(filepath.Join(work, "main.tf"), []byte(tf.String()), 0o644); err != nil {
		return err
	}

	tfx, err := newTerraform(work)
	if err != nil {
		return err
	}
	log.Printf("terraform init (%s)", tfx.describe())
	if _, err := tfx.run("init", "-input=false", "-no-color"); err != nil {
		return err
	}
	log.Print("terraform providers schema -json")
	schemaJSON, err := tfx.run("providers", "schema", "-json")
	if err != nil {
		return err
	}
	versionJSON, err := tfx.run("version", "-json")
	if err != nil {
		return err
	}
	log.Print("terraform metadata functions -json")
	functionsJSON, err := tfx.run("metadata", "functions", "-json")
	if err != nil {
		return err
	}

	var schemas tfSchemas
	if err := json.Unmarshal(schemaJSON, &schemas); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	var ver struct {
		TerraformVersion   string            `json:"terraform_version"`
		ProviderSelections map[string]string `json:"provider_selections"`
	}
	if err := json.Unmarshal(versionJSON, &ver); err != nil {
		return fmt.Errorf("decode version: %w", err)
	}
	functions, err := trimFunctions(functionsJSON)
	if err != nil {
		return err
	}
	functions.Terraform = ver.TerraformVersion

	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	var index schemafile.Index
	for _, p := range providers {
		addr := "registry.terraform.io/" + p.source
		ps, ok := schemas.ProviderSchemas[addr]
		if !ok {
			return fmt.Errorf("provider %s missing from schema output", addr)
		}
		provider := trim(ps.Provider)
		f := schemafile.File{
			Name:      p.name,
			Source:    p.source,
			Version:   ver.ProviderSelections[addr],
			Provider:  &provider,
			Resources: map[string]schemafile.Entry{},
			Data:      map[string]schemafile.Entry{},
		}
		ip := schemafile.IndexProvider{Name: f.Name, Source: f.Source, Version: f.Version}
		for typ, s := range ps.ResourceSchemas {
			f.Resources[typ] = trim(s)
			ip.Resources = append(ip.Resources, typ)
		}
		for typ, s := range ps.DataSourceSchemas {
			f.Data[typ] = trim(s)
			ip.Data = append(ip.Data, typ)
		}
		sort.Strings(ip.Resources)
		sort.Strings(ip.Data)
		index.Providers = append(index.Providers, ip)
		path := filepath.Join(out, p.name+".json.gz")
		size, err := writeGzip(path, f)
		if err != nil {
			return err
		}
		log.Printf("%-8s %-8s %5d resources %5d data sources  %7.1f KiB  %s",
			p.name, f.Version, len(f.Resources), len(f.Data), float64(size)/1024, path)
	}
	size, err := writeGzip(filepath.Join(out, "index.json.gz"), index)
	if err != nil {
		return err
	}
	log.Printf("index %7.1f KiB", float64(size)/1024)
	size, err = writeGzip(filepath.Join(out, "functions.json.gz"), functions)
	if err != nil {
		return err
	}
	log.Printf("functions %d  %7.1f KiB", len(functions.Functions), float64(size)/1024)
	return nil
}

// trimFunctions converts `terraform metadata functions -json` into the
// embedded list: core functions under their plain names ("core::abs" is
// the same function as "abs"), descriptions shortened like attributes'.
func trimFunctions(raw []byte) (schemafile.Functions, error) {
	type param struct {
		Name        string          `json:"name"`
		Type        json.RawMessage `json:"type"`
		Description string          `json:"description"`
		IsNullable  bool            `json:"is_nullable"`
	}
	var meta struct {
		Signatures map[string]struct {
			Description       string          `json:"description"`
			ReturnType        json.RawMessage `json:"return_type"`
			Parameters        []param         `json:"parameters"`
			VariadicParameter *param          `json:"variadic_parameter"`
		} `json:"function_signatures"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return schemafile.Functions{}, fmt.Errorf("decode functions: %w", err)
	}
	conv := func(p param) model.FunctionParameter {
		return model.FunctionParameter{Name: p.Name, Type: p.Type, Description: cleanDescription(p.Description), Nullable: p.IsNullable}
	}
	var out schemafile.Functions
	for name, s := range meta.Signatures {
		if strings.Contains(name, "::") {
			continue
		}
		fn := model.FunctionSignature{
			Name:        name,
			Description: cleanDescription(s.Description),
			ReturnType:  s.ReturnType,
			Parameters:  []model.FunctionParameter{},
		}
		for _, p := range s.Parameters {
			fn.Parameters = append(fn.Parameters, conv(p))
		}
		if s.VariadicParameter != nil {
			v := conv(*s.VariadicParameter)
			fn.VariadicParameter = &v
		}
		out.Functions = append(out.Functions, fn)
	}
	sort.Slice(out.Functions, func(i, j int) bool { return out.Functions[i].Name < out.Functions[j].Name })
	return out, nil
}

func writeGzip(path string, v any) (int, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return 0, err
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return 0, err
	}
	if _, err := zw.Write(raw); err != nil {
		return 0, err
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	return buf.Len(), os.WriteFile(path, buf.Bytes(), 0o644)
}

// terraform runs the host binary when available, otherwise the official
// docker image with the work dir mounted.
type terraform struct {
	dir    string
	binary string
	docker bool
}

func newTerraform(dir string) (*terraform, error) {
	if bin, err := exec.LookPath("terraform"); err == nil {
		return &terraform{dir: dir, binary: bin}, nil
	}
	if bin, err := exec.LookPath("docker"); err == nil {
		return &terraform{dir: dir, binary: bin, docker: true}, nil
	}
	return nil, fmt.Errorf("neither terraform nor docker found on PATH")
}

func (t *terraform) describe() string {
	if t.docker {
		return "docker hashicorp/terraform"
	}
	return t.binary
}

func (t *terraform) run(args ...string) ([]byte, error) {
	var cmd *exec.Cmd
	if t.docker {
		full := append([]string{"run", "--rm", "-v", t.dir + ":/work", "-w", "/work", "hashicorp/terraform:latest"}, args...)
		cmd = exec.Command(t.binary, full...)
	} else {
		cmd = exec.Command(t.binary, args...)
		cmd.Dir = t.dir
		cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
		if cache, err := os.UserCacheDir(); err == nil {
			dir := filepath.Join(cache, "nimbo", "terraform-plugins")
			if os.MkdirAll(dir, 0o755) == nil {
				cmd.Env = append(cmd.Env, "TF_PLUGIN_CACHE_DIR="+dir)
			}
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("terraform %s: %w\n%s%s", strings.Join(args, " "), err, stderr.String(), stdout.String())
	}
	return stdout.Bytes(), nil
}

type tfSchemas struct {
	ProviderSchemas map[string]struct {
		Provider          tfSchema            `json:"provider"`
		ResourceSchemas   map[string]tfSchema `json:"resource_schemas"`
		DataSourceSchemas map[string]tfSchema `json:"data_source_schemas"`
	} `json:"provider_schemas"`
}

type tfSchema struct {
	Block tfBlock `json:"block"`
}

type tfBlock struct {
	Attributes  map[string]tfAttribute `json:"attributes"`
	BlockTypes  map[string]tfBlockType `json:"block_types"`
	Description string                 `json:"description"`
	Deprecated  bool                   `json:"deprecated"`
}

type tfAttribute struct {
	Type        json.RawMessage `json:"type"`
	NestedType  *tfNestedType   `json:"nested_type"`
	Description string          `json:"description"`
	Required    bool            `json:"required"`
	Optional    bool            `json:"optional"`
	Computed    bool            `json:"computed"`
	Sensitive   bool            `json:"sensitive"`
	Deprecated  bool            `json:"deprecated"`
}

type tfNestedType struct {
	Attributes  map[string]tfAttribute `json:"attributes"`
	NestingMode string                 `json:"nesting_mode"`
}

type tfBlockType struct {
	NestingMode string  `json:"nesting_mode"`
	Block       tfBlock `json:"block"`
	MinItems    int     `json:"min_items"`
	MaxItems    int     `json:"max_items"`
}

func trim(s tfSchema) schemafile.Entry {
	return schemafile.Entry{
		Description: cleanDescription(s.Block.Description),
		Block:       trimBlock(s.Block),
	}
}

func trimBlock(b tfBlock) model.SchemaBlock {
	out := model.SchemaBlock{
		Attributes: []model.SchemaAttribute{},
		Blocks:     []model.SchemaNestedBlock{},
	}
	for name, a := range b.Attributes {
		out.Attributes = append(out.Attributes, model.SchemaAttribute{
			Name:        name,
			Type:        attrType(a),
			Required:    a.Required,
			Optional:    a.Optional,
			Computed:    a.Computed,
			Sensitive:   a.Sensitive,
			Deprecated:  a.Deprecated || strings.HasPrefix(strings.ToLower(a.Description), "deprecated"),
			Description: cleanDescription(a.Description),
		})
	}
	sort.Slice(out.Attributes, func(i, j int) bool { return out.Attributes[i].Name < out.Attributes[j].Name })
	for name, bt := range b.BlockTypes {
		out.Blocks = append(out.Blocks, model.SchemaNestedBlock{
			Name:     name,
			Nesting:  bt.NestingMode,
			MinItems: bt.MinItems,
			MaxItems: bt.MaxItems,
			Block:    trimBlock(bt.Block),
		})
	}
	sort.Slice(out.Blocks, func(i, j int) bool { return out.Blocks[i].Name < out.Blocks[j].Name })
	return out
}

// attrType returns the cty type JSON of an attribute; attributes declared with
// a nested_type (protocol 6) are converted to the equivalent object type.
func attrType(a tfAttribute) json.RawMessage {
	if a.NestedType == nil {
		return a.Type
	}
	obj := map[string]json.RawMessage{}
	for name, na := range a.NestedType.Attributes {
		obj[name] = attrType(na)
	}
	objJSON, _ := json.Marshal([]any{"object", obj})
	var t any = json.RawMessage(objJSON)
	switch a.NestedType.NestingMode {
	case "list":
		t = []any{"list", t}
	case "set":
		t = []any{"set", t}
	case "map":
		t = []any{"map", t}
	}
	raw, _ := json.Marshal(t)
	return raw
}

// cleanDescription keeps the first paragraph of a description, bounded in
// size: full provider docs would multiply the embedded data size while the
// UI only shows a short hint.
func cleanDescription(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.Join(strings.Fields(s), " ")
	const max = 400
	if len(s) > max {
		cut := strings.LastIndex(s[:max], " ")
		if cut < max/2 {
			cut = max
		}
		s = s[:cut] + "…"
	}
	return s
}
