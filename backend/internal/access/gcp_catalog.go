package access

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

//go:generate go run ./gen/gcp

// gcpRolesGzip is the pinned list of Google Cloud basic and predefined
// roles (gen/gcp): ID, title, description and service of every role of
// the official IAM roles and permissions index. Basic roles are
// Privileged.
//
//go:embed data/gcp_roles.json.gz
var gcpRolesGzip []byte

type gcpReference struct {
	Source model.AccessSource `json:"source"`
	Roles  []model.AccessRole `json:"roles"`
	byID   map[string]int
}

var gcpRef = sync.OnceValues(func() (*gcpReference, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gcpRolesGzip))
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	var r gcpReference
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	r.byID = make(map[string]int, len(r.Roles))
	for i, role := range r.Roles {
		r.byID[role.ID] = i
	}
	return &r, nil
})

func init() { RegisterCatalog("gcp", gcpCatalog) }

// GCPIAMTarget is a resource type with resource-level IAM
// (google_<x>_iam_member / _binding / _policy).
type GCPIAMTarget struct {
	// ResourceType is the Terraform type of the resource ("" when the
	// IAM resource targets something Nimbo doesn't model as a node).
	ResourceType string
	// IAM is the IAM type prefix ("google_storage_bucket_iam").
	IAM string
	// Attr is the IAM resource's attribute naming the resource ("bucket").
	Attr string
	// Refs are the IAM attributes a grant sets and the resource attribute
	// each one reads, Attr first ({"bucket", "name"}).
	Refs [][2]string
	// Roles per level (Read, Write, Manage), least privilege first; the
	// first role of a level is the one a grant writes.
	Roles [3][]string
	// Project marks project, folder and organization IAM: every role
	// applies to all resources below.
	Project bool
}

