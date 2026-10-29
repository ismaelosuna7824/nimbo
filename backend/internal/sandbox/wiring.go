package sandbox

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Run-time files written next to the exported Terraform. Override files
// merge into the user's provider blocks (Terraform override semantics), so
// exported code stays cloud-ready.
const (
	overrideFile  = "zz_nimbo_floci_override.tf"
	providersFile = "zz_nimbo_floci_providers.tf"
	tfvarsFile    = "zz_nimbo.auto.tfvars"

	ociKeyPath = "/work/.nimbo/oci_api_key.pem"
	certDir    = ".nimbo/certs"
)

// Fake identities accepted by the emulators.
const (
	azureSubscription = "00000000-0000-0000-0000-000000000001"
	azureTenant       = "00000000-0000-0000-0000-000000000002"
	azureClient       = "00000000-0000-0000-0000-000000000003"
	ociTenancy        = "ocid1.tenancy.oc1..flocilocaltenancy0000000000000000000000000000000000000000"
	ociUser           = "ocid1.user.oc1..nimbolocaluser000000000000000000000000000000000000000000000"
	ociFingerprint    = "aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99"
	gcpProject        = "floci-local"
)

var defaultLocalNames = map[model.Cloud][]string{
	model.CloudAWS:   {"aws"},
	model.CloudAzure: {"azurerm"},
	model.CloudGCP:   {"google"},
	model.CloudOCI:   {"oci"},
}

func cloudOfSource(source string) (model.Cloud, bool) {
	s := strings.ToLower(source)
	s = strings.TrimPrefix(s, "registry.terraform.io/")
	switch s {
	case "hashicorp/aws":
		return model.CloudAWS, true
	case "hashicorp/azurerm":
		return model.CloudAzure, true
	case "hashicorp/google", "hashicorp/google-beta":
		return model.CloudGCP, true
	case "oracle/oci", "hashicorp/oci":
		return model.CloudOCI, true
	}
	return "", false
}

func cloudOfLocalName(name string) (model.Cloud, bool) {
	switch name {
	case "aws":
		return model.CloudAWS, true
	case "azurerm":
		return model.CloudAzure, true
	case "google", "google-beta":
		return model.CloudGCP, true
	case "oci":
		return model.CloudOCI, true
	}
	return "", false
}

// evalConst evaluates a value that must be a constant expression.
func evalConst(v model.Value) (cty.Value, bool) {
	if v.Expr != "" {
		expr, diags := hclsyntax.ParseExpression([]byte(v.Expr), "value.hcl", hcl.InitialPos)
		if diags.HasErrors() {
			return cty.NilVal, false
		}
		val, diags := expr.Value(nil)
		if diags.HasErrors() || !val.IsWhollyKnown() {
			return cty.NilVal, false
		}
		return val, true
	}
	if len(v.Literal) > 0 {
		var s string
		if json.Unmarshal(v.Literal, &s) == nil {
			return cty.StringVal(s), true
		}
	}
	return cty.NilVal, false
}

func constString(v model.Value) (string, bool) {
	val, ok := evalConst(v)
	if !ok || val.IsNull() || val.Type() != cty.String {
		return "", false
	}
	return val.AsString(), true
}

func findAttr(b model.Body, name string) (model.Attr, bool) {
	for _, a := range b.Attrs {
		if a.Name == name {
			return a, true
		}
	}
	return model.Attr{}, false
}

func hasAttr(b model.Body, name string) bool {
	_, ok := findAttr(b, name)
	return ok
}

