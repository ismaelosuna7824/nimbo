package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Root calls used by the module cases.
const (
	callApp = `module "app" { source = "./modules/app" }
`
	queueModule = `resource "aws_sqs_queue" "jobs" {}
resource "aws_sqs_queue" "dlq" {}
output "jobs_arn" {
  value       = aws_sqs_queue.jobs.arn
  description = "x"
}
output "dlq_arn" {
  value       = aws_sqs_queue.dlq.arn
  description = "x"
}
`
)

// structureCases are the rule cases that need local modules, environments
// or a layout (run by TestRules with the other cases).
var structureCases = map[string][]structureCase{
	"bp_provider_in_module": {
		{name: "provider block in a module", src: callApp, modules: map[string]string{
			"modules/app": queueModule + `provider "aws" { region = "us-east-1" }`,
		}, want: 1},
		{name: "proxy alias block", src: callApp, modules: map[string]string{
			"modules/app": queueModule + `provider "aws" { alias = "west" }`,
		}, want: 0},
		{name: "configuration_aliases only", src: callApp, modules: map[string]string{
			"modules/app": queueModule + `terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = ">= 6.0", configuration_aliases = [aws.west] }
  }
}`,
		}, want: 0},
		{name: "nested module with a provider", src: callApp, modules: map[string]string{
			"modules/app": queueModule + `module "db" { source = "../db" }`,
			"modules/db": `provider "aws" {}` + "\n" + `provider "aws" {
  alias  = "replica"
  region = "us-west-2"
}
resource "aws_sqs_queue" "a" {}
resource "aws_sqs_queue" "b" {}`,
			"modules/other": `provider "aws" { region = "x" }`,
		}, want: 1},
		{name: "root provider blocks are fine", src: `provider "aws" { region = "us-east-1" }`, want: 0},
	},
	"bp_module_version_unpinned": {
		{name: "registry without version", src: `module "vpc" { source = "terraform-aws-modules/vpc/aws" }`, want: 1},
		{name: "registry with version", src: `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 6.0"
}`, want: 0},
		{name: "private registry without version", src: `module "vpc" { source = "app.terraform.io/acme/vpc/aws" }`, want: 1},
		{name: "git without ref", src: `module "vpc" { source = "git::https://example.com/vpc.git" }`, want: 1},
		{name: "github shorthand without ref", src: `module "vpc" { source = "github.com/acme/terraform-vpc" }`, want: 1},
		{name: "git pinned to a tag", src: `module "vpc" { source = "git::https://example.com/vpc.git?ref=v1.2.0" }`, want: 0},
		{name: "git pinned to a branch", src: `module "vpc" { source = "git::https://example.com/vpc.git?ref=main" }`, want: 1},
		{name: "local module", src: callApp, want: 0},
		{name: "registry call inside a local module", src: callApp, modules: map[string]string{
			"modules/app": queueModule + `module "vpc" { source = "terraform-aws-modules/vpc/aws" }`,
		}, want: 1},
	},
	"bp_module_thin_wrapper": {
		{name: "single resource", src: callApp, modules: map[string]string{
			"modules/app": `resource "aws_sqs_queue" "jobs" {}
output "arn" {
  value       = aws_sqs_queue.jobs.arn
  description = "x"
}`,
		}, want: 1},
		{name: "logical group", src: callApp, modules: map[string]string{"modules/app": queueModule}, want: 0},
		{name: "one resource plus a nested module", src: callApp, modules: map[string]string{
			"modules/app": `resource "aws_sqs_queue" "jobs" {}
module "db" { source = "../db" }`,
			"modules/db": queueModule,
		}, want: 0},
		{name: "called twice, reported once", src: callApp + `module "app2" { source = "./modules/app" }`, modules: map[string]string{
			"modules/app": `resource "aws_sqs_queue" "jobs" {}`,
		}, want: 1},
		// Corpus: terraform-aws-rds composes single-resource submodules.
		{name: "submodule composed by a module", src: callApp, modules: map[string]string{
			"modules/app":    queueModule + `module "params" { source = "../params" }`,
			"modules/params": `resource "aws_db_parameter_group" "this" { family = "postgres16" }`,
		}, want: 0},
	},
	"bp_module_nesting_depth": {
		{name: "two levels", src: callApp, modules: map[string]string{
			"modules/app": queueModule + `module "db" { source = "../db" }`,
			"modules/db":  queueModule,
		}, want: 0},
		{name: "three levels", src: callApp, modules: map[string]string{
			"modules/app":   queueModule + `module "db" { source = "../db" }`,
			"modules/db":    queueModule + `module "cache" { source = "../cache" }`,
			"modules/cache": queueModule,
		}, want: 1},
		{name: "four levels, one finding per root call", src: callApp, modules: map[string]string{
			"modules/app":   queueModule + `module "db" { source = "../db" }`,
			"modules/db":    queueModule + `module "cache" { source = "../cache" }`,
			"modules/cache": queueModule + `module "x" { source = "../x" }`,
			"modules/x":     queueModule,
		}, want: 1},
		// Corpus: terraform-aws-security-group calls module "disabled_submodule".
		{name: "names containing module", src: `module "disabled_submodule" { source = "./modules/app" }`, modules: map[string]string{
			"modules/app": queueModule + `module "inner_module" { source = "../db" }`,
			"modules/db":  queueModule,
		}, want: 0},
		{name: "two levels inside the environments layout's service module", src: callApp, layout: model.LayoutEnvironments, modules: map[string]string{
			"modules/app": queueModule + `module "db" { source = "../db" }`,
			"modules/db":  queueModule,
		}, want: 1},
		{name: "one level inside the environments layout's service module", src: callApp, envs: []string{"dev", "prod"}, modules: map[string]string{
			"modules/app": queueModule,
		}, want: 0},
	},
	"bp_module_output_missing": {
		{name: "every resource exposed", src: callApp, modules: map[string]string{"modules/app": queueModule}, want: 0},
		{name: "a queue without output", src: callApp, modules: map[string]string{
			"modules/app": `resource "aws_sqs_queue" "jobs" {}
resource "aws_sqs_queue" "dlq" {}
resource "aws_sqs_queue" "extra" {}
output "jobs_arn" {
  value       = aws_sqs_queue.jobs.arn
  description = "x"
}`,
		}, want: 1},
		{name: "reached through what it is attached to", src: callApp, modules: map[string]string{
			"modules/app": `resource "aws_sqs_queue" "dlq" {}
resource "aws_sqs_queue" "jobs" {
  redrive_policy = jsonencode({ deadLetterTargetArn = aws_sqs_queue.dlq.arn, maxReceiveCount = 5 })
}
output "jobs_arn" {
  value       = aws_sqs_queue.jobs.arn
  description = "x"
}`,
		}, want: 0},
		{name: "a shared data source doesn't connect", src: callApp, modules: map[string]string{
			"modules/app": `data "aws_region" "current" {}
resource "aws_sqs_queue" "jobs" { name = "jobs-${data.aws_region.current.region}" }
resource "aws_sqs_queue" "other" { name = "other-${data.aws_region.current.region}" }
output "jobs_arn" {
  value       = aws_sqs_queue.jobs.arn
  description = "x"
}`,
		}, want: 1},
		{name: "exposed through a local", src: callApp, modules: map[string]string{
			"modules/app": `resource "aws_sqs_queue" "jobs" {}
resource "aws_sqs_queue" "dlq" {}
locals {
  arns = { jobs = aws_sqs_queue.jobs.arn, dlq = aws_sqs_queue.dlq[*].arn }
}
output "arns" {
  value       = local.arns
  description = "x"
}`,
		}, want: 0},
		{name: "associations export only an id", src: callApp, modules: map[string]string{
			"modules/app": `resource "aws_route_table" "rt" { vpc_id = "vpc-1" }
resource "aws_route_table_association" "a" {
  subnet_id      = "subnet-1"
  route_table_id = aws_route_table.rt.id
}
output "route_table_id" {
  value       = aws_route_table.rt.id
  description = "x"
}`,
		}, want: 0},
		{name: "root resources are not checked", src: `resource "aws_sqs_queue" "jobs" {}`, want: 0},
	},
	"bp_inline_sg_rules": {
		{name: "inline rules only", src: `resource "aws_security_group" "web" {
  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
}`, want: 0},
		{name: "attachment rules only", src: `resource "aws_security_group" "web" {}
resource "aws_vpc_security_group_ingress_rule" "https" {
  security_group_id = aws_security_group.web.id
  cidr_ipv4         = "10.0.0.0/16"
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
}`, want: 0},
		{name: "styles mixed across groups", src: `resource "aws_security_group" "web" {
  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["10.0.0.0/16"]
  }
}
resource "aws_security_group" "db" {}
resource "aws_vpc_security_group_ingress_rule" "pg" {
  security_group_id            = aws_security_group.db.id
  referenced_security_group_id = aws_security_group.web.id
  from_port                    = 5432
  to_port                      = 5432
  ip_protocol                  = "tcp"
}`, want: 1},
		{name: "both styles on one group", src: `resource "aws_security_group" "web" {
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}
resource "aws_security_group_rule" "https" {
  type              = "ingress"
  security_group_id = aws_security_group.web.id
  from_port         = 443
  to_port           = 443
  protocol          = "tcp"
  cidr_blocks       = ["10.0.0.0/16"]
}`, want: 1},
		{name: "empty inline list clears rules", src: `resource "aws_security_group" "web" {
  ingress = []
}
resource "aws_vpc_security_group_ingress_rule" "https" {
  security_group_id = aws_security_group.web.id
  cidr_ipv4         = "10.0.0.0/16"
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
}`, want: 0},
		{name: "mixed inside a local module", src: callApp, modules: map[string]string{
			"modules/app": `resource "aws_security_group" "web" {
  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["10.0.0.0/16"]
  }
}
resource "aws_vpc_security_group_egress_rule" "all" {
  security_group_id = aws_security_group.web.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}`,
		}, want: 1},
	},
	"bp_no_remote_backend": {
		{name: "auto falls back to single", src: `resource "aws_sqs_queue" "q" {}`, envs: []string{"dev", "my prod"}, want: 1},
		{name: "two environments in the single layout", src: `resource "aws_sqs_queue" "q" {}`, envs: []string{"dev", "prod"}, layout: model.LayoutSingle, want: 1},
		{name: "two environments, auto layout", src: `resource "aws_sqs_queue" "q" {}`, envs: []string{"dev", "prod"}, want: 0},
		{name: "two environments, environments layout", src: `resource "aws_sqs_queue" "q" {}`, envs: []string{"dev", "prod"}, layout: model.LayoutEnvironments, want: 0},
		{name: "single layout with a backend", src: `terraform {
  backend "s3" {}
}`, envs: []string{"dev", "prod"}, layout: model.LayoutSingle, want: 0},
		{name: "single layout with HCP Terraform", src: `terraform {
  cloud {}
}`, envs: []string{"dev", "prod"}, layout: model.LayoutSingle, want: 0},
		{name: "one environment", src: `resource "aws_sqs_queue" "q" {}`, envs: []string{"dev"}, layout: model.LayoutSingle, want: 0},
	},
	"bp_state_too_large": {
		{name: "small", src: queues(49), want: 0},
		{name: "fifty resources", src: queues(50), want: 1},
		{name: "count expands", src: `resource "aws_sqs_queue" "q" { count = 60 }`, want: 1},
		{name: "unknown count counts once", src: `resource "aws_sqs_queue" "q" { count = var.n }`, want: 0},
		// Corpus: published modules gate every block on a flag.
		{name: "feature-flagged blocks count as absent", src: strings.ReplaceAll(queues(60), "{}", "{ count = var.create ? 1 : 0 }"), want: 0},
		{name: "flagged for_each counts as absent", src: strings.ReplaceAll(queues(60), "{}", "{ for_each = var.create ? toset([\"a\"]) : toset([]) }"), want: 0},
		{name: "local module resources count, per call", src: `module "app" {
  source = "./modules/app"
  count  = 30
}`, modules: map[string]string{"modules/app": queueModule}, want: 1},
	},
	"bp_workspace_branching": {
		{name: "local from the workspace", src: `locals { env = terraform.workspace }`, want: 1},
		{name: "resource attribute", src: `resource "aws_sqs_queue" "q" { name = "jobs-${terraform.workspace}" }`, want: 1},
		{name: "several places, one finding", src: `locals { env = terraform.workspace }
resource "aws_sqs_queue" "q" { name = "jobs-${terraform.workspace}" }`, want: 1},
		{name: "variable instead", src: `resource "aws_sqs_queue" "q" { name = "jobs-${var.environment}" }`, want: 0},
		{name: "inside a local module", src: callApp, modules: map[string]string{
			"modules/app": queueModule + `locals { env = terraform.workspace }`,
		}, want: 1},
	},
}