// gcpIAMTargets follow each service's "Access control with IAM" page on
// cloud.google.com for the least-privilege role of a level.
var gcpIAMTargets = []GCPIAMTarget{
	{ResourceType: "google_storage_bucket", IAM: "google_storage_bucket_iam", Attr: "bucket",
		Refs:  [][2]string{{"bucket", "name"}},
		Roles: [3][]string{{"roles/storage.objectViewer", "roles/storage.legacyBucketReader"}, {"roles/storage.objectUser", "roles/storage.objectCreator"}, {"roles/storage.objectAdmin", "roles/storage.admin"}}},
	{ResourceType: "google_secret_manager_secret", IAM: "google_secret_manager_secret_iam", Attr: "secret_id",
		Refs:  [][2]string{{"secret_id", "id"}},
		Roles: [3][]string{{"roles/secretmanager.secretAccessor", "roles/secretmanager.viewer"}, {"roles/secretmanager.secretVersionAdder", "roles/secretmanager.secretVersionManager"}, {"roles/secretmanager.admin"}}},
	{ResourceType: "google_pubsub_topic", IAM: "google_pubsub_topic_iam", Attr: "topic",
		Refs:  [][2]string{{"topic", "id"}},
		Roles: [3][]string{{"roles/pubsub.viewer"}, {"roles/pubsub.publisher"}, {"roles/pubsub.admin"}}},
	{ResourceType: "google_pubsub_subscription", IAM: "google_pubsub_subscription_iam", Attr: "subscription",
		Refs:  [][2]string{{"subscription", "id"}},
		Roles: [3][]string{{"roles/pubsub.subscriber", "roles/pubsub.viewer"}, {"roles/pubsub.editor"}, {"roles/pubsub.admin"}}},
	{ResourceType: "google_cloud_run_v2_service", IAM: "google_cloud_run_v2_service_iam", Attr: "name",
		Refs:  [][2]string{{"name", "name"}, {"location", "location"}, {"project", "project"}},
		Roles: [3][]string{{"roles/run.invoker", "roles/run.viewer"}, {"roles/run.developer"}, {"roles/run.admin"}}},
	{ResourceType: "google_cloud_run_v2_job", IAM: "google_cloud_run_v2_job_iam", Attr: "name",
		Refs:  [][2]string{{"name", "name"}, {"location", "location"}, {"project", "project"}},
		Roles: [3][]string{{"roles/run.invoker", "roles/run.viewer"}, {"roles/run.developer"}, {"roles/run.admin"}}},
	{ResourceType: "google_cloud_run_service", IAM: "google_cloud_run_service_iam", Attr: "service",
		Refs:  [][2]string{{"service", "name"}, {"location", "location"}, {"project", "project"}},
		Roles: [3][]string{{"roles/run.invoker", "roles/run.viewer"}, {"roles/run.developer"}, {"roles/run.admin"}}},
	{ResourceType: "google_cloudfunctions2_function", IAM: "google_cloudfunctions2_function_iam", Attr: "cloud_function",
		Refs:  [][2]string{{"cloud_function", "name"}, {"location", "location"}, {"project", "project"}},
		Roles: [3][]string{{"roles/cloudfunctions.invoker", "roles/cloudfunctions.viewer"}, {"roles/cloudfunctions.developer"}, {"roles/cloudfunctions.admin"}}},
	{ResourceType: "google_cloudfunctions_function", IAM: "google_cloudfunctions_function_iam", Attr: "cloud_function",
		Refs:  [][2]string{{"cloud_function", "name"}, {"region", "region"}, {"project", "project"}},
		Roles: [3][]string{{"roles/cloudfunctions.invoker", "roles/cloudfunctions.viewer"}, {"roles/cloudfunctions.developer"}, {"roles/cloudfunctions.admin"}}},
	{ResourceType: "google_bigquery_dataset", IAM: "google_bigquery_dataset_iam", Attr: "dataset_id",
		Refs:  [][2]string{{"dataset_id", "dataset_id"}, {"project", "project"}},
		Roles: [3][]string{{"roles/bigquery.dataViewer", "roles/bigquery.metadataViewer"}, {"roles/bigquery.dataEditor"}, {"roles/bigquery.dataOwner"}}},
	{ResourceType: "google_bigquery_table", IAM: "google_bigquery_table_iam", Attr: "table_id",
		Refs:  [][2]string{{"table_id", "table_id"}, {"dataset_id", "dataset_id"}, {"project", "project"}},
		Roles: [3][]string{{"roles/bigquery.dataViewer"}, {"roles/bigquery.dataEditor"}, {"roles/bigquery.dataOwner"}}},
	{ResourceType: "google_artifact_registry_repository", IAM: "google_artifact_registry_repository_iam", Attr: "repository",
		Refs:  [][2]string{{"repository", "name"}, {"location", "location"}, {"project", "project"}},
		Roles: [3][]string{{"roles/artifactregistry.reader"}, {"roles/artifactregistry.writer", "roles/artifactregistry.createOnPushWriter"}, {"roles/artifactregistry.repoAdmin", "roles/artifactregistry.admin"}}},
	{ResourceType: "google_kms_crypto_key", IAM: "google_kms_crypto_key_iam", Attr: "crypto_key_id",
		Refs:  [][2]string{{"crypto_key_id", "id"}},
		Roles: [3][]string{{"roles/cloudkms.cryptoKeyDecrypter", "roles/cloudkms.cryptoKeyEncrypter", "roles/cloudkms.viewer"}, {"roles/cloudkms.cryptoKeyEncrypterDecrypter"}, {"roles/cloudkms.admin"}}},
	{ResourceType: "google_kms_key_ring", IAM: "google_kms_key_ring_iam", Attr: "key_ring_id",
		Refs:  [][2]string{{"key_ring_id", "id"}},
		Roles: [3][]string{{"roles/cloudkms.cryptoKeyDecrypter", "roles/cloudkms.viewer"}, {"roles/cloudkms.cryptoKeyEncrypterDecrypter"}, {"roles/cloudkms.admin"}}},
	{ResourceType: "google_spanner_database", IAM: "google_spanner_database_iam", Attr: "database",
		Refs:  [][2]string{{"database", "name"}, {"instance", "instance"}, {"project", "project"}},
		Roles: [3][]string{{"roles/spanner.databaseReader"}, {"roles/spanner.databaseUser"}, {"roles/spanner.databaseAdmin"}}},
	{ResourceType: "google_project", IAM: "google_project_iam", Attr: "project", Project: true,
		Refs:  [][2]string{{"project", "project_id"}},
		Roles: [3][]string{{"roles/viewer"}, {"roles/editor"}, {"roles/owner"}}},
	{ResourceType: "google_folder", IAM: "google_folder_iam", Attr: "folder", Project: true,
		Refs:  [][2]string{{"folder", "name"}},
		Roles: [3][]string{{"roles/viewer"}, {"roles/editor"}, {"roles/owner"}}},
	{IAM: "google_organization_iam", Attr: "org_id", Project: true},
	{IAM: "google_service_account_iam", Attr: "service_account_id", ResourceType: "google_service_account",
		Roles: [3][]string{{"roles/iam.serviceAccountUser"}, {"roles/iam.serviceAccountTokenCreator"}, {"roles/iam.serviceAccountAdmin"}}},
	{IAM: "google_storage_managed_folder_iam", Attr: "managed_folder", ResourceType: "google_storage_managed_folder"},
	{IAM: "google_compute_instance_iam", Attr: "instance_name", ResourceType: "google_compute_instance"},
	{IAM: "google_compute_subnetwork_iam", Attr: "subnetwork", ResourceType: "google_compute_subnetwork"},
	{IAM: "google_spanner_instance_iam", Attr: "instance", ResourceType: "google_spanner_instance"},
	{IAM: "google_billing_account_iam", Attr: "billing_account_id"},
}

