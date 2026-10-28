package compose

import (
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Rules for the recommendations of the CIS Docker Benchmark that a compose
// project shows and the rules in rules.go don't decide on their own
// (backend/internal/compliance maps the benchmark onto both). The ones that
// would flag most ordinary services (read-only root, dropped NET_RAW, pids
// limit, interface-bound ports) are opt-in so Problems stays quiet; the
// compliance view still runs them.

const docsRun = "https://docs.docker.com/reference/cli/docker/container/run/"

var cisRules = []Rule{
	rule("dk_cap_add", "Dangerous added capabilities", catSecurity, model.SeverityWarning,
		"Services should not cap_add ALL, SYS_ADMIN, NET_ADMIN, SYS_PTRACE or SYS_MODULE (CIS Docker 5.4).", "https://docs.docker.com/engine/containers/run/#runtime-privilege-and-linux-capabilities",
		"These capabilities are nearly root on the host: SYS_ADMIN mounts filesystems, SYS_MODULE loads kernel modules, SYS_PTRACE reads other processes' memory, NET_ADMIN rewrites the host's network when combined with host networking."),
	optIn(rule("dk_net_raw", "NET_RAW dropped", catSecurity, model.SeverityInfo,
		"Services should drop the NET_RAW capability (cap_drop: [ALL] or [NET_RAW]) unless they need raw sockets (CIS Docker 5.4).", "https://docs.docker.com/engine/containers/run/#runtime-privilege-and-linux-capabilities",
		"NET_RAW is in Docker's default capability set: a compromised container can craft raw packets to spoof traffic or poison ARP on its network.")),
	optIn(rule("dk_read_only", "Read-only root filesystem", catSecurity, model.SeverityInfo,
		"Services should run with read_only: true, writing only to volumes and tmpfs mounts (CIS Docker 5.13).", docsComposeSvc+"#read_only",
		"A writable root filesystem lets an attacker drop tools or change the app's own files; read-only keeps the container as the image built it.")),
	optIn(rule("dk_no_new_privileges", "No new privileges", catSecurity, model.SeverityInfo,
		"Services should set security_opt: [\"no-new-privileges:true\"] (CIS Docker 5.26).", docsRun+"#security-opt",
		"Without it a process can gain privileges through setuid or setgid binaries in the image (su, sudo, ping).")),
	rule("dk_apparmor_unconfined", "AppArmor stays on", catSecurity, model.SeverityWarning,
		"Services should not set security_opt apparmor=unconfined (CIS Docker 5.2).", "https://docs.docker.com/engine/security/apparmor/",
		"On hosts with AppArmor, Docker confines every container with its docker-default profile; unconfined removes that layer of protection."),
	rule("dk_selinux_disabled", "SELinux labels stay on", catSecurity, model.SeverityWarning,
		"Services should not set security_opt label=disable (CIS Docker 5.3).", docsRun+"#security-opt",
		"On SELinux hosts, labels keep a container from reading or writing other containers' and the host's files; label=disable switches that separation off."),
	rule("dk_seccomp_unconfined", "seccomp stays on", catSecurity, model.SeverityWarning,
		"Services should not set security_opt seccomp=unconfined (CIS Docker 5.22).", "https://docs.docker.com/engine/security/seccomp/",
		"Docker's default seccomp profile blocks about 44 system calls containers don't need (kernel module loading, keyring, clock changes); unconfined exposes all of them to an attacker."),
	rule("dk_host_dirs", "Sensitive host directories", catSecurity, model.SeverityWarning,
		"Services should not bind-mount /, /boot, /dev, /etc, /lib, /lib64, /proc, /sys or /usr, and never anything under them read-write (CIS Docker 5.6).", "https://docs.docker.com/engine/storage/bind-mounts/",
		"These directories hold the host's configuration, credentials, kernel interfaces and binaries: read-write access lets the container change the host, read access leaks its secrets."),
	rule("dk_host_ipc", "Host IPC namespace", catSecurity, model.SeverityWarning,
		"Services should not set ipc: host (CIS Docker 5.17).", docsComposeSvc+"#ipc",
		"Sharing the host's IPC namespace exposes its shared memory and semaphores: the container can read or corrupt other processes' data."),
	rule("dk_host_uts", "Host UTS namespace", catSecurity, model.SeverityWarning,
		"Services should not set uts: host (CIS Docker 5.21).", docsComposeSvc+"#uts",
		"Sharing the host's UTS namespace lets the container change the host's hostname."),
	rule("dk_host_userns", "Host user namespace", catSecurity, model.SeverityWarning,
		"Services should not set userns_mode: host (CIS Docker 5.31).", docsComposeSvc+"#userns_mode",
		"When the daemon remaps users, userns_mode: host opts the container out: its root is the host's root again."),
	rule("dk_devices", "Host devices", catSecurity, model.SeverityInfo,
		"Services should not map host devices (devices:) unless they need them, with the narrowest permissions (CIS Docker 5.18).", docsComposeSvc+"#devices",
		"A mapped device is reachable without privileged mode: a block device gives raw access to the host's disk."),
	rule("dk_mount_propagation", "Shared mount propagation", catSecurity, model.SeverityWarning,
		"Bind mounts should not use shared or rshared propagation (CIS Docker 5.20).", "https://docs.docker.com/engine/storage/bind-mounts/#configure-bind-propagation",
		"With shared propagation, mounts the container makes appear on the host and in every other container sharing the mount."),
	optIn(rule("dk_pids_limit", "Process limits", catCompose, model.SeverityInfo,
		"Services should cap their number of processes (pids_limit or deploy.resources.limits.pids) (CIS Docker 5.29).", docsComposeSvc+"#pids_limit",
		"Without a limit a fork bomb or a runaway worker pool exhausts the host's process table and takes every container down.")),
	rule("dk_default_bridge", "Default bridge network", catSecurity, model.SeverityInfo,
		"Services should use the project's networks instead of network_mode: bridge (Docker's default docker0 bridge) (CIS Docker 5.30).", "https://docs.docker.com/engine/network/drivers/bridge/#differences-between-user-defined-bridges-and-the-default-bridge",
		"Every container started without a network shares docker0, which applies no filtering: containers of unrelated projects reach each other and are exposed to ARP spoofing."),
	optIn(rule("dk_port_interface", "Ports bound to an interface", catSecurity, model.SeverityInfo,
		"Published ports should name the host interface they listen on (\"127.0.0.1:8080:8080\") (CIS Docker 5.14).", docsComposeSvc+"#ports",
		"A port without a host IP listens on every interface of the host, including ones not screened by the firewall or load balancer meant for incoming traffic.")),
	optIn(rule("dk_privileged_port", "Privileged host ports", catSecurity, model.SeverityInfo,
		"Services should not publish host ports below 1024 (except a load balancer's 80/443) (CIS Docker 5.8).", docsComposeSvc+"#ports",
		"Ports below 1024 are reserved for system services: a container publishing one can impersonate them.")),
	rule("dk_update_alone", "Package updates with the install", catBuild, model.SeverityInfo,
		"Package index updates and upgrades (apt-get update, apk update, yum update…) should run in the same RUN as the install (CIS Docker 4.7).", docsBestPractices+"#apt-get",
		"A RUN that only updates is cached as its own layer: later builds reuse that stale index, so new installs miss security fixes or fail to find packages."),
	optIn(rule("dk_add_any", "COPY instead of any ADD", catBuild, model.SeverityInfo,
		"Dockerfiles should use COPY, not ADD, even for archives and checksummed downloads (CIS Docker 4.9).", docsBestPractices+"#add-or-copy",
		"ADD fetches URLs and unpacks archives as it copies: downloaded or decompressed content lands in the image without being scanned.")),
}

func init() {
	// ruleByCode is built before init functions run.
	for _, r := range cisRules {
		rules = append(rules, r)
		ruleByCode[r.Code] = r
	}
}

// sensitiveHostDirs are the host directories CIS Docker 5.6 lists.
var sensitiveHostDirs = []string{"/", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/sys", "/usr"}

// capName normalizes a capability (cap_sys_admin, SYS_ADMIN).
func capName(c string) string {
	return strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(c)), "CAP_")
}

