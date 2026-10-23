package sandbox

import (
	"math"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestCgroupParser(t *testing.T) {
	if uid := podUID("/sys/fs/cgroup/kubepods/burstable/pod0a2ff4ae-61e3-4558-92e4-ebfd25a30e92"); uid != "0a2ff4ae-61e3-4558-92e4-ebfd25a30e92" {
		t.Errorf("cgroupfs uid %q", uid)
	}
	if uid := podUID("/sys/fs/cgroup/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod0a2ff4ae_61e3_4558.slice"); uid != "0a2ff4ae-61e3-4558" {
		t.Errorf("systemd uid %q", uid)
	}
	now := time.Now()
	var p cgroupParser
	lines := []string{
		"@ 100.00",
		"/sys/fs/cgroup/kubepods/burstable/podA usage_usec 1000000 50000000 inactive_file 10000000",
		"garbage line",
		"@ 101.00",
		"/sys/fs/cgroup/kubepods/burstable/podA usage_usec 1500000 60000000 inactive_file 10000000",
		"/sys/fs/cgroup/kubepods/burstable/podB usage_usec 10 20 inactive_file 30",
		"@ 103.00",
		"/sys/fs/cgroup/kubepods/burstable/podA usage_usec 3500000 60000000 inactive_file 70000000",
	}
	var got []statsLine
	var uids []string
	for _, l := range lines {
		if uid, st, ok := p.line(l, now); ok {
			uids = append(uids, uid)
			got = append(got, st)
		}
	}
	if len(got) != 2 || uids[0] != "A" || uids[1] != "A" {
		t.Fatalf("parsed %v %+v", uids, got)
	}
	// 0.5 s of CPU in 1 s = 50%; 2 s in 2 s = 100%; memory without page cache.
	if math.Abs(got[0].cpuPercent-50) > 0.01 || got[0].memBytes != 50000000 {
		t.Errorf("first = %+v", got[0])
	}
	if math.Abs(got[1].cpuPercent-100) > 0.01 || got[1].memBytes != 0 {
		t.Errorf("second = %+v", got[1])
	}
	// Aggregation per workload with the pods' limits.
	info := map[string]containerInfo{"A": {service: "Deployment/default/web", cpuLimit: 0.25, memLimit: 64 << 20}, "B": {service: "Deployment/default/web", cpuLimit: 0.25, memLimit: 64 << 20}}
	us := aggregateUsage(info, map[string]statsLine{"A": got[1], "B": {at: now, cpuPercent: 10, memBytes: 1 << 20}}, hostInfo{cpus: 8, memory: 16 << 30}, now)
	if len(us) != 1 || us[0].Service != "Deployment/default/web" || us[0].Containers != 2 || us[0].CPULimit != 0.5 || !us[0].CPULimited || us[0].CPUPercent != 110 {
		t.Errorf("usage = %+v", us)
	}
}

func TestWorkloadLabelsAndTarget(t *testing.T) {
	ws := []model.KubeWorkloadStatus{
		{NodeID: "Deployment/default/web", Namespace: "default", Name: "web"},
		{NodeID: "Deployment/a/api", Namespace: "a", Name: "api"},
		{NodeID: "StatefulSet/b/api", Namespace: "b", Name: "api"},
		{NodeID: "Deployment/c/x", Namespace: "c", Name: "x"},
		{NodeID: "StatefulSet/c/x", Namespace: "c", Name: "x"},
	}
	l := workloadLabels(ws)
	want := map[string]string{"Deployment/default/web": "web", "Deployment/a/api": "a/api", "StatefulSet/b/api": "b/api", "Deployment/c/x": "Deployment/c/x", "StatefulSet/c/x": "StatefulSet/c/x"}
	for k, v := range want {
		if l[k] != v {
			t.Errorf("label %s = %q, want %q", k, l[k], v)
		}
	}

	st := model.KubeStatus{
		Cluster:   model.KubeClusterStatus{IngressPort: 61000},
		Workloads: ws,
		Forwards:  []model.KubeForward{{NodeID: "Service/default/web", HostPort: 8080}},
		Ingresses: []model.KubeIngressURL{{NodeID: "Ingress/default/web", Host: "web.localhost", Path: "/", ServiceNodeID: "Service/default/web"},
			{NodeID: "Ingress/a/api", Host: "api.localhost", Path: "/v1", ServiceNodeID: "Service/a/api"}},
	}
	links := map[string][]string{"Service/default/web": {"Deployment/default/web"}, "Service/a/api": {"Deployment/a/api"}}
	http := func(u string, h map[string]string) *model.HTTPTarget { return &model.HTTPTarget{URL: u, Headers: h} }
	cases := []struct {
		target model.LoadTarget
		want   string
	}{
		{model.LoadTarget{NodeID: "StatefulSet/b/api"}, "StatefulSet/b/api"},
		{model.LoadTarget{NodeID: "Service/default/web"}, "Deployment/default/web"},
		{model.LoadTarget{NodeID: "Ingress/a/api", HTTP: http("http://127.0.0.1:61000/v1/x", nil)}, "Deployment/a/api"},
		{model.LoadTarget{HTTP: http("http://127.0.0.1:8080/", nil)}, "Deployment/default/web"},
		{model.LoadTarget{HTTP: http("http://127.0.0.1:61000/v1", map[string]string{"host": "api.localhost"})}, "Deployment/a/api"},
		{model.LoadTarget{HTTP: http("http://127.0.0.1:9999/", nil)}, ""},
	}
	for _, c := range cases {
		if got := kubeTargetWorkload(st, links, c.target); got != c.want {
			t.Errorf("%+v → %q, want %q", c.target, got, c.want)
		}
	}
	p := &kubeProbe{labels: l, ids: map[string]string{"web": "Deployment/default/web"}}
	if p.NodeID("web") != "Deployment/default/web" || p.NodeID("unknown") != "unknown" || p.label("Deployment/a/api") != "a/api" {
		t.Error("probe labels")
	}
}
