package catalog

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func connectRecipe(t *testing.T, c *Catalog, wf *model.Workflow, src, dst, id string) {
	t.Helper()
	a, b := nodeByAddr(t, wf, src), nodeByAddr(t, wf, dst)
	if !slices.ContainsFunc(c.Connections(wf, a.ID, b.ID), func(r model.ConnectionRecipe) bool { return r.ID == id }) {
		t.Fatalf("%s not offered for %s -> %s", id, src, dst)
	}
	if err := c.Connect(wf, a.ID, b.ID, id); err != nil {
		t.Fatal(err)
	}
	for _, d := range hclcodec.Normalize(wf) {
		if d.Severity == model.SeverityError {
			t.Fatalf("after %s: %+v", id, d)
		}
	}
}

func attrSrc(t *testing.T, wf *model.Workflow, addr, name string) string {
	t.Helper()
	a := getAttr(&nodeByAddr(t, wf, addr).Body, name)
	if a == nil {
		return ""
	}
	return valueSource(a.Value)
}

func TestS3RequireTLSRecipe(t *testing.T) {
	c := mustLoad(t)
	wf := importWF(t, `
resource "aws_s3_bucket" "data" {}
resource "aws_s3_bucket_public_access_block" "data" {
  bucket = aws_s3_bucket.data.id
}
resource "aws_s3_bucket_policy" "tls" {}
`)
	// Either drag direction offers it.
	connectRecipe(t, c, wf, "aws_s3_bucket.data", "aws_s3_bucket_policy.tls", "s3-require-tls")
	if got := attrSrc(t, wf, "aws_s3_bucket_policy.tls", "bucket"); got != "aws_s3_bucket.data.id" {
		t.Errorf("bucket = %s", got)
	}
	if got := attrSrc(t, wf, "aws_s3_bucket_policy.tls", "depends_on"); !strings.Contains(got, "aws_s3_bucket_public_access_block.data") {
		t.Errorf("depends_on = %s", got)
	}
	d := policyDoc(t, wf, "aws_s3_bucket_policy.tls", "policy")
	if d.Encoding != "jsonencode" || len(d.Statements) != 1 {
		t.Fatalf("doc = %+v", d)
	}
	s := d.Statements[0]
	if s.Sid != "DenyInsecureTransport" || s.Effect != "Deny" || !slices.Equal(s.Actions, []string{"s3:*"}) ||
		!slices.Equal(s.Resources, []string{"aws_s3_bucket.data.arn", `"${aws_s3_bucket.data.arn}/*"`}) ||
		len(s.Principals) != 1 || s.Principals[0].Type != "*" ||
		len(s.Extra) != 1 || s.Extra[0].Key != "Condition" || !strings.Contains(s.Extra[0].Source, `"aws:SecureTransport" = "false"`) {
		t.Fatalf("statement = %+v", s)
	}
	// Applying it again adds nothing.
	connectRecipe(t, c, wf, "aws_s3_bucket_policy.tls", "aws_s3_bucket.data", "s3-require-tls")
	if d := policyDoc(t, wf, "aws_s3_bucket_policy.tls", "policy"); len(d.Statements) != 1 {
		t.Fatalf("statements after reapply = %+v", d.Statements)
	}
}

func TestS3RequireTLSRecipeJSONPolicy(t *testing.T) {
	c := mustLoad(t)
	wf := importWF(t, `
resource "aws_s3_bucket" "data" {}
resource "aws_s3_bucket_policy" "p" {
  bucket = aws_s3_bucket.data.id
  policy = <<-EOT
    {
      "Version": "2012-10-17",
      "Statement": [
        {"Sid": "Read", "Effect": "Allow", "Principal": {"AWS": "arn:aws:iam::123456789012:root"}, "Action": "s3:GetObject", "Resource": "${aws_s3_bucket.data.arn}/*"}
      ]
    }
  EOT
}
`)
	connectRecipe(t, c, wf, "aws_s3_bucket_policy.p", "aws_s3_bucket.data", "s3-require-tls")
	pol := attrSrc(t, wf, "aws_s3_bucket_policy.p", "policy")
	if !strings.HasPrefix(pol, "<<-EOT") || !strings.Contains(pol, `"aws:SecureTransport": "false"`) || !strings.Contains(pol, `"Sid": "Read"`) {
		t.Fatalf("policy:\n%s", pol)
	}
}

func TestCloudFrontS3OACRecipe(t *testing.T) {
	c := mustLoad(t)
	wf := importWF(t, `
resource "aws_s3_bucket" "site" {}
resource "aws_s3_bucket_public_access_block" "site" {
  bucket = aws_s3_bucket.site.id
}
resource "aws_cloudfront_distribution" "site" {
  enabled = true
  origin {
    domain_name = ""
    origin_id   = ""
  }
  default_cache_behavior {
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    target_origin_id       = ""
    viewer_protocol_policy = "redirect-to-https"
  }
}
`)
	connectRecipe(t, c, wf, "aws_cloudfront_distribution.site", "aws_s3_bucket.site", "cloudfront-s3-oac")
	oac := nodeByAddr(t, wf, "aws_cloudfront_origin_access_control.site")
	for name, want := range map[string]string{"origin_access_control_origin_type": `"s3"`, "signing_behavior": `"always"`, "signing_protocol": `"sigv4"`} {
		if a := getAttr(&oac.Body, name); a == nil || valueSource(a.Value) != want {
			t.Errorf("oac %s = %+v", name, a)
		}
	}
	dist := nodeByAddr(t, wf, "aws_cloudfront_distribution.site")
	var origins []model.Block
	for _, b := range dist.Body.Blocks {
		if b.Type == "origin" {
			origins = append(origins, b)
		}
	}
	if len(origins) != 1 {
		t.Fatalf("origins = %+v", origins)
	}
	o := origins[0].Body
	if valueSource(getAttr(&o, "domain_name").Value) != "aws_s3_bucket.site.bucket_regional_domain_name" ||
		valueSource(getAttr(&o, "origin_access_control_id").Value) != "aws_cloudfront_origin_access_control.site.id" ||
		valueSource(getAttr(&o, "origin_id").Value) != `"s3-site"` {
		t.Fatalf("origin = %+v", o.Attrs)
	}
	dcb := getBlock(&dist.Body, "default_cache_behavior")
	if got := valueSource(getAttr(&dcb.Body, "target_origin_id").Value); got != `"s3-site"` {
		t.Errorf("target_origin_id = %s", got)
	}
	d := policyDoc(t, wf, "aws_s3_bucket_policy.site_policy", "policy")
	if len(d.Statements) != 1 {
		t.Fatalf("doc = %+v", d)
	}
	s := d.Statements[0]
	if s.Effect != "Allow" || !slices.Equal(s.Actions, []string{"s3:GetObject"}) || s.Principals[0].Type != "Service" ||
		s.Principals[0].Values[0] != `"cloudfront.amazonaws.com"` ||
		!strings.Contains(s.Extra[0].Source, `"AWS:SourceArn" = aws_cloudfront_distribution.site.arn`) {
		t.Fatalf("statement = %+v", s)
	}
	if got := attrSrc(t, wf, "aws_s3_bucket_policy.site_policy", "depends_on"); !strings.Contains(got, "aws_s3_bucket_public_access_block.site") {
		t.Errorf("depends_on = %s", got)
	}
}
