package setup

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ---------------------------------------------------------------------------
// wsl (Windows only)

func (c *Checker) checkWSL(ctx context.Context) model.SetupCheck {
	ch := model.SetupCheck{ID: CheckWSL, Title: "WSL 2 is installed", Required: true}
	intro := "Docker Desktop on Windows runs Linux containers inside WSL 2 (Windows Subsystem for Linux), " +
		"a small Linux environment built into Windows."
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "wsl", "--status").Run(); err != nil {
		ch.Status = model.SetupMissing
		ch.Summary = "WSL 2 is not installed"
		ch.Detail = intro + " Install it from PowerShell opened with \"Run as administrator\", restart your computer, " +
			"then come back. If it says virtualization is disabled, turn on \"Virtualization\" (Intel VT-x / AMD-V) in your PC's BIOS settings."
		ch.Fixes = wslFixes()
		return ch
	}
	ch.Status = model.SetupOK
	ch.Summary = "WSL is installed"
	ch.Detail = intro
	return ch
}

func wslFixes() []model.SetupFix {
	return []model.SetupFix{
		{Kind: model.FixCommand, Label: "Install WSL 2 (PowerShell as administrator, then restart)", Command: "wsl --install"},
		{Kind: model.FixLink, Label: "Microsoft's WSL install guide", URL: "https://learn.microsoft.com/windows/wsl/install"},
	}
}

// ---------------------------------------------------------------------------
// docker-cli

const dockerExplained = "Docker runs small isolated mini-computers (containers) on your machine. " +
	"Nimbo uses them to run a private, offline copy of AWS/Azure/GCP so nothing touches your real cloud account or bill."

// installFixes are the ways to install Docker on the platform.
func installFixes(p Platform) []model.SetupFix {
	switch p.GOOS {
	case "darwin":
		chip, dmgArch := "Intel chip", "amd64"
		if p.Arch == "arm64" {
			chip, dmgArch = "Apple silicon", "arm64"
		}
		fixes := []model.SetupFix{
			{Kind: model.FixLink, Label: "Download OrbStack (recommended, lightweight)", URL: "https://orbstack.dev/download"},
		}
		if p.PackageManager == "brew" {
			fixes = append(fixes, model.SetupFix{Kind: model.FixCommand, Label: "Or install OrbStack with Homebrew", Command: "brew install --cask orbstack"})
		}
		fixes = append(fixes, model.SetupFix{Kind: model.FixLink, Label: "Download Docker Desktop for Mac (" + chip + ")",
			URL: "https://desktop.docker.com/mac/main/" + dmgArch + "/Docker.dmg"})
		if p.PackageManager == "brew" {
			fixes = append(fixes, model.SetupFix{Kind: model.FixCommand, Label: "Or install Docker Desktop with Homebrew", Command: "brew install --cask docker-desktop"})
		}
		return fixes
	case "windows":
		arch := "amd64"
		if p.Arch == "arm64" {
			arch = "arm64"
		}
		fixes := []model.SetupFix{{Kind: model.FixLink, Label: "Download Docker Desktop for Windows",
			URL: "https://desktop.docker.com/win/main/" + arch + "/Docker%20Desktop%20Installer.exe"}}
		switch p.PackageManager {
		case "winget":
			fixes = append(fixes, model.SetupFix{Kind: model.FixCommand, Label: "Or install with winget", Command: "winget install -e --id Docker.DockerDesktop"})
		case "choco":
			fixes = append(fixes, model.SetupFix{Kind: model.FixCommand, Label: "Or install with Chocolatey (as administrator)", Command: "choco install -y docker-desktop"})
		}
		return append(fixes, model.SetupFix{Kind: model.FixLink, Label: "Docker Desktop for Windows guide",
			URL: "https://docs.docker.com/desktop/setup/install/windows-install/"})
	case "linux":
		return linuxInstallFixes(p)
	}
	return []model.SetupFix{{Kind: model.FixLink, Label: "Docker install guide", URL: "https://docs.docker.com/get-started/get-docker/"}}
}

// getDockerDistros are the distributions Docker's convenience script
// (get.docker.com) supports.
var getDockerDistros = map[string]bool{
	"ubuntu": true, "debian": true, "raspbian": true, "fedora": true, "centos": true, "rhel": true,
}

// linuxPostInstall starts Docker at boot and lets the user run it without sudo.
const linuxPostInstall = "sudo systemctl enable --now docker && sudo usermod -aG docker $USER"

