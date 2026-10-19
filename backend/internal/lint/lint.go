// Package lint runs Nimbo's best-practice checks over a workflow. Findings
// are diagnostics with codes "bp_*", severity info or warning (never error,
// so they never block a deploy), a short why (Detail) and a suggested fix
// (Fix). Rules can be turned off as a whole or one by one in Settings.
//
// Rules only read the root workflow (never module sources) and are tuned to
// stay quiet on the catalog templates and on code written the recommended
// way; docs/ARCHITECTURE.md ("lint") lists them with their corpus noise counts.
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
	CategoryInputs     = "inputs"
	CategoryVersions   = "versions"
	CategoryHardcoding = "hardcoding"
	CategoryTagging    = "tagging"
	CategoryNaming     = "naming"
	CategorySecurity   = "security"
	CategoryStructure  = "structure"
)

type rule struct {
	model.BestPracticeRule
	check func(*pass)
}

var rules = []rule{
	{info("bp_variable_type", "Variables have a type", CategoryInputs,
		"Every input variable declares its type so wrong values fail early with a clear message."), checkVariableType},
	{info("bp_variable_description", "Variables are described", CategoryInputs,
		"Every input variable has a description; it shows up in plans, docs and the Nimbo forms."), checkVariableDescription},
	{info("bp_output_description", "Outputs are described", CategoryInputs,
		"Every output has a description so consumers of the module know what it returns."), checkOutputDescription},
	{warning("bp_sensitive_not_marked", "Secrets are marked sensitive", CategoryInputs,
		"Variables and outputs that look like secrets (password, token, secret, private key, …) or expose a sensitive value set sensitive = true."), checkSensitive},
	{info("bp_required_version", "Terraform version is pinned", CategoryVersions,
		"The terraform block sets required_version so everyone runs a compatible Terraform."), checkRequiredVersion},
	{warning("bp_provider_version", "Provider versions are pinned", CategoryVersions,
		"Every provider the configuration uses is listed in required_providers with a version constraint."), checkProviderVersion},
	{info("bp_hardcoded_region", "No hardcoded regions", CategoryHardcoding,
		"Resource attributes don't hardcode a region or Azure location; use a variable or the provider's region."), checkHardcodedRegion},
	{info("bp_hardcoded_az", "No hardcoded availability zones", CategoryHardcoding,
		"Resource attributes don't hardcode availability zones; use data.aws_availability_zones or a variable."), checkHardcodedAZ},
	{info("bp_hardcoded_account_id", "No hardcoded account IDs", CategoryHardcoding,
		"Resource attributes don't hardcode AWS account IDs or ARNs containing them; use data.aws_caller_identity or a reference."), checkHardcodedAccount},
	{info("bp_missing_tags", "Resources are tagged", CategoryTagging,
		"Taggable AWS/Azure resources have tags and GCP resources have labels (AWS default_tags and GCP default_labels count)."), checkMissingTags},
	{info("bp_name_prefix", "Names use local.name_prefix", CategoryNaming,
		"When the workflow defines local.name_prefix, resource names are derived from it instead of fixed strings."), checkNamePrefix},
	{warning("bp_open_ingress", "No admin/database ports open to the internet", CategorySecurity,
		"Firewall rules don't allow 0.0.0.0/0 (or ::/0) to SSH, RDP or database ports."), checkOpenIngress},
	{warning("bp_public_bucket", "Buckets are private unless meant to be public", CategorySecurity,
		"Public bucket access (public ACLs, open public access blocks, allUsers bindings, public containers) needs an explicit intent: a website configuration or a Public = \"true\" tag."), checkPublicBucket},
	{warning("bp_unencrypted_storage", "Storage is encrypted at rest", CategorySecurity,
		"Storage with an encryption switch turns it on: S3 SSE configuration, DynamoDB server_side_encryption, RDS storage_encrypted, EBS/EFS encrypted, ElastiCache at-rest encryption."), checkUnencrypted},
	{warning("bp_iam_wildcard", "No IAM policies granting * on *", CategorySecurity,
		"IAM policy statements don't allow every action on every resource; scope actions and resources (ARNs)."), checkIAMWildcard},
	{info("bp_log_retention", "Lambda logs have a retention", CategorySecurity,
		"Each Lambda function has a CloudWatch log group with retention_in_days, so logs don't pile up forever."), checkLogRetention},
	{info("bp_large_file", "Files stay small", CategoryStructure,
		"No Terraform file holds more than 15 resources; split by concern or extract a module."), checkLargeFile},
	{info("bp_module_candidate", "Related resources form modules", CategoryStructure,
		"A large group of related resources (network, API, containers) in a big workflow is a good module candidate."), checkModuleCandidate},
	{info("bp_duplicate_literal", "Repeated values live in locals", CategoryStructure,
		"The same literal value (CIDR, ARN, ID, …) in three or more resources is defined once in locals."), checkDuplicateLiteral},
}

func info(code, title, category, desc string) model.BestPracticeRule {
	return model.BestPracticeRule{Code: code, Title: title, Category: category, Severity: model.SeverityInfo, Description: desc}
}

func warning(code, title, category, desc string) model.BestPracticeRule {
	r := info(code, title, category, desc)
	r.Severity = model.SeverityWarning
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
// pairs filled, edges derived). It never returns error-severity findings.
func Check(wf *model.Workflow, cat *catalog.Catalog, cfg model.BestPracticeSettings) []model.Diagnostic {
	if cfg.Disabled {
		return nil
	}
	p := newPass(wf, cat)
	for i := range rules {
		r := &rules[i]
		if slices.Contains(cfg.DisabledRules, r.Code) {
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
