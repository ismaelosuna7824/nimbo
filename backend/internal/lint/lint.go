// Package lint runs Nimbo's best-practice checks over a workflow. Findings
// are diagnostics with codes "bp_*", severity info or warning (never error,
// so they never block a deploy), a short why (Detail) and a suggested fix
// (Fix). Rules can be turned off as a whole or one by one in Settings.
//
// Rules read the root workflow and, for the module rules, the local modules
// it calls (LocalModules; never registry or remote module code). They are
// tuned to stay quiet on the catalog templates and on code written the
// recommended way; docs/ARCHITECTURE.md ("lint") lists them with their
// corpus noise counts.
package lint

import (
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Prefix starts every best-practice diagnostic code.
const Prefix = "bp_"

// Categories of rules.
const (
	CategoryInputs      = "inputs"
	CategoryVersions    = "versions"
	CategoryHardcoding  = "hardcoding"
	CategoryTagging     = "tagging"
	CategoryNaming      = "naming"
	CategorySecurity    = "security"
	CategoryStructure   = "structure"
	CategoryReliability = "reliability"
	CategoryPerformance = "performance"
	CategoryCost        = "cost"
	CategoryOperations  = "operations"
)

// categoryPillars are the Well-Architected pillars a category's rules serve,
// most relevant first; rulePillars overrides them per rule.
var categoryPillars = map[string][]model.Pillar{
	CategoryInputs:      {model.PillarOperations},
	CategoryVersions:    {model.PillarOperations, model.PillarReliability},
	CategoryHardcoding:  {model.PillarOperations},
	CategoryTagging:     {model.PillarOperations, model.PillarCost},
	CategoryNaming:      {model.PillarOperations},
	CategorySecurity:    {model.PillarSecurity},
	CategoryStructure:   {model.PillarOperations},
	CategoryReliability: {model.PillarReliability},
	CategoryPerformance: {model.PillarPerformance},
	CategoryCost:        {model.PillarCost},
	CategoryOperations:  {model.PillarOperations},
}

// rulePillars are the pillars of rules that serve more (or other) pillars
// than their category's: backups and versioning are reliability, audit and
// flow logs operations too, and so on.
var rulePillars = map[string][]model.Pillar{
	"bp_sensitive_not_marked": {model.PillarSecurity, model.PillarOperations},
	"bp_hardcoded_region":     {model.PillarOperations, model.PillarReliability},
	"bp_hardcoded_az":         {model.PillarReliability, model.PillarOperations},
	"bp_hardcoded_account_id": {model.PillarOperations, model.PillarSecurity},
	"bp_log_retention":        {model.PillarCost, model.PillarOperations, model.PillarSecurity},
	"bp_provider_in_module":   {model.PillarOperations, model.PillarReliability},
	"bp_inline_sg_rules":      {model.PillarOperations, model.PillarReliability},
	"bp_no_remote_backend":    {model.PillarReliability, model.PillarOperations},
	"bp_state_too_large":      {model.PillarOperations, model.PillarPerformance},
	"bp_workspace_branching":  {model.PillarOperations, model.PillarReliability},
	"bp_count_over_list":      {model.PillarReliability, model.PillarOperations},

	"bp_rds_minor_upgrade":          {model.PillarSecurity, model.PillarOperations},
	"bp_rds_multi_az":               {model.PillarReliability},
	"bp_cloudtrail_multi_region":    {model.PillarSecurity, model.PillarOperations},
	"bp_cloudtrail_bucket_logging":  {model.PillarSecurity, model.PillarOperations},
	"bp_cloudtrail_s3_write_events": {model.PillarSecurity, model.PillarOperations},
	"bp_cloudtrail_s3_read_events":  {model.PillarSecurity, model.PillarOperations},
	"bp_vpc_flow_logs":              {model.PillarSecurity, model.PillarOperations},

	"bp_azure_blob_soft_delete":       {model.PillarReliability, model.PillarSecurity},
	"bp_azure_container_soft_delete":  {model.PillarReliability, model.PillarSecurity},
	"bp_azure_blob_versioning":        {model.PillarReliability, model.PillarSecurity},
	"bp_azure_file_share_soft_delete": {model.PillarReliability, model.PillarSecurity},
	"bp_azure_diagnostic_logs":        {model.PillarSecurity, model.PillarOperations},
	"bp_azure_kv_purge_protection":    {model.PillarSecurity, model.PillarReliability},
	"bp_azure_flow_log_retention":     {model.PillarSecurity, model.PillarOperations},
	"bp_azure_appgw_http2":            {model.PillarPerformance, model.PillarSecurity},
	"bp_azure_vnet_ddos":              {model.PillarSecurity, model.PillarReliability},

	"bp_gcp_audit_logs":                        {model.PillarSecurity, model.PillarOperations},
	"bp_gcp_log_bucket_lock":                   {model.PillarSecurity, model.PillarReliability},
	"bp_gcp_dns_logging":                       {model.PillarSecurity, model.PillarOperations},
	"bp_gcp_lb_logging":                        {model.PillarSecurity, model.PillarOperations},
	"bp_gcp_flow_logs":                         {model.PillarSecurity, model.PillarOperations},
	"bp_gcp_sql_backups":                       {model.PillarReliability, model.PillarSecurity},
	"bp_gcp_sql_pg_log_connections":            {model.PillarSecurity, model.PillarOperations},
	"bp_gcp_sql_pg_log_disconnections":         {model.PillarSecurity, model.PillarOperations},
	"bp_gcp_sql_pg_log_error_verbosity":        {model.PillarSecurity, model.PillarOperations},
	"bp_gcp_sql_pg_log_min_duration_statement": {model.PillarSecurity, model.PillarOperations},
	"bp_gcp_sql_pg_log_min_error_statement":    {model.PillarSecurity, model.PillarOperations},
	"bp_gcp_sql_pg_log_min_messages":           {model.PillarSecurity, model.PillarOperations},
	"bp_gcp_sql_pg_log_statement":              {model.PillarSecurity, model.PillarOperations},
	"bp_gcp_sql_pg_pgaudit":                    {model.PillarSecurity, model.PillarOperations},
	"bp_gcp_sql_sqlserver_user_connections":    {model.PillarSecurity, model.PillarReliability},

	"bp_oci_flow_logs":            {model.PillarSecurity, model.PillarOperations},
	"bp_oci_bucket_write_logs":    {model.PillarSecurity, model.PillarOperations},
	"bp_oci_bucket_versioning":    {model.PillarReliability, model.PillarSecurity},
	"bp_oci_storage_admin_delete": {model.PillarSecurity, model.PillarReliability},
}

type rule struct {
	model.BestPracticeRule
	check func(*pass)
}

var rules = []rule{
	{documented(info("bp_variable_type", "Variables have a type", CategoryInputs,
		"Every input variable declares its type so wrong values fail early with a clear message."),
		"https://developer.hashicorp.com/terraform/language/values/variables#type-constraints"), checkVariableType},
	{documented(info("bp_variable_description", "Variables are described", CategoryInputs,
		"Every input variable has a description; it shows up in plans, docs and the Nimbo forms."),
		"https://developer.hashicorp.com/terraform/language/values/variables#input-variable-documentation"), checkVariableDescription},
	{documented(info("bp_output_description", "Outputs are described", CategoryInputs,
		"Every output has a description so consumers of the module know what it returns."),
		"https://developer.hashicorp.com/terraform/language/values/outputs"), checkOutputDescription},
	{documented(warning("bp_sensitive_not_marked", "Secrets are marked sensitive", CategoryInputs,
		"Variables and outputs that look like secrets (password, token, secret, private key, …) or expose a sensitive value set sensitive = true."),
		"https://developer.hashicorp.com/terraform/language/values/variables#suppressing-values-in-cli-output"), checkSensitive},
	{documented(info("bp_required_version", "Terraform version is pinned", CategoryVersions,
		"The terraform block sets required_version so everyone runs a compatible Terraform."),
		"https://developer.hashicorp.com/terraform/language/expressions/version-constraints"), checkRequiredVersion},
	{documented(warning("bp_provider_version", "Provider versions are pinned", CategoryVersions,
		"Every provider the configuration uses is listed in required_providers with a version constraint."),
		"https://developer.hashicorp.com/terraform/language/providers/requirements"), checkProviderVersion},
	{documented(warning("bp_module_version_unpinned", "Module versions are pinned", CategoryVersions,
		"Registry modules set version and git modules pin ?ref= to a tag or commit, so init always downloads the code you tested."),
		"https://developer.hashicorp.com/terraform/language/modules/sources"), checkModuleVersionUnpinned},
	{documented(info("bp_hardcoded_region", "No hardcoded regions", CategoryHardcoding,
		"Resource attributes don't hardcode a region or Azure location; use a variable or the provider's region."),
		"https://registry.terraform.io/providers/hashicorp/aws/latest/docs/data-sources/region"), checkHardcodedRegion},
	{documented(info("bp_hardcoded_az", "No hardcoded availability zones", CategoryHardcoding,
		"Resource attributes don't hardcode availability zones; use data.aws_availability_zones or a variable."),
		"https://registry.terraform.io/providers/hashicorp/aws/latest/docs/data-sources/availability_zones"), checkHardcodedAZ},
	{documented(info("bp_hardcoded_account_id", "No hardcoded account IDs", CategoryHardcoding,
		"Resource attributes don't hardcode AWS account IDs or ARNs containing them; use data.aws_caller_identity or a reference."),
		"https://registry.terraform.io/providers/hashicorp/aws/latest/docs/data-sources/caller_identity"), checkHardcodedAccount},
	{documented(info("bp_missing_tags", "Resources are tagged", CategoryTagging,
		"Taggable AWS/Azure resources have tags and GCP resources have labels (AWS default_tags and GCP default_labels count)."),
		"https://docs.aws.amazon.com/whitepapers/latest/tagging-best-practices/tagging-best-practices.html"), checkMissingTags},
	{documented(info("bp_name_prefix", "Names use local.name_prefix", CategoryNaming,
		"When the workflow defines local.name_prefix, resource names are derived from it instead of fixed strings."),
		"https://developer.hashicorp.com/terraform/language/values/locals"), checkNamePrefix},
	{documented(warning("bp_open_ingress", "No admin/database ports open to the internet", CategorySecurity,
		"Firewall rules don't allow 0.0.0.0/0 (or ::/0) to SSH, RDP, SMB/CIFS (445) or database ports."),
		"https://docs.aws.amazon.com/vpc/latest/userguide/vpc-security-best-practices.html"), checkOpenIngress},
	{documented(warning("bp_public_bucket", "Buckets are private unless meant to be public", CategorySecurity,
		"Public bucket access (public ACLs, open public access blocks, allUsers bindings, public containers) needs an explicit intent: a website configuration or a Public = \"true\" tag."),
		"https://docs.aws.amazon.com/AmazonS3/latest/userguide/access-control-block-public-access.html"), checkPublicBucket},
	{documented(warning("bp_unencrypted_storage", "Storage is encrypted at rest", CategorySecurity,
		"Storage with an encryption switch turns it on: S3 SSE configuration, DynamoDB server_side_encryption, RDS storage_encrypted, EBS/EFS encrypted, ElastiCache at-rest encryption."),
		"https://docs.aws.amazon.com/wellarchitected/latest/security-pillar/protecting-data-at-rest.html"), checkUnencrypted},
	{documented(warning("bp_iam_wildcard", "No IAM policies granting * on *", CategorySecurity,
		"IAM policy statements don't allow every action on every resource; scope actions and resources (ARNs)."),
		"https://docs.aws.amazon.com/IAM/latest/UserGuide/best-practices.html#grant-least-privilege"), checkIAMWildcard},
	{documented(info("bp_log_retention", "Lambda logs have a retention", CategorySecurity,
		"Each Lambda function has a CloudWatch log group with retention_in_days, so logs don't pile up forever."),
		"https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/Working-with-log-groups-and-streams.html#SettingLogRetention"), checkLogRetention},
	{documented(info("bp_large_file", "Files stay small", CategoryStructure,
		"No Terraform file holds more than 15 resources; split by concern or extract a module."),
		"https://developer.hashicorp.com/terraform/language/style#file-names"), checkLargeFile},
	{documented(info("bp_module_candidate", "Related resources form modules", CategoryStructure,
		"A large logical group (network foundation, data tier, API, container service, cluster) in a big workflow is a good module candidate."),
		"https://developer.hashicorp.com/terraform/language/modules/develop#when-to-write-a-module"), checkModuleCandidate},
	{documented(info("bp_duplicate_literal", "Repeated values live in locals", CategoryStructure,
		"The same literal value (CIDR, ARN, ID, …) in three or more resources is defined once in locals."),
		"https://developer.hashicorp.com/terraform/language/values/locals"), checkDuplicateLiteral},
	{documented(warning("bp_provider_in_module", "Modules don't configure providers", CategoryStructure,
		"Local modules declare required_providers (with configuration_aliases for aliases) but leave provider blocks to the root."),
		"https://developer.hashicorp.com/terraform/language/modules/develop/providers"), checkProviderInModule},
	{documented(info("bp_module_thin_wrapper", "Modules group more than one resource", CategoryStructure,
		"A local module wraps a logical group of resources, not a single resource."),
		"https://developer.hashicorp.com/terraform/language/modules/develop#when-to-write-a-module"), checkModuleThinWrapper},
	{documented(info("bp_module_nesting_depth", "Module trees stay shallow", CategoryStructure,
		"Local modules are nested at most 2 levels deep; deeper trees are composed from the root."),
		"https://developer.hashicorp.com/terraform/language/modules/develop/composition"), checkModuleNestingDepth},
	{documented(info("bp_module_output_missing", "Module resources are exposed", CategoryStructure,
		"Every resource of a local module that exports attributes (ID, ARN, URL) is referenced by at least one module output."),
		"https://developer.hashicorp.com/terraform/language/modules/develop/structure"), checkModuleOutputMissing},
	{documented(info("bp_inline_sg_rules", "Security group rules use one style", CategoryStructure,
		"Where standalone security group rule resources are used, security groups don't also define inline ingress/egress rules (a warning when both target the same group)."),
		"https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/security_group"), checkInlineSGRules},
	{documented(info("bp_no_remote_backend", "Environments don't share a state", CategoryStructure,
		"A workflow with two or more environments in the single layout has a backend; otherwise every environment shares one local state (use the environments layout)."),
		"https://developer.hashicorp.com/terraform/language/backend"), checkNoRemoteBackend},
	{documented(info("bp_state_too_large", "States stay small", CategoryStructure,
		"One state manages fewer than 50 resource instances (a warning from 100); larger designs split into workflows per layer (network, data, app)."),
		"https://developer.hashicorp.com/terraform/language/state"), checkStateTooLarge},
	{documented(info("bp_workspace_branching", "No terraform.workspace branching", CategoryStructure,
		"Expressions don't depend on terraform.workspace; environments get their own values and state instead of CLI workspaces."),
		"https://developer.hashicorp.com/terraform/language/state/workspaces#when-not-to-use-multiple-workspaces"), checkWorkspaceBranching},
	{documented(info("bp_count_over_list", "Lists use for_each, not count", CategoryReliability,
		"Resources and module calls that create one instance per element of a list use for_each instead of count = length(list) with list[count.index], so removing or reordering an element doesn't destroy and recreate the instances after it."),
		"https://developer.hashicorp.com/terraform/language/meta-arguments/count#when-to-use-for_each-instead-of-count"), checkCountOverList},
}

// info builds a rule's metadata with its pillars: rulePillars, else its
// category's.
func info(code, title, category, desc string) model.BestPracticeRule {
	pillars, ok := rulePillars[code]
	if !ok {
		pillars = categoryPillars[category]
	}
	return model.BestPracticeRule{Code: code, Title: title, Category: category, Severity: model.SeverityInfo, Description: desc, Pillars: slices.Clone(pillars)}
}

func warning(code, title, category, desc string) model.BestPracticeRule {
	r := info(code, title, category, desc)
	r.Severity = model.SeverityWarning
	return r
}

// documented returns r linking to docs.
func documented(r model.BestPracticeRule, docs string) model.BestPracticeRule {
	r.Docs = docs
	return r
}

// Rules lists every best-practice rule in display order.
func Rules() []model.BestPracticeRule {
	out := make([]model.BestPracticeRule, len(rules))
	for i, r := range rules {
		out[i] = r.BestPracticeRule
	}
	return out
}

// IsFinding reports whether d is a best-practice finding.
func IsFinding(d model.Diagnostic) bool { return strings.HasPrefix(d.Code, Prefix) }

// Check runs the enabled rules over wf, which must be normalized (value
// pairs filled, edges derived): every rule but the ones in DisabledRules,
// and opt-in (DefaultOff) rules only when listed in EnabledRules. mods are
// the local modules wf calls (LocalModules; nil when it calls none or they
// can't be read). It never returns error-severity findings.
func Check(wf *model.Workflow, cat *catalog.Catalog, cfg model.BestPracticeSettings, mods *model.ModuleTree) []model.Diagnostic {
	if cfg.Disabled {
		return nil
	}
	p := newPass(wf, cat, mods)
	for i := range rules {
		r := &rules[i]
		if slices.Contains(cfg.DisabledRules, r.Code) || r.DefaultOff && !slices.Contains(cfg.EnabledRules, r.Code) {
			continue
		}
		p.rule = r
		r.check(p)
	}
	return p.out
}

// Strip returns diags without best-practice findings.
func Strip(diags []model.Diagnostic) []model.Diagnostic {
	out := make([]model.Diagnostic, 0, len(diags))
	for _, d := range diags {
		if !IsFinding(d) {
			out = append(out, d)
		}
	}
	return out
}
