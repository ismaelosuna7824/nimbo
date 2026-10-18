package sandbox

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Host ports of sandbox pods come from this range, one block of
// len(emulatorPorts()) consecutive ports per workflow, so URLs saved by the
// user (Postman, scripts, .env files) keep working across stop/start and
// pod re-creation.
const (
	portRangeLo = 45000
	portRangeHi = 49999
)

// sandboxFile is <home>/workflows/<id>/sandbox.json: the pod's host ports.
type sandboxFile struct {
	// Ports maps emulator (container) ports to 127.0.0.1 host ports.
	Ports map[int]int `json:"ports"`
}

func (m *Manager) sandboxFilePath(wfID string) string {
	return filepath.Join(m.wfDir(wfID), "sandbox.json")
}

// emulatorPorts lists the native emulator ports in cloud display order.
func emulatorPorts() []int {
	out := make([]int, 0, len(model.AllClouds))
	for _, c := range model.AllClouds {
		out = append(out, emulators[c].port)
	}
	return out
}

// savedPorts reads the host ports saved for wfID; nil when none are saved.
func (m *Manager) savedPorts(wfID string) map[int]int {
	data, err := os.ReadFile(m.sandboxFilePath(wfID))
	if err != nil {
		return nil
	}
	var f sandboxFile
	if json.Unmarshal(data, &f) != nil || len(f.Ports) == 0 {
		return nil
	}
	return f.Ports
}

func (m *Manager) savePorts(wfID string, ports map[int]int) error {
	data, err := json.MarshalIndent(sandboxFile{Ports: ports}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.wfDir(wfID), 0o755); err != nil {
		return err
	}
	tmp := m.sandboxFilePath(wfID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.sandboxFilePath(wfID))
}

// reservedPorts collects host ports saved by every other workflow, so a
// stopped sandbox keeps its ports even while they are not bound.
func (m *Manager) reservedPorts(wfID string) map[int]bool {
	out := map[int]bool{}
	paths, _ := filepath.Glob(filepath.Join(m.cfg.Home, "workflows", "*", "sandbox.json"))
	for _, p := range paths {
		if filepath.Base(filepath.Dir(p)) == wfID {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var f sandboxFile
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		for _, hp := range f.Ports {
			out[hp] = true
		}
	}
	return out
}

// portFree reports whether a TCP listener can bind 127.0.0.1:port now.
func portFree(port int) bool {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// allocatePorts assigns a host port to each native port. Saved ports are
// kept when usable (not reserved by another workflow and free); the rest
// come from the first fully usable block of consecutive ports, scanning the
// range from a block derived from the workflow ID; when only some saved
// ports are lost, replacements are taken one by one from that block on.
func allocatePorts(wfID string, native []int, saved map[int]int, reserved map[int]bool, free func(int) bool) (map[int]int, error) {
	usable := func(p int, taken map[int]bool) bool {
		return p >= portRangeLo && p <= portRangeHi && !reserved[p] && !taken[p] && free(p)
	}
	ports := map[int]int{}
	taken := map[int]bool{}
	var missing []int
	for _, np := range native {
		if hp, ok := saved[np]; ok && usable(hp, taken) {
			ports[np] = hp
			taken[hp] = true
			continue
		}
		missing = append(missing, np)
	}
	if len(missing) == 0 {
		return ports, nil
	}

	width := len(native)
	blocks := (portRangeHi - portRangeLo + 1) / width
	h := fnv.New32a()
	h.Write([]byte(wfID))
	start := int(h.Sum32() % uint32(blocks))
	// A fresh allocation takes a whole block; a partial one (some saved
	// ports kept) fills the gaps from the preferred block onwards.
	if len(missing) == width {
		for i := range blocks {
			base := portRangeLo + ((start+i)%blocks)*width
			ok := true
			for j := range width {
				if !usable(base+j, taken) {
					ok = false
					break
				}
			}
			if ok {
				for j, np := range native {
					ports[np] = base + j
				}
				return ports, nil
			}
		}
	}
	span := portRangeHi - portRangeLo + 1
	next := 0
	for _, np := range missing {
		found := false
		for ; next < span; next++ {
			p := portRangeLo + (start*width+next)%span
			if usable(p, taken) {
				ports[np] = p
				taken[p] = true
				found = true
				next++
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("no free host port in %d-%d for the sandbox", portRangeLo, portRangeHi)
		}
	}
	return ports, nil
}

// movedNotice is the status message shown when ports saved before differ
// from the ones the new pod got.
func movedNotice(before, after map[int]int) string {
	var parts []string
	for _, np := range emulatorPorts() {
		if b, ok := before[np]; ok && after[np] != b {
			parts = append(parts, fmt.Sprintf("%d → %d", b, after[np]))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Some sandbox ports were in use by another program and changed (" + strings.Join(parts, ", ") +
		"); update URLs you saved for this sandbox."
}

// portBusy recognizes docker failures caused by a host port in use.
func portBusy(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "port is already allocated") || strings.Contains(s, "address already in use")
}

// podPorts allocates host ports for a new pod of wfID, avoiding exclude,
// and persists them.
func (m *Manager) podPorts(wfID string, exclude map[int]bool) (map[int]int, error) {
	reserved := m.reservedPorts(wfID)
	for p := range exclude {
		reserved[p] = true
	}
	ports, err := allocatePorts(wfID, emulatorPorts(), m.savedPorts(wfID), reserved, portFree)
	if err != nil {
		return nil, err
	}
	if err := m.savePorts(wfID, ports); err != nil {
		return nil, err
	}
	return ports, nil
}

// savedPortsFree reports whether every saved host port can be bound.
func savedPortsFree(saved map[int]int) bool {
	for _, hp := range saved {
		if !portFree(hp) {
			return false
		}
	}
	return true
}

// StableEmulatorURL returns the host base URL of the cloud's emulator: the
// live port while the pod runs, else the fixed port saved for the pod, which
// the next start keeps unless another program took it meanwhile.
func (m *Manager) StableEmulatorURL(wfID string, cloud model.Cloud) (string, bool) {
	if u, ok := m.EmulatorURL(wfID, cloud); ok {
		return u, true
	}
	return m.savedEmulatorURL(wfID, cloud)
}

// savedEmulatorURL is the emulator's host base URL on its saved port.
func (m *Manager) savedEmulatorURL(wfID string, cloud model.Cloud) (string, bool) {
	e, ok := emulators[cloud]
	if !ok || checkID(wfID) != nil {
		return "", false
	}
	if hp := m.savedPorts(wfID)[e.port]; hp > 0 {
		return hostBase(hp), true
	}
	return "", false
}

// Identities are the fake identities the sandbox wires into Terraform
// providers; clients on the host use the same values.
type Identities struct {
	AzureSubscription, AzureTenant, AzureClient string
	GCPProject                                  string
	OCITenancy, OCIUser, OCIFingerprint         string
}

// SandboxIdentities returns the identities every sandbox uses.
func SandboxIdentities() Identities {
	return Identities{
		AzureSubscription: azureSubscription, AzureTenant: azureTenant, AzureClient: azureClient,
		GCPProject: gcpProject, OCITenancy: ociTenancy, OCIUser: ociUser, OCIFingerprint: ociFingerprint,
	}
}