// requiredProviders maps local provider names declared in
// terraform.required_providers to their cloud ("" for other providers).
func requiredProviders(wf *model.Workflow) map[string]model.Cloud {
	out := map[string]model.Cloud{}
	if wf.Terraform == nil {
		return out
	}
	for _, blk := range wf.Terraform.Blocks {
		if blk.Type != "required_providers" {
			continue
		}
		for _, a := range blk.Body.Attrs {
			c, _ := cloudOfLocalName(a.Name)
			if val, ok := evalConst(a.Value); ok && !val.IsNull() {
				src := cty.NullVal(cty.String)
				switch {
				case val.Type().IsObjectType() && val.Type().HasAttribute("source"):
					src = val.GetAttr("source")
				case val.Type().IsMapType() && val.HasIndex(cty.StringVal("source")).True():
					src = val.Index(cty.StringVal("source"))
				}
				if src.Type() == cty.String && !src.IsNull() {
					c, _ = cloudOfSource(src.AsString())
				}
			}
			out[a.Name] = c
		}
	}
	return out
}

// providerCloud resolves the cloud of a provider local name.
func providerCloud(required map[string]model.Cloud, name string) (model.Cloud, bool) {
	if c, declared := required[name]; declared {
		return c, c != ""
	}
	return cloudOfLocalName(name)
}

type hclAttr struct {
	name string
	expr string // rendered HCL expression
}

