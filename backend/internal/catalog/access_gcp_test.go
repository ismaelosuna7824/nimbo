package catalog_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const gcpChallenges = "20261023120000_gcp_challenges.yaml"

// TestGCPCloudRunLeastPrivilegeChallengeGrant solves the IAM part of
// gcp-cloud-run-least-privilege on the canvas: the service has no
// dedicated service account (no grant is offered), so add one, link
// template.service_account to it, drag the service onto the secret and
// pick Read, and delete the project-level Editor grant.
func TestGCPCloudRunLeastPrivilegeChallengeGrant(t *testing.T) {
	c := cloudCat(t)
	ch, wf := cloudChallenge(t, c, gcpChallenges, "gcp-cloud-run-least-privilege")
	const run, secret = "google_cloud_run_v2_service.orders", "google_secret_manager_secret.db_password"
	if rs := cloudGrants(t, c, wf, run, secret); len(rs) != 0 {
		t.Fatalf("grants offered for a service without a google_service_account: %+v", rs)
	}

	saID, err := c.AddNode(wf, model.KindResource, "google_service_account", "orders", model.Position{X: 400, Y: 200})
	if err != nil {
		t.Fatal(err)
	}
	var sa string
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == saID {
			sa = hclcodec.Address(&wf.Nodes[i])
		}
	}
	tpl := access.Blocks(&cloudNode(t, wf, run).Body, "template")[0]
	access.SetExpr(&tpl.Body, "service_account", sa+".email")
	hclcodec.Normalize(wf)

	rs := cloudGrants(t, c, wf, run, secret)
	if len(rs) != 3 {
		t.Fatalf("grants %+v", rs)
	}
	wantRead := `Creates google_secret_manager_secret_iam_member.orders_db_password_read: secret_id = google_secret_manager_secret.db_password.id, ` +
		`role = "roles/secretmanager.secretAccessor", member = "serviceAccount:${` + sa + `.email}" (the service account google_cloud_run_v2_service.orders runs as). ` +
		`Secret Manager Secret Accessor: Allows accessing the payload of secrets.`
	if rs[0].Description != wantRead {
		t.Errorf("read preview\n%s\nwant\n%s", rs[0].Description, wantRead)
	}
	if !strings.Contains(rs[1].Description, `"roles/secretmanager.secretVersionAdder"`) || !strings.Contains(rs[2].Description, `"roles/secretmanager.admin"`) {
		t.Errorf("write/manage %q / %q", rs[1].Description, rs[2].Description)
	}
	// The service account itself offers the same grant.
	if direct := cloudGrants(t, c, wf, sa, secret); len(direct) != 3 || !strings.Contains(direct[0].Description, "roles/secretmanager.secretAccessor") {
		t.Errorf("service account grants %+v", direct)
	}
	cloudGrant(t, c, wf, run, secret, model.AccessRead)

	// The Access section of the old grant flags Editor; delete it.
	editor := cloudNode(t, wf, "google_project_iam_member.runtime_editor")
	v, err := access.View(wf, editor)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Warnings) != 1 || v.Warnings[0].Code != "gcp_basic_role" {
		t.Errorf("editor warnings %+v", v.Warnings)
	}
	wf.Nodes = slices.DeleteFunc(wf.Nodes, func(n model.Node) bool { return n.ID == editor.ID })
	hclcodec.Normalize(wf)

	passed, failed := cloudGrade(t, c, ch, wf)
	for _, id := range []string{"runtime-sa", "no-basic-roles", "secret-scoped", "validate"} {
		if !slices.Contains(passed, id) {
			t.Errorf("check %s fails (failed: %v)", id, failed)
		}
	}
	t.Logf("gcp-cloud-run-least-privilege after the grant: passed %v, still failing %v", passed, failed)
}

// TestGCPStoragePrivateBucketChallengeEditor fixes the public grant of
// gcp-storage-private-bucket in the GCP editor: the member becomes the
// analysts' group.
func TestGCPStoragePrivateBucketChallengeEditor(t *testing.T) {
	c := cloudCat(t)
	ch, wf := cloudChallenge(t, c, gcpChallenges, "gcp-storage-private-bucket")
	n := cloudNode(t, wf, "google_storage_bucket_iam_member.public_read")
	v, err := access.View(wf, n)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Warnings) != 1 || v.Warnings[0].Code != "gcp_public_member" {
		t.Fatalf("warnings %+v", v.Warnings)
	}
	wf.Variables = append(wf.Variables, model.NamedBlock{Name: "analysts_group", Body: model.Body{Attrs: []model.Attr{
		{Name: "type", Value: model.Value{Expr: "string"}},
		{Name: "description", Value: model.Value{Expr: `"Email of the analysts' Google group."`}},
	}, Blocks: []model.Block{}}})
	b := *v.GCP
	b.Members = []string{`"group:${var.analysts_group}"`}
	if err := access.Apply(wf, n, model.AccessEdit{NodeID: n.ID, GCP: &b}); err != nil {
		t.Fatal(err)
	}
	hclcodec.Normalize(wf)
	if v, _ = access.View(wf, n); len(v.Warnings) != 0 {
		t.Errorf("warnings after the edit %+v", v.Warnings)
	}
	passed, failed := cloudGrade(t, c, ch, wf)
	for _, id := range []string{"no-allusers", "group-reader", "no-public", "validate"} {
		if !slices.Contains(passed, id) {
			t.Errorf("check %s fails (failed: %v)", id, failed)
		}
	}
	t.Logf("gcp-storage-private-bucket after the edit: passed %v, still failing %v", passed, failed)
}

