package lint

import (
	"fmt"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// TestRules runs these with the other rule cases.
func init() {
	for code, list := range cisAWSCases {
		for _, tc := range list {
			structureCases[code] = append(structureCases[code], structureCase{name: tc.name, src: tc.src, want: tc.want})
		}
	}
}

const (
	adminArn  = `"arn:aws:iam::aws:policy/AdministratorAccess"`
	trailHead = `resource "aws_cloudtrail" "t" {
  name           = "t"
  s3_bucket_name = "logs"
`
	vpcV = `resource "aws_vpc" "v" { cidr_block = "10.0.0.0/16" }
`
)

// sgWithIngress renders a security group with one inline ingress rule.
func sgWithIngress(cidr, proto string, from, to int) string {
	source := fmt.Sprintf("cidr_blocks = [%q]", cidr)
	if cidr == "::/0" {
		source = `ipv6_cidr_blocks = ["::/0"]`
	}
	return fmt.Sprintf(`resource "aws_security_group" "s" {
  ingress {
    protocol  = %q
    from_port = %d
    to_port   = %d
    %s
  }
}`, proto, from, to, source)
}

var cisAWSCases = map[string][]ruleCase{
	"bp_iam_password_length": {
		{"unset", `resource "aws_iam_account_password_policy" "p" { password_reuse_prevention = 24 }`, 1},
		{"too short", `resource "aws_iam_account_password_policy" "p" { minimum_password_length = 8 }`, 1},
		{"14", `resource "aws_iam_account_password_policy" "p" { minimum_password_length = 14 }`, 0},
		{"variable", `resource "aws_iam_account_password_policy" "p" { minimum_password_length = var.len }`, 0},
	},
	"bp_iam_password_reuse": {
		{"unset", `resource "aws_iam_account_password_policy" "p" { minimum_password_length = 14 }`, 1},
		{"few", `resource "aws_iam_account_password_policy" "p" { password_reuse_prevention = 5 }`, 1},
		{"24", `resource "aws_iam_account_password_policy" "p" { password_reuse_prevention = 24 }`, 0},
	},
	"bp_iam_user_policy": {
		{"user attachment", `resource "aws_iam_user_policy_attachment" "a" {
  user       = "alice"
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}`, 1},
		{"inline user policy", `resource "aws_iam_user_policy" "a" {
  user   = "alice"
  policy = "{}"
}`, 1},
		{"shared attachment with users", `resource "aws_iam_policy_attachment" "a" {
  name       = "a"
  users      = ["alice"]
  roles      = ["app"]
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}`, 1},
		{"shared attachment to roles", `resource "aws_iam_policy_attachment" "a" {
  name       = "a"
  roles      = ["app"]
  users      = []
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}`, 0},
		{"group attachment", `resource "aws_iam_group_policy_attachment" "a" {
  group      = "devs"
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}`, 0},
		{"exclusive none", `resource "aws_iam_user_policy_attachments_exclusive" "a" {
  user_name   = "alice"
  policy_arns = []
}`, 0},
	},
	"bp_iam_admin_attached": {
		{"role attachment", `resource "aws_iam_role_policy_attachment" "a" {
  role       = "ops"
  policy_arn = ` + adminArn + `
}`, 1},
		{"partition interpolated", `resource "aws_iam_group_policy_attachment" "a" {
  group      = "ops"
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AdministratorAccess"
}`, 1},
		{"managed_policy_arns", `resource "aws_iam_role" "r" {
  name                = "ops"
  assume_role_policy  = "{}"
  managed_policy_arns = [` + adminArn + `]
}`, 1},
		{"similar name", `resource "aws_iam_role_policy_attachment" "a" {
  role       = "ops"
  policy_arn = "arn:aws:iam::aws:policy/AdministratorAccess-Amplify"
}`, 0},
		{"read only", `resource "aws_iam_role_policy_attachment" "a" {
  role       = "ops"
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}`, 0},
		{"wildcard document", `data "aws_iam_policy_document" "admin" {
  statement {
    actions   = ["*"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "p" {
  name   = "p"
  policy = data.aws_iam_policy_document.admin.json
}
resource "aws_iam_role" "r" {
  name               = "r"
  assume_role_policy = "{}"
  inline_policy {
    name   = "all"
    policy = data.aws_iam_policy_document.admin.json
  }
}`, 2},
		{"scoped document", `data "aws_iam_policy_document" "d" {
  statement {
    actions   = ["sqs:SendMessage"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "p" {
  name   = "p"
  policy = data.aws_iam_policy_document.d.json
}`, 0},
	},
	"bp_s3_deny_http": {
		{"no policy", `resource "aws_s3_bucket" "b" { bucket = "b" }`, 1},
		{"jsonencode deny", `resource "aws_s3_bucket" "b" { bucket = "b" }
resource "aws_s3_bucket_policy" "p" {
  bucket = aws_s3_bucket.b.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Deny"
      Principal = "*"
      Action    = "s3:*"
      Resource  = [aws_s3_bucket.b.arn, "${aws_s3_bucket.b.arn}/*"]
      Condition = { Bool = { "aws:SecureTransport" = "false" } }
    }]
  })
}`, 0},
		{"json deny", `resource "aws_s3_bucket" "b" { bucket = "b" }
resource "aws_s3_bucket_policy" "p" {
  bucket = aws_s3_bucket.b.id
  policy = <<-EOT
    {"Version": "2012-10-17", "Statement": [{"Effect": "Deny", "Principal": "*", "Action": "s3:*",
      "Resource": ["arn:aws:s3:::b", "arn:aws:s3:::b/*"], "Condition": {"Bool": {"aws:SecureTransport": "false"}}}]}
  EOT
}`, 0},
		{"policy document deny", `resource "aws_s3_bucket" "b" { bucket = "b" }
data "aws_iam_policy_document" "tls" {
  statement {
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = ["${aws_s3_bucket.b.arn}/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}
resource "aws_s3_bucket_policy" "p" {
  bucket = aws_s3_bucket.b.id
  policy = data.aws_iam_policy_document.tls.json
}`, 0},
		{"policy without deny", `resource "aws_s3_bucket" "b" { bucket = "b" }
resource "aws_s3_bucket_policy" "p" {
  bucket = aws_s3_bucket.b.id
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { AWS = "arn:aws:iam::123456789012:root" }, Action = "s3:GetObject", Resource = "${aws_s3_bucket.b.arn}/*" }]
  })
}`, 1},
		{"opaque policy", `resource "aws_s3_bucket" "b" { bucket = "b" }
resource "aws_s3_bucket_policy" "p" {
  bucket = aws_s3_bucket.b.id
  policy = templatefile("policy.json", {})
}`, 0},
	},
	"bp_s3_block_public_access": {
		{"all on", `resource "aws_s3_bucket_public_access_block" "b" {
  bucket                  = "b"
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}`, 0},
		{"one missing", `resource "aws_s3_bucket_public_access_block" "b" {
  bucket              = "b"
  block_public_acls   = true
  block_public_policy = true
  ignore_public_acls  = true
}`, 1},
		{"website still counts", `resource "aws_s3_bucket" "b" { bucket = "b" }
resource "aws_s3_bucket_website_configuration" "w" {
  bucket = aws_s3_bucket.b.id
  index_document { suffix = "index.html" }
}
resource "aws_s3_bucket_public_access_block" "b" {
  bucket                  = aws_s3_bucket.b.id
  block_public_acls       = false
  block_public_policy     = false
  ignore_public_acls      = false
  restrict_public_buckets = false
}`, 1},
		{"account level off", `resource "aws_s3_account_public_access_block" "a" { block_public_acls = true }`, 1},
	},
	"bp_rds_public": {
		{"public instance", `resource "aws_db_instance" "d" { publicly_accessible = true }`, 1},
		{"public cluster instance", `resource "aws_rds_cluster_instance" "d" { publicly_accessible = true }`, 1},
		{"private", `resource "aws_db_instance" "d" { publicly_accessible = false }`, 0},
		{"default", `resource "aws_db_instance" "d" { engine = "postgres" }`, 0},
	},
	"bp_rds_minor_upgrade": {
		{"off", `resource "aws_db_instance" "d" { auto_minor_version_upgrade = false }`, 1},
		{"default", `resource "aws_db_instance" "d" { engine = "postgres" }`, 0},
	},
	"bp_rds_multi_az": {
		{"single AZ", `resource "aws_db_instance" "d" { engine = "postgres" }`, 1},
		{"multi AZ", `resource "aws_db_instance" "d" { multi_az = true }`, 0},
		{"replica", `resource "aws_db_instance" "d" { replicate_source_db = "main" }`, 0},
	},
	"bp_cloudtrail_multi_region": {
		{"single region", trailHead + `}`, 1},
		{"multi region", trailHead + `  is_multi_region_trail = true
}`, 0},
		{"logging off", trailHead + `  is_multi_region_trail = true
  enable_logging        = false
}`, 1},
		{"read-only management events", trailHead + `  is_multi_region_trail = true
  event_selector { read_write_type = "ReadOnly" }
}`, 1},
		{"all management events", trailHead + `  is_multi_region_trail = true
  event_selector {
    read_write_type           = "All"
    include_management_events = true
  }
}`, 0},
		{"advanced management selector", trailHead + `  is_multi_region_trail = true
  advanced_event_selector {
    field_selector {
      field  = "eventCategory"
      equals = ["Management"]
    }
  }
}`, 0},
		{"advanced data only", trailHead + `  is_multi_region_trail = true
  advanced_event_selector {
    field_selector {
      field  = "eventCategory"
      equals = ["Data"]
    }
    field_selector {
      field  = "resources.type"
      equals = ["AWS::S3::Object"]
    }
  }
}`, 1},
	},
	"bp_cloudtrail_log_validation": {
		{"off", trailHead + `}`, 1},
		{"on", trailHead + `  enable_log_file_validation = true
}`, 0},
	},
	"bp_cloudtrail_kms": {
		{"no key", trailHead + `}`, 1},
		{"key", trailHead + `  kms_key_id = aws_kms_key.k.arn
}
resource "aws_kms_key" "k" { enable_key_rotation = true }`, 0},
	},
	"bp_cloudtrail_bucket_logging": {
		{"bucket without logging", `resource "aws_s3_bucket" "logs" { bucket = "logs" }
resource "aws_cloudtrail" "t" {
  name           = "t"
  s3_bucket_name = aws_s3_bucket.logs.id
}`, 1},
		{"bucket with logging", `resource "aws_s3_bucket" "logs" { bucket = "logs" }
resource "aws_s3_bucket_logging" "l" {
  bucket        = aws_s3_bucket.logs.id
  target_bucket = "access-logs"
  target_prefix = "cloudtrail/"
}
resource "aws_cloudtrail" "t" {
  name           = "t"
  s3_bucket_name = aws_s3_bucket.logs.id
}`, 0},
		{"bucket outside the design", trailHead + `}`, 0},
	},
	"bp_cloudtrail_s3_write_events": {
		{"no data events", trailHead + `}`, 1},
		{"basic selector", trailHead + `  event_selector {
    read_write_type = "WriteOnly"
    data_resource {
      type   = "AWS::S3::Object"
      values = ["arn:aws:s3"]
    }
  }
}`, 0},
		{"reads only", trailHead + `  event_selector {
    read_write_type = "ReadOnly"
    data_resource {
      type   = "AWS::S3::Object"
      values = ["arn:aws:s3"]
    }
  }
}`, 1},
		{"advanced selector", trailHead + `  advanced_event_selector {
    field_selector {
      field  = "eventCategory"
      equals = ["Data"]
    }
    field_selector {
      field  = "resources.type"
      equals = ["AWS::S3::Object"]
    }
    field_selector {
      field  = "readOnly"
      equals = ["false"]
    }
  }
}`, 0},
		{"another trail covers it", trailHead + `}
resource "aws_cloudtrail" "data" {
  name           = "data"
  s3_bucket_name = "logs"
  event_selector {
    data_resource {
      type   = "AWS::S3::Object"
      values = ["arn:aws:s3"]
    }
  }
}`, 0},
	},
	"bp_cloudtrail_s3_read_events": {
		{"no data events", trailHead + `}`, 1},
		{"writes only", trailHead + `  advanced_event_selector {
    field_selector {
      field  = "eventCategory"
      equals = ["Data"]
    }
    field_selector {
      field  = "resources.type"
      equals = ["AWS::S3::Object"]
    }
    field_selector {
      field  = "readOnly"
      equals = ["false"]
    }
  }
}`, 1},
		{"all object events", trailHead + `  event_selector {
    read_write_type = "All"
    data_resource {
      type   = "AWS::S3::Object"
      values = ["arn:aws:s3"]
    }
  }
}`, 0},
	},
	"bp_kms_rotation": {
		{"unset", `resource "aws_kms_key" "k" { description = "k" }`, 1},
		{"off", `resource "aws_kms_key" "k" { enable_key_rotation = false }`, 1},
		{"on", `resource "aws_kms_key" "k" { enable_key_rotation = true }`, 0},
		{"asymmetric", `resource "aws_kms_key" "k" { customer_master_key_spec = "RSA_2048" }`, 0},
		{"hmac", `resource "aws_kms_key" "k" {
  key_usage                = "GENERATE_VERIFY_MAC"
  customer_master_key_spec = "HMAC_256"
}`, 0},
		{"custom key store", `resource "aws_kms_key" "k" { custom_key_store_id = "cks-1" }`, 0},
	},
	"bp_vpc_flow_logs": {
		{"no flow log", vpcV, 1},
		{"all traffic", vpcV + `resource "aws_flow_log" "f" {
  vpc_id          = aws_vpc.v.id
  traffic_type    = "ALL"
  log_destination = "arn:aws:s3:::flow-logs"
}`, 0},
		{"rejects", vpcV + `resource "aws_flow_log" "f" {
  vpc_id       = aws_vpc.v.id
  traffic_type = "REJECT"
}`, 0},
		{"accepted only", vpcV + `resource "aws_flow_log" "f" {
  vpc_id       = aws_vpc.v.id
  traffic_type = "ACCEPT"
}`, 1},
	},
	"bp_nacl_admin_ports": {
		{"ssh from anywhere", `resource "aws_network_acl" "n" {
  vpc_id = "vpc-1"
  ingress {
    rule_no    = 100
    action     = "allow"
    protocol   = "tcp"
    cidr_block = "0.0.0.0/0"
    from_port  = 22
    to_port    = 22
  }
}`, 1},
		{"ephemeral range includes 3389", `resource "aws_network_acl" "n" {
  vpc_id = "vpc-1"
  ingress {
    rule_no    = 100
    action     = "allow"
    protocol   = "tcp"
    cidr_block = "0.0.0.0/0"
    from_port  = 1024
    to_port    = 65535
  }
}`, 1},
		{"https only", `resource "aws_network_acl" "n" {
  vpc_id = "vpc-1"
  ingress {
    rule_no    = 100
    action     = "allow"
    protocol   = "tcp"
    cidr_block = "0.0.0.0/0"
    from_port  = 443
    to_port    = 443
  }
}`, 0},
		{"deny rule", `resource "aws_network_acl" "n" {
  vpc_id = "vpc-1"
  ingress {
    rule_no    = 100
    action     = "deny"
    protocol   = "-1"
    cidr_block = "0.0.0.0/0"
    from_port  = 0
    to_port    = 0
  }
}`, 0},
		{"standalone rule all protocols ipv6", `resource "aws_network_acl_rule" "r" {
  network_acl_id  = "acl-1"
  rule_number     = 100
  egress          = false
  protocol        = "-1"
  rule_action     = "allow"
  ipv6_cidr_block = "::/0"
}`, 1},
		{"standalone egress", `resource "aws_network_acl_rule" "r" {
  network_acl_id = "acl-1"
  rule_number    = 100
  egress         = true
  protocol       = "tcp"
  rule_action    = "allow"
  cidr_block     = "0.0.0.0/0"
  from_port      = 22
  to_port        = 22
}`, 0},
	},
	"bp_sg_admin_ipv4": {
		{"ssh", sgWithIngress("0.0.0.0/0", "tcp", 22, 22), 1},
		{"all protocols", sgWithIngress("0.0.0.0/0", "-1", 0, 0), 1},
		{"ipv6 only", sgWithIngress("::/0", "tcp", 22, 22), 0},
		{"database port", sgWithIngress("0.0.0.0/0", "tcp", 5432, 5432), 0},
		{"private source", sgWithIngress("10.0.0.0/8", "tcp", 22, 22), 0},
		{"vpc rule rdp", `resource "aws_vpc_security_group_ingress_rule" "r" {
  security_group_id = "sg-1"
  ip_protocol       = "tcp"
  from_port         = 3389
  to_port           = 3389
  cidr_ipv4         = "0.0.0.0/0"
}`, 1},
	},
	"bp_sg_admin_ipv6": {
		{"ssh", sgWithIngress("::/0", "tcp", 22, 22), 1},
		{"ipv4 only", sgWithIngress("0.0.0.0/0", "tcp", 22, 22), 0},
		{"vpc rule", `resource "aws_vpc_security_group_ingress_rule" "r" {
  security_group_id = "sg-1"
  ip_protocol       = "tcp"
  from_port         = 22
  to_port           = 22
  cidr_ipv6         = "::/0"
}`, 1},
	},
	"bp_sg_open_cifs": {
		{"smb ipv4", sgWithIngress("0.0.0.0/0", "tcp", 445, 445), 1},
		{"smb ipv6", sgWithIngress("::/0", "tcp", 445, 445), 1},
		{"https", sgWithIngress("0.0.0.0/0", "tcp", 443, 443), 0},
		{"rule resource", `resource "aws_security_group_rule" "r" {
  type              = "ingress"
  security_group_id = "sg-1"
  protocol          = "tcp"
  from_port         = 0
  to_port           = 1000
  cidr_blocks       = ["0.0.0.0/0"]
}`, 1},
	},
	"bp_default_sg_closed": {
		{"unmanaged default group", vpcV, 1},
		{"closed default group", vpcV + `resource "aws_default_security_group" "d" { vpc_id = aws_vpc.v.id }`, 0},
		{"default group with rules", vpcV + `resource "aws_default_security_group" "d" {
  vpc_id = aws_vpc.v.id
  egress {
    protocol    = "-1"
    from_port   = 0
    to_port     = 0
    cidr_blocks = ["0.0.0.0/0"]
  }
}`, 2},
		{"rule added to the default group", vpcV + `resource "aws_default_security_group" "d" { vpc_id = aws_vpc.v.id }
resource "aws_vpc_security_group_ingress_rule" "r" {
  security_group_id = aws_vpc.v.default_security_group_id
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
  cidr_ipv4         = "10.0.0.0/8"
}`, 1},
		{"default vpc", `resource "aws_default_vpc" "d" {}
resource "aws_default_security_group" "d" {}`, 0},
		{"rule on the default group resource", vpcV + `resource "aws_default_security_group" "d" { vpc_id = aws_vpc.v.id }
resource "aws_security_group_rule" "r" {
  type              = "ingress"
  security_group_id = aws_default_security_group.d.id
  protocol          = "tcp"
  from_port         = 443
  to_port           = 443
  cidr_blocks       = ["10.0.0.0/8"]
}`, 1},
		{"rule on another group", vpcV + `resource "aws_default_security_group" "d" { vpc_id = aws_vpc.v.id }
resource "aws_security_group" "app" { vpc_id = aws_vpc.v.id }
resource "aws_security_group_rule" "r" {
  type              = "ingress"
  security_group_id = aws_security_group.app.id
  protocol          = "tcp"
  from_port         = 443
  to_port           = 443
  cidr_blocks       = ["10.0.0.0/8"]
}`, 0},
	},
	"bp_imdsv2": {
		{"unset", `resource "aws_instance" "i" { instance_type = "t3.micro" }`, 1},
		{"optional", `resource "aws_instance" "i" {
  metadata_options { http_tokens = "optional" }
}`, 1},
		{"required", `resource "aws_launch_template" "t" {
  metadata_options { http_tokens = "required" }
}`, 0},
		{"endpoint disabled", `resource "aws_instance" "i" {
  metadata_options { http_endpoint = "disabled" }
}`, 0},
		{"launch template unset", `resource "aws_launch_template" "t" { name = "t" }`, 1},
		{"account default", `resource "aws_ec2_instance_metadata_defaults" "d" { http_tokens = "required" }
resource "aws_instance" "i" { instance_type = "t3.micro" }`, 0},
	},
}

// An explicit http_tokens = "optional" is a warning; an unset one is info
// (the AMI or account may still require IMDSv2).
func TestIMDSv2Severity(t *testing.T) {
	src := `resource "aws_instance" "a" {
  metadata_options { http_tokens = "optional" }
}
resource "aws_instance" "b" { instance_type = "t3.micro" }`
	got := map[string]model.Severity{}
	for _, d := range findings(t, src, "bp_imdsv2") {
		got[d.Message] = d.Severity
	}
	if got[`aws_instance.a allows IMDSv1 (http_tokens = "optional")`] != model.SeverityWarning || got["aws_instance.b doesn't require IMDSv2"] != model.SeverityInfo {
		t.Errorf("severities = %v", got)
	}
}
