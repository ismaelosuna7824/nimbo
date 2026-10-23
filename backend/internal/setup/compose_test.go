package setup

import (
	"errors"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestComposeCheck(t *testing.T) {
	linux := &Checker{plat: Platform{GOOS: "linux", PackageManager: "apt"}}
	mac := &Checker{plat: Platform{GOOS: "darwin", PackageManager: "brew"}}

	ok := mac.composeCheck(cliInfo{bin: "docker"}, "v2.40.0", nil)
	if ok.ID != CheckDockerCompose || ok.Required || ok.Status != model.SetupOK || ok.Summary != "Docker Compose 2.40.0" {
		t.Errorf("ok = %+v", ok)
	}

	missing := linux.composeCheck(cliInfo{bin: "docker"}, "", errors.New("docker: 'compose' is not a docker command."))
	if missing.Status != model.SetupMissing || len(missing.Fixes) != 2 ||
		missing.Fixes[0].Command != "sudo apt-get install -y docker-compose-plugin" || missing.Fixes[1].URL != composeInstallURL {
		t.Errorf("missing on linux = %+v", missing)
	}
	if m := mac.composeCheck(cliInfo{bin: "docker"}, "", errors.New("unknown command")); len(m.Fixes) != 1 || m.Fixes[0].Kind != model.FixLink {
		t.Errorf("missing on mac = %+v", m)
	}

	// Without the docker CLI the check waits on the install step (no fixes).
	pending := mac.composeCheck(cliInfo{err: errors.New("not found")}, "", nil)
	if pending.Status != model.SetupMissing || len(pending.Fixes) != 0 {
		t.Errorf("pending = %+v", pending)
	}
}
