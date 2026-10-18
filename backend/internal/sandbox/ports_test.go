package sandbox

import (
	"net"
	"strconv"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

var native = []int{4566, 4577, 4588, 4599}

func allFree(int) bool { return true }

func TestAllocatePortsDeterministicBlock(t *testing.T) {
	a, err := allocatePorts("wf-one", native, nil, nil, allFree)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := allocatePorts("wf-one", native, nil, nil, allFree)
	for _, np := range native {
		if a[np] != b[np] {
			t.Fatalf("allocation not deterministic: %v vs %v", a, b)
		}
	}
	base := a[4566]
	if base < portRangeLo || base+3 > portRangeHi || (base-portRangeLo)%len(native) != 0 {
		t.Fatalf("base %d is not a block start in range", base)
	}
	for i, np := range native {
		if a[np] != base+i {
			t.Fatalf("ports are not one consecutive block: %v", a)
		}
	}
	other, _ := allocatePorts("wf-two", native, nil, nil, allFree)
	if other[4566] == base {
		t.Fatalf("different workflows got the same preferred block %d", base)
	}
}

func TestAllocatePortsSkipsBusyAndReserved(t *testing.T) {
	pref, _ := allocatePorts("wf-busy", native, nil, nil, allFree)
	base := pref[4566]
	busy := map[int]bool{base + 2: true}
	got, err := allocatePorts("wf-busy", native, nil, nil, func(p int) bool { return !busy[p] })
	if err != nil {
		t.Fatal(err)
	}
	if got[4566] == base {
		t.Fatalf("block with a busy port was used: %v", got)
	}
	for _, hp := range got {
		if busy[hp] {
			t.Fatalf("busy port %d allocated", hp)
		}
	}
	// Ports saved by another workflow are skipped even when free.
	reserved := map[int]bool{base: true}
	got, _ = allocatePorts("wf-busy", native, nil, reserved, allFree)
	for _, hp := range got {
		if reserved[hp] {
			t.Fatalf("reserved port %d allocated", hp)
		}
	}
}

func TestAllocatePortsReusesSaved(t *testing.T) {
	saved := map[int]int{4566: 47000, 4577: 47001, 4588: 47002, 4599: 47003}
	got, err := allocatePorts("wf-saved", native, saved, nil, allFree)
	if err != nil {
		t.Fatal(err)
	}
	for np, hp := range saved {
		if got[np] != hp {
			t.Fatalf("saved port %d -> %d not reused: %v", np, hp, got)
		}
	}
	// Only the busy saved port moves; the others stay.
	got, _ = allocatePorts("wf-saved", native, saved, nil, func(p int) bool { return p != 47000 })
	if got[4566] == 47000 || got[4577] != 47001 || got[4588] != 47002 || got[4599] != 47003 {
		t.Fatalf("unexpected reallocation: %v", got)
	}
	if msg := movedNotice(saved, got); msg == "" {
		t.Fatal("moved port not reported")
	}
	if msg := movedNotice(saved, saved); msg != "" {
		t.Fatalf("unchanged ports reported as moved: %q", msg)
	}
}

func TestPodPortsPersistAndReserve(t *testing.T) {
	m := &Manager{cfg: Config{Home: t.TempDir()}}
	first, err := m.podPorts("wf-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.savedPorts("wf-a"); got[4566] != first[4566] || len(got) != 4 {
		t.Fatalf("saved ports %v, allocated %v", got, first)
	}
	again, err := m.podPorts("wf-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	for np, hp := range first {
		if again[np] != hp {
			t.Fatalf("re-creation moved %d: %d -> %d", np, hp, again[np])
		}
	}
	for _, hp := range first {
		if !m.reservedPorts("wf-b")[hp] {
			t.Fatalf("port %d of wf-a not reserved for wf-b", hp)
		}
	}
	if len(m.reservedPorts("wf-a")) != 0 {
		t.Fatal("a workflow's own ports must not be reserved against it")
	}

	// A saved port taken by another program is replaced and persisted.
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(first[4566]))
	if err != nil {
		t.Skipf("cannot bind %d: %v", first[4566], err)
	}
	defer l.Close()
	moved, err := m.podPorts("wf-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if moved[4566] == first[4566] || m.savedPorts("wf-a")[4566] != moved[4566] {
		t.Fatalf("busy port kept: before %v after %v", first, moved)
	}
	if u, ok := m.savedEmulatorURL("wf-a", model.CloudAWS); !ok || u != hostBase(moved[4566]) {
		t.Fatalf("stable URL %q, want port %d", u, moved[4566])
	}
}
