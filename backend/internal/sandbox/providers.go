package sandbox

import (
	"fmt"
	"strings"

	"github.com/hashicorp/go-version"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// lockMismatch is how terraform init reports a lock file entry outside the
// configuration's constraints (it then needs -upgrade).
const lockMismatch = "does not match configured version constraint"

// LockedVersions returns the provider versions the sandbox's
// .terraform.lock.hcl selected for wfID (provider type -> version), or,
// before the first init, those of the lock file in assetsDir ("" = none).
func (m *Manager) LockedVersions(wfID, assetsDir string) map[string]string {
	if v := readLockVersions(m.WorkDir(wfID)); len(v) > 0 || assetsDir == "" {
		return v
	}
	return readLockVersions(assetsDir)
}

// staleLocks describes the lock entries whose version the workflow's
// required_providers constraints no longer allow ("hashicorp/google
// 6.50.0 (required: ~> 7.46)"): init then fails unless it may upgrade.
func staleLocks(wf *model.Workflow, lock map[string]string) []string {
	var out []string
	for _, rp := range catalog.RequiredProviders(wf) {
		locked := lock[rp.Type]
		if locked == "" || strings.TrimSpace(rp.Constraint) == "" {
			continue
		}
		cs, err := version.NewConstraint(rp.Constraint)
		if err != nil {
			continue
		}
		v, err := version.NewVersion(locked)
		if err != nil || cs.Check(v) {
			continue
		}
		name := rp.Source
		if name == "" {
			name = rp.Local
		}
		out = append(out, fmt.Sprintf("%s %s (required: %s)", name, locked, rp.Constraint))
	}
	return out
}

// providerWarnings reports the providers init selected (lock maps provider
// type to version) that are older than the oldest release verified on the
// local emulators or known to break some of wf's resources there.
func providerWarnings(cat *catalog.Catalog, wf *model.Workflow, lock map[string]string) []string {
	if cat == nil {
		return nil
	}
	required := map[string]catalog.RequiredProvider{}
	for _, rp := range catalog.RequiredProviders(wf) {
		required[rp.Type] = rp
	}
	types := catalog.ResourceTypes(wf)
	var out []string
	for _, pc := range cat.ProviderCompats() {
		locked := lock[pc.Name]
		if locked == "" {
			continue
		}
		local := pc.Name
		if rp, ok := required[pc.Name]; ok {
			if _, same := cat.CompatFor(rp); !same {
				continue // another namespace's provider of the same type
			}
			local = rp.Local
		}
		vc := pc.CheckVersion(locked, types)
		if vc.Invalid || !vc.BelowMinimum && len(vc.Issues) == 0 {
			continue
		}
		subject := fmt.Sprintf("%s %s (selected by the lock file)", pc.Source, locked)
		fix := fmt.Sprintf("set version = %q for %s in required_providers (the constraint Nimbo writes); the next run upgrades the sandbox's lock file.", pc.Recommended, local)
		if len(vc.Issues) == 0 {
			out = append(out, fmt.Sprintf("%s is older than %s, the oldest release verified on the local emulators, so resources may fail locally and Nimbo's %s schema may list attributes it lacks: %s",
				subject, pc.LocalMinimum, pc.Catalog, upperFirst(fix)))
			continue
		}
		for _, is := range vc.Issues {
			var failing []string
			for i := range wf.Nodes {
				if n := &wf.Nodes[i]; n.Kind == model.KindResource && strings.HasPrefix(n.Type, pc.Name+"_") && is.Affects([]string{n.Type}) {
					failing = append(failing, hclcodec.Address(n))
				}
			}
			if len(failing) == 0 {
				failing = []string{"its resources"}
			}
			action := upperFirst(fix)
			switch {
			case pc.AffectsCatalog(is):
				action = is.Fix + "." // the recommended constraint is affected too
			case is.Fix != "":
				action = is.Fix + ": " + fix
			}
			out = append(out, fmt.Sprintf("%s is known to break %s on the local emulators: %s. %s", subject, strings.Join(failing, ", "), is.Message, action))
		}
	}
	return out
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
