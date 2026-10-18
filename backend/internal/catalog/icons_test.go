package catalog

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// iconMapFile is the desktop app's explicit lucide icon map; it only bundles
// the icons listed there, so every curated icon must have an entry.
const iconMapFile = "../../../apps/desktop/src/components/resource-icon.tsx"

var iconEntry = regexp.MustCompile(`(?m)^\s*"([a-z0-9-]+)": [A-Za-z0-9]+Icon,$`)

func TestCuratedIconsInDesktopMap(t *testing.T) {
	cur, err := loadCurated()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(iconMapFile)
	if err != nil {
		t.Fatal(err)
	}
	mapped := map[string]bool{}
	for _, m := range iconEntry.FindAllStringSubmatch(string(src), -1) {
		mapped[m[1]] = true
	}
	if len(mapped) == 0 {
		t.Fatalf("no icon entries found in %s", iconMapFile)
	}

	used := map[string]string{}
	add := func(icon, where string) {
		if icon != "" {
			used[icon] = where
		}
	}
	for cat, icon := range cur.CategoryIcons {
		add(icon, "category "+cat)
	}
	for cloud, cc := range cur.Clouds {
		for _, s := range cc.Services {
			add(s.Icon, "service "+string(cloud)+"/"+s.Key)
		}
	}
	for typ, ct := range cur.Resources {
		add(ct.Icon, "resource "+typ)
	}
	for typ, ct := range cur.Data {
		add(ct.Icon, "data "+typ)
	}

	var missing []string
	for icon, where := range used {
		if !mapped[icon] {
			missing = append(missing, icon+" ("+where+")")
		}
	}
	slices.Sort(missing)
	for _, m := range missing {
		t.Errorf("curated icon %s has no entry in %s", m, iconMapFile)
	}
}
