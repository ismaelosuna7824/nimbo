package capacity

import (
	"cmp"
	"math"
	"slices"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Nodes: every planned replica is packed by its requests onto nodes of
// one shape (first-fit decreasing), after the system reserve every node
// keeps for the OS, the kubelet/container runtime and per-node agents.

// MinNodes keeps the replicas of a service on more than one machine, so a
// node failure or drain never takes all of them down.
const MinNodes = 2

// Per-node agents (kube-proxy, CNI, log and metrics collectors).
const (
	agentCPU    = 0.1  // cores
	agentMemGiB = 0.25 // GiB
	evictionGiB = 100.0 / 1024
)

// tiers are (size, fraction) bands of a progressive reservation.
type tier struct{ size, frac float64 }

// The reservation follows the progressive formula managed Kubernetes
// services document for kube-reserved: CPU 6% of the first core, 1% of
// the second, 0.5% of the next two and 0.25% of the rest; memory 25% of
// the first 4 GiB, 20% of the next 4, 10% of the next 8, 6% of the next
// 112 and 2% above, plus a 100 MiB eviction threshold.
var (
	cpuTiers = []tier{{1, 0.06}, {1, 0.01}, {2, 0.005}, {math.Inf(1), 0.0025}}
	memTiers = []tier{{4, 0.25}, {4, 0.20}, {8, 0.10}, {112, 0.06}, {math.Inf(1), 0.02}}
)

func progressive(v float64, tiers []tier) float64 {
	out := 0.0
	for _, t := range tiers {
		if v <= 0 {
			break
		}
		part := math.Min(v, t.size)
		out += part * t.frac
		v -= part
	}
	return out
}

// Allocatable is the CPU (cores) and memory (GiB) of a shape left for
// workloads after the system reserve and per-node agents.
func Allocatable(s Shape) (cpu, memGiB float64) {
	cpu = s.VCPU - progressive(s.VCPU, cpuTiers) - agentCPU
	memGiB = s.MemGiB - progressive(s.MemGiB, memTiers) - evictionGiB - agentMemGiB
	return math.Max(cpu, 0), math.Max(memGiB, 0)
}

// Item is one replica's requests.
type Item struct {
	CPU    float64 // cores
	MemGiB float64
}

// Pack places items on nodes of shape s with first-fit decreasing and
// returns the node count (at least MinNodes when there is anything to
// place) and the fraction of the nodes' allocatable CPU and memory
// requested; ok is false when an item does not fit on one node.
func Pack(s Shape, items []Item) (nodes int, cpuUsed, memUsed float64, ok bool) {
	ac, am := Allocatable(s)
	if len(items) == 0 {
		return 0, 0, 0, true
	}
	if ac <= 0 || am <= 0 {
		return 0, 0, 0, false
	}
	sorted := slices.Clone(items)
	weight := func(it Item) float64 { return math.Max(it.CPU/ac, it.MemGiB/am) }
	slices.SortStableFunc(sorted, func(a, b Item) int { return cmp.Compare(weight(b), weight(a)) })
	type bin struct{ cpu, mem float64 }
	var bins []bin
	const eps = 1e-9
	var totalCPU, totalMem float64
	for _, it := range sorted {
		if it.CPU > ac+eps || it.MemGiB > am+eps {
			return 0, 0, 0, false
		}
		totalCPU += it.CPU
		totalMem += it.MemGiB
		placed := false
		for i := range bins {
			if bins[i].cpu+it.CPU <= ac+eps && bins[i].mem+it.MemGiB <= am+eps {
				bins[i].cpu += it.CPU
				bins[i].mem += it.MemGiB
				placed = true
				break
			}
		}
		if !placed {
			bins = append(bins, bin{it.CPU, it.MemGiB})
		}
	}
	nodes = max(len(bins), MinNodes)
	return nodes, totalCPU / (float64(nodes) * ac), totalMem / (float64(nodes) * am), true
}

// Estimate packs items on every shape of cloud and returns the options
// that fit, cheapest first (fewer nodes on a tie).
func Estimate(cloud string, items []Item) (model.CloudEstimate, bool) {
	out := model.CloudEstimate{Cloud: cloud, Region: Regions[cloud], Options: []model.NodeOption{}}
	for _, s := range Shapes {
		if s.Cloud != cloud {
			continue
		}
		n, cu, mu, ok := Pack(s, items)
		if !ok {
			continue
		}
		ac, am := Allocatable(s)
		out.Options = append(out.Options, model.NodeOption{
			Shape: s.Name, Arch: s.Arch, VCPU: s.VCPU, MemGiB: s.MemGiB,
			AllocCPU: round2(ac), AllocMemGiB: round2(am),
			HourlyUSD: s.Hourly, Nodes: n,
			MonthlyUSD: MonthlyCost(s.Hourly, n),
			CPUUsed:    round2(cu), MemUsed: round2(mu),
		})
	}
	if len(out.Options) == 0 {
		return out, false
	}
	slices.SortStableFunc(out.Options, func(a, b model.NodeOption) int {
		return cmp.Or(cmp.Compare(a.MonthlyUSD, b.MonthlyUSD), cmp.Compare(a.Nodes, b.Nodes))
	})
	out.Recommended = out.Options[0]
	return out, true
}

// MonthlyCost is nodes × hourly × HoursPerMonth, rounded to cents.
func MonthlyCost(hourly float64, nodes int) float64 {
	return math.Round(hourly*float64(nodes)*HoursPerMonth*100) / 100
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
