package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ignoreChanges parses an azure override file into resource name ->
// ignore_changes entries.
func ignoreChanges(t *testing.T, src []byte) map[string][]string {
	t.Helper()
	f, diags := hclsyntax.ParseConfig(src, "o.tf", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parse: %s\n%s", diags.Error(), src)
	}
	out := map[string][]string{}
	for _, blk := range f.Body.(*hclsyntax.Body).Blocks {
		if blk.Type != "resource" || blk.Labels[0] != "azurerm_storage_account" {
			t.Fatalf("unexpected block %s %v", blk.Type, blk.Labels)
		}
		if len(blk.Body.Attributes) != 0 || len(blk.Body.Blocks) != 1 || blk.Body.Blocks[0].Type != "lifecycle" {
			t.Fatalf("%s: only a lifecycle block expected\n%s", blk.Labels[1], src)
		}
		lc := blk.Body.Blocks[0].Body
		if len(lc.Attributes) != 1 {
			t.Fatalf("%s: lifecycle must only set ignore_changes (other arguments merge from the user's block)\n%s", blk.Labels[1], src)
		}
		var entries []string
		for _, e := range lc.Attributes["ignore_changes"].Expr.(*hclsyntax.TupleConsExpr).Exprs {
			entries = append(entries, string(e.Range().SliceBytes(src)))
		}
		out[blk.Labels[1]] = entries
	}
	return out
}