func hasCap(list []string, names ...string) bool {
	for _, c := range list {
		for _, n := range names {
			if capName(c) == n {
				return true
			}
		}
	}
	return false
}

// securityOpt splits a security_opt entry: "seccomp=unconfined" and the
// older "seccomp:unconfined" both work; "no-new-privileges" has no value.
func securityOpt(o string) (key, value string) {
	i := strings.IndexAny(o, "=:")
	if i < 0 {
		return strings.ToLower(o), ""
	}
	return strings.ToLower(o[:i]), strings.ToLower(o[i+1:])
}

func noNewPrivileges(opts []string) bool {
	for _, o := range opts {
		if k, v := securityOpt(o); k == "no-new-privileges" && (v == "" || v == "true") {
			return true
		}
	}
	return false
}

// cisService checks one service's runtime settings.
func (p *Project) cisService(name string, s types.ServiceConfig) {
	id := ServiceID(name)

	// Capabilities.
	for _, c := range s.CapAdd {
		switch capName(c) {
		case "ALL", "SYS_ADMIN", "NET_ADMIN", "SYS_PTRACE", "SYS_MODULE":
			file, line := p.refLine(name, "cap_add", c)
			p.diag(model.SeverityWarning, "dk_cap_add", file, line, id, fmt.Sprintf("%s adds the %s capability", name, c),
				"Remove it; add only the narrow capability the process needs (e.g. NET_BIND_SERVICE to bind ports below 1024).")
		}
	}
	switch {
	case s.Privileged || hasCap(s.CapAdd, "NET_RAW", "ALL"):
		file, line := p.fieldLine(name, "cap_add")
		if s.Privileged {
			file, line = p.fieldLine(name, "privileged")
		}
		p.diag(model.SeverityInfo, "dk_net_raw", file, line, id, fmt.Sprintf("%s has the NET_RAW capability", name),
			"Remove NET_RAW (or ALL, or privileged: true) unless the service sends raw packets, and add cap_drop: [ALL].")
	case !hasCap(s.CapDrop, "ALL", "NET_RAW"):
		file, line := p.fieldLine(name, "cap_drop")
		p.diag(model.SeverityInfo, "dk_net_raw", file, line, id, fmt.Sprintf("%s keeps Docker's default NET_RAW capability", name),
			"Add cap_drop: [ALL] (then cap_add: only what it needs), or at least cap_drop: [NET_RAW].")
	}

	// Filesystem and privilege escalation.
	if !s.ReadOnly {
		file, line := p.fieldLine(name, "read_only")
		p.diag(model.SeverityInfo, "dk_read_only", file, line, id, fmt.Sprintf("%s's root filesystem is writable", name),
			"Add read_only: true, with tmpfs: [/tmp] (and volumes) for the paths it writes.")
	}
	if !noNewPrivileges(s.SecurityOpt) {
		file, line := p.fieldLine(name, "security_opt")
		p.diag(model.SeverityInfo, "dk_no_new_privileges", file, line, id, fmt.Sprintf("%s can gain privileges through setuid binaries", name),
			"Add security_opt: [\"no-new-privileges:true\"].")
	}

	// Kernel security profiles switched off.
	for _, o := range s.SecurityOpt {
		code, what := "", ""
		switch k, v := securityOpt(o); {
		case k == "seccomp" && v == "unconfined":
			code, what = "dk_seccomp_unconfined", "without Docker's seccomp profile"
		case k == "apparmor" && v == "unconfined":
			code, what = "dk_apparmor_unconfined", "without an AppArmor profile"
		case k == "label" && v == "disable":
			code, what = "dk_selinux_disabled", "without SELinux labels"
		default:
			continue
		}
		file, line := p.refLine(name, "security_opt", o)
		p.diag(model.SeverityWarning, code, file, line, id, fmt.Sprintf("%s runs %s (security_opt: %s)", name, what, o),
			fmt.Sprintf("Remove %s from security_opt (write a custom profile if the default one blocks something the app needs).", o))
	}

	// Host directories, mount propagation.
	for _, v := range s.Volumes {
		if v.Type != types.VolumeTypeBind || v.Source == "" {
			continue
		}
		if v.Bind != nil && (v.Bind.Propagation == types.PropagationShared || v.Bind.Propagation == types.PropagationRShared) {
			file, line := p.refLine(name, "volumes", v.Source)
			p.diag(model.SeverityWarning, "dk_mount_propagation", file, line, id,
				fmt.Sprintf("%s mounts %s with %s propagation", name, v.Source, v.Bind.Propagation),
				"Remove bind.propagation (the default rprivate keeps mounts inside the container).")
		}
		if r, err := filepath.Rel(p.Dir, v.Source); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			continue // the workflow's own files
		}
		src := path.Clean(filepath.ToSlash(v.Source))
		for _, d := range sensitiveHostDirs {
			exact := src == d
			if !exact && (d == "/" || !strings.HasPrefix(src, d+"/") || v.ReadOnly) {
				continue
			}
			sev, mode := model.SeverityWarning, "read-write"
			if v.ReadOnly {
				sev, mode = model.SeverityInfo, "read-only"
			}
			file, line := p.refLine(name, "volumes", v.Source)
			p.diag(sev, "dk_host_dirs", file, line, id, fmt.Sprintf("%s mounts the host's %s %s", name, src, mode),
				"Remove the mount; mount only the single file it needs, read-only (e.g. /etc/localtime:/etc/localtime:ro), and keep data in a named volume.")
			break
		}
	}

	// Host namespaces not covered by dk_privileged.
	hostNS := []struct {
		on          bool
		code, field string
	}{
		{s.Ipc == "host", "dk_host_ipc", "ipc"},
		{s.Uts == "host", "dk_host_uts", "uts"},
		{s.UserNSMode == "host", "dk_host_userns", "userns_mode"},
	}
	for _, h := range hostNS {
		if h.on {
			file, line := p.fieldLine(name, h.field)
			p.diag(model.SeverityWarning, h.code, file, line, id, fmt.Sprintf("%s shares the host's namespace (%s: host)", name, h.field),
				fmt.Sprintf("Remove %s: host.", h.field))
		}
	}
	if len(s.Devices) > 0 {
		var devs []string
		for _, d := range s.Devices {
			devs = append(devs, d.Source)
		}
		file, line := p.fieldLine(name, "devices")
		p.diag(model.SeverityInfo, "dk_devices", file, line, id, fmt.Sprintf("%s maps host devices: %s", name, strings.Join(devs, ", ")),
			"Remove devices: unless the service needs the hardware; map only that device, with read-only permissions when it only reads (/dev/x:/dev/x:r).")
	}

	// Processes.
	if s.PidsLimit <= 0 && (s.Deploy == nil || s.Deploy.Resources.Limits == nil || s.Deploy.Resources.Limits.Pids <= 0) {
		file, line := p.ServiceLine(name)
		p.diag(model.SeverityInfo, "dk_pids_limit", file, line, id, fmt.Sprintf("%s can start any number of processes", name),
			"Cap it: deploy: {resources: {limits: {pids: 200}}} (size it above the app's worker and thread count).")
	}

	// Networking.
	if s.NetworkMode == "bridge" {
		file, line := p.fieldLine(name, "network_mode")
		p.diag(model.SeverityInfo, "dk_default_bridge", file, line, id, fmt.Sprintf("%s runs on Docker's default bridge (network_mode: bridge)", name),
			"Remove network_mode: the service then joins the project's default network (or list networks: of your own).")
	}
	var allIfaces, low []string
	for _, port := range s.Ports {
		if port.Published == "" {
			continue
		}
		if allInterfaces(port.HostIP) {
			allIfaces = append(allIfaces, port.Published)
		}
		lo, _, _ := strings.Cut(port.Published, "-")
		if n, err := strconv.Atoi(lo); err == nil && n > 0 && n < 1024 {
			low = append(low, port.Published)
		}
	}
	if len(allIfaces) > 0 {
		file, line := p.fieldLine(name, "ports")
		p.diag(model.SeverityInfo, "dk_port_interface", file, line, id,
			fmt.Sprintf("%s publishes %s on every host interface", name, strings.Join(allIfaces, ", ")),
			fmt.Sprintf("Bind each port to the interface meant for that traffic, e.g. \"127.0.0.1:%s:…\" (or the host's public IP).", allIfaces[0]))
	}
	if len(low) > 0 {
		file, line := p.fieldLine(name, "ports")
		p.diag(model.SeverityInfo, "dk_privileged_port", file, line, id,
			fmt.Sprintf("%s publishes privileged host port %s", name, strings.Join(low, ", ")),
			"Publish a host port of 1024 or more (e.g. \"8080:80\") unless this is the host's load balancer serving 80/443.")
	}
}