// queues renders n SQS queues.
func queues(n int) string {
	var sb strings.Builder
	for i := range n {
		fmt.Fprintf(&sb, "resource \"aws_sqs_queue\" \"q%d\" {}\n", i)
	}
	return sb.String()
}

// Severities that depend on the case: a real conflict or a state of 100+.
func TestStructureSeverities(t *testing.T) {
	sev := func(tc structureCase, code string) model.Severity {
		t.Helper()
		got := caseFindings(t, tc, code)
		if len(got) != 1 {
			t.Fatalf("%s: %d findings, want 1", code, len(got))
		}
		return got[0].Severity
	}
	for _, tc := range structureCases["bp_inline_sg_rules"] {
		want := model.SeverityInfo
		if tc.name == "both styles on one group" || tc.name == "mixed inside a local module" {
			want = model.SeverityWarning
		}
		if tc.want == 1 {
			if got := sev(tc, "bp_inline_sg_rules"); got != want {
				t.Errorf("%s: severity %s, want %s", tc.name, got, want)
			}
		}
	}
	if got := sev(structureCase{src: queues(99)}, "bp_state_too_large"); got != model.SeverityInfo {
		t.Errorf("99 resources: %s", got)
	}
	if got := sev(structureCase{src: queues(100)}, "bp_state_too_large"); got != model.SeverityWarning {
		t.Errorf("100 resources: %s", got)
	}
	// Module findings sit on the root module call.
	d := caseFindings(t, structureCases["bp_module_thin_wrapper"][0], "bp_module_thin_wrapper")
	wf := importHCL(t, callApp)
	if len(d) != 1 || d[0].NodeID != wf.Nodes[0].ID {
		t.Errorf("thin wrapper finding not on module.app: %+v", d)
	}
}