func TestAzureOverrideFiles(t *testing.T) {
	assets := t.TempDir()
	if err := os.MkdirAll(filepath.Join(assets, "modules", "legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
		"modules/legacy/main.tf": `resource "azurerm_storage_account" "x" {}`,
		// Replaced by the run's file of the same name.
		"main.tf": `resource "azurerm_storage_account" "stale" {}`,
	} {
		if err := os.WriteFile(filepath.Join(assets, filepath.FromSlash(name)), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{
		"main.tf": []byte(`
resource "azurerm_storage_account" "plain" {
  name = "plain"
}

resource "azurerm_storage_account" "user" {
  for_each = toset(["a"])
  lifecycle {
    prevent_destroy = false
    ignore_changes  = [tags["owner"], queue_encryption_key_type]
  }
}

resource "azurerm_storage_account" "everything" {
  lifecycle {
    ignore_changes = all
  }
}

resource "azurerm_storage_container" "c" {}
`),
		// A user override replaces the primary ignore_changes; ours must
		// keep its entries.
		"user_override.tf": []byte(`
resource "azurerm_storage_account" "plain" {
  lifecycle {
    ignore_changes = [account_tier]
  }
}
`),
		"modules/store/main.tf":         []byte(`resource "azurerm_storage_account" "m" {}`),
		"modules/empty/main.tf":         []byte(`resource "azurerm_resource_group" "rg" {}`),
		"environments/dev/main.tf":      []byte(`resource "azurerm_storage_account" "env" {}`),
		"broken.tf":                     []byte(`resource "azurerm_storage_account" "b" {`),
		azureOverrideFile:               []byte(`resource "azurerm_storage_account" "old" {}`),
		"modules/store/" + overrideOnly: []byte(`resource "azurerm_storage_account" "only_override" {}`),
	}
	wf := &model.Workflow{ID: "wf", Clouds: []model.Cloud{model.CloudAzure}}
	out := azureOverrideFiles(wf, files, assets, "4.81.0")

	if len(out) != 3 {
		t.Fatalf("override files %v, want root, modules/store and modules/legacy", keys(out))
	}
	root := ignoreChanges(t, out[azureOverrideFile])
	want := map[string]string{
		"plain": "account_tier, " + v4,
		"user":  `tags["owner"], queue_encryption_key_type, table_encryption_key_type, min_tls_version, https_traffic_only_enabled`,
	}
	if len(root) != len(want) {
		t.Errorf("root overrides %v, want %v (ignore_changes = all, non-storage, stale asset and unparsable files are left alone)", root, want)
	}
	for name, w := range want {
		if got := strings.Join(root[name], ", "); got != w {
			t.Errorf("%s: ignore_changes = [%s], want [%s]", name, got, w)
		}
	}
	for dir, name := range map[string]string{"modules/store/": "m", "modules/legacy/": "x"} {
		got := ignoreChanges(t, out[dir+azureOverrideFile])
		if len(got) != 1 || strings.Join(got[name], ", ") != v4 {
			t.Errorf("%s: %v", dir, got)
		}
	}

	wf.Clouds = []model.Cloud{model.CloudAWS}
	if out := azureOverrideFiles(wf, files, assets, "4.81.0"); len(out) != 0 {
		t.Errorf("no Azure emulator, no overrides: %v", keys(out))
	}
}

const v4 = "queue_encryption_key_type, table_encryption_key_type, min_tls_version, https_traffic_only_enabled"

func TestAzureStorageIgnored(t *testing.T) {
	for version, want := range map[string]string{
		"":        v4, // not initialized yet: the catalog's major
		"4.9.0":   v4,
		"3.117.0": "queue_encryption_key_type, table_encryption_key_type, min_tls_version, enable_https_traffic_only",
		"5.11.0":  v4 + ", public_network_access",
	} {
		if got := strings.Join(azureStorageIgnored(version), ", "); got != want {
			t.Errorf("%q: %s, want %s", version, got, want)
		}
	}
}

// overrideOnly declares a resource only in an override file, which
// Terraform rejects anyway: no override is generated for it.
const overrideOnly = "extra_override.tf"

// TestAzureOverrideInWorkdir checks the override reaches the work
// directory with the run's files and disappears once the account is gone.
func TestAzureOverrideInWorkdir(t *testing.T) {
	work := t.TempDir()
	wf := &model.Workflow{ID: "wf", Clouds: []model.Cloud{model.CloudAzure}}
	req := RunRequest{Workflow: wf, Files: map[string][]byte{
		"main.tf":               []byte(`module "store" { source = "./modules/store" }`),
		"modules/store/main.tf": []byte(`resource "azurerm_storage_account" "sa" {}`),
	}}
	if _, err := syncWorkdir(work, runFiles(req, nil), ""); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(work, "modules", "store", azureOverrideFile)
	if b, err := os.ReadFile(module); err != nil || !strings.Contains(string(b), "table_encryption_key_type") {
		t.Fatalf("module override: %v\n%s", err, b)
	}
	if _, err := os.Stat(filepath.Join(work, azureOverrideFile)); !os.IsNotExist(err) {
		t.Errorf("root override written without a root storage account: %v", err)
	}

	req.Files["modules/store/main.tf"] = []byte(`resource "azurerm_storage_container" "c" {}`)
	if _, err := syncWorkdir(work, runFiles(req, nil), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(module); !os.IsNotExist(err) {
		t.Errorf("stale module override kept (Terraform rejects overrides of missing resources): %v", err)
	}
}

// TestAzureOverrideFollowsInit: the override written before the first init
// (version unknown) is rewritten for the release init selects.
func TestAzureOverrideFollowsInit(t *testing.T) {
	home := t.TempDir()
	m, err := New(Config{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	wf := &model.Workflow{ID: "wf-az", Clouds: []model.Cloud{model.CloudAzure}}
	work := m.WorkDir(wf.ID)
	req := RunRequest{Workflow: wf, Files: map[string][]byte{"main.tf": []byte(`resource "azurerm_storage_account" "sa" {}`)}}
	if _, err := syncWorkdir(work, runFiles(req, nil), ""); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(work, azureOverrideFile)
	for _, version := range []string{"5.11.0", "3.117.0"} {
		lock := filepath.Join(home, "lock")
		if err := os.WriteFile(lock, []byte(lockFile(map[string]string{"azurerm": version})), 0o644); err != nil {
			t.Fatal(err)
		}
		m.cfg.Docker = filepath.Join(home, "docker")
		if err := os.WriteFile(m.cfg.Docker, []byte("#!/bin/sh\ncp "+lock+" "+filepath.Join(work, ".terraform.lock.hcl")+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		rs := &runState{run: model.Run{ID: "r1", WorkflowID: wf.ID}}
		if err := m.initWorkdir(context.Background(), rs, wf, work, readLockVersions(work), nil, nil); err != nil {
			t.Fatalf("initWorkdir: %v", err)
		}
		got := ignoreChanges(t, mustRead(t, override))["sa"]
		if want := azureStorageIgnored(version); strings.Join(got, ", ") != strings.Join(want, ", ") {
			t.Errorf("after init selected %s: %v, want %v", version, got, want)
		}
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