// packageUpdates are the subcommands that refresh or upgrade packages, and
// installs the ones that install them, per package manager.
var (
	packageUpdates = map[string][]string{
		"apt-get": {"update", "upgrade", "dist-upgrade", "full-upgrade"}, "apt": {"update", "upgrade", "full-upgrade"},
		"apk": {"update", "upgrade"}, "yum": {"update", "upgrade", "makecache", "check-update"},
		"dnf": {"update", "upgrade", "makecache", "check-update"}, "microdnf": {"update", "upgrade", "makecache"},
		"zypper": {"refresh", "ref", "update", "up"},
	}
	packageInstalls = map[string][]string{
		"apt-get": {"install"}, "apt": {"install"}, "apk": {"add"}, "yum": {"install"}, "dnf": {"install"}, "microdnf": {"install"},
		"zypper": {"install", "in"},
	}
)

// updateAlone is the package update a RUN runs without installing anything
// in the same RUN ("" if none).
func updateAlone(segs [][]string) string {
	update := ""
	for _, w := range segs {
		pm := path.Base(w[0])
		has := func(subs []string) bool {
			for _, a := range w[1:] {
				for _, s := range subs {
					if a == s {
						return true
					}
				}
			}
			return false
		}
		if has(packageInstalls[pm]) {
			return ""
		}
		if update == "" && has(packageUpdates[pm]) {
			update = shortCmd(w)
		}
	}
	return update
}

