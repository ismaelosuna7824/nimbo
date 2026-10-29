package sandbox

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// azureOverrideFile is written into every configuration directory (root
// and local modules) declaring an azurerm_storage_account.
const azureOverrideFile = "zz_nimbo_floci_azure_override.tf"

// azureStorageIgnored are the azurerm_storage_account arguments floci-az
// doesn't keep, for azurerm of version ("" = unknown, the catalog's 4.x
// until init selects one; the wiring is rewritten then). Verified with
// floci-az 0.15.0 and azurerm 4.9, 4.81 and 5.11: the next plan after an
// apply replaced every account because the queue and table encryption key
// types read back "Account" (default "Service", both force a new
// resource), and updated it in place because HTTPS-only and the minimum
// TLS version read back false and TLS1_0 (defaults true and TLS1_2) and,
// in 5.x, public_network_access reads back empty (default Enabled).
func azureStorageIgnored(version string) []string {
	major, _ := parseVersion(version)
	if major == 0 {
		major = 4
	}
	out := []string{"queue_encryption_key_type", "table_encryption_key_type", "min_tls_version"}
	if major < 4 {
		out = append(out, "enable_https_traffic_only") // renamed in 4.0
	} else {
		out = append(out, "https_traffic_only_enabled")
	}
	if major >= 5 {
		out = append(out, "public_network_access")
	}
	return out
}

// azureOverrideFiles returns, per configuration directory, an override file
// adding azureStorageIgnored(version) to the lifecycle ignore_changes of
// each azurerm_storage_account the directory declares. files are the run's
// files (written over the assets of assetsDir, as syncWorkdir does).
// Terraform replaces ignore_changes rather than merging it, so the user's
// entries are kept; resources ignoring all changes are left alone.
// Resources of remote modules (downloaded by init) can't be overridden.
// The file set depends only on the configuration, version selects the
// contents.
func azureOverrideFiles(wf *model.Workflow, files map[string][]byte, assetsDir, version string) map[string][]byte {
	if !slices.Contains(wf.Clouds, model.CloudAzure) {
		return nil
	}
	sources := map[string][]byte{}
	if assets, err := listAssets(assetsDir); err == nil {
		for _, a := range assets {
			if !strings.HasSuffix(a, ".tf") {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(assetsDir, filepath.FromSlash(a))); err == nil {
				sources[a] = b
			}
		}
	}
	for name, b := range files {
		if p := path.Clean(filepath.ToSlash(name)); strings.HasSuffix(p, ".tf") {
			sources[p] = b
		}
	}

	type resource struct {
		declared bool     // in a primary (non-override) file
		all      bool     // ignore_changes = all, or not a static list
		ignore   []string // the user's ignore_changes entries, as written
	}
	dirs := map[string]map[string]*resource{} // dir -> name -> resource
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	// Terraform merges override files in lexical order after the primary
	// files; the last ignore_changes set wins.
	sort.Slice(names, func(i, j int) bool {
		oi, oj := isOverrideFile(names[i]), isOverrideFile(names[j])
		if oi != oj {
			return oj
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		dir, base := path.Split(name)
		if base == azureOverrideFile || strings.HasPrefix(name, "environments/") {
			continue
		}
		src := sources[name]
		f, diags := hclsyntax.ParseConfig(src, name, hcl.InitialPos)
		if diags.HasErrors() {
			continue // terraform reports it
		}
		for _, blk := range f.Body.(*hclsyntax.Body).Blocks {
			if blk.Type != "resource" || len(blk.Labels) != 2 || blk.Labels[0] != "azurerm_storage_account" {
				continue
			}
			if dirs[dir] == nil {
				dirs[dir] = map[string]*resource{}
			}
			r := dirs[dir][blk.Labels[1]]
			if r == nil {
				r = &resource{}
				dirs[dir][blk.Labels[1]] = r
			}
			r.declared = r.declared || !isOverrideFile(name)
			for _, lc := range blk.Body.Blocks {
				if lc.Type != "lifecycle" {
					continue
				}
				attr, ok := lc.Body.Attributes["ignore_changes"]
				if !ok {
					continue
				}
				tuple, ok := attr.Expr.(*hclsyntax.TupleConsExpr)
				if !ok {
					r.all = true // `all` or anything this can't extend
					continue
				}
				r.all = false
				r.ignore = r.ignore[:0]
				for _, e := range tuple.Exprs {
					r.ignore = append(r.ignore, strings.TrimSpace(string(e.Range().SliceBytes(src))))
				}
			}
		}
	}

	ignored := azureStorageIgnored(version)
	out := map[string][]byte{}
	for dir, resources := range dirs {
		var b strings.Builder
		rnames := make([]string, 0, len(resources))
		for name := range resources {
			rnames = append(rnames, name)
		}
		sort.Strings(rnames)
		for _, name := range rnames {
			r := resources[name]
			if !r.declared || r.all {
				continue
			}
			ignore := slices.Clone(r.ignore)
			for _, a := range ignored {
				if !slices.Contains(ignore, a) {
					ignore = append(ignore, a)
				}
			}
			if b.Len() == 0 {
				b.WriteString("# Generated by Nimbo for the local sandbox: the Azure emulator doesn't keep\n# these storage account settings, so every plan after an apply would change\n# or replace the accounts.\n\n")
			}
			b.WriteString("resource \"azurerm_storage_account\" " + strconv.Quote(name) + " {\n  lifecycle {\n")
			b.WriteString("    ignore_changes = [" + strings.Join(ignore, ", ") + "]\n  }\n}\n\n")
		}
		if b.Len() > 0 {
			out[dir+azureOverrideFile] = []byte(strings.TrimSuffix(b.String(), "\n"))
		}
	}
	return out
}

// isOverrideFile reports a Terraform override file name (override.tf,
// *_override.tf).
func isOverrideFile(name string) bool {
	base := path.Base(name)
	return base == "override.tf" || strings.HasSuffix(base, "_override.tf")
}
