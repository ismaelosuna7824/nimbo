package api

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The structure summary reports each embedded provider's constraint, the
// locked release (the imported lock file before the sandbox's first init)
// and how it fares locally.
func TestStructureProviderVersions(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("versions")
	applyCode(t, e, wf.ID, map[string]string{"main.tf": `terraform {
  required_providers {
    google = { source = "hashicorp/google", version = "~> 6.0" }
    random = { source = "hashicorp/random", version = "~> 3.9" }
    other  = { source = "acme/other", version = "~> 1.0" }
  }
}
resource "google_sql_database" "app" {
  name     = "app"
  instance = "db"
}
resource "random_id" "x" {
  byte_length = 4
}
`})
	writeFile(t, filepath.Join(e.st.AssetsDir(wf.ID), ".terraform.lock.hcl"), `provider "registry.terraform.io/hashicorp/google" {
  version = "6.50.0"
}
`)
	var s model.StructureSummary
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/structure", nil, &s)
	google, _ := testCatalog.ProviderCompat("google")
	random, _ := testCatalog.ProviderCompat("random")
	want := []model.ProviderVersion{
		{Name: "google", Constraint: "~> 6.0", Locked: "6.50.0", Recommended: google.Recommended, Status: catalog.StatusBrokenLocally},
		{Name: "random", Constraint: "~> 3.9", Recommended: random.Recommended, Status: catalog.StatusOK},
	}
	if !slices.Equal(s.Providers, want) {
		t.Errorf("providers = %+v, want %+v", s.Providers, want)
	}
}