const gcpGrantSrc = `data "google_project" "current" {}

resource "google_service_account" "etl" {
  account_id = "etl"
}

resource "google_storage_bucket" "raw" {
  name     = "raw"
  location = "EU"
}

resource "google_pubsub_subscription" "jobs" {
  name  = "jobs"
  topic = "projects/p/topics/jobs"
}

resource "google_cloud_run_v2_service" "api" {
  name     = "api"
  location = "europe-west1"
  template {
    containers {
      image = "img"
    }
  }
}

resource "google_compute_instance" "vm" {
  name         = "vm"
  machine_type = "e2-small"
  zone         = "europe-west1-b"

  boot_disk {
    initialize_params {
      image = "debian-cloud/debian-12"
    }
  }

  network_interface {
    network = "default"
  }

  service_account {
    email  = google_service_account.etl.email
    scopes = ["cloud-platform"]
  }
}

resource "google_storage_bucket_iam_binding" "readers" {
  bucket  = google_storage_bucket.raw.name
  role    = "roles/storage.objectViewer"
  members = ["serviceAccount:${google_service_account.etl.email}"]
}
`

func TestGCPGrantBucketLevels(t *testing.T) {
	c := cloudCat(t)
	wf := cloudImport(t, gcpGrantSrc)
	rs := cloudGrants(t, c, wf, "google_service_account.etl", "google_storage_bucket.raw")
	if len(rs) != 3 {
		t.Fatalf("grants %+v", rs)
	}
	// Read is already granted by the binding.
	if want := "Nothing to write: google_storage_bucket_iam_binding.readers already grants roles/storage.objectViewer on google_storage_bucket.raw to google_service_account.etl."; rs[0].Description != want {
		t.Errorf("read %q", rs[0].Description)
	}
	wantWrite := `Creates google_storage_bucket_iam_member.etl_raw_write: bucket = google_storage_bucket.raw.name, role = "roles/storage.objectUser", member = "serviceAccount:${google_service_account.etl.email}". Storage Object User:`
	if !strings.HasPrefix(rs[1].Description, wantWrite) {
		t.Errorf("write %q", rs[1].Description)
	}
	if !strings.Contains(rs[2].Description, `role = "roles/storage.objectAdmin"`) {
		t.Errorf("manage %q", rs[2].Description)
	}
	n := len(wf.Nodes)
	cloudGrant(t, c, wf, "google_service_account.etl", "google_storage_bucket.raw", model.AccessRead)
	if len(wf.Nodes) != n {
		t.Errorf("duplicate read wrote a node")
	}
	cloudGrant(t, c, wf, "google_storage_bucket.raw", "google_service_account.etl", model.AccessWrite)
	m := cloudNode(t, wf, "google_storage_bucket_iam_member.etl_raw_write")
	if cloudAttr(t, m, "bucket") != "google_storage_bucket.raw.name" || cloudAttr(t, m, "role") != `"roles/storage.objectUser"` ||
		cloudAttr(t, m, "member") != `"serviceAccount:${google_service_account.etl.email}"` {
		t.Errorf("member %+v", m.Body.Attrs)
	}
	v, err := access.View(wf, m)
	if err != nil {
		t.Fatal(err)
	}
	if !v.GCP.Editable || v.GCP.Role != "roles/storage.objectUser" || len(v.Warnings) != 0 {
		t.Errorf("view %+v %+v", v.GCP, v.Warnings)
	}
}

func TestGCPGrantWorkloadsAndTargets(t *testing.T) {
	c := cloudCat(t)
	wf := cloudImport(t, gcpGrantSrc)
	// The VM runs as google_service_account.etl.
	sub := cloudGrant(t, c, wf, "google_compute_instance.vm", "google_pubsub_subscription.jobs", model.AccessRead)
	if !strings.HasPrefix(sub.Description, `Creates google_pubsub_subscription_iam_member.vm_jobs_read: subscription = google_pubsub_subscription.jobs.id, role = "roles/pubsub.subscriber", member = "serviceAccount:${google_service_account.etl.email}" (the service account google_compute_instance.vm runs as).`) {
		t.Errorf("subscription %q", sub.Description)
	}
	inv := cloudGrant(t, c, wf, "google_service_account.etl", "google_cloud_run_v2_service.api", model.AccessRead)
	if !strings.HasPrefix(inv.Description, `Creates google_cloud_run_v2_service_iam_member.etl_api_read: name = google_cloud_run_v2_service.api.name, location = google_cloud_run_v2_service.api.location, project = google_cloud_run_v2_service.api.project, role = "roles/run.invoker"`) {
		t.Errorf("invoker %q", inv.Description)
	}
	// The API service has no service account of its own: nothing to grant from it.
	if rs := cloudGrants(t, c, wf, "google_cloud_run_v2_service.api", "google_storage_bucket.raw"); len(rs) != 0 {
		t.Errorf("grants from a service without a service account %+v", rs)
	}
	// Project grants are basic roles, with a warning.
	proj := cloudGrants(t, c, wf, "google_service_account.etl", "data.google_project.current")
	if len(proj) != 3 || !strings.Contains(proj[1].Description, `project = data.google_project.current.project_id, role = "roles/editor"`) ||
		!strings.Contains(proj[1].Description, "Warning: roles/editor is a basic role on the whole project") {
		t.Errorf("project grants %+v", proj)
	}
}
