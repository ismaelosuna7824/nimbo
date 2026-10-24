package access

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const gcpSrc = `resource "google_service_account" "run" {
  account_id = "run"
}

resource "google_storage_bucket" "reports" {
  name     = "reports"
  location = "EU"
}

resource "google_storage_bucket_iam_member" "public_read" {
  bucket = google_storage_bucket.reports.name
  role   = "roles/storage.objectViewer"
  member = "allUsers"
}

resource "google_storage_bucket_iam_member" "run_read" {
  bucket = google_storage_bucket.reports.name # one bucket
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.run.email}"
}

# Analysts during office hours.
resource "google_storage_bucket_iam_binding" "analysts" {
  bucket = google_storage_bucket.reports.name
  role   = "roles/storage.objectViewer"
  members = [
    "group:${var.analysts}",
    "user:ana@example.com",
  ]

  condition {
    title       = "office-hours"
    description = "Only during office hours"
    expression  = "request.time.getHours('Europe/Madrid') < 18"
  }
}

resource "google_storage_bucket_iam_binding" "computed" {
  bucket  = google_storage_bucket.reports.name
  role    = "roles/storage.objectViewer"
  members = var.members
}

resource "google_project_iam_member" "dynamic_role" {
  project = var.project_id
  role    = var.role
  member  = "serviceAccount:${google_service_account.run.email}"
}

resource "google_project_iam_member" "editor" {
  project = var.project_id
  role    = "roles/editor"
  member  = "serviceAccount:${google_service_account.run.email}"
}

resource "google_project_iam_member" "secrets" {
  project = var.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${google_service_account.run.email}"
}

resource "google_project_iam_member" "act_as" {
  project = var.project_id
  role    = "roles/iam.serviceAccountUser"
  member  = "group:${var.analysts}"
}

resource "google_cloud_run_v2_service" "a" {
  name     = "a"
  location = "europe-west1"
  template {
    containers {
      image = "img"
    }
  }
}

resource "google_cloud_run_v2_service" "b" {
  name     = "b"
  location = "europe-west4"
  template {
    containers {
      image = "img"
    }
  }
}

resource "google_cloud_run_v2_service_iam_member" "invoker" {
  name     = google_cloud_run_v2_service.a.name
  location = google_cloud_run_v2_service.a.location
  role     = "roles/run.invoker"
  member   = "allAuthenticatedUsers"
}

variable "analysts" {
  type = string
}

variable "members" {
  type = list(string)
}

variable "role" {
  type = string
}

variable "project_id" {
  type = string
}
`

func gcpView(t *testing.T, wf *model.Workflow, addr string) *model.AccessView {
	t.Helper()
	v, err := View(wf, iamNode(t, wf, addr))
	if err != nil {
		t.Fatal(err)
	}
	if v.Provider != "gcp" || v.GCP == nil {
		t.Fatalf("view %+v", v)
	}
	return v
}

func TestGCPViewMembers(t *testing.T) {
	wf := iamImport(t, gcpSrc)
	lit := gcpView(t, wf, "google_storage_bucket_iam_member.public_read").GCP
	if lit.Kind != "member" || lit.ResourceAttr != "bucket" || lit.Resource != "google_storage_bucket.reports.name" ||
		lit.Role != "roles/storage.objectViewer" || !slices.Equal(lit.Members, []string{`"allUsers"`}) || !lit.Editable {
		t.Errorf("literal member %+v", lit)
	}
	v := gcpView(t, wf, "google_storage_bucket_iam_member.run_read")
	if !slices.Equal(v.GCP.Members, []string{`"serviceAccount:${google_service_account.run.email}"`}) || len(v.Warnings) != 0 {
		t.Errorf("reference member %+v %+v", v.GCP, v.Warnings)
	}
	if len(v.Related) != 2 || v.Related[0].Address != "google_service_account.run" || v.Related[1].Address != "google_storage_bucket.reports" {
		t.Errorf("related %+v", v.Related)
	}
	b := gcpView(t, wf, "google_storage_bucket_iam_binding.analysts").GCP
	if b.Kind != "binding" || !slices.Equal(b.Members, []string{`"group:${var.analysts}"`, `"user:ana@example.com"`}) || !b.Editable {
		t.Errorf("binding %+v", b)
	}
	if !strings.HasPrefix(b.Condition, "condition {") || !strings.Contains(b.Condition, `expression  = "request.time.getHours('Europe/Madrid') < 18"`) {
		t.Errorf("condition %q", b.Condition)
	}
}