// cisDockerfile checks the stages a service builds.
func (p *Project) cisDockerfile(name string, s types.ServiceConfig, rel string, seen map[string]bool) {
	stages, target := p.stages(s)
	if target < 0 {
		return
	}
	df := p.Dockerfiles[rel]
	add := func(code string, line int, msg, fix string) {
		k := code + "\x00" + rel + "\x00" + strconv.Itoa(line)
		if seen[k] {
			return
		}
		seen[k] = true
		p.diag(model.SeverityInfo, code, rel, line, ServiceID(name), msg, fix)
	}
	for _, i := range reachable(stages, target) {
		for _, ii := range stages[i].Instructions[1:] {
			in := &df.Instructions[ii]
			switch in.Cmd {
			case "ADD":
				add("dk_add_any", in.StartLine, fmt.Sprintf("%s:%d uses ADD", rel, in.StartLine),
					"Use COPY for local files; download with RUN curl and verify the checksum, and unpack archives with an explicit RUN tar.")
			case "RUN":
				if u := updateAlone(segments(runText(in))); u != "" {
					add("dk_update_alone", in.StartLine, fmt.Sprintf("%s:%d: %s runs without an install in the same RUN", rel, in.StartLine, u),
						"Update and install in one RUN: RUN apt-get update && apt-get install -y --no-install-recommends <package>=<version> && rm -rf /var/lib/apt/lists/* (apk add --no-cache needs no update).")
				}
			}
		}
	}
}
