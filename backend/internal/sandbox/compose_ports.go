package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// prober answers host port questions; tests replace it.
type prober interface {
	// free reports whether proto port can be bound on hostIP ("" = all
	// addresses) and nothing answers on it at 127.0.0.1.
	free(hostIP string, port int, proto string) bool
	// pick returns a free port that is not in used.
	pick(hostIP, proto string, used map[string]bool) (int, error)
	// holder names the process listening on the port ("" = unknown).
	holder(port int, proto string) string
}

func portKey(proto string, port int) string { return proto + "/" + strconv.Itoa(port) }

// ownPorts are the host ports the project's own containers publish: an up
// recreating them frees and takes them again.
func ownPorts(cs []inspected) map[string]bool {
	own := map[string]bool{}
	for _, c := range cs {
		for _, p := range c.container().Ports {
			own[portKey(p.Protocol, p.Host)] = true
		}
	}
	return own
}

type portPlan struct {
	kept      []model.PortRemap    // saved remaps still needed (original port busy, remap port usable)
	conflicts []model.PortConflict // busy ports without a usable remap, with a suggested port
}

// remaps is the remap set of the run: kept ones, plus the suggestions for
// the conflicts when remap is set.
func (p portPlan) remaps(remap bool) []model.PortRemap {
	out := append([]model.PortRemap{}, p.kept...)
	if remap {
		for _, c := range p.conflicts {
			if c.SuggestedPort != 0 {
				out = append(out, model.PortRemap{Service: c.Service, ContainerPort: c.ContainerPort, Protocol: c.Protocol, HostPort: c.HostPort, Port: c.SuggestedPort})
			}
		}
	}
	slices.SortFunc(out, func(a, b model.PortRemap) int {
		if a.Service != b.Service {
			return strings.Compare(a.Service, b.Service)
		}
		return a.HostPort - b.HostPort
	})
	return out
}

// planPorts checks every fixed published port of cfg. A port is fine when
// it is free or held by the project itself; otherwise a saved remap for it
// is kept when its port is usable, else the port is a conflict. A remap is
// dropped as soon as its original port is free again.
func planPorts(cfg *composeConfig, own map[string]bool, saved []model.PortRemap, pr prober) portPlan {
	var plan portPlan
	used := map[string]bool{}
	usable := func(hostIP string, port int, proto string) bool {
		return !used[portKey(proto, port)] && (own[portKey(proto, port)] || pr.free(hostIP, port, proto))
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
		for _, p := range cfg.Services[name].Ports {
			hp, proto := p.fixedPort(), p.proto()
			if hp == 0 {
				continue
			}
			if usable(p.HostIP, hp, proto) {
				used[portKey(proto, hp)] = true
				continue
			}
			i := slices.IndexFunc(saved, func(r model.PortRemap) bool {
				return r.Service == name && r.ContainerPort == p.Target && r.Protocol == proto && r.HostPort == hp
			})
			if i >= 0 && usable(p.HostIP, saved[i].Port, proto) {
				used[portKey(proto, saved[i].Port)] = true
				plan.kept = append(plan.kept, saved[i])
				continue
			}
			c := model.PortConflict{Service: name, ContainerPort: p.Target, Protocol: proto, HostIP: p.HostIP, HostPort: hp, Holder: pr.holder(hp, proto)}
			if port, err := pr.pick(p.HostIP, proto, used); err == nil {
				c.SuggestedPort = port
				used[portKey(proto, port)] = true
			}
			plan.conflicts = append(plan.conflicts, c)
		}
	}
	return plan
}