func linuxInstallFixes(p Platform) []model.SetupFix {
	var fixes []model.SetupFix
	if getDockerDistros[p.Distro] {
		fixes = append(fixes, model.SetupFix{Kind: model.FixCommand, Label: "Install Docker Engine (official script)",
			Command: "curl -fsSL https://get.docker.com | sudo sh"})
	}
	switch p.PackageManager {
	case "apt":
		fixes = append(fixes, model.SetupFix{Kind: model.FixCommand, Label: "Or install your distribution's package",
			Command: "sudo apt-get update && sudo apt-get install -y docker.io"})
	case "dnf":
		fixes = append(fixes, model.SetupFix{Kind: model.FixCommand, Label: "Or install your distribution's package",
			Command: "sudo dnf install -y moby-engine"})
	case "pacman":
		fixes = append(fixes, model.SetupFix{Kind: model.FixCommand, Label: "Install Docker",
			Command: "sudo pacman -S --needed docker"})
	case "zypper":
		fixes = append(fixes, model.SetupFix{Kind: model.FixCommand, Label: "Install Docker",
			Command: "sudo zypper install -y docker"})
	}
	if len(fixes) == 0 {
		fixes = append(fixes, model.SetupFix{Kind: model.FixCommand, Label: "Install Docker Engine (official script)",
			Command: "curl -fsSL https://get.docker.com | sudo sh"})
	}
	fixes = append(fixes,
		model.SetupFix{Kind: model.FixCommand, Label: "Then start Docker and allow your user (log out and back in afterwards)", Command: linuxPostInstall},
		model.SetupFix{Kind: model.FixLink, Label: "Docker Engine install guide", URL: linuxGuide(p.Distro)})
	return fixes
}

func linuxGuide(distro string) string {
	switch distro {
	case "ubuntu", "debian", "fedora", "centos", "rhel", "raspbian", "sles":
		return "https://docs.docker.com/engine/install/" + distro + "/"
	case "arch", "manjaro", "endeavouros":
		return "https://wiki.archlinux.org/title/Docker"
	case "opensuse-leap", "opensuse-tumbleweed":
		return "https://en.opensuse.org/Docker"
	}
	return "https://docs.docker.com/engine/install/"
}

func (c *Checker) cliCheck(cli cliInfo, flavor string) model.SetupCheck {
	ch := model.SetupCheck{ID: CheckDockerCLI, Title: "Docker is installed", Required: true}
	if cli.err != nil {
		ch.Status = model.SetupMissing
		ch.Summary = "Docker is not installed"
		ch.Detail = dockerExplained + " " + installAdvice(c.plat) + " (Nimbo looked for the docker command: " + cli.err.Error() + ".)"
		ch.Fixes = installFixes(c.plat)
		return ch
	}
	ch.Status = model.SetupOK
	name := "Docker"
	if flavor == FlavorPodman {
		name = "Podman"
	}
	ch.Summary = strings.TrimSpace(name + " " + parseCLIVersion(cli.versionLine))
	if flavor != "" && flavor != FlavorPodman {
		ch.Summary += " via " + flavor
	}
	ch.Detail = dockerExplained + " Found the docker command at " + cli.bin + "."
	return ch
}

func installAdvice(p Platform) string {
	switch p.GOOS {
	case "darwin":
		return "Install one of the free options below, open it once from Applications, then come back here."
	case "windows":
		return "Install Docker Desktop (free for personal use), keep \"Use WSL 2\" selected during setup, " +
			"restart if asked, open Docker Desktop once from the Start menu, then come back here."
	case "linux":
		return "Install Docker Engine with one of the commands below in a terminal, then run the follow-up command so Docker " +
			"starts automatically and works without sudo, and log out and back in."
	}
	return "Install Docker, start it, then come back here."
}

// ---------------------------------------------------------------------------
// docker-daemon

func (c *Checker) startFixes(flavor string) []model.SetupFix {
	if c.plat.GOOS == "linux" {
		if flavor == FlavorDockerDesktop {
			return []model.SetupFix{{Kind: model.FixCommand, Label: "Start Docker Desktop", Command: "systemctl --user start docker-desktop"}}
		}
		return []model.SetupFix{{Kind: model.FixCommand, Label: "Start Docker now and at every boot", Command: "sudo systemctl enable --now docker"}}
	}
	fix := model.SetupFix{Kind: model.FixAction, Label: "Start Docker", Action: model.SetupActionStartDocker}
	if app := c.engineApp(flavor); app != "" {
		fix.Label = "Start " + appName(app)
		switch c.plat.GOOS {
		case "darwin":
			fix.Command = "open -a " + shellQuote(app)
		case "windows":
			fix.Command = `Start-Process "` + app + `"`
		}
	} else if _, err := exec.LookPath("colima"); err == nil && c.plat.GOOS == "darwin" {
		fix.Label = "Start Colima"
		fix.Command = "colima start"
	}
	return []model.SetupFix{fix}
}

// isPermissionDenied reports whether docker failed because the user may not
// use the Docker socket (Linux without docker group membership).
func isPermissionDenied(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "permission denied")
}

