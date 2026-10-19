package catalog

import (
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func localExpr(wf *model.Workflow, name string) string {
	for _, l := range wf.Locals {
		if l.Name == name {
			return valueSource(l.Value)
		}
	}
	return ""
}

func TestAddNodeConventionsFreshAWS(t *testing.T) {
	c := mustLoad(t)
	wf := &model.Workflow{ID: "wf", Name: "Orders API (v2)"}
	addNode(t, c, wf, "aws_s3_bucket", 0)

	if got := variableDefault(t, wf, "project"); got != `"orders-api-v2"` {
		t.Errorf("project default = %s", got)
	}
	if got := variableDefault(t, wf, "environment"); got != `"dev"` {
		t.Errorf("environment default = %s", got)
	}
	for _, v := range wf.Variables {
		if (v.Name == "project" || v.Name == "environment") && (getAttr(&v.Body, "description") == nil || getBlock(&v.Body, "validation") == nil) {
			t.Errorf("variable %s lacks description or validation: %+v", v.Name, v.Body)
		}
	}
	if got := localExpr(wf, "name_prefix"); got != `"${var.project}-${var.environment}"` {
		t.Errorf("name_prefix = %q", got)
	}
	if got := localExpr(wf, "common_tags"); !strings.Contains(got, `ManagedBy   = "nimbo"`) || !strings.Contains(got, "Project     = var.project") {
		t.Errorf("common_tags = %q", got)
	}
	tags := getBlock(&wf.Providers[0].Body, "default_tags")
	if tags == nil || valueSource(getAttr(&tags.Body, "tags").Value) != "local.common_tags" {
		t.Errorf("aws provider default_tags = %+v", wf.Providers[0].Body)
	}
	if a := getAttr(wf.Terraform, "required_version"); a == nil || valueSource(a.Value) != `">= 1.6"` {
		t.Errorf("required_version = %+v", a)
	}

	bucket := nodeByAddr(t, wf, "aws_s3_bucket.bucket")
	if got := attrExpr(t, bucket, "bucket"); !strings.HasPrefix(got, `"${local.name_prefix}-bucket-`) {
		t.Errorf("bucket name = %s", got)
	}
	if getAttr(&bucket.Body, "tags") != nil {
		t.Error("bucket tagged per resource although the provider has default_tags")
	}
	sse := nodeByAddr(t, wf, "aws_s3_bucket_server_side_encryption_configuration.bucket_encryption")
	if attrExpr(t, sse, "bucket") != "aws_s3_bucket.bucket.id" || attrExpr(t, sse, "rule", "apply_server_side_encryption_by_default", "sse_algorithm") != `"AES256"` {
		t.Errorf("encryption = %+v", sse.Body)
	}
	pab := nodeByAddr(t, wf, "aws_s3_bucket_public_access_block.bucket_public_access")
	for _, flag := range []string{"block_public_acls", "block_public_policy", "ignore_public_acls", "restrict_public_buckets"} {
		if attrExpr(t, pab, flag) != "true" {
			t.Errorf("%s = %s", flag, attrExpr(t, pab, flag))
		}
	}

	// A second resource of the same cloud adds nothing new.
	vars, locals := len(wf.Variables), len(wf.Locals)
	addNode(t, c, wf, "aws_sqs_queue", 0)
	if len(wf.Variables) != vars || len(wf.Locals) != locals {
		t.Errorf("variables %d -> %d, locals %d -> %d", vars, len(wf.Variables), locals, len(wf.Locals))
	}
	if got := attrExpr(t, nodeByAddr(t, wf, "aws_sqs_queue.queue"), "name"); got != `"${local.name_prefix}-queue"` {
		t.Errorf("queue name = %s", got)
	}
}

func TestAddNodeConventionsAzureGCP(t *testing.T) {
	c := mustLoad(t)
	wf := newWorkflow()
	addNode(t, c, wf, "azurerm_storage_account", 0) // adds a resource group
	rg := nodeByAddr(t, wf, "azurerm_resource_group.rg")
	if attrExpr(t, rg, "location") != "var.location" || attrExpr(t, rg, "tags") != "local.common_tags" {
		t.Errorf("resource group = %+v", rg.Body.Attrs)
	}
	if got := attrExpr(t, rg, "name"); got != `"${local.name_prefix}-rg"` {
		t.Errorf("resource group name = %s", got)
	}
	if got := variableDefault(t, wf, "location"); got != `"eastus"` {
		t.Errorf("location default = %s", got)
	}
	sa := nodeByAddr(t, wf, "azurerm_storage_account.storage")
	if attrExpr(t, sa, "tags") != "local.common_tags" {
		t.Errorf("storage account tags missing: %+v", sa.Body.Attrs)
	}

	addNode(t, c, wf, "google_pubsub_topic", 0)
	if !strings.Contains(localExpr(wf, "common_labels"), `managed_by  = "nimbo"`) {
		t.Errorf("common_labels = %q", localExpr(wf, "common_labels"))
	}
	topic := nodeByAddr(t, wf, "google_pubsub_topic.topic")
	if attrExpr(t, topic, "labels") != "local.common_labels" {
		t.Errorf("topic labels missing: %+v", topic.Body.Attrs)
	}
	var google *model.NamedBlock
	for i := range wf.Providers {
		if wf.Providers[i].Name == "google" {
			google = &wf.Providers[i]
		}
	}
	if google == nil || valueSource(getAttr(&google.Body, "region").Value) != "var.gcp_region" {
		t.Errorf("google provider = %+v", google)
	}
	run := addNode(t, c, wf, "google_cloud_run_v2_service", 0)
	if attrExpr(t, run, "location") != "var.gcp_region" {
		t.Errorf("cloud run location = %s", attrExpr(t, run, "location"))
	}
}

// Imported projects keep their own scheme: no conventions are added to a
// cloud that already has a provider block, and names stay plain.
func TestAddNodeKeepsImportedScheme(t *testing.T) {
	c := mustLoad(t)
	wf := newWorkflow()
	wf.Providers = []model.NamedBlock{{Name: "aws", Body: model.Body{Attrs: []model.Attr{{Name: "region", Value: lit("eu-west-1")}}}}}
	q := addNode(t, c, wf, "aws_sqs_queue", 0)
	if len(wf.Variables) != 0 || len(wf.Locals) != 0 {
		t.Errorf("conventions added to an imported AWS project: vars %+v locals %+v", wf.Variables, wf.Locals)
	}
	if got := attrExpr(t, q, "name"); got != `"queue"` {
		t.Errorf("queue name = %s", got)
	}
	if getAttr(&q.Body, "tags") != nil {
		t.Error("tags set without local.common_tags")
	}

	// With a common_tags local but no default_tags, AWS resources are
	// tagged one by one.
	wf.Locals = []model.Attr{{Name: "common_tags", Value: expr("{ Team = \"x\" }")}}
	q2 := addNode(t, c, wf, "aws_sqs_queue", 0)
	if attrExpr(t, q2, "tags") != "local.common_tags" {
		t.Errorf("queue tags = %+v", q2.Body.Attrs)
	}
}

func TestProjectSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Orders API (v2)":                  "orders-api-v2",
		"  2024 report ":                   "report",
		"x":                                "app",
		"Ünïcode wörkflow":                 "n-code-w-rkflow",
		"a very long workflow name indeed": "a-very-long-workflow",
	} {
		if got := projectSlug(in); got != want {
			t.Errorf("projectSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
