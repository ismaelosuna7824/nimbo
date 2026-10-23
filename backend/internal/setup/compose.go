package setup

import (
	"context"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const composeInstallURL = "https://docs.docker.com/compose/install/"

// composePackages is the Compose v2 plugin package per Linux package manager.
var composePackages = map[string]string{
	"apt":    "sudo apt-get install -y docker-compose-plugin",
	"dnf":    "sudo dnf install -y docker-compose-plugin",
	"pacman": "sudo pacman -S --needed docker-compose",
	"zypper": "sudo zypper install -y docker-compose",
}

// composeVersion runs `docker compose version --short` ("" + error when
// the plugin is missing).
func (c *Checker) composeVersion(ctx context.Context, cli cliInfo) (string, error) {
	return docker(ctx, 3*time.Second, cli.bin, "compose", "version", "--short")
}

// composeCheck reports the Docker Compose v2 plugin docker workflows run
// with. It is not required: Terraform workflows never use it.
func (c *Checker) composeCheck(cli cliInfo, version string, err error) model.SetupCheck {
	ch := model.SetupCheck{ID: CheckDockerCompose, Title: "Docker Compose is installed"}
	const intro = "Docker workspaces (compose.yaml projects) run with Docker Compose v2, the `docker compose` command."
	switch {
	case cli.err != nil:
		ch.Status = model.SetupMissing
		ch.Summary = "Checked once Docker is installed"
		ch.Detail = intro + " It comes with Docker Desktop, OrbStack and most Docker installs."
	case err != nil:
		ch.Status = model.SetupMissing
		ch.Summary = "Docker Compose v2 is missing"
		ch.Detail = intro + " Your docker command doesn't have it (Docker said: " + firstLine(err.Error()) +
			"). Docker Desktop and OrbStack include it; on Linux install the compose plugin. Terraform workflows work without it."
		ch.Fixes = []model.SetupFix{{Kind: model.FixLink, Label: "How to install Docker Compose", URL: composeInstallURL}}
		if cmd := composePackages[c.plat.PackageManager]; c.plat.GOOS == "linux" && cmd != "" {
			ch.Fixes = append([]model.SetupFix{{Kind: model.FixCommand, Label: "Install the Compose plugin", Command: cmd}}, ch.Fixes...)
		}
	default:
		ch.Status = model.SetupOK
		ch.Summary = "Docker Compose " + strings.TrimPrefix(version, "v")
		ch.Detail = intro + " Found it, so Nimbo can run your compose projects."
	}
	return ch
}
