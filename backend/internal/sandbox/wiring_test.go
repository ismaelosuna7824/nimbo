package sandbox

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func expr(s string) model.Value { return model.Value{Expr: s} }

func provider(name string, attrs ...model.Attr) model.NamedBlock {
	return model.NamedBlock{Name: name, Body: model.Body{Attrs: attrs}}
}

func attr(name, e string) model.Attr { return model.Attr{Name: name, Value: expr(e)} }

// parseBlocks parses generated HCL and returns "type label alias" keys with
// their attribute source text.
func parseBlocks(t *testing.T, src []byte) map[string]map[string]string {
	t.Helper()
	f, diags := hclsyntax.ParseConfig(src, "gen.tf", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("generated HCL does not parse: %s\n%s", diags.Error(), src)
	}
	out := map[string]map[string]string{}
	for _, b := range f.Body.(*hclsyntax.Body).Blocks {
		attrs := map[string]string{}
		for name, a := range b.Body.Attributes {
			attrs[name] = strings.TrimSpace(string(a.Expr.Range().SliceBytes(src)))
		}
		for _, nb := range b.Body.Blocks {
			key := "block:" + nb.Type
			if nb.Type == "backend" {
				key += ":" + nb.Labels[0]
			}
			attrs[key] = ""
		}
		key := b.Type
		if len(b.Labels) > 0 {
			key += " " + b.Labels[0]
		}
		if alias, ok := attrs["alias"]; ok {
			key += " " + strings.Trim(alias, `"`)
		}
		out[key] = attrs
	}
	return out
}

func TestWiringAWSAliasesAndBackend(t *testing.T) {
	wf := &model.Workflow{
		ID:     "wf",
		Clouds: []model.Cloud{model.CloudAWS},
		Providers: []model.NamedBlock{
			provider("aws", attr("region", `"eu-west-1"`), attr("profile", `"prod"`)),
			provider("aws", attr("alias", `"use1"`), attr("region", `"us-east-1"`)),
		},
		Terraform: &model.Body{Blocks: []model.Block{
			{Type: "backend", Labels: []string{"s3"}, Body: model.Body{Attrs: []model.Attr{attr("bucket", `"state"`)}}},
		}},
	}
	files := wiringFiles(wf, nil)
	if _, ok := files[providersFile]; ok {
		t.Fatalf("no generated provider block expected when an unaliased aws block exists:\n%s", files[providersFile])
	}
	blocks := parseBlocks(t, files[overrideFile])

	if _, ok := blocks["terraform"]["block:backend:local"]; !ok {
		t.Errorf("backend not overridden with local: %v", blocks["terraform"])
	}
	def, ok := blocks["provider aws"]
	if !ok {
		t.Fatalf("missing default aws override: %v", blocks)
	}
	for name, want := range map[string]string{
		"skip_credentials_validation": "true",
		"skip_metadata_api_check":     "true",
		"skip_requesting_account_id":  "false",
		"s3_use_path_style":           "true",
		"access_key":                  `"test"`,
		"secret_key":                  `"test"`,
		"profile":                     "null",
	} {
		if def[name] != want {
			t.Errorf("default aws %s = %q, want %q", name, def[name], want)
		}
	}
	if _, set := def["region"]; set {
		t.Errorf("region must be left as the user set it")
	}
	aliased, ok := blocks["provider aws use1"]
	if !ok {
		t.Fatalf("missing aliased aws override: %v", blocks)
	}
	if aliased["s3_use_path_style"] != "true" {
		t.Errorf("aliased override lacks floci settings: %v", aliased)
	}
	if _, set := aliased["profile"]; set {
		t.Errorf("profile nulled on a block that did not set it")
	}
}

func TestWiringAliasOnlyAddsDefaultProvider(t *testing.T) {
	wf := &model.Workflow{
		Clouds:    []model.Cloud{model.CloudAWS},
		Providers: []model.NamedBlock{provider("aws", attr("alias", `"west"`))},
	}
	files := wiringFiles(wf, nil)
	if _, ok := parseBlocks(t, files[overrideFile])["provider aws west"]; !ok {
		t.Errorf("aliased block not overridden:\n%s", files[overrideFile])
	}
	gen := parseBlocks(t, files[providersFile])
	if gen["provider aws"]["s3_use_path_style"] != "true" {
		t.Errorf("default aws provider not generated:\n%s", files[providersFile])
	}
}