func TestGCPRoundTripKeepsCondition(t *testing.T) {
	wf := iamImport(t, gcpSrc)
	before := iamExport(t, wf)
	b := *gcpView(t, wf, "google_storage_bucket_iam_binding.analysts").GCP
	if got := iamSave(t, wf, "google_storage_bucket_iam_binding.analysts", model.AccessEdit{GCP: &b}); got != before {
		t.Fatalf("unchanged edit rewrote the file:\n%s", got)
	}

	wf = iamImport(t, before)
	b.Members = append(b.Members, `"serviceAccount:${google_service_account.run.email}"`)
	b.Role = "roles/storage.objectUser"
	got := iamSave(t, wf, "google_storage_bucket_iam_binding.analysts", model.AccessEdit{GCP: &b})
	for _, keep := range []string{"# Analysts during office hours.", `title       = "office-hours"`, `expression  = "request.time.getHours('Europe/Madrid') < 18"`,
		`"group:${var.analysts}"`, `"serviceAccount:${google_service_account.run.email}"`, `role   = "roles/storage.objectUser"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("missing %q in\n%s", keep, got)
		}
	}
	// Everything outside the binding is untouched.
	start := strings.Index(before, "# Analysts during office hours.")
	end := strings.Index(before, `resource "google_storage_bucket_iam_binding" "computed"`)
	if got[:start] != before[:start] || !strings.HasSuffix(got, before[end:]) {
		t.Errorf("other blocks changed:\n%s", got)
	}
	after := ReadGCPBinding(iamNode(t, wf, "google_storage_bucket_iam_binding.analysts"))
	if len(after.Members) != 3 || after.Condition != b.Condition {
		t.Errorf("read back %+v", after)
	}

	// The condition is read-only.
	b.Condition = "condition {}"
	n := iamNode(t, wf, "google_storage_bucket_iam_binding.analysts")
	var ee *EditError
	if err := Apply(wf, n, model.AccessEdit{NodeID: n.ID, GCP: &b}); !errors.As(err, &ee) {
		t.Errorf("condition edit: %v", err)
	}
}

func TestGCPMemberEdit(t *testing.T) {
	wf := iamImport(t, gcpSrc)
	m := *gcpView(t, wf, "google_storage_bucket_iam_member.public_read").GCP
	m.Members = []string{`"group:${var.analysts}"`}
	got := iamSave(t, wf, "google_storage_bucket_iam_member.public_read", model.AccessEdit{GCP: &m})
	if !strings.Contains(got, `member = "group:${var.analysts}"`) || strings.Contains(got, `"allUsers"`) {
		t.Errorf("got\n%s", got)
	}
	if ws := gcpView(t, wf, "google_storage_bucket_iam_member.public_read").Warnings; len(ws) != 0 {
		t.Errorf("warnings after fix %+v", ws)
	}
	m.Members = []string{`"user:a@example.com"`, `"user:b@example.com"`}
	n := iamNode(t, wf, "google_storage_bucket_iam_member.public_read")
	var ee *EditError
	if err := Apply(wf, n, model.AccessEdit{NodeID: n.ID, GCP: &m}); !errors.As(err, &ee) {
		t.Errorf("two members on an iam_member: %v", err)
	}
	m.Members = []string{`"user:a@example.com"`}
	m.Role = "storage.objectViewer"
	if err := Apply(wf, n, model.AccessEdit{NodeID: n.ID, GCP: &m}); !errors.As(err, &ee) {
		t.Errorf("bad role: %v", err)
	}
}

func TestGCPResourceChangeRepointsCompanions(t *testing.T) {
	wf := iamImport(t, gcpSrc)
	m := *gcpView(t, wf, "google_cloud_run_v2_service_iam_member.invoker").GCP
	if m.ResourceAttr != "name" {
		t.Fatalf("resource attr %q", m.ResourceAttr)
	}
	m.Resource = "google_cloud_run_v2_service.b.name"
	m.Members = []string{`"serviceAccount:${google_service_account.run.email}"`}
	got := iamSave(t, wf, "google_cloud_run_v2_service_iam_member.invoker", model.AccessEdit{GCP: &m})
	if !strings.Contains(got, "name     = google_cloud_run_v2_service.b.name") || !strings.Contains(got, "location = google_cloud_run_v2_service.b.location") {
		t.Errorf("got\n%s", got)
	}
}

func TestGCPReadOnlyFallback(t *testing.T) {
	wf := iamImport(t, gcpSrc)
	for addr, want := range map[string]string{
		"google_storage_bucket_iam_binding.computed": "var.members",
		"google_project_iam_member.dynamic_role":     "var.role",
	} {
		v := gcpView(t, wf, addr)
		if v.GCP.Editable || !strings.Contains(v.GCP.Reason, want) {
			t.Errorf("%s: %+v", addr, v.GCP)
		}
		before := iamExport(t, wf)
		edit := *v.GCP
		edit.Role = "roles/storage.objectViewer"
		edit.Members = []string{`"user:a@example.com"`}
		n := iamNode(t, wf, addr)
		var ee *EditError
		if err := Apply(wf, n, model.AccessEdit{NodeID: n.ID, GCP: &edit}); !errors.As(err, &ee) {
			t.Errorf("%s: apply = %v", addr, err)
		}
		if iamExport(t, wf) != before {
			t.Errorf("%s: refused edit changed the file", addr)
		}
	}
}

func TestGCPWarnings(t *testing.T) {
	wf := iamImport(t, gcpSrc)
	for addr, want := range map[string][]string{
		"google_storage_bucket_iam_member.public_read":   {"gcp_public_member@members/0"},
		"google_storage_bucket_iam_member.run_read":      {},
		"google_project_iam_member.editor":               {"gcp_basic_role@role"},
		"google_project_iam_member.secrets":              {"gcp_project_scope@scope"},
		"google_project_iam_member.act_as":               {"gcp_service_account_user@role"},
		"google_cloud_run_v2_service_iam_member.invoker": {"gcp_public_member@members/0"},
	} {
		got := iamCodes(gcpView(t, wf, addr).Warnings)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: %v, want %v", addr, got, want)
		}
	}
	ws := gcpView(t, wf, "google_project_iam_member.secrets").Warnings
	if !strings.Contains(ws[0].Message, "every secret in the project") || !strings.Contains(ws[0].Fix, "google_secret_manager_secret_iam_member") {
		t.Errorf("project scope %+v", ws[0])
	}
	if w := gcpView(t, wf, "google_project_iam_member.act_as").Warnings[0]; w.Severity != "info" {
		t.Errorf("service account user severity %+v", w)
	}
	admin := GCPWarnings("google_project_iam_binding", model.GCPIAMBinding{Role: "roles/iam.serviceAccountAdmin"})
	if len(admin) != 1 || admin[0].Code != "gcp_service_account_admin" {
		t.Errorf("admin %+v", admin)
	}
}

func TestGCPResourceAttrFallback(t *testing.T) {
	b := &model.Body{Attrs: []model.Attr{{Name: "project"}, {Name: "location"}, {Name: "service_id"}, {Name: "role"}, {Name: "member"}}}
	if got := GCPResourceAttr("google_some_new_service_iam_member", b); got != "service_id" {
		t.Errorf("fallback = %q", got)
	}
	if got := GCPResourceAttr("google_secret_manager_secret_iam_member", nil); got != "secret_id" {
		t.Errorf("known = %q", got)
	}
}

func TestGCPCatalog(t *testing.T) {
	c, err := Catalog("gcp", CatalogQuery{ResourceType: "google_storage_bucket"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Source.URL != "https://cloud.google.com/iam/docs/roles-permissions" || len(c.Source.Retrieved) != 10 {
		t.Errorf("source %+v", c.Source)
	}
	if len(c.Roles) < 1500 || c.Roles[0].ID != "roles/storage.objectViewer" || c.Roles[0].Suggested != model.AccessRead || c.Roles[0].Description == "" {
		t.Fatalf("%d roles, first %+v", len(c.Roles), c.Roles[0])
	}
	// The IAM type suggests the same.
	if ic, _ := Catalog("gcp", CatalogQuery{ResourceType: "google_storage_bucket_iam_member"}); ic.Roles[0].ID != "roles/storage.objectViewer" {
		t.Errorf("iam type first %+v", ic.Roles[0])
	}
	for _, id := range []string{"roles/owner", "roles/editor", "roles/viewer"} {
		if r, ok := GCPRole(id); !ok || !r.Privileged {
			t.Errorf("%s %+v", id, r)
		}
	}
	if last := c.Roles[len(c.Roles)-1]; !last.Privileged {
		t.Errorf("basic roles are not last: %+v", last)
	}
	for _, tg := range gcpIAMTargets {
		for _, ids := range tg.Roles {
			for _, id := range ids {
				if _, ok := GCPRole(id); !ok {
					t.Errorf("%s suggests unknown role %s", tg.IAM, id)
				}
			}
		}
	}
	s, _ := Catalog("gcp", CatalogQuery{Search: "secretAccessor"})
	if len(s.Roles) == 0 || s.Roles[0].ID != "roles/secretmanager.secretAccessor" {
		t.Errorf("search %+v", s.Roles)
	}
}