// ComposePorts is the host port pre-check of an up, on the workflow's files
// (SourceDir) as they are: conflicts and the remaps that would be kept.
func (m *Manager) ComposePorts(ctx context.Context, wfID, sourceDir string, files, profiles []string) (model.ComposePorts, error) {
	if err := checkID(wfID); err != nil {
		return model.ComposePorts{}, err
	}
	if err := m.composeAvailable(ctx); err != nil {
		return model.ComposePorts{}, err
	}
	cmd, err := m.composeCommand(wfID, sourceDir, files, profiles)
	if err != nil {
		return model.ComposePorts{}, err
	}
	cfg, err := m.composeConfig(ctx, cmd)
	if err != nil {
		return model.ComposePorts{}, err
	}
	cs, err := m.composeContainers(ctx, wfID)
	if err != nil {
		return model.ComposePorts{}, err
	}
	plan := planPorts(cfg, ownPorts(cs), loadRemaps(m.composeDir(wfID)), portProber{})
	out := model.ComposePorts{Conflicts: plan.conflicts, Remaps: plan.remaps(false)}
	if out.Conflicts == nil {
		out.Conflicts = []model.PortConflict{}
	}
	return out, nil
}

func loadRemaps(work string) []model.PortRemap {
	data, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(composeRemapsFile)))
	if err != nil {
		return []model.PortRemap{}
	}
	var out []model.PortRemap
	if json.Unmarshal(data, &out) != nil || out == nil {
		return []model.PortRemap{}
	}
	return out
}

func saveRemaps(work string, remaps []model.PortRemap) error {
	if remaps == nil {
		remaps = []model.PortRemap{}
	}
	data, err := json.Marshal(remaps)
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(work, filepath.FromSlash(composeRemapsFile)), data)
}

// portProber probes the real host.
type portProber struct{}

func bindHost(hostIP string) string {
	if hostIP == "0.0.0.0" {
		return ""
	}
	return hostIP
}

func (portProber) free(hostIP string, port int, proto string) bool {
	addr := net.JoinHostPort(bindHost(hostIP), strconv.Itoa(port))
	if proto == "udp" {
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			return false
		}
		pc.Close()
		return true
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	ln.Close()
	// BSD sockets let a wildcard bind succeed next to a listener on one
	// address (e.g. a dev server on 127.0.0.1): ask the loopback too.
	if hostReachable(hostIP) {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond); err == nil {
			c.Close()
			return false
		}
	}
	return true
}

// firstRemapPort is where remap ports start: the IANA dynamic range, which no
// service publishes on purpose.
const firstRemapPort = 49152

// pick returns the lowest free port of the dynamic range not in used. Lowest,
// not whatever the kernel hands out, so the port the pre-check of an up
// suggests is the one the up then publishes on.
func (pp portProber) pick(hostIP, proto string, used map[string]bool) (int, error) {
	for port := firstRemapPort; port <= 65535; port++ {
		if !used[portKey(proto, port)] && pp.free(hostIP, port, proto) {
			return port, nil
		}
	}
	return 0, errors.New("no free port found")
}

// holder asks lsof (macOS, Linux) which process listens on the port. Only
// the user's own processes are visible; Docker-published ports show as the
// Docker engine's process.
func (portProber) holder(port int, proto string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	bin, err := exec.LookPath("lsof")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	args := []string{"-nP", "-Fc", fmt.Sprintf("-i%s:%d", strings.ToUpper(proto), port)}
	if proto != "udp" {
		args = append(args, "-sTCP:LISTEN")
	}
	out, _ := exec.CommandContext(ctx, bin, args...).Output()
	return describeHolder(string(out))
}

// describeHolder turns `lsof -Fc` output into a process name; Docker
// engines' port forwarders become "a Docker container (<engine>)".
func describeHolder(lsof string) string {
	for line := range strings.SplitSeq(lsof, "\n") {
		name, ok := strings.CutPrefix(strings.TrimSpace(line), "c")
		if !ok || name == "" {
			continue
		}
		lower := strings.ToLower(name)
		for _, engine := range []string{"docker", "orbstack", "vpnkit", "gvproxy", "limactl", "colima", "rancher", "podman"} {
			if strings.Contains(lower, engine) {
				return "a Docker container (" + name + ")"
			}
		}
		return name
	}
	return ""
}