func TestStructureSummary(t *testing.T) {
	wf := importHCL(t, `terraform {
  backend "s3" {}
}
resource "aws_sqs_queue" "q" { count = 2 }
module "app" {
  source = "./modules/app"
  count  = 3
}
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 6.0"
}`)
	wf.Environments = []model.Environment{{Name: "dev"}, {Name: "prod"}}
	files := map[string]string{
		"modules/app": queueModule + `module "db" { source = "../db" }`,
		"modules/db":  `resource "aws_db_instance" "main" {}`,
	}
	mods, err := LocalModules(wf, ".", func(dir string) (map[string][]byte, error) {
		if src, ok := files[dir]; ok {
			return map[string][]byte{"main.tf": []byte(src)}, nil
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s := Structure(wf, mods)
	if s.Layout != model.LayoutEnvironments || s.LayoutSetting != "" || s.States != 2 || s.Backend != "s3" {
		t.Errorf("layout/states/backend: %+v", s)
	}
	// root: 2 queues; module.app: 3 calls x (2 queues + 1 db) = 9.
	if s.RootResources != 2 || s.ResourcesPerState != 11 || s.MaxNesting != 2 {
		t.Errorf("counts: %+v", s)
	}
	var calls []string
	for _, m := range s.Modules {
		calls = append(calls, fmt.Sprintf("%s:%s:%d", m.Call, m.Kind, m.Resources))
	}
	if got := strings.Join(calls, " "); got != "module.app:local:3 module.app.module.db:local:1 module.vpc:registry:0" {
		t.Errorf("modules: %s", got)
	}
	if !strings.Contains(s.State, "one state per environment") {
		t.Errorf("state: %s", s.State)
	}

	wf.Layout = model.LayoutSingle
	wf.Terraform = nil
	if s := Structure(wf, nil); s.States != 1 || !strings.Contains(s.State, "shares one local terraform.tfstate") || len(s.Modules) != 2 {
		t.Errorf("single layout without backend: %+v", s)
	}
}
