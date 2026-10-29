package lint

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

var testCatalog = func() *catalog.Catalog {
	c, err := catalog.Load()
	if err != nil {
		panic(err)
	}
	return c
}()

// pinned is the boilerplate that keeps the version rules quiet: the
// constraints Nimbo writes (catalog.ProviderCompat Recommended).
const pinned = `
terraform {
  required_version = ">= 1.6"
  required_providers {
    aws     = { source = "hashicorp/aws", version = "~> 6.69" }
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.81" }
    google  = { source = "hashicorp/google", version = "~> 7.46" }
  }
}
`

func importHCL(t *testing.T, src string) *model.Workflow {
	t.Helper()
	wf, diags, err := hclcodec.Import("test", map[string][]byte{"main.tf": []byte(src)})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, d := range diags {
		if d.Severity == model.SeverityError {
			t.Fatalf("import: %+v", d)
		}
	}
	hclcodec.Normalize(wf)
	return wf
}

func findings(t *testing.T, src, code string) []model.Diagnostic {
	t.Helper()
	return caseFindings(t, structureCase{src: src}, code)
}

// caseFindings runs the rules over tc.src (main.tf of the root) with its
// local modules (tc.modules: directory -> main.tf), environments and layout,
// and returns the findings of code (opt-in rules on, so their cases run).
func caseFindings(t *testing.T, tc structureCase, code string) []model.Diagnostic {
	t.Helper()
	wf := importHCL(t, tc.src)
	for _, e := range tc.envs {
		wf.Environments = append(wf.Environments, model.Environment{Name: e})
	}
	wf.Layout = tc.layout
	var mods *model.ModuleTree
	if len(tc.modules) > 0 {
		var err error
		mods, err = LocalModules(wf, ".", func(dir string) (map[string][]byte, error) {
			src, ok := tc.modules[dir]
			if !ok {
				return nil, nil
			}
			return map[string][]byte{"main.tf": []byte(src)}, nil
		})
		if err != nil {
			t.Fatalf("modules: %v", err)
		}
	}
	var out []model.Diagnostic
	all := model.BestPracticeSettings{}
	for _, r := range Rules() {
		if r.DefaultOff {
			all.EnabledRules = append(all.EnabledRules, r.Code)
		}
	}
	for _, d := range Check(wf, testCatalog, all, mods) {
		if d.Severity == model.SeverityError {
			t.Fatalf("error-severity finding %+v", d)
		}
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}

type ruleCase struct {
	name string
	src  string
	want int
}

// structureCase is a rule case with local modules, environments or a
// layout (see structureCases).
type structureCase struct {
	name    string
	src     string
	modules map[string]string // local module directory -> main.tf
	envs    []string
	layout  string
	want    int
}

func TestRules(t *testing.T) {
	cases := map[string][]ruleCase{
		"bp_variable_type": {
			{"missing", `variable "a" { description = "x" }`, 1},
			{"typed", `variable "a" {
  type        = string
  description = "x"
}`, 0},
		},
		"bp_variable_description": {
			{"missing", `variable "a" { type = string }`, 1},
			{"blank", `variable "a" {
  type        = string
  description = " "
}`, 1},
			{"described", `variable "a" {
  type        = string
  description = "x"
}`, 0},
		},
		"bp_output_description": {
			{"missing", `output "a" { value = 1 }`, 1},
			{"described", `output "a" {
  value       = 1
  description = "x"
}`, 0},
		},
		"bp_sensitive_not_marked": {
			{"password variable", `variable "db_password" { type = string }`, 1},
			{"api key variable", `variable "stripe_api_key" { type = string }`, 1},
			{"marked", `variable "db_password" {
  type      = string
  sensitive = true
}`, 0},
			{"flag, not a secret", `variable "manage_master_user_password" { type = bool }`, 0},
			{"arn of a secret", `variable "secret_arn" { type = string }`, 0},
			{"token ttl", `variable "token_ttl" { type = number }`, 0},
			// Corpus: terraform-aws-iam/examples/iam-user prints PGP-encrypted secrets and key metadata.
			{"encrypted secret output", `output "iam_user_login_profile_encrypted_password" {
  value       = "x"
  description = "x"
}`, 0},
			{"access key fingerprint output", `output "iam_user_access_key_fingerprint" {
  value       = "x"
  description = "x"
}`, 0},
			{"connection string output", `output "namespace_connection_string" {
  value       = "x"
  description = "x"
}`, 1},
			{"output of a sensitive variable", `variable "pw" {
  type      = string
  sensitive = true
}
output "pw_out" { value = var.pw }`, 1},
			{"output of a sensitive attribute", pinned + `resource "aws_db_instance" "db" {
  instance_class = "db.t3.micro"
  password       = "x"
}
output "db" { value = aws_db_instance.db.password }`, 1},
			{"output of a plain attribute", pinned + `resource "aws_sqs_queue" "q" {}
output "url" { value = aws_sqs_queue.q.url }`, 0},
		},
		"bp_required_version": {
			{"missing", `provider "aws" {}`, 1},
			{"empty workflow", ``, 0},
			{"set", pinned + `provider "aws" {}`, 0},
		},
		"bp_provider_version": {
			{"not listed", `terraform { required_version = ">= 1.6" }
resource "aws_sqs_queue" "q" {}`, 1},
			{"source only", `terraform {
  required_providers {
    aws = { source = "hashicorp/aws" }
  }
}
resource "aws_sqs_queue" "q" {}`, 1},
			{"legacy string", `terraform {
  required_providers {
    aws = "hashicorp/aws"
  }
}
resource "aws_sqs_queue" "q" {}`, 1},
			{"pinned", pinned + `resource "aws_sqs_queue" "q" {}`, 0},
			{"builtin terraform_data", pinned + `resource "terraform_data" "x" {}`, 0},
		},
		"bp_provider_local_compat": {
			{"recommended constraints", pinned + `resource "google_sql_database" "d" {}`, 0},
			{"previous major", googleConstraint("~> 6.0") + `resource "google_storage_bucket" "b" {}`, 1},
			{"allows a range that breaks a resource", googleConstraint("~> 7.0") + `resource "google_sql_database" "d" {}`, 1},
			{"allows releases older than the local minimum", googleConstraint("~> 7.0") + `resource "google_storage_bucket" "b" {}`, 1},
			{"forces a broken release", googleConstraint("= 7.20.0") + `resource "google_sql_database" "d" {}`, 1},
			{"minimum at the verified release", googleConstraint(">= 7.26.0, < 8.0.0") + `resource "google_sql_database" "d" {}`, 0},
			{"no constraint is bp_provider_version's", googleConstraint("") + `resource "google_storage_bucket" "b" {}`, 0},
			{"unparseable constraint", googleConstraint("latest") + `resource "google_storage_bucket" "b" {}`, 0},
			{"another namespace's provider", `terraform {
  required_providers {
    google = { source = "acme/google", version = "~> 1.0" }
  }
}
resource "google_storage_bucket" "b" {}`, 0},
			{"legacy string constraint", `terraform {
  required_providers {
    aws = "~> 5.0"
  }
}
resource "aws_sqs_queue" "q" {}`, 1},
			{"emulator gap of the catalog release is the run's", `terraform {
  required_providers {
    oci = { source = "oracle/oci", version = "~> 9.10" }
  }
}
resource "oci_functions_application" "app" {}`, 0},
			{"previous oci major", `terraform {
  required_providers {
    oci = { source = "oracle/oci", version = "~> 8.29" }
  }
}
resource "oci_objectstorage_bucket" "b" {}`, 1},
			{"one finding per provider", `terraform {
  required_providers {
    aws    = { source = "hashicorp/aws", version = "~> 5.0" }
    google = { source = "hashicorp/google", version = "~> 6.0" }
  }
}
resource "aws_sqs_queue" "q" {}
resource "google_sql_database" "d" {}`, 2},
		},
		"bp_hardcoded_region": {
			{"aws region literal", pinned + `resource "aws_dynamodb_table" "t" {
  name     = "t"
  hash_key = "id"
  attribute {
    name = "id"
    type = "S"
  }
  replica {
    region_name = "us-west-2"
  }
}`, 1},
			{"azure location literal", pinned + `resource "azurerm_resource_group" "rg" {
  name     = "rg"
  location = "westeurope"
  tags     = { a = "b" }
}`, 1},
			{"azure location variable", pinned + `variable "location" {
  type        = string
  description = "x"
}
resource "azurerm_resource_group" "rg" {
  name     = "rg"
  location = var.location
  tags     = { a = "b" }
}`, 0},
			{"provider region is fine", pinned + `provider "aws" { region = "us-east-1" }`, 0},
			{"gcp multi-region is fine", pinned + `resource "google_storage_bucket" "b" {
  name     = "b"
  location = "US"
  labels   = { a = "b" }
}`, 0},
			{"gcp region literal", pinned + `resource "google_cloud_run_v2_service" "s" {
  name     = "s"
  location = "europe-west1"
  labels   = { a = "b" }
  template {
    containers { image = "x" }
  }
}`, 1},
		},
		"bp_hardcoded_az": {
			{"aws az", pinned + `resource "aws_subnet" "s" {
  vpc_id            = "vpc-1"
  cidr_block        = "10.0.1.0/24"
  availability_zone = "us-east-1a"
}`, 1},
			{"data source", pinned + `data "aws_availability_zones" "az" {}
resource "aws_subnet" "s" {
  vpc_id            = "vpc-1"
  cidr_block        = "10.0.1.0/24"
  availability_zone = data.aws_availability_zones.az.names[0]
}`, 0},
		},
		"bp_hardcoded_account_id": {
			{"arn with account", pinned + `resource "aws_iam_role_policy_attachment" "a" {
  role       = "r"
  policy_arn = "arn:aws:iam::123456789012:policy/x"
}`, 1},
			{"account inside jsonencode", pinned + `resource "aws_iam_policy" "p" {
  name   = "p"
  policy = jsonencode({ Statement = [{ Effect = "Allow", Action = ["s3:GetObject"], Resource = ["arn:aws:s3:us-east-1:123456789012:x"] }] })
}`, 1},
			{"source_account literal", pinned + `resource "aws_lambda_permission" "p" {
  action         = "lambda:InvokeFunction"
  function_name  = "f"
  principal      = "s3.amazonaws.com"
  source_account = "123456789012"
}`, 1},
			{"aws managed policy", pinned + `resource "aws_iam_role_policy_attachment" "a" {
  role       = "r"
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}`, 0},
		},
		"bp_missing_tags": {
			{"aws without default_tags", pinned + `provider "aws" {}
resource "aws_sqs_queue" "a" {}
resource "aws_sqs_queue" "b" {}`, 1},
			{"aws default_tags", pinned + `provider "aws" {
  default_tags {
    tags = { Project = "x" }
  }
}
resource "aws_sqs_queue" "a" {}`, 0},
			{"aws tagged", pinned + `resource "aws_sqs_queue" "a" { tags = { a = "b" } }`, 0},
			{"aws empty tags", pinned + `resource "aws_sqs_queue" "a" { tags = {} }`, 1},
			{"aws alias without defaults", pinned + `provider "aws" {
  default_tags {
    tags = { Project = "x" }
  }
}
provider "aws" { alias = "west" }
resource "aws_sqs_queue" "a" { provider = aws.west }`, 1},
			{"not taggable", pinned + `resource "aws_iam_role_policy" "p" {
  role   = "r"
  policy = "{}"
}`, 0},
			{"azure per resource", pinned + `resource "azurerm_resource_group" "a" {
  name     = "a"
  location = var.location
}
resource "azurerm_resource_group" "b" {
  name     = "b"
  location = var.location
}
variable "location" {
  type        = string
  description = "x"
}`, 2},
			{"gcp default_labels", pinned + `provider "google" { default_labels = { project = "x" } }
resource "google_pubsub_topic" "t" { name = "t" }`, 0},
			{"gcp unlabeled", pinned + `resource "google_pubsub_topic" "t" { name = "t" }`, 1},
		},
		"bp_name_prefix": {
			{"fixed name", pinned + `locals { name_prefix = "x" }
resource "aws_sqs_queue" "q" { name = "jobs" }`, 1},
			{"derived name", pinned + `locals { name_prefix = "x" }
resource "aws_sqs_queue" "q" { name = "${local.name_prefix}-jobs" }`, 0},
			{"no prefix local", pinned + `resource "aws_sqs_queue" "q" { name = "jobs" }`, 0},
			{"constrained name type", pinned + `locals { name_prefix = "x" }
resource "azurerm_storage_account" "s" {
  name                     = "abc123"
  resource_group_name      = "rg"
  location                 = var.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
  tags                     = {}
}
variable "location" {
  type        = string
  description = "x"
}`, 0},
		},
		"bp_open_ingress": {
			{"ssh from anywhere", pinned + `resource "aws_security_group" "sg" {
  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
}`, 1},
			{"all traffic", pinned + `resource "aws_security_group" "sg" {
  ingress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}`, 1},
			{"https from anywhere", pinned + `resource "aws_security_group" "sg" {
  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
}`, 0},
			{"ssh from vpn", pinned + `resource "aws_security_group" "sg" {
  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["10.8.0.0/16"]
  }
}`, 0},
			{"egress all is fine", pinned + `resource "aws_security_group" "sg" {
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}`, 0},
			{"smb from anywhere", pinned + `resource "aws_security_group" "sg" {
  ingress {
    from_port   = 445
    to_port     = 445
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
}`, 1},
			{"azure smb", pinned + `resource "azurerm_network_security_rule" "r" {
  name                        = "smb"
  priority                    = 100
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = "Tcp"
  source_port_range           = "*"
  destination_port_range      = "445"
  source_address_prefix       = "Internet"
  destination_address_prefix  = "*"
  resource_group_name         = "rg"
  network_security_group_name = "nsg"
}`, 1},
			{"sg rule postgres", pinned + `resource "aws_security_group_rule" "r" {
  type              = "ingress"
  security_group_id = "sg-1"
  from_port         = 5432
  to_port           = 5432
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
}`, 1},
			{"vpc ingress rule rdp ipv6", pinned + `resource "aws_vpc_security_group_ingress_rule" "r" {
  security_group_id = "sg-1"
  from_port         = 3389
  to_port           = 3389
  ip_protocol       = "tcp"
  cidr_ipv6         = "::/0"
}`, 1},
			{"gcp firewall ssh range", pinned + `resource "google_compute_firewall" "f" {
  name          = "f"
  network       = "n"
  source_ranges = ["0.0.0.0/0"]
  allow {
    protocol = "tcp"
    ports    = ["20-25"]
  }
}`, 1},
			{"azure nsg rule", pinned + `resource "azurerm_network_security_rule" "r" {
  name                        = "ssh"
  priority                    = 100
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = "Tcp"
  source_port_range           = "*"
  destination_port_range      = "22"
  source_address_prefix       = "*"
  destination_address_prefix  = "*"
  resource_group_name         = "rg"
  network_security_group_name = "nsg"
}`, 1},
		},
		"bp_public_bucket": {
			{"open public access block", pinned + `resource "aws_s3_bucket" "b" {}
resource "aws_s3_bucket_public_access_block" "p" {
  bucket              = aws_s3_bucket.b.id
  block_public_acls   = false
  block_public_policy = true
}`, 1},
			{"closed public access block", pinned + `resource "aws_s3_bucket" "b" {}
resource "aws_s3_bucket_public_access_block" "p" {
  bucket                  = aws_s3_bucket.b.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}`, 0},
			{"website intent", pinned + `resource "aws_s3_bucket" "b" {}
resource "aws_s3_bucket_website_configuration" "w" {
  bucket = aws_s3_bucket.b.id
  index_document { suffix = "index.html" }
}
resource "aws_s3_bucket_public_access_block" "p" {
  bucket              = aws_s3_bucket.b.id
  block_public_acls   = false
}`, 0},
			{"tag intent", pinned + `resource "aws_s3_bucket" "b" { tags = { Public = "true" } }
resource "aws_s3_bucket_acl" "a" {
  bucket = aws_s3_bucket.b.id
  acl    = "public-read"
}`, 0},
			{"public acl", pinned + `resource "aws_s3_bucket" "b" {}
resource "aws_s3_bucket_acl" "a" {
  bucket = aws_s3_bucket.b.id
  acl    = "public-read"
}`, 1},
			{"public policy", pinned + `resource "aws_s3_bucket" "b" {}
resource "aws_s3_bucket_policy" "p" {
  bucket = aws_s3_bucket.b.id
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = "*", Action = "s3:GetObject", Resource = "${aws_s3_bucket.b.arn}/*" }]
  })
}`, 1},
			{"policy with condition", pinned + `resource "aws_s3_bucket" "b" {}
resource "aws_s3_bucket_policy" "p" {
  bucket = aws_s3_bucket.b.id
  policy = jsonencode({
    Statement = [{ Effect = "Allow", Principal = "*", Action = "s3:GetObject", Resource = "x", Condition = { StringEquals = { "aws:SourceVpce" = "vpce-1" } } }]
  })
}`, 0},
			{"gcp allUsers", pinned + `resource "google_storage_bucket_iam_member" "m" {
  bucket = "b"
  role   = "roles/storage.objectViewer"
  member = "allUsers"
}`, 1},
			{"azure public container", pinned + `resource "azurerm_storage_container" "c" {
  name                  = "c"
  storage_account_id    = "x"
  container_access_type = "blob"
}`, 1},
		},
		"bp_unencrypted_storage": {
			{"s3 without sse", pinned + `resource "aws_s3_bucket" "b" {}`, 1},
			{"s3 with sse", pinned + `resource "aws_s3_bucket" "b" {}
resource "aws_s3_bucket_server_side_encryption_configuration" "e" {
  bucket = aws_s3_bucket.b.id
  rule {
    apply_server_side_encryption_by_default { sse_algorithm = "AES256" }
  }
}`, 0},
			{"rds unencrypted", pinned + `resource "aws_db_instance" "d" { instance_class = "db.t3.micro" }`, 1},
			{"rds encrypted", pinned + `resource "aws_db_instance" "d" {
  instance_class    = "db.t3.micro"
  storage_encrypted = true
}`, 0},
			{"rds replica inherits", pinned + `resource "aws_db_instance" "d" {
  instance_class      = "db.t3.micro"
  replicate_source_db = "x"
}`, 0},
			{"rds encryption from a variable", pinned + `variable "enc" {
  type        = bool
  description = "x"
}
resource "aws_db_instance" "d" {
  instance_class    = "db.t3.micro"
  storage_encrypted = var.enc
}`, 0},
			{"dynamodb default key", pinned + `resource "aws_dynamodb_table" "t" {
  name     = "t"
  hash_key = "id"
  attribute {
    name = "id"
    type = "S"
  }
}`, 1},
			{"ebs volume", pinned + `resource "aws_ebs_volume" "v" {
  availability_zone = var.az
  size              = 8
}
variable "az" {
  type        = string
  description = "x"
}`, 1},
			{"ebs default encryption", pinned + `resource "aws_ebs_encryption_by_default" "e" { enabled = true }
resource "aws_ebs_volume" "v" {
  availability_zone = var.az
  size              = 8
}
variable "az" {
  type        = string
  description = "x"
}`, 0},
			{"instance root device", pinned + `resource "aws_instance" "i" {
  ami           = "ami-1"
  instance_type = "t3.micro"
  root_block_device { volume_size = 8 }
}`, 1},
			{"instance without block devices", pinned + `resource "aws_instance" "i" {
  ami           = "ami-1"
  instance_type = "t3.micro"
}`, 0},
		},
		"bp_iam_wildcard": {
			{"admin policy jsonencode", pinned + `resource "aws_iam_policy" "p" {
  name   = "p"
  policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Action = "*", Resource = "*" }] })
}`, 1},
			{"admin policy heredoc", pinned + `resource "aws_iam_role_policy" "p" {
  role   = "r"
  policy = <<EOF
{"Version": "2012-10-17", "Statement": {"Effect": "Allow", "Action": ["*"], "Resource": "*"}}
EOF
}`, 1},
			{"admin policy document", pinned + `data "aws_iam_policy_document" "d" {
  statement {
    actions   = ["*"]
    resources = ["*"]
  }
}`, 1},
			{"deny is fine", pinned + `data "aws_iam_policy_document" "d" {
  statement {
    effect    = "Deny"
    actions   = ["*"]
    resources = ["*"]
  }
}`, 0},
			{"scoped actions on star", pinned + `resource "aws_iam_policy" "p" {
  name   = "p"
  policy = jsonencode({ Statement = [{ Effect = "Allow", Action = ["logs:PutLogEvents"], Resource = "*" }] })
}`, 0},
			{"star on scoped resources", pinned + `resource "aws_sqs_queue" "q" {}
resource "aws_iam_policy" "p" {
  name   = "p"
  policy = jsonencode({ Statement = [{ Effect = "Allow", Action = "*", Resource = [aws_sqs_queue.q.arn] }] })
}`, 0},
		},
		"bp_log_retention": {
			{"no log group", pinned + `resource "aws_lambda_function" "f" {
  function_name = "f"
  role          = "r"
}`, 1},
			{"log group by reference", pinned + `resource "aws_lambda_function" "f" {
  function_name = "f"
  role          = "r"
}
resource "aws_cloudwatch_log_group" "l" {
  name              = "/aws/lambda/${aws_lambda_function.f.function_name}"
  retention_in_days = 14
}`, 0},
			{"log group by name", pinned + `locals { name_prefix = "x" }
resource "aws_lambda_function" "f" {
  function_name = "${local.name_prefix}-api"
  role          = "r"
}
resource "aws_cloudwatch_log_group" "l" {
  name              = "/aws/lambda/${local.name_prefix}-api"
  retention_in_days = 7
}`, 0},
			{"logging_config", pinned + `resource "aws_cloudwatch_log_group" "l" {
  name              = "/custom/logs"
  retention_in_days = 7
}
resource "aws_lambda_function" "f" {
  function_name = "f"
  role          = "r"
  logging_config {
    log_format = "Text"
    log_group  = aws_cloudwatch_log_group.l.name
  }
}`, 0},
			{"log group without retention", pinned + `resource "aws_lambda_function" "f" {
  function_name = "f"
  role          = "r"
}
resource "aws_cloudwatch_log_group" "l" { name = "/aws/lambda/f" }`, 1},
		},
		"bp_large_file": {
			{"small file", pinned + `resource "aws_sqs_queue" "q" { tags = { a = "b" } }`, 0},
		},
		"bp_module_candidate": {
			{"small workflow", pinned + network(6), 0},
		},
		"bp_duplicate_literal": {
			{"cidr in three resources", pinned + `resource "aws_route" "a" {
  route_table_id         = "rt-1"
  destination_cidr_block = "10.20.0.0/16"
}
resource "aws_route" "b" {
  route_table_id         = "rt-2"
  destination_cidr_block = "10.20.0.0/16"
}
resource "aws_route" "c" {
  route_table_id         = "rt-3"
  destination_cidr_block = "10.20.0.0/16"
}`, 1},
			// Corpus: terraform-provider-oci/examples/object_storage repeats content types.
			{"mime types are fine", pinned + `resource "aws_s3_object" "a" {
  bucket       = "b"
  key          = "a"
  content_type = "text/html"
}
resource "aws_s3_object" "b" {
  bucket       = "b"
  key          = "b"
  content_type = "text/html"
}
resource "aws_s3_object" "c" {
  bucket       = "b"
  key          = "c"
  content_type = "text/html"
}`, 0},
			{"twice is fine", pinned + `resource "aws_route" "a" {
  route_table_id         = "rt-1"
  destination_cidr_block = "10.20.0.0/16"
}
resource "aws_route" "b" {
  route_table_id         = "rt-2"
  destination_cidr_block = "10.20.0.0/16"
}`, 0},
			{"enums and words are fine", pinned + `resource "aws_lambda_function" "a" {
  function_name = "a"
  role          = "r"
  runtime       = "nodejs22.x"
}
resource "aws_lambda_function" "b" {
  function_name = "b"
  role          = "r"
  runtime       = "nodejs22.x"
}
resource "aws_lambda_function" "c" {
  function_name = "c"
  role          = "r"
  runtime       = "nodejs22.x"
}`, 0},
		},
		"bp_emulator_host_loopback": {
			{"cloud run env on localhost", cloudRunEnv("PUBSUB_EMULATOR_HOST", `"localhost.floci.io:4588"`), 1},
			{"cloud run env from a variable", cloudRunEnv("PUBSUB_EMULATOR_HOST", `var.gcp_emulator_host`), 0},
		},
	}
	for _, r := range Rules() {
		if len(cases[r.Code]) == 0 && len(structureCases[r.Code]) == 0 {
			t.Errorf("rule %s has no test cases", r.Code)
		}
	}
	all := map[string][]structureCase{}
	for code, list := range cases {
		for _, tc := range list {
			all[code] = append(all[code], structureCase{name: tc.name, src: tc.src, want: tc.want})
		}
	}
	for code, list := range structureCases {
		all[code] = append(all[code], list...)
	}
	for code, list := range all {
		if !slices.ContainsFunc(Rules(), func(r model.BestPracticeRule) bool { return r.Code == code }) {
			t.Errorf("cases for unknown rule %s", code)
		}
		for _, tc := range list {
			t.Run(code+"/"+tc.name, func(t *testing.T) {
				got := caseFindings(t, tc, code)
				if len(got) != tc.want {
					t.Errorf("%d findings, want %d: %+v", len(got), tc.want, got)
				}
				for _, d := range got {
					if d.Message == "" || d.Detail == "" || d.Fix == "" {
						t.Errorf("finding without message/why/fix: %+v", d)
					}
				}
			})
		}
	}
}

// network renders n VPC resources.
func network(n int) string {
	var sb strings.Builder
	sb.WriteString("resource \"aws_vpc\" \"v\" {\n  cidr_block = \"10.0.0.0/16\"\n  tags = { a = \"b\" }\n}\n")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&sb, "resource \"aws_subnet\" \"s%d\" {\n  vpc_id     = aws_vpc.v.id\n  cidr_block = \"10.0.%d.0/24\"\n  tags = { a = \"b\" }\n}\n", i, i)
	}
	return sb.String()
}