func hclString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`, "${", "$${", "%{", "%%{")
	return `"` + r.Replace(s) + `"`
}

func writeBlock(b *strings.Builder, header string, attrs []hclAttr, nested []string) {
	b.WriteString(header + " {\n")
	width := 0
	for _, a := range attrs {
		width = max(width, len(a.name))
	}
	for _, a := range attrs {
		fmt.Fprintf(b, "  %-*s = %s\n", width, a.name, a.expr)
	}
	for _, n := range nested {
		b.WriteString("  " + n + "\n")
	}
	b.WriteString("}\n\n")
}

const flociAWS = "http://localhost:4566"
const flociGCP = "http://localhost:4588"
const flociOCI = "http://localhost:4599"

// gcpEndpoints are the google provider *_custom_endpoint settings floci-gcp
// serves over REST.
var gcpEndpoints = []hclAttr{
	{"storage_custom_endpoint", hclString(flociGCP + "/storage/v1/")},
	{"pubsub_custom_endpoint", hclString(flociGCP + "/v1/")},
	{"kms_custom_endpoint", hclString(flociGCP + "/v1/")},
	{"secret_manager_custom_endpoint", hclString(flociGCP + "/v1/")},
	{"iam_custom_endpoint", hclString(flociGCP + "/")},
	{"iam_beta_custom_endpoint", hclString(flociGCP + "/v1/")},
	{"service_usage_custom_endpoint", hclString(flociGCP + "/v1/")},
	{"resource_manager_custom_endpoint", hclString(flociGCP + "/v1/")},
	{"cloud_run_custom_endpoint", hclString(flociGCP + "/v2/")},
	{"cloud_run_v2_custom_endpoint", hclString(flociGCP + "/v2/")},
	{"sql_custom_endpoint", hclString(flociGCP + "/sql/v1beta4/")},
	{"big_query_custom_endpoint", hclString(flociGCP + "/bigquery/v2/")},
	{"container_custom_endpoint", hclString(flociGCP + "/container/v1/")},
	{"compute_custom_endpoint", hclString(flociGCP + "/compute/v1/")},
	{"cloud_scheduler_custom_endpoint", hclString(flociGCP + "/v1/")},
	{"cloudfunctions2_custom_endpoint", hclString(flociGCP + "/v2/")},
}

// gcpLegacyEndpoints replace gcpEndpoints entries for hashicorp/google and
// google-beta before 7.26. Those versions strip the API version from a
// custom endpoint only when it starts with https://, so resources built on
// Google's API client libraries, which append the versioned path
// themselves, requested doubled paths (/sql/v1beta4/sql/v1beta4/...,
// /v1/v1/...) and got 404: Cloud SQL instances and users, project IAM
// members, google_project_service, GKE. These roots serve them; the
// resources generated from the REST definitions on the same APIs then miss
// the version (google_sql_database), see legacyGoogleWarning.
var gcpLegacyEndpoints = map[string]string{
	"sql_custom_endpoint":              flociGCP + "/",
	"resource_manager_custom_endpoint": flociGCP + "/",
	"service_usage_custom_endpoint":    flociGCP + "/",
	"container_custom_endpoint":        flociGCP + "/container/",
}

// legacyGoogle reports a hashicorp/google(-beta) version before 7.26
// (false when unknown: init then selects the latest).
func legacyGoogle(version string) bool {
	major, minor := parseVersion(version)
	return major > 0 && (major < 7 || major == 7 && minor < 26)
}

// gcpProviderEndpoints are the custom endpoints for a google provider of
// version ("" when unknown).
func gcpProviderEndpoints(version string) []hclAttr {
	if !legacyGoogle(version) {
		return gcpEndpoints
	}
	out := slices.Clone(gcpEndpoints)
	for i, a := range out {
		if v, ok := gcpLegacyEndpoints[a.name]; ok {
			out[i].expr = hclString(v)
		}
	}
	return out
}

// lockVersion is the version .terraform.lock.hcl selected for the provider
// block named name of cloud c ("" when unknown or not version-dependent).
func lockVersion(c model.Cloud, name string, versions map[string]string) string {
	switch c {
	case model.CloudAzure:
		return versions["azurerm"]
	case model.CloudGCP:
		if name == "google-beta" {
			return versions["google-beta"]
		}
		return versions["google"]
	}
	return ""
}

// wiringVersionsChanged reports whether the lock-file versions that select
// wiring settings differ between before and after.
func wiringVersionsChanged(before, after map[string]string) bool {
	for _, p := range []string{"azurerm", "google", "google-beta"} {
		if before[p] != after[p] {
			return true
		}
	}
	return false
}

// legacyGoogleWarning names the resources of wf that fail in the sandbox
// because the lock file selected a google provider before 7.26 ("" when
// none do).
func legacyGoogleWarning(wf *model.Workflow, versions map[string]string) string {
	var version string
	for _, p := range []string{"google", "google-beta"} {
		if legacyGoogle(versions[p]) {
			version = "hashicorp/" + p + " " + versions[p]
			break
		}
	}
	if version == "" {
		return ""
	}
	var failing []string
	for _, n := range wf.Nodes {
		if n.Kind == model.KindResource && n.Type == "google_sql_database" {
			failing = append(failing, n.Type+"."+n.Name)
		}
	}
	if len(failing) == 0 {
		return ""
	}
	return fmt.Sprintf("Warning: %s predates 7.26: on the sandbox's local endpoints it requests Cloud SQL databases on a path the emulator doesn't serve, so creating %s fails locally (instances and users work). "+
		"Require hashicorp/google >= 7.26 in required_providers (e.g. version = \"~> 7.26\") to deploy them in the sandbox.", version, strings.Join(failing, ", "))
}

// ociClients are the OCI SDK clients floci-oci implements.
var ociClients = []string{
	"oci_identity.IdentityClient",
	"oci_object_storage.ObjectStorageClient",
	"oci_queue.QueueAdminClient",
	"oci_queue.QueueClient",
	"oci_kms.KmsVaultClient",
	"oci_kms.KmsManagementClient",
	"oci_kms.KmsCryptoClient",
	"oci_vault.VaultsClient",
	"oci_secrets.SecretsClient",
	"oci_streaming.StreamAdminClient",
	"oci_streaming.StreamClient",
	"oci_functions.FunctionsManagementClient",
	"oci_functions.FunctionsInvokeClient",
	"oci_containerengine.ContainerEngineClient",
}

// providerSettings returns the floci settings merged into a provider block
// of cloud c. body is the user's block (empty for generated blocks);
// version is the block's provider version from the lock file ("" when
// unknown), selecting version-specific azurerm and google settings.
func providerSettings(c model.Cloud, body model.Body, version string) (attrs []hclAttr, nested []string) {
	nullIfSet := func(names ...string) {
		for _, n := range names {
			if hasAttr(body, n) {
				attrs = append(attrs, hclAttr{n, "null"})
			}
		}
	}
	switch c {
	case model.CloudAWS:
		attrs = []hclAttr{
			{"access_key", hclString("test")},
			{"secret_key", hclString("test")},
			{"skip_credentials_validation", "true"},
			{"skip_metadata_api_check", "true"},
			// The provider asks floci's STS for the account ID: resources that build their ID from it (Glue
			// catalog databases and tables "<account>:<name>", …) got ":<name>" when it was skipped, which the
			// next refresh refuses ("unexpected format for ID").
			{"skip_requesting_account_id", "false"},
			{"s3_use_path_style", "true"},
		}
		nullIfSet("profile", "token", "shared_config_files", "shared_credentials_files")
		for _, blk := range body.Blocks {
			// Explicit endpoints win over AWS_ENDPOINT_URL; point them at floci.
			if blk.Type != "endpoints" {
				continue
			}
			var b strings.Builder
			b.WriteString("endpoints {\n")
			for _, a := range blk.Body.Attrs {
				fmt.Fprintf(&b, "    %s = %s\n", a.Name, hclString(flociAWS))
			}
			b.WriteString("  }")
			nested = append(nested, b.String())
		}
	case model.CloudAzure:
		attrs = []hclAttr{
			{"environment", hclString("stack")},
			{"metadata_host", hclString("localhost:4577")},
			{"subscription_id", hclString(azureSubscription)},
			{"tenant_id", hclString(azureTenant)},
			{"client_id", hclString(azureClient)},
			{"client_secret", hclString("fake-secret")},
			{"use_cli", "false"},
			{"use_msi", "false"},
			{"use_oidc", "false"},
		}
		major, minor := parseVersion(version)
		if major > 0 && major < 4 {
			attrs = append(attrs, hclAttr{"skip_provider_registration", "true"})
		} else {
			attrs = append(attrs, hclAttr{"resource_provider_registrations", hclString("none")})
		}
		// storage.data_plane_available exists since azurerm 4.9; an unknown
		// version resolves to the latest release.
		if major == 0 || major > 4 || (major == 4 && minor >= 9) {
			nested = append(nested, azureFeatures(body))
		}
		nullIfSet("client_certificate_path", "client_certificate", "client_id_file_path", "client_secret_file_path", "auxiliary_tenant_ids")
	case model.CloudGCP:
		attrs = append(attrs, hclAttr{"access_token", hclString("fake")})
		if !hasAttr(body, "project") {
			attrs = append(attrs, hclAttr{"project", hclString(gcpProject)})
		}
		attrs = append(attrs, hclAttr{"user_project_override", "false"})
		attrs = append(attrs, gcpProviderEndpoints(version)...)
		nullIfSet("credentials", "impersonate_service_account")
	case model.CloudOCI:
		if !hasAttr(body, "tenancy_ocid") {
			attrs = append(attrs, hclAttr{"tenancy_ocid", hclString(ociTenancy)})
		}
		if !hasAttr(body, "region") {
			attrs = append(attrs, hclAttr{"region", hclString("us-ashburn-1")})
		}
		attrs = append(attrs,
			hclAttr{"auth", hclString("ApiKey")},
			hclAttr{"user_ocid", hclString(ociUser)},
			hclAttr{"fingerprint", hclString(ociFingerprint)},
			hclAttr{"private_key_path", hclString(ociKeyPath)},
			hclAttr{"disable_auto_retries", "true"},
		)
		nullIfSet("private_key", "private_key_password", "config_file_profile")
	}
	return attrs, nested
}

// wiringFiles renders the run-time override files for the clouds in
// wf.Clouds. providerVersions maps provider type ("azurerm", "google") to
// the version selected in .terraform.lock.hcl (empty when not yet
// initialized).
func wiringFiles(wf *model.Workflow, providerVersions map[string]string) map[string][]byte {
	required := requiredProviders(wf)
	clouds := map[model.Cloud]bool{}
	for _, c := range wf.Clouds {
		clouds[c] = true
	}

	var override, providers strings.Builder
	if wf.Terraform != nil {
		for _, blk := range wf.Terraform.Blocks {
			if blk.Type == "backend" || blk.Type == "cloud" {
				override.WriteString("terraform {\n  backend \"local\" {}\n}\n\n")
				break
			}
		}
	}

	unaliased := map[string]bool{}
	for _, p := range wf.Providers {
		c, ok := providerCloud(required, p.Name)
		if !ok || !clouds[c] {
			continue
		}
		header := "provider " + strconv.Quote(p.Name)
		attrs, nested := providerSettings(c, p.Body, lockVersion(c, p.Name, providerVersions))
		if a, ok := findAttr(p.Body, "alias"); ok {
			alias, ok := constString(a.Value)
			if !ok {
				continue
			}
			attrs = append([]hclAttr{{"alias", hclString(alias)}}, attrs...)
		} else {
			unaliased[p.Name] = true
		}
		writeBlock(&override, header, attrs, nested)
	}

	for _, c := range model.AllClouds {
		if !clouds[c] {
			continue
		}
		var names []string
		for name, rc := range required {
			if rc == c {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			names = defaultLocalNames[c]
		}
		sort.Strings(names)
		for _, name := range names {
			if unaliased[name] {
				continue
			}
			attrs, nested := providerSettings(c, model.Body{}, lockVersion(c, name, providerVersions))
			if c == model.CloudAzure && len(nested) == 0 {
				nested = []string{"features {}"}
			}
			writeBlock(&providers, "provider "+strconv.Quote(name), attrs, nested)
		}
	}

	files := map[string][]byte{}
	if override.Len() > 0 {
		files[overrideFile] = []byte(override.String())
	}
	if providers.Len() > 0 {
		files[providersFile] = []byte(providers.String())
	}
	return files
}

// parseVersion returns major and minor of a version like "4.12.0" (0, 0
// when unknown).
func parseVersion(v string) (major, minor int) {
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	major, _ = strconv.Atoi(parts[0])
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	return major, minor
}

// azureFeatures renders the user's features block with
// storage.data_plane_available = false added. Storage data-plane hosts
// (<account>.blob.core.windows.net, ...) do not resolve inside the
// sandbox; with the flag azurerm skips its data-plane availability polling,
// which otherwise stalls every storage account for minutes.
func azureFeatures(body model.Body) string {
	var features model.Body
	for _, blk := range body.Blocks {
		if blk.Type == "features" {
			features = blk.Body
			break
		}
	}
	flag := model.Attr{Name: "data_plane_available", Value: model.Value{Expr: "false"}}
	blocks := make([]model.Block, 0, len(features.Blocks)+1)
	found := false
	for _, blk := range features.Blocks {
		if blk.Type == "storage" {
			found = true
			if !hasAttr(blk.Body, flag.Name) {
				blk.Body.Attrs = append(slices.Clone(blk.Body.Attrs), flag)
			}
		}
		blocks = append(blocks, blk)
	}
	if !found {
		blocks = append(blocks, model.Block{Type: "storage", Body: model.Body{Attrs: []model.Attr{flag}}})
	}
	var b strings.Builder
	b.WriteString("features {\n")
	renderBody(&b, model.Body{Attrs: features.Attrs, Blocks: blocks}, "    ")
	b.WriteString("  }")
	return b.String()
}

// renderBody writes body as HCL; values use their canonical expression.
func renderBody(b *strings.Builder, body model.Body, indent string) {
	for _, a := range body.Attrs {
		expr := a.Value.Expr
		if expr == "" {
			expr = string(a.Value.Literal)
		}
		if expr == "" {
			expr = "null"
		}
		fmt.Fprintf(b, "%s%s = %s\n", indent, a.Name, expr)
	}
	for _, blk := range body.Blocks {
		b.WriteString(indent + blk.Type)
		for _, l := range blk.Labels {
			b.WriteString(" " + hclString(l))
		}
		b.WriteString(" {\n")
		renderBody(b, blk.Body, indent+"  ")
		b.WriteString(indent + "}\n")
	}
}

// lockVersions reads provider versions from .terraform.lock.hcl, keyed by
// provider type ("registry.terraform.io/hashicorp/azurerm" -> "azurerm").
func lockVersions(src []byte) map[string]string {
	out := map[string]string{}
	file, diags := hclsyntax.ParseConfig(src, ".terraform.lock.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		return out
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return out
	}
	for _, blk := range body.Blocks {
		if blk.Type != "provider" || len(blk.Labels) != 1 {
			continue
		}
		attr, ok := blk.Body.Attributes["version"]
		if !ok {
			continue
		}
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() || val.Type() != cty.String || val.IsNull() {
			continue
		}
		addr := blk.Labels[0]
		out[addr[strings.LastIndex(addr, "/")+1:]] = val.AsString()
	}
	return out
}

// tfvarsFileContent renders environment values as `name = <expr>` lines.
func tfvarsFileContent(env *model.Environment) []byte {
	if env == nil || len(env.Values) == 0 {
		return nil
	}
	names := make([]string, 0, len(env.Values))
	for n := range env.Values {
		names = append(names, n)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, n := range names {
		v := env.Values[n]
		expr := v.Expr
		if expr == "" && len(v.Literal) > 0 {
			// JSON is valid HCL expression syntax.
			expr = string(v.Literal)
		}
		if expr == "" {
			continue
		}
		fmt.Fprintf(&b, "%s = %s\n", n, expr)
	}
	return []byte(b.String())
}

// cloudEnv returns the non-secret environment for Terraform runs of
// workflow wfID targeting clouds; awsRegion is the default AWS region. GCP
// runs set model.GCPEmulatorHostVar to the sandbox pod's name on the
// workflow network: floci-gcp injects no emulator address into the
// containers it runs, and localhost (or localhost.floci.io) there is the
// container itself.
func cloudEnv(wfID string, clouds []model.Cloud, awsRegion string) []string {
	env := []string{}
	for _, c := range clouds {
		switch c {
		case model.CloudAWS:
			env = append(env,
				"AWS_ENDPOINT_URL="+flociAWS,
				"AWS_ACCESS_KEY_ID=test",
				"AWS_SECRET_ACCESS_KEY=test",
				"AWS_REGION="+awsRegion,
				"AWS_DEFAULT_REGION="+awsRegion,
				"AWS_EC2_METADATA_DISABLED=true",
			)
		case model.CloudAzure:
			env = append(env,
				"SSL_CERT_DIR=/etc/ssl/certs:/work/"+certDir,
				"ARM_SUBSCRIPTION_ID="+azureSubscription,
				"ARM_TENANT_ID="+azureTenant,
			)
		case model.CloudGCP:
			env = append(env, "GOOGLE_OAUTH_ACCESS_TOKEN=fake", "GOOGLE_PROJECT="+gcpProject,
				"TF_VAR_"+model.GCPEmulatorHostVar+"="+podName(wfID)+":"+strconv.Itoa(emulators[model.CloudGCP].port))
		case model.CloudOCI:
			overrides := make([]string, len(ociClients))
			for i, cl := range ociClients {
				overrides[i] = cl + "=" + flociOCI
			}
			v := strings.Join(overrides, ";")
			env = append(env, "TF_VAR_CLIENT_HOST_OVERRIDES="+v, "OCI_CLIENT_HOST_OVERRIDES="+v)
		}
	}
	return env
}
