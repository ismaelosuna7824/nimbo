package setup

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestAugmentPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	exists := func(d string) bool { return d != "/missing" }
	got := augmentPath(strings.Join([]string{"/usr/bin", "/bin", "", "/usr/sbin", "/bin/"}, sep),
		[]string{"/usr/local/bin", "/missing", "/opt/homebrew/bin", "/usr/bin", "/usr/local/bin"}, exists)
	want := strings.Join([]string{"/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin", "/usr/sbin"}, sep)
	if got != want {
		t.Fatalf("augmentPath = %q, want %q", got, want)
	}
	// A shell user's PATH keeps its order; nothing is added twice.
	shell := strings.Join([]string{"/opt/homebrew/bin", "/Users/me/bin", "/usr/local/bin"}, sep)
	if got := augmentPath(shell, []string{"/usr/local/bin", "/opt/homebrew/bin"}, exists); got != shell {
		t.Fatalf("augmentPath(shell) = %q, want unchanged", got)
	}
}

func TestWellKnownDirsPerOS(t *testing.T) {
	mac := wellKnownDirs("darwin", "/Users/me")
	for _, d := range []string{"/opt/homebrew/bin", "/Users/me/.orbstack/bin", "/Applications/Docker.app/Contents/Resources/bin", "/Users/me/.rd/bin"} {
		if !slices.Contains(mac, d) {
			t.Errorf("darwin dirs lack %s: %v", d, mac)
		}
	}
	if mac[0] != "/usr/local/bin" || mac[len(mac)-1] != "/usr/bin" {
		t.Errorf("darwin order = %v", mac)
	}
	if linux := wellKnownDirs("linux", "/home/me"); !slices.Contains(linux, "/snap/bin") || slices.Contains(linux, "/opt/homebrew/bin") {
		t.Errorf("linux dirs = %v", linux)
	}
	if win := wellKnownDirs("windows", `C:\Users\me`); win[0] != `C:\Program Files\Docker\Docker\resources\bin` {
		t.Errorf("windows dirs = %v", win)
	}
}

func TestAllRequiredOK(t *testing.T) {
	checks := []model.SetupCheck{
		{ID: "a", Required: true, Status: model.SetupOK},
		{ID: "b", Required: false, Status: model.SetupWarning},
		{ID: "c", Required: false, Status: model.SetupMissing},
	}
	if !allRequiredOK(checks) {
		t.Fatal("optional failures must not block readiness")
	}
	for _, s := range []model.SetupCheckStatus{model.SetupMissing, model.SetupWarning, model.SetupError} {
		checks[0].Status = s
		if allRequiredOK(checks) {
			t.Fatalf("required check %s reported ready", s)
		}
	}
}