func TestStructureRules(t *testing.T) {
	var queues strings.Builder
	for i := range 16 {
		fmt.Fprintf(&queues, "resource \"aws_sqs_queue\" \"q%d\" { tags = { a = \"b\" } }\n", i)
	}
	wf := importHCL(t, pinned+queues.String())
	// Imported files are kept: all 16 queues stay in main.tf.
	if got := codes(Check(wf, testCatalog, model.BestPracticeSettings{}, nil)); got["bp_large_file"] != 1 {
		t.Errorf("16 resources in one file: %v", got)
	}

	wf = importHCL(t, pinned+network(8)+queues.String())
	if got := codes(Check(wf, testCatalog, model.BestPracticeSettings{}, nil)); got["bp_module_candidate"] != 1 {
		t.Errorf("8 network resources in a 24-resource workflow: %v", got)
	}
	var fix string
	for _, d := range Check(wf, testCatalog, model.BestPracticeSettings{}, nil) {
		if d.Code == "bp_module_candidate" {
			fix = d.Fix
		}
	}
	if !strings.Contains(fix, "extract_module") || !strings.Contains(fix, "aws_vpc.v") {
		t.Errorf("module candidate fix = %q", fix)
	}

	// Security group rules belong with the service they protect, not the
	// network foundation.
	sgRules := `resource "aws_security_group" "app" { vpc_id = aws_vpc.v.id }
resource "aws_vpc_security_group_egress_rule" "a" {
  security_group_id = aws_security_group.app.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}
resource "aws_vpc_security_group_ingress_rule" "b" {
  security_group_id = aws_security_group.app.id
  cidr_ipv4         = "10.0.0.0/16"
  ip_protocol       = "-1"
}
`
	wf = importHCL(t, pinned+network(4)+sgRules+queues.String())
	if got := codes(Check(wf, testCatalog, model.BestPracticeSettings{}, nil)); got["bp_module_candidate"] != 0 {
		t.Errorf("4 network resources plus security group rules: %v", got)
	}

	var tables strings.Builder
	for i := range 5 {
		fmt.Fprintf(&tables, "resource \"aws_dynamodb_table\" \"t%d\" {\n  name     = \"t%d\"\n  hash_key = \"id\"\n}\n", i, i)
	}
	wf = importHCL(t, pinned+tables.String()+queues.String())
	found := false
	for _, d := range Check(wf, testCatalog, model.BestPracticeSettings{}, nil) {
		if d.Code == "bp_module_candidate" && strings.Contains(d.Message, "data tier") && strings.Contains(d.Fix, `"data"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("5 DynamoDB tables are a data tier candidate")
	}
}

func codes(diags []model.Diagnostic) map[string]int {
	out := map[string]int{}
	for _, d := range diags {
		out[d.Code]++
	}
	return out
}

func TestSettingsDisableRules(t *testing.T) {
	src := `variable "a" {}`
	wf := importHCL(t, src)
	if got := codes(Check(wf, testCatalog, model.BestPracticeSettings{}, nil)); got["bp_variable_type"] != 1 || got["bp_variable_description"] != 1 {
		t.Fatalf("all rules: %v", got)
	}
	if got := codes(Check(wf, testCatalog, model.BestPracticeSettings{DisabledRules: []string{"bp_variable_type"}}, nil)); got["bp_variable_type"] != 0 || got["bp_variable_description"] != 1 {
		t.Errorf("one rule off: %v", got)
	}
	if got := Check(wf, testCatalog, model.BestPracticeSettings{Disabled: true}, nil); len(got) != 0 {
		t.Errorf("checks off: %v", got)
	}
}

// The catalog templates follow every practice.
func TestTemplatesAreClean(t *testing.T) {
	for _, tpl := range testCatalog.Templates() {
		files, ok := testCatalog.TemplateFiles(tpl.ID)
		if !ok {
			t.Fatalf("template %s", tpl.ID)
		}
		wf, _, err := hclcodec.Import(tpl.ID, files)
		if err != nil {
			t.Fatal(err)
		}
		hclcodec.Normalize(wf)
		for _, d := range Check(wf, testCatalog, model.BestPracticeSettings{}, nil) {
			t.Errorf("%s: %s %s", tpl.ID, d.Code, d.Message)
		}
	}
}

// A fresh workflow built from every popular starter of a cloud (one cloud
// per workflow and all four together) has no findings.
func TestPopularStartersAreClean(t *testing.T) {
	all := &model.Workflow{Name: "Everything"}
	for _, cloud := range []model.Cloud{model.CloudAWS, model.CloudAzure, model.CloudGCP, model.CloudOCI} {
		wf := &model.Workflow{Name: "Fresh " + string(cloud)}
		for _, r := range testCatalog.Search(catalog.Query{Cloud: cloud, Kind: model.KindResource, PopularOnly: true}) {
			for _, target := range []*model.Workflow{wf, all} {
				if _, err := testCatalog.AddNode(target, model.KindResource, r.Type, "", model.Position{}); err != nil {
					t.Fatalf("add %s: %v", r.Type, err)
				}
			}
		}
		hclcodec.Normalize(wf)
		for _, d := range Check(wf, testCatalog, model.BestPracticeSettings{}, nil) {
			if d.Code == "bp_large_file" || d.Code == "bp_module_candidate" {
				continue // one big generated workflow per cloud, by construction
			}
			t.Errorf("%s: %s %s", cloud, d.Code, d.Message)
		}
	}
	hclcodec.Normalize(all)
	for _, d := range Check(all, testCatalog, model.BestPracticeSettings{}, nil) {
		if d.Code == "bp_large_file" || d.Code == "bp_module_candidate" || d.Code == "bp_state_too_large" {
			continue
		}
		t.Errorf("all clouds: %s %s", d.Code, d.Message)
	}
}