func TestWiringGeneratedProvidersPerCloud(t *testing.T) {
	wf := &model.Workflow{Clouds: []model.Cloud{model.CloudAWS, model.CloudAzure, model.CloudGCP, model.CloudOCI}}
	files := wiringFiles(wf, nil)
	if _, ok := files[overrideFile]; ok {
		t.Errorf("override file written without user blocks:\n%s", files[overrideFile])
	}
	blocks := parseBlocks(t, files[providersFile])

	az := blocks["provider azurerm"]
	if _, ok := az["block:features"]; !ok {
		t.Errorf("azurerm block lacks features {}: %v", az)
	}
	if az["metadata_host"] != `"localhost:4577"` || az["environment"] != `"stack"` || az["use_cli"] != "false" {
		t.Errorf("azurerm wiring wrong: %v", az)
	}
	if az["resource_provider_registrations"] != `"none"` {
		t.Errorf("azurerm v4 default should use resource_provider_registrations: %v", az)
	}

	g := blocks["provider google"]
	if g["project"] != `"floci-local"` || g["storage_custom_endpoint"] != `"http://localhost:4588/storage/v1/"` || g["access_token"] != `"fake"` {
		t.Errorf("google wiring wrong: %v", g)
	}

	o := blocks["provider oci"]
	if o["tenancy_ocid"] != `"`+ociTenancy+`"` || o["private_key_path"] != `"`+ociKeyPath+`"` || o["region"] != `"us-ashburn-1"` {
		t.Errorf("oci wiring wrong: %v", o)
	}
}

func TestWiringAzureV3AndUserValuesKept(t *testing.T) {
	wf := &model.Workflow{
		Clouds: []model.Cloud{model.CloudAzure, model.CloudGCP, model.CloudOCI},
		Providers: []model.NamedBlock{
			{Name: "azurerm", Body: model.Body{Blocks: []model.Block{{Type: "features"}}}},
			provider("google", attr("project", `var.project`), attr("credentials", `file("key.json")`)),
			provider("oci", attr("region", `"eu-frankfurt-1"`), attr("tenancy_ocid", `var.tenancy`)),
		},
	}
	blocks := parseBlocks(t, wiringFiles(wf, map[string]string{"azurerm": "3.117.0"})[overrideFile])
	az := blocks["provider azurerm"]
	if az["skip_provider_registration"] != "true" {
		t.Errorf("azurerm v3 should use skip_provider_registration: %v", az)
	}
	if _, ok := az["resource_provider_registrations"]; ok {
		t.Errorf("v4-only attribute set for azurerm v3: %v", az)
	}
	if _, ok := az["block:features"]; ok {
		t.Errorf("override must not replace the user's features block")
	}
	g := blocks["provider google"]
	if _, ok := g["project"]; ok {
		t.Errorf("user project overridden: %v", g)
	}
	if g["credentials"] != "null" {
		t.Errorf("user credentials not cleared: %v", g)
	}
	o := blocks["provider oci"]
	if _, ok := o["region"]; ok {
		t.Errorf("user region overridden: %v", o)
	}
	if _, ok := o["tenancy_ocid"]; ok {
		t.Errorf("user tenancy overridden: %v", o)
	}
}