func TestDetectFlavor(t *testing.T) {
	for _, tc := range []struct {
		name, version, host, os, infoName, want string
	}{
		{"orbstack info", "Docker version 29.8.2, build 7fc2dff", "unix:///Users/me/.orbstack/run/docker.sock", "OrbStack", "orbstack", FlavorOrbStack},
		{"orbstack context only", "Docker version 29.8.2, build 7fc2dff", "unix:///Users/me/.orbstack/run/docker.sock", "", "", FlavorOrbStack},
		{"docker desktop mac", "Docker version 28.3.0, build 38b7060", "unix:///Users/me/.docker/run/docker.sock", "Docker Desktop", "docker-desktop", FlavorDockerDesktop},
		{"docker desktop windows", "Docker version 28.3.0, build 38b7060", "npipe:////./pipe/dockerDesktopLinuxEngine", "", "", FlavorDockerDesktop},
		{"docker desktop default socket", "Docker version 28.3.0", "unix:///var/run/docker.sock", "Docker Desktop", "docker-desktop", FlavorDockerDesktop},
		{"rancher", "Docker version 27.5.1-rd, build 0c97515", "unix:///Users/me/.rd/docker.sock", "Rancher Desktop WSL Distribution", "lima-rancher-desktop", FlavorRancher},
		{"colima", "Docker version 27.4.0", "unix:///Users/me/.colima/default/docker.sock", "Ubuntu 24.04.1 LTS", "colima", FlavorColima},
		{"podman alias", "podman version 5.2.1", "", "", "", FlavorPodman},
		{"podman socket", "Docker version 27.0.3", "unix:///run/user/1000/podman/podman.sock", "fedora", "fedora", FlavorPodman},
		{"plain engine", "Docker version 27.0.3, build 7d4bcd8", "unix:///var/run/docker.sock", "Ubuntu 24.04 LTS", "myhost", ""},
	} {
		if got := detectFlavor(tc.version, tc.host, tc.os, tc.infoName); got != tc.want {
			t.Errorf("%s: flavor = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestParseCLIVersion(t *testing.T) {
	for line, want := range map[string]string{
		"Docker version 29.8.2, build 7fc2dff":    "29.8.2",
		"Docker version 27.5.1-rd, build 0c97515": "27.5.1-rd",
		"podman version 5.2.1":                    "5.2.1",
		"garbage":                                 "",
	} {
		if got := parseCLIVersion(line); got != want {
			t.Errorf("parseCLIVersion(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestPullTracker(t *testing.T) {
	out := []string{
		"latest: Pulling from hashicorp/terraform",
		"f18232174bc9: Already exists",
		"4f4fb700ef54: Pulling fs layer",
		"a1b2c3d4e5f6: Pulling fs layer",
		"a1b2c3d4e5f6: Waiting",
		"4f4fb700ef54: Download complete",
		"a1b2c3d4e5f6: Downloading",
		"a1b2c3d4e5f6: Verifying Checksum",
		"4f4fb700ef54: Pull complete",
		"a1b2c3d4e5f6: Download complete",
		"a1b2c3d4e5f6: Pull complete",
		"a1b2c3d4e5f6: Pull complete",
		"Digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"Status: Downloaded newer image for hashicorp/terraform:latest",
		"docker.io/hashicorp/terraform:latest",
	}
	tr := newPullTracker()
	type snap struct{ done, total int }
	var snaps []snap
	for _, l := range out {
		if tr.line(l) {
			d, n := tr.progress()
			snaps = append(snaps, snap{d, n})
		}
	}
	want := []snap{{1, 1}, {1, 2}, {1, 3}, {2, 3}, {3, 3}}
	if !slices.Equal(snaps, want) {
		t.Fatalf("progress = %v, want %v", snaps, want)
	}
}

func TestParseOSRelease(t *testing.T) {
	name, id := parseOSRelease(strings.NewReader("NAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nID=ubuntu\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\n# comment\n"))
	if name != "Ubuntu 24.04.1 LTS" || id != "ubuntu" {
		t.Fatalf("ubuntu = %q %q", name, id)
	}
	name, id = parseOSRelease(strings.NewReader("NAME=Fedora Linux\nVERSION_ID=41\nID=fedora\n"))
	if name != "Fedora Linux 41" || id != "fedora" {
		t.Fatalf("fedora = %q %q", name, id)
	}
}

func TestWindowsName(t *testing.T) {
	for ver, want := range map[string]string{
		"\r\nMicrosoft Windows [Version 10.0.22631.4317]\r\n": "Windows 11",
		"Microsoft Windows [Version 10.0.19045.5011]":         "Windows 10",
		"nothing": "Windows",
	} {
		if got := windowsName(ver); got != want {
			t.Errorf("windowsName(%q) = %q, want %q", ver, got, want)
		}
	}
}

func TestDetectPackageManager(t *testing.T) {
	has := func(bins ...string) func(string) bool {
		return func(b string) bool { return slices.Contains(bins, b) }
	}
	for _, tc := range []struct {
		goos, distro string
		has          func(string) bool
		want         string
	}{
		{"darwin", "", has("brew"), "brew"},
		{"darwin", "", has(), ""},
		{"windows", "", has("choco", "winget"), "winget"},
		{"windows", "", has("choco"), "choco"},
		{"linux", "ubuntu", has(), "apt"},
		{"linux", "fedora", has("apt-get"), "dnf"},
		{"linux", "arch", has(), "pacman"},
		{"linux", "opensuse-tumbleweed", has(), "zypper"},
		{"linux", "someos", has("zypper"), "zypper"},
	} {
		if got := detectPackageManager(tc.goos, tc.distro, tc.has); got != tc.want {
			t.Errorf("%s/%s: %q, want %q", tc.goos, tc.distro, got, tc.want)
		}
	}
}

// fixText flattens fixes for assertions.
func fixText(fixes []model.SetupFix) string {
	var b strings.Builder
	for _, f := range fixes {
		b.WriteString(f.Kind + "|" + f.Label + "|" + f.URL + "|" + f.Command + "\n")
	}
	return b.String()
}

func TestInstallFixesPerPlatform(t *testing.T) {
	for _, tc := range []struct {
		name    string
		p       Platform
		want    []string
		notWant []string
	}{
		{"mac apple silicon with brew", Platform{GOOS: "darwin", Arch: "arm64", PackageManager: "brew"},
			[]string{"orbstack.dev/download", "brew install --cask orbstack", "mac/main/arm64/Docker.dmg", "Apple silicon", "brew install --cask docker-desktop"},
			[]string{"winget", "apt-get", "amd64"}},
		{"mac intel without brew", Platform{GOOS: "darwin", Arch: "amd64"},
			[]string{"mac/main/amd64/Docker.dmg", "Intel chip"},
			[]string{"brew install"}},
		{"windows winget", Platform{GOOS: "windows", Arch: "amd64", PackageManager: "winget"},
			[]string{"win/main/amd64/Docker%20Desktop%20Installer.exe", "winget install -e --id Docker.DockerDesktop", "windows-install"},
			[]string{"brew", "choco", "sudo"}},
		{"windows choco", Platform{GOOS: "windows", Arch: "arm64", PackageManager: "choco"},
			[]string{"win/main/arm64/", "choco install -y docker-desktop"},
			[]string{"winget"}},
		{"ubuntu", Platform{GOOS: "linux", Distro: "ubuntu", PackageManager: "apt"},
			[]string{"get.docker.com", "apt-get install -y docker.io", linuxPostInstall, "engine/install/ubuntu/"},
			[]string{"brew", "winget", "pacman", "dnf"}},
		{"fedora", Platform{GOOS: "linux", Distro: "fedora", PackageManager: "dnf"},
			[]string{"get.docker.com", "dnf install -y moby-engine", "engine/install/fedora/"},
			[]string{"apt-get"}},
		{"arch", Platform{GOOS: "linux", Distro: "arch", PackageManager: "pacman"},
			[]string{"pacman -S --needed docker", "wiki.archlinux.org", linuxPostInstall},
			[]string{"get.docker.com", "apt-get"}},
		{"opensuse", Platform{GOOS: "linux", Distro: "opensuse-tumbleweed", PackageManager: "zypper"},
			[]string{"zypper install -y docker", "en.opensuse.org"},
			[]string{"get.docker.com"}},
		{"unknown linux", Platform{GOOS: "linux", Distro: "mystery"},
			[]string{"get.docker.com", "docs.docker.com/engine/install/"},
			nil},
	} {
		got := fixText(installFixes(tc.p))
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: fixes lack %q:\n%s", tc.name, w, got)
			}
		}
		for _, w := range tc.notWant {
			if strings.Contains(got, w) {
				t.Errorf("%s: fixes contain %q:\n%s", tc.name, w, got)
			}
		}
	}
}

func TestLinuxDaemonAndPermissions(t *testing.T) {
	c := &Checker{plat: Platform{GOOS: "linux", Distro: "ubuntu"}}
	cli := cliInfo{bin: "/usr/bin/docker"}
	denied := errors.New("permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock")

	daemon := c.daemonCheck(cli, nil, denied, "")
	perm := c.permissionsCheck(cli, nil, denied)
	if daemon.Status != model.SetupMissing || len(daemon.Fixes) != 0 {
		t.Errorf("daemon on permission denied = %+v", daemon)
	}
	if perm.Status != model.SetupError || !strings.Contains(fixText(perm.Fixes), "sudo usermod -aG docker $USER") {
		t.Errorf("permissions = %+v", perm)
	}

	down := errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?")
	daemon = c.daemonCheck(cli, nil, down, "")
	if got := fixText(daemon.Fixes); daemon.Status != model.SetupMissing || !strings.Contains(got, "command|") ||
		!strings.Contains(got, "sudo systemctl enable --now docker") || strings.Contains(got, "action|") {
		t.Errorf("linux daemon down fixes = %s", got)
	}
	if !strings.Contains(daemon.Detail, "service") {
		t.Errorf("linux daemon detail = %q", daemon.Detail)
	}
	if perm := c.permissionsCheck(cli, nil, down); perm.Status != model.SetupMissing || len(perm.Fixes) != 0 {
		t.Errorf("permissions while daemon down = %+v", perm)
	}
	if perm := c.permissionsCheck(cli, &dockerInfo{ServerVersion: "27.0"}, nil); perm.Status != model.SetupOK {
		t.Errorf("permissions when docker answers = %+v", perm)
	}
}

func TestStartAdviceMatchesOS(t *testing.T) {
	if s := startAdvice("darwin", FlavorDockerDesktop); !strings.Contains(s, "menu bar") {
		t.Errorf("darwin: %q", s)
	}
	if s := startAdvice("windows", FlavorDockerDesktop); !strings.Contains(s, "system tray") || !strings.Contains(s, "Start menu") {
		t.Errorf("windows: %q", s)
	}
	if s := startAdvice("linux", ""); strings.Contains(s, "menu bar") || strings.Contains(s, "tray") {
		t.Errorf("linux: %q", s)
	}
}

func TestPullCommand(t *testing.T) {
	imgs := requiredImages(model.Settings{})
	if got := pullCommand("darwin", imgs); got != "docker pull floci/floci:latest && docker pull hashicorp/terraform:latest && docker pull alpine:3" {
		t.Errorf("darwin = %q", got)
	}
	if got := pullCommand("windows", imgs); strings.Contains(got, "&&") {
		t.Errorf("windows = %q", got)
	}
}
