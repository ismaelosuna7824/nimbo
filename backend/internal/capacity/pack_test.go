package capacity

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func shape(t *testing.T, name string) Shape {
	t.Helper()
	for _, s := range Shapes {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no shape %s", name)
	return Shape{}
}

func TestAllocatableKeepsSystemReserve(t *testing.T) {
	// 2 vCPU: 6% + 1% of a core + 0.1 for agents = 0.17 reserved.
	// 8 GiB: 25% of 4 + 20% of 4 = 1.8, + 100 MiB eviction + 256 MiB.
	cpu, mem := Allocatable(shape(t, "m7g.large"))
	if math.Abs(cpu-1.83) > 1e-9 || math.Abs(mem-(8-1.8-100.0/1024-0.25)) > 1e-9 {
		t.Fatalf("m7g.large allocatable %v CPU %v GiB", cpu, mem)
	}
	// 8 vCPU: 0.06 + 0.01 + 0.01 + 4 × 0.0025 + 0.1.
	cpu, _ = Allocatable(shape(t, "m7g.2xlarge"))
	if math.Abs(cpu-(8-0.09-0.1)) > 1e-9 {
		t.Fatalf("m7g.2xlarge allocatable %v CPU", cpu)
	}
}

func TestPackFirstFitDecreasing(t *testing.T) {
	large := shape(t, "m7g.large") // 1.83 CPU, ~5.85 GiB allocatable
	for _, c := range []struct {
		name  string
		items []Item
		nodes int
		ok    bool
	}{
		{"nothing", nil, 0, true},
		{"one small replica still gets two nodes", []Item{{0.1, 0.1}}, MinNodes, true},
		{"1 CPU replicas: one per node", []Item{{1, 0.5}, {1, 0.5}, {1, 0.5}}, 3, true},
		{"three 0.5 CPU per node", []Item{{0.5, 0.5}, {0.5, 0.5}, {0.5, 0.5}, {0.5, 0.5}, {0.5, 0.5}, {0.5, 0.5}, {0.5, 0.5}}, 3, true},
		{"memory-bound", []Item{{0.1, 3}, {0.1, 3}, {0.1, 3}}, 3, true},
		{"exactly the allocatable CPU fits", []Item{{1.83, 1}}, MinNodes, true},
		{"bigger than a node", []Item{{2, 1}}, 0, false},
		{"big ones first fill the gaps", []Item{{0.3, 0.2}, {1.5, 0.2}, {0.3, 0.2}, {1.5, 0.2}}, 2, true},
	} {
		nodes, _, _, ok := Pack(large, c.items)
		if nodes != c.nodes || ok != c.ok {
			t.Errorf("%s: %d nodes ok=%v, want %d ok=%v", c.name, nodes, ok, c.nodes, c.ok)
		}
	}
}

func TestPackUsage(t *testing.T) {
	_, cpu, mem, ok := Pack(shape(t, "m7g.large"), []Item{{1.83, 0}, {1.83, 0}})
	if !ok || math.Abs(cpu-1) > 1e-9 || mem != 0 {
		t.Fatalf("usage %v %v %v", cpu, mem, ok)
	}
}

func TestMonthlyCost(t *testing.T) {
	if got := MonthlyCost(0.0816, 3); got != 178.70 { // 0.0816 × 3 × 730 = 178.704
		t.Fatalf("MonthlyCost = %v", got)
	}
	if got := MonthlyCost(0.077, 0); got != 0 {
		t.Fatalf("no nodes cost %v", got)
	}
}

func TestEstimatePicksCheapest(t *testing.T) {
	items := []Item{{1, 0.5}, {1, 0.5}, {1, 0.5}}
	est, ok := Estimate("aws", items)
	if !ok || len(est.Options) == 0 {
		t.Fatal("no AWS option")
	}
	for i := 1; i < len(est.Options); i++ {
		if est.Options[i].MonthlyUSD < est.Options[i-1].MonthlyUSD {
			t.Fatalf("options not cheapest first: %+v", est.Options)
		}
	}
	if est.Recommended != est.Options[0] || est.Region != "us-east-1" {
		t.Fatalf("recommended %+v region %s", est.Recommended, est.Region)
	}
	// Nothing fits a 20-CPU replica.
	if _, ok := Estimate("gcp", []Item{{20, 1}}); ok {
		t.Fatal("20 CPU replica fitted")
	}
}

func TestPriceTableSanity(t *testing.T) {
	if _, err := time.Parse("2006-01-02", PriceDate); err != nil {
		t.Fatalf("PriceDate %q: %v", PriceDate, err)
	}
	seen := map[string]bool{}
	for _, c := range Clouds {
		if Regions[c] == "" {
			t.Errorf("%s has no region", c)
		}
		var arm, x86 bool
		for _, s := range Shapes {
			if s.Cloud != c {
				continue
			}
			arm = arm || s.Arch == "arm64"
			x86 = x86 || s.Arch == "x86_64"
		}
		if !arm || !x86 {
			t.Errorf("%s needs ARM and x86 shapes (arm %v, x86 %v)", c, arm, x86)
		}
	}
	for _, s := range Shapes {
		if seen[s.Cloud+"/"+s.Name] {
			t.Errorf("duplicate shape %s", s.Name)
		}
		seen[s.Cloud+"/"+s.Name] = true
		if !slices.Contains(Clouds, s.Cloud) || s.VCPU < 1 || s.MemGiB < 2 || s.Arch != "arm64" && s.Arch != "x86_64" {
			t.Errorf("bad shape %+v", s)
		}
		// On-demand list prices sit between 1 and 10 cents per vCPU-hour.
		if per := s.Hourly / s.VCPU; per < 0.01 || per > 0.1 {
			t.Errorf("%s: $%.4f per vCPU-hour looks wrong", s.Name, per)
		}
		if a, m := Allocatable(s); a <= 0 || m <= 0 {
			t.Errorf("%s has nothing allocatable", s.Name)
		}
	}
	// OCI flex shapes are priced from the unit rates.
	if a1 := shape(t, "VM.Standard.A1.Flex 2 OCPU/12 GB"); math.Abs(a1.Hourly-(2*0.01+12*0.0015)) > 1e-12 || a1.VCPU != 2 {
		t.Fatalf("A1 = %+v", a1)
	}
	if e4 := shape(t, "VM.Standard.E4.Flex 1 OCPU/8 GB"); e4.VCPU != 2 {
		t.Fatalf("E4 OCPU is 2 vCPUs: %+v", e4)
	}
	info := PriceInfo()
	if info.AsOf != PriceDate || !strings.Contains(info.Note, "compute only") || len(info.Regions) != len(Clouds) {
		t.Fatalf("info = %+v", info)
	}
}

func TestApplyValuesKubernetes(t *testing.T) {
	svc := model.CapacityService{
		NodeID: "Deployment/hello-prod/web", Role: model.CapacityApp, Replicas: 4,
		HPA:        &model.HPAPolicy{Min: 3, Max: 6, TargetCPU: 70},
		CPURequest: "250m", CPULimit: "250m", MemRequest: "128Mi", MemLimit: "128Mi",
	}
	settings := []model.EnvSetting{
		{Key: "replicas", Label: "Replicas", Value: "2", Editable: true},
		{Key: "hpa.min", Value: "2", Editable: true},
		{Key: "hpa.max", Value: "5", Editable: true},
		{Key: "hpa.cpu", Value: "70", Editable: true},
		{Key: "cpu.request:nginx", Value: "50m", Editable: true},
		{Key: "cpu.limit:nginx", Value: "", Editable: false, Reason: "set by a JSON patch"},
		{Key: "memory.request:nginx", Value: "64Mi", Editable: true},
		{Key: "memory.limit:nginx", Value: "0.125Gi", Editable: true}, // = 128Mi
		{Key: "cpu.request:sidecar", Value: "10m", Editable: true},
	}
	a := ApplyValues("kubernetes", svc, settings)
	want := map[string]string{"replicas": "3", "hpa.min": "3", "hpa.max": "6", "cpu.request:nginx": "250m", "memory.request:nginx": "128Mi"}
	if len(a.Values) != len(want) {
		t.Fatalf("values = %v", a.Values)
	}
	for k, v := range want {
		if a.Values[k] != v {
			t.Fatalf("values = %v", a.Values)
		}
	}
	skipped := strings.Join(a.Skipped, "\n")
	if !strings.Contains(skipped, "set by a JSON patch") || !strings.Contains(skipped, "sidecar") {
		t.Fatalf("skipped = %v", a.Skipped)
	}
	if !slices.Contains(a.Changes, "Min replicas: 2 → 3") {
		t.Fatalf("changes = %v", a.Changes)
	}

	// No HPA in the environment: fixed replicas, the autoscaler is skipped.
	a = ApplyValues("kubernetes", svc, settings[:1])
	if a.Values["replicas"] != "4" || !strings.Contains(strings.Join(a.Skipped, ""), "no HPA") {
		t.Fatalf("no HPA: %+v", a)
	}

	// Dependencies keep their replicas.
	dep := svc
	dep.Role, dep.HPA = model.CapacityDependency, nil
	if a = ApplyValues("kubernetes", dep, settings[:1]); a != nil {
		t.Fatalf("dependency replicas changed: %+v", a)
	}
}

func TestApplyValuesDocker(t *testing.T) {
	svc := model.CapacityService{NodeID: "service:api", Role: model.CapacityApp, Replicas: 3, CPULimit: "1", MemLimit: "384Mi"}
	settings := []model.EnvSetting{
		{Key: "replicas", Value: "1", Editable: true},
		{Key: "cpus", Value: "1", Editable: true},
		{Key: "memory", Value: "256M", Editable: true},
	}
	a := ApplyValues("docker", svc, settings)
	if a.Target != "api" || a.Values["replicas"] != "3" || a.Values["memory"] != "384M" || len(a.Values) != 2 {
		t.Fatalf("apply = %+v", a)
	}
	settings[2].Value = "384m" // compose sizes are binary: same as 384Mi
	settings[0].Value = "3"
	if a = ApplyValues("docker", svc, settings); a != nil {
		t.Fatalf("unchanged service applied: %+v", a)
	}
}

func TestPlanStoreRoundTrip(t *testing.T) {
	home := t.TempDir()
	if _, ok := LastPlan(home, "wf-1", "prod"); ok {
		t.Fatal("plan before save")
	}
	p := &model.CapacityPlan{WorkflowID: "wf-1", Environment: "prod", TargetRPS: 500}
	if err := SavePlan(home, p); err != nil {
		t.Fatal(err)
	}
	base := &model.CapacityPlan{WorkflowID: "wf-1", TargetRPS: 50}
	if err := SavePlan(home, base); err != nil {
		t.Fatal(err)
	}
	got, ok := LastPlan(home, "wf-1", "prod")
	if !ok || got.TargetRPS != 500 {
		t.Fatalf("prod plan %+v", got)
	}
	if got, ok = LastPlan(home, "wf-1", ""); !ok || got.TargetRPS != 50 {
		t.Fatalf("base plan %+v", got)
	}
	if _, err := os.Stat(filepath.Join(home, "workflows", "wf-1", "capacity", "_base.json")); err != nil {
		t.Fatal(err)
	}
	if err := SavePlan(home, &model.CapacityPlan{WorkflowID: "../x"}); err == nil {
		t.Fatal("path traversal accepted")
	}
}
