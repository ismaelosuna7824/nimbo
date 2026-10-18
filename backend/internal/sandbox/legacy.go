package sandbox

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Legacy Docker naming used before the product was renamed from "IaC Cloud"
// to Nimbo. This file is the only place the old names may appear: it removes
// leftover sandbox objects so the next Start recreates them under nimbo-
// names, reusing the emulator data and tfstate kept in the (migrated) home.
const (
	legacyPrefix        = "iacc-"
	legacyLabelWorkflow = "io.iaccloud.workflow"
	legacyPluginVolume  = "iacc-tf-plugins"
)

var legacyEmulatorSuffixes = []string{"-pod", "-aws", "-azure", "-gcp", "-oci"}

// legacyContainerOwned reports whether a container of the old naming belongs
// to one of the workflows in wfs (workflow IDs known to the current home).
func legacyContainerOwned(c container, wfs map[string]bool) bool {
	if wf := c.label(legacyLabelWorkflow); wf != "" && wfs[wf] {
		return true
	}
	if ns := c.label("floci_namespace"); strings.HasPrefix(ns, legacyPrefix) && wfs[strings.TrimPrefix(ns, legacyPrefix)] {
		return true
	}
	for _, name := range strings.Split(c.Names, ",") {
		name = strings.TrimPrefix(name, "/")
		for wf := range wfs {
			base := legacyPrefix + wf
			for _, s := range legacyEmulatorSuffixes {
				if name == base+s {
					return true
				}
			}
			if strings.HasPrefix(name, base+"-tf-") {
				return true
			}
		}
	}
	return false
}

// legacyNetworkOwned reports whether a network of the old naming belongs to
// one of the workflows in wfs.
func legacyNetworkOwned(name, labels string, wfs map[string]bool) bool {
	if strings.HasPrefix(name, legacyPrefix) && wfs[strings.TrimPrefix(name, legacyPrefix)] {
		return true
	}
	c := container{Labels: labels}
	ns := c.label("floci_namespace")
	return strings.HasPrefix(ns, legacyPrefix) && wfs[strings.TrimPrefix(ns, legacyPrefix)]
}

// cleanupLegacy removes Docker objects created under the pre-rename naming
// for workflows present in this home. The shared plugin cache volume is only
// removed when the legacy home was migrated (homeMigrated). Best effort:
// failures are logged, never fatal.
func (m *Manager) cleanupLegacy(ctx context.Context, homeMigrated bool) {
	entries, err := os.ReadDir(filepath.Join(m.cfg.Home, "workflows"))
	if err != nil {
		return
	}
	wfs := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() && checkID(e.Name()) == nil {
			wfs[e.Name()] = true
		}
	}
	if len(wfs) == 0 && !homeMigrated {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cs, err := m.ps(ctx)
	if err != nil {
		slog.Debug("legacy cleanup: docker unavailable", "err", err)
		return
	}
	for _, c := range cs {
		if legacyContainerOwned(c, wfs) {
			if _, err := m.docker(ctx, "rm", "-f", "-v", c.ID); err != nil {
				slog.Warn("legacy cleanup: remove container", "name", c.Names, "err", err)
			} else {
				slog.Info("legacy cleanup: removed container", "name", c.Names)
			}
		}
	}
	out, err := m.docker(ctx, "network", "ls", "--format", "{{.Name}}\t{{.Labels}}")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			name, labels, _ := strings.Cut(strings.TrimSpace(line), "\t")
			if name != "" && legacyNetworkOwned(name, labels, wfs) {
				if err := m.removeNetwork(ctx, name); err != nil {
					slog.Warn("legacy cleanup: remove network", "name", name, "err", err)
				} else {
					slog.Info("legacy cleanup: removed network", "name", name)
				}
			}
		}
	}
	if homeMigrated {
		if _, err := m.docker(ctx, "volume", "rm", legacyPluginVolume); err == nil {
			slog.Info("legacy cleanup: removed volume", "name", legacyPluginVolume)
		}
	}
}
