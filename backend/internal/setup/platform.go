package setup

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Platform describes the machine the checks tailor their advice to.
type Platform struct {
	GOOS           string // darwin | linux | windows
	Arch           string // runtime.GOARCH: arm64 | amd64 | …
	OSName         string // human name, e.g. "macOS 26.0", "Ubuntu 24.04 LTS", "Windows 11"
	Distro         string // Linux os-release ID (ubuntu, debian, fedora, arch, …); "" elsewhere
	PackageManager string // brew | apt | dnf | pacman | zypper | winget | choco | ""
}

// DetectPlatform inspects the running machine.
func DetectPlatform() Platform {
	p := Platform{GOOS: runtime.GOOS, Arch: runtime.GOARCH}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	switch p.GOOS {
	case "darwin":
		p.OSName = "macOS"
		if out, err := exec.CommandContext(ctx, "sw_vers", "-productVersion").Output(); err == nil {
			p.OSName += " " + strings.TrimSpace(string(out))
		}
	case "linux":
		if f, err := os.Open("/etc/os-release"); err == nil {
			p.OSName, p.Distro = parseOSRelease(f)
			f.Close()
		}
		if p.OSName == "" {
			p.OSName = "Linux"
		}
	case "windows":
		p.OSName = "Windows"
		if out, err := exec.CommandContext(ctx, "cmd", "/c", "ver").Output(); err == nil {
			p.OSName = windowsName(string(out))
		}
	default:
		p.OSName = p.GOOS
	}
	p.PackageManager = detectPackageManager(p.GOOS, p.Distro, func(name string) bool {
		_, err := exec.LookPath(name)
		return err == nil
	})
	return p
}

// parseOSRelease returns PRETTY_NAME (or NAME VERSION_ID) and ID from an
// /etc/os-release file.
func parseOSRelease(r io.Reader) (name, id string) {
	vals := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		if uq, err := strconv.Unquote(v); err == nil {
			v = uq
		} else {
			v = strings.Trim(v, `'"`)
		}
		vals[k] = v
	}
	name = vals["PRETTY_NAME"]
	if name == "" {
		name = strings.TrimSpace(vals["NAME"] + " " + vals["VERSION_ID"])
	}
	return name, strings.ToLower(vals["ID"])
}

var winBuildRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// windowsName maps `ver` output ("Microsoft Windows [Version
// 10.0.22631.4317]") to "Windows 11" (build ≥ 22000) or "Windows 10".
func windowsName(ver string) string {
	m := winBuildRe.FindStringSubmatch(ver)
	if m == nil {
		return "Windows"
	}
	major, _ := strconv.Atoi(m[1])
	build, _ := strconv.Atoi(m[3])
	switch {
	case major == 10 && build >= 22000:
		return "Windows 11"
	case major == 10:
		return "Windows 10"
	}
	return "Windows " + m[1] + "." + m[2]
}

// distroPackageManagers maps os-release IDs to their package manager.
var distroPackageManagers = map[string]string{
	"ubuntu": "apt", "debian": "apt", "linuxmint": "apt", "pop": "apt", "elementary": "apt", "raspbian": "apt", "kali": "apt",
	"fedora": "dnf", "rhel": "dnf", "centos": "dnf", "rocky": "dnf", "almalinux": "dnf",
	"arch": "pacman", "manjaro": "pacman", "endeavouros": "pacman",
	"opensuse-leap": "zypper", "opensuse-tumbleweed": "zypper", "sles": "zypper",
}

// detectPackageManager picks the package manager users of goos/distro
// install software with; has reports whether a command exists.
func detectPackageManager(goos, distro string, has func(string) bool) string {
	switch goos {
	case "darwin":
		if has("brew") {
			return "brew"
		}
	case "windows":
		for _, pm := range []string{"winget", "choco"} {
			if has(pm) {
				return pm
			}
		}
	case "linux":
		if pm, ok := distroPackageManagers[distro]; ok {
			return pm
		}
		for _, c := range []struct{ bin, pm string }{{"apt-get", "apt"}, {"dnf", "dnf"}, {"pacman", "pacman"}, {"zypper", "zypper"}} {
			if has(c.bin) {
				return c.pm
			}
		}
	}
	return ""
}