func (c *Checker) daemonCheck(cli cliInfo, info *dockerInfo, infoErr error, flavor string) model.SetupCheck {
	ch := model.SetupCheck{ID: CheckDockerDaemon, Title: "Docker is running", Required: true}
	switch {
	case cli.err != nil:
		ch.Status = model.SetupMissing
		ch.Summary = "Checked once Docker is installed"
		ch.Detail = "Docker has to be installed and running before Nimbo can start sandboxes."
	case info == nil && c.plat.GOOS == "linux" && isPermissionDenied(infoErr):
		ch.Status = model.SetupMissing
		ch.Summary = "Checked once Docker permissions are fixed"
		ch.Detail = "Docker answered \"permission denied\", so your user isn't allowed to use it yet (see the next step)."
	case info == nil:
		ch.Status = model.SetupMissing
		ch.Summary = "Docker is installed but not running"
		ch.Detail = startAdvice(c.plat.GOOS, flavor) + " (Docker said: " + firstLine(infoErr.Error()) + ")"
		ch.Fixes = c.startFixes(flavor)
	default:
		ch.Status = model.SetupOK
		ch.Summary = "Docker engine " + info.ServerVersion + " is running"
		if flavor != "" {
			ch.Summary += " (" + flavor + ")"
		}
		ch.Detail = "The Docker engine is up and answering, so Nimbo can start and stop sandboxes."
	}
	return ch
}

func startAdvice(goos, flavor string) string {
	switch goos {
	case "darwin":
		switch flavor {
		case FlavorOrbStack:
			return "Docker is installed, but OrbStack isn't running. Open OrbStack from Applications; its icon appears in the menu bar " +
				"at the top of the screen when it's ready."
		case FlavorColima:
			return "Docker is installed, but Colima isn't running. Start it with \"colima start\" in Terminal."
		case FlavorRancher:
			return "Docker is installed, but Rancher Desktop isn't running. Open it from Applications and wait until it finishes starting."
		}
		return "Docker is installed, but its engine isn't running. Open Docker Desktop from Applications and wait until the whale " +
			"icon in the menu bar at the top of the screen stops animating (the first start can take a minute)."
	case "windows":
		return "Docker is installed, but Docker Desktop isn't running. Open Docker Desktop from the Start menu and wait until the " +
			"whale icon in the system tray (bottom-right, near the clock) says \"Docker Desktop is running\"."
	case "linux":
		if flavor == FlavorDockerDesktop {
			return "Docker Desktop is installed but not running. Start it from your applications menu or with the command below."
		}
		return "Docker is installed, but the Docker service isn't running. Start it with the command below; it also makes " +
			"Docker start automatically every time your computer boots."
	}
	return "Docker is installed but its engine isn't running. Start it and check again."
}

// ---------------------------------------------------------------------------
// docker-permissions (Linux only)

func (c *Checker) permissionsCheck(cli cliInfo, info *dockerInfo, infoErr error) model.SetupCheck {
	ch := model.SetupCheck{ID: CheckDockerPermissions, Title: "You're allowed to use Docker", Required: true}
	intro := "On Linux, only members of the \"docker\" group may use Docker without typing sudo every time."
	switch {
	case info != nil:
		ch.Status = model.SetupOK
		ch.Summary = "Your user can use Docker"
		ch.Detail = intro
	case cli.err == nil && isPermissionDenied(infoErr):
		ch.Status = model.SetupError
		ch.Summary = "Permission denied when talking to Docker"
		ch.Detail = intro + " Add yourself to the group with the command below, then log out and log back in " +
			"(or restart) so the change takes effect, and check again."
		ch.Fixes = permissionFixes()
	default:
		ch.Status = model.SetupMissing
		ch.Summary = "Checked once Docker is running"
		ch.Detail = intro
	}
	return ch
}

func permissionFixes() []model.SetupFix {
	return []model.SetupFix{
		{Kind: model.FixCommand, Label: "Add your user to the docker group (then log out and back in)", Command: "sudo usermod -aG docker $USER"},
		{Kind: model.FixCommand, Label: "Or apply it to the current terminal only", Command: "newgrp docker"},
		{Kind: model.FixLink, Label: "Docker's Linux post-install guide", URL: "https://docs.docker.com/engine/install/linux-postinstall/"},
	}
}

// ---------------------------------------------------------------------------
// helpers

func appName(app string) string {
	base := filepath.Base(strings.ReplaceAll(app, `\`, "/"))
	return strings.TrimSuffix(strings.TrimSuffix(base, ".app"), ".exe")
}

// shellQuote single-quotes s for a POSIX shell when it needs quoting.
func shellQuote(s string) string {
	if !strings.ContainsAny(s, " '\"\\$`!&*()[]{};<>?|~#") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
