package setup

import (
	"regexp"
	"strings"
)

// Docker engine flavors reported in summaries and used to tailor advice.
const (
	FlavorDockerDesktop = "Docker Desktop"
	FlavorOrbStack      = "OrbStack"
	FlavorRancher       = "Rancher Desktop"
	FlavorColima        = "Colima"
	FlavorPodman        = "Podman"
)

// detectFlavor names the engine behind the docker CLI from `docker
// --version`, the current context's endpoint host and, when the daemon
// answered, `docker info`'s OperatingSystem and Name. It returns "" for a
// plain Docker Engine or an unknown setup.
func detectFlavor(versionLine, contextHost, infoOS, infoName string) string {
	v := strings.ToLower(versionLine)
	host := strings.ToLower(contextHost)
	osName := strings.ToLower(infoOS)
	name := strings.ToLower(infoName)
	switch {
	case strings.Contains(v, "podman") || strings.Contains(host, "podman") || strings.Contains(osName, "podman"):
		return FlavorPodman
	case strings.Contains(osName, "orbstack") || strings.Contains(host, "/.orbstack/") || name == "orbstack":
		return FlavorOrbStack
	case strings.Contains(host, "/.rd/") || strings.Contains(name, "rancher-desktop"):
		return FlavorRancher
	case strings.Contains(host, "/.colima/") || name == "colima" || strings.HasPrefix(name, "colima-"):
		return FlavorColima
	case strings.Contains(osName, "docker desktop") || name == "docker-desktop" ||
		strings.Contains(host, "/.docker/run/docker.sock") || strings.Contains(host, "dockerdesktop"):
		return FlavorDockerDesktop
	}
	return ""
}

var versionRe = regexp.MustCompile(`(?i)version\s+v?([0-9]+(?:\.[0-9]+)*[0-9A-Za-z.+-]*)`)

// parseCLIVersion extracts the version from `docker --version` output such
// as "Docker version 29.8.2, build 7fc2dff" or "podman version 5.2.1".
func parseCLIVersion(line string) string {
	if m := versionRe.FindStringSubmatch(line); m != nil {
		return strings.TrimSuffix(m[1], ",")
	}
	return ""
}
