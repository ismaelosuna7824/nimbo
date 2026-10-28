package compliance_test

import (
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compliance"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The CIS AWS pack grades a real design through the real rules, opt-in
// ones included, each control on its own resource types.
func TestCISAWSGradesADesign(t *testing.T) {
	src := `
resource "aws_vpc" "v" { cidr_block = "10.0.0.0/16" }
resource "aws_security_group" "admin" {
  vpc_id = aws_vpc.v.id
  ingress {
    protocol    = "tcp"
    from_port   = 22
    to_port     = 22
    cidr_blocks = ["0.0.0.0/0"]
  }
}
resource "aws_db_instance" "db" {
  engine              = "postgres"
  instance_class      = "db.t3.micro"
  storage_encrypted   = true
  publicly_accessible = true
}
resource "aws_s3_bucket" "logs" { bucket = "logs" }
resource "aws_s3_bucket_public_access_block" "logs" {
  bucket                  = aws_s3_bucket.logs.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}
resource "aws_cloudtrail" "t" {
  name                       = "t"
  s3_bucket_name             = aws_s3_bucket.logs.id
  enable_log_file_validation = true
}
resource "aws_iam_role" "ops" {
  name               = "ops"
  assume_role_policy = "{}"
}
resource "aws_iam_role_policy_attachment" "ops" {
  role       = aws_iam_role.ops.name
  policy_arn = "arn:aws:iam::aws:policy/AdministratorAccess"
}
`
	wf, _, err := hclcodec.Import("aws", map[string][]byte{"main.tf": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	hclcodec.Normalize(wf)
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	fw, ok := compliance.Lookup("cis-aws-7.0")
	if !ok {
		t.Fatal("cis-aws-7.0 is not registered")
	}
	cfg := compliance.GradingSettings(model.BestPracticeSettings{}, fw)
	r := compliance.Evaluate(fw, wf, lint.Check(wf, cat, cfg, nil), cfg, nil)
	want := map[string]compliance.Status{
		"2.5":   compliance.StatusManual,
		"2.8":   compliance.StatusNotApplicable, // no password policy in the design
		"2.14":  compliance.StatusViolated,      // AdministratorAccess attached to the ops role
		"3.1.1": compliance.StatusViolated,      // no deny-HTTP bucket policy (opt-in rule)
		"3.1.4": compliance.StatusMet,
		"3.2.1": compliance.StatusMet, // storage_encrypted
		"3.2.3": compliance.StatusViolated,
		"3.2.4": compliance.StatusViolated, // single AZ (opt-in rule)
		"4.1":   compliance.StatusViolated, // single-region trail
		"4.2":   compliance.StatusMet,
		"4.4":   compliance.StatusViolated, // trail bucket without access logging
		"4.7":   compliance.StatusViolated, // no flow log (opt-in rule)
		"6.1.1": compliance.StatusNotApplicable,
		"6.1.2": compliance.StatusMet,
		"6.3":   compliance.StatusViolated, // SSH open to 0.0.0.0/0 (opt-in rule)
		"6.4":   compliance.StatusMet,      // nothing open to ::/0
		"6.5":   compliance.StatusViolated, // default security group left as created (opt-in rule)
	}
	got := map[string]compliance.Status{}
	for _, c := range r.Controls {
		got[c.ID] = c.Status
	}
	for id, status := range want {
		if got[id] != status {
			t.Errorf("control %s = %q, want %q", id, got[id], status)
		}
	}
}