func TestWiringAzureFeaturesKeepUserSettings(t *testing.T) {
	userFeatures := model.Block{Type: "features", Body: model.Body{Blocks: []model.Block{
		{Type: "resource_group", Body: model.Body{Attrs: []model.Attr{attr("prevent_deletion_if_contains_resources", "false")}}},
		{Type: "key_vault", Body: model.Body{Attrs: []model.Attr{attr("purge_soft_delete_on_destroy", "true")}}},
	}}}
	wf := &model.Workflow{
		Clouds:    []model.Cloud{model.CloudAzure},
		Providers: []model.NamedBlock{{Name: "azurerm", Body: model.Body{Blocks: []model.Block{userFeatures}}}},
	}
	src := wiringFiles(wf, map[string]string{"azurerm": "4.81.0"})[overrideFile]
	f, diags := hclsyntax.ParseConfig(src, "o.tf", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parse: %s\n%s", diags.Error(), src)
	}
	prov := f.Body.(*hclsyntax.Body).Blocks[0]
	var features *hclsyntax.Block
	for _, b := range prov.Body.Blocks {
		if b.Type == "features" {
			features = b
		}
	}
	if features == nil {
		t.Fatalf("no features override:\n%s", src)
	}
	got := map[string]string{}
	for _, b := range features.Body.Blocks {
		for name, a := range b.Body.Attributes {
			got[b.Type+"."+name] = strings.TrimSpace(string(a.Expr.Range().SliceBytes(src)))
		}
	}
	want := map[string]string{
		"resource_group.prevent_deletion_if_contains_resources": "false",
		"key_vault.purge_soft_delete_on_destroy":                "true",
		"storage.data_plane_available":                          "false",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("features %s = %q, want %q\n%s", k, got[k], v, src)
		}
	}
	if wf.Providers[0].Body.Blocks[0].Body.Blocks[0].Body.Attrs[0].Name != "prevent_deletion_if_contains_resources" || len(userFeatures.Body.Blocks) != 2 {
		t.Errorf("workflow mutated")
	}

	old := wiringFiles(wf, map[string]string{"azurerm": "4.8.0"})[overrideFile]
	if strings.Contains(string(old), "data_plane_available") || strings.Contains(string(old), "features") {
		t.Errorf("azurerm < 4.9 must keep the user's features block untouched:\n%s", old)
	}
}

func TestWiringRequiredProvidersLocalNames(t *testing.T) {
	wf := &model.Workflow{
		Clouds: []model.Cloud{model.CloudAWS},
		Terraform: &model.Body{Blocks: []model.Block{{Type: "required_providers", Body: model.Body{Attrs: []model.Attr{
			attr("awsx", `{ source = "hashicorp/aws", version = "~> 6.0" }`),
		}}}}},
		Providers: []model.NamedBlock{provider("awsx", attr("region", `"us-west-2"`))},
	}
	files := wiringFiles(wf, nil)
	if _, ok := parseBlocks(t, files[overrideFile])["provider awsx"]; !ok {
		t.Errorf("provider with custom local name not overridden:\n%s", files[overrideFile])
	}
	if _, ok := files[providersFile]; ok {
		t.Errorf("unexpected generated providers:\n%s", files[providersFile])
	}
}

func TestLockVersions(t *testing.T) {
	src := []byte(`
provider "registry.terraform.io/hashicorp/azurerm" {
  version     = "4.12.0"
  constraints = "~> 4.0"
  hashes = ["h1:abc="]
}
provider "registry.terraform.io/hashicorp/aws" {
  version = "6.69.1"
}
`)
	v := lockVersions(src)
	if v["azurerm"] != "4.12.0" || v["aws"] != "6.69.1" {
		t.Errorf("lockVersions = %v", v)
	}
}

func TestTfvarsContent(t *testing.T) {
	env := &model.Environment{Values: map[string]model.Value{
		"name":  {Expr: `"demo"`},
		"count": {Literal: json.RawMessage(`3`)},
		"tags":  {Literal: json.RawMessage(`{"env":"dev"}`)},
	}}
	got := string(tfvarsFileContent(env))
	want := "count = 3\nname = \"demo\"\ntags = {\"env\":\"dev\"}\n"
	if got != want {
		t.Errorf("tfvars:\n%s\nwant:\n%s", got, want)
	}
	if _, diags := hclsyntax.ParseConfig([]byte(got), "x.tfvars", hcl.InitialPos); diags.HasErrors() {
		t.Errorf("tfvars does not parse: %s", diags.Error())
	}
}

func TestRunEnvUserValuesWin(t *testing.T) {
	base, user := runEnv([]model.Cloud{model.CloudAWS}, &model.Environment{EnvVars: []model.EnvVar{
		{Key: "AWS_REGION", Value: "eu-west-1"},
		{Key: "API_TOKEN", Value: "s3cr3t", Secret: true},
	}}, DefaultAWSRegion)
	for _, kv := range base {
		if strings.HasPrefix(kv, "AWS_REGION=") {
			t.Errorf("default AWS_REGION kept despite user value: %v", base)
		}
	}
	if len(user) != 2 || user[1] != "API_TOKEN=s3cr3t" {
		t.Errorf("user env = %v", user)
	}
}
