package compliance_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compliance"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
)

// Every registered framework is well formed and maps only rules that
// exist: a mistyped code would never find anything and grade its control
// met.
func TestRegisteredFrameworks(t *testing.T) {
	rules := map[string]string{} // code -> platform family
	for _, r := range lint.Rules() {
		rules[r.Code] = "terraform"
	}
	for _, r := range compose.Rules() {
		rules[r.Code] = "docker"
	}
	for _, r := range kuberules.Rules() {
		rules[r.Code] = "kubernetes"
	}
	family := map[string]string{"aws": "terraform", "azurerm": "terraform", "google": "terraform", "oci": "terraform", "docker": "docker", "kubernetes": "kubernetes"}
	for _, f := range compliance.Frameworks() {
		if family[f.Platform] == "" || f.Name == "" || f.Version == "" || !strings.HasPrefix(f.Source, "https://") || len(f.Controls) == 0 {
			t.Errorf("%s: incomplete framework (platform %q, source %q)", f.ID, f.Platform, f.Source)
		}
		seen := map[string]bool{}
		for _, c := range f.Controls {
			where := f.ID + " " + c.ID
			if seen[c.ID] {
				t.Errorf("%s: duplicate control", where)
			}
			seen[c.ID] = true
			if c.Title == "" || c.Section == "" || c.Guidance == "" {
				t.Errorf("%s: title, section and guidance are required", where)
			}
			if c.Manual == (len(c.Rules) > 0) {
				t.Errorf("%s: a control is either manual or decided by rules (manual=%v, rules=%v)", where, c.Manual, c.Rules)
			}
			if slices.Contains(c.AppliesTo, "") {
				t.Errorf("%s: empty AppliesTo type", where)
			}
			for _, code := range c.Rules {
				got, ok := rules[code]
				if strings.HasPrefix(code, "net_") {
					// Network findings (netcheck) have no rule list and
					// can't be turned off.
					got, ok = "terraform", true
				}
				if !ok {
					t.Errorf("%s: unknown rule %s", where, code)
				} else if got != family[f.Platform] {
					t.Errorf("%s: rule %s is a %s rule, not %s", where, code, got, family[f.Platform])
				}
			}
		}
	}
}