// GCPIAMTargetFor is the IAM of a resource type that grants can write
// (Refs set), or nil.
func GCPIAMTargetFor(resourceType string) *GCPIAMTarget {
	for i := range gcpIAMTargets {
		if t := &gcpIAMTargets[i]; t.ResourceType == resourceType && len(t.Refs) > 0 {
			return t
		}
	}
	return nil
}

// GCPIAMTargetOf is the target of an IAM type
// ("google_storage_bucket_iam_member"), or nil when unknown.
func GCPIAMTargetOf(iamType string) *GCPIAMTarget {
	prefix := gcpIAMPrefix(iamType)
	for i := range gcpIAMTargets {
		if gcpIAMTargets[i].IAM == prefix {
			return &gcpIAMTargets[i]
		}
	}
	return nil
}

// gcpIAMPrefix strips _member / _binding / _policy.
func gcpIAMPrefix(iamType string) string {
	for _, s := range []string{"_member", "_binding", "_policy"} {
		if p, ok := strings.CutSuffix(iamType, s); ok {
			return p
		}
	}
	return iamType
}

// GCPRole looks up a basic or predefined role by ID ("roles/run.invoker").
func GCPRole(id string) (model.AccessRole, bool) {
	r, err := gcpRef()
	if err != nil {
		return model.AccessRole{}, false
	}
	i, ok := r.byID[strings.TrimSpace(id)]
	if !ok {
		return model.AccessRole{}, false
	}
	return r.Roles[i], true
}

// gcpCatalog serves the roles: with q.ResourceType (a resource type or an
// IAM type) the ones suggested for it first (Read, Write, Manage order,
// Suggested set), then every predefined role by ID, then the basic roles;
// q.Search filters by ID, title, description or service.
func gcpCatalog(q CatalogQuery) (*model.AccessCatalog, error) {
	r, err := gcpRef()
	if err != nil {
		return nil, err
	}
	out := &model.AccessCatalog{Provider: "gcp", Source: r.Source}
	search := strings.ToLower(strings.TrimSpace(q.Search))
	match := func(role model.AccessRole) bool {
		return search == "" || strings.Contains(strings.ToLower(role.ID), search) ||
			strings.Contains(strings.ToLower(role.Name), search) ||
			strings.Contains(strings.ToLower(role.Description), search) ||
			strings.Contains(strings.ToLower(role.Category), search)
	}
	var t *GCPIAMTarget
	if strings.Contains(q.ResourceType, "_iam_") {
		t = GCPIAMTargetOf(q.ResourceType)
	} else if q.ResourceType != "" {
		for i := range gcpIAMTargets {
			if gcpIAMTargets[i].ResourceType == q.ResourceType {
				t = &gcpIAMTargets[i]
				break
			}
		}
	}
	taken := map[string]bool{}
	if t != nil {
		for i, ids := range t.Roles {
			for _, id := range ids {
				idx, ok := r.byID[id]
				if !ok || taken[id] || !match(r.Roles[idx]) {
					continue
				}
				role := r.Roles[idx]
				role.Suggested = model.AccessLevels[i]
				taken[id] = true
				out.Roles = append(out.Roles, role)
			}
		}
	}
	var basic, rest []model.AccessRole
	for _, role := range r.Roles {
		switch {
		case taken[role.ID] || !match(role):
		case role.Privileged:
			basic = append(basic, role)
		default:
			rest = append(rest, role)
		}
	}
	slices.SortStableFunc(rest, func(a, b model.AccessRole) int { return strings.Compare(a.ID, b.ID) })
	out.Roles = append(append(out.Roles, rest...), basic...)
	return out, nil
}
