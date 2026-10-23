package loadtest

import (
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestSuggestKube(t *testing.T) {
	st := model.KubeStatus{
		Cluster: model.KubeClusterStatus{State: model.ClusterRunning, IngressPort: 61080},
		Forwards: []model.KubeForward{
			{NodeID: "Service/default/web", Service: "web", Port: 80, HostPort: 8080, URL: "http://127.0.0.1:8080/"},
			{NodeID: "Service/default/db", Service: "db", Port: 5432, HostPort: 55432},
		},
		Ingresses: []model.KubeIngressURL{
			{NodeID: "Ingress/default/web", Ingress: "web", Host: "web.shop.localhost", Path: "/", Address: "http://127.0.0.1:61080/"},
			{NodeID: "Ingress/default/any", Ingress: "any", Path: "/api", Address: "http://127.0.0.1:61080/api"},
		},
	}
	got := SuggestKube(st)
	if len(got) != 3 {
		t.Fatalf("targets = %+v", got)
	}
	if got[0].NodeID != "Service/default/web" || got[0].HTTP.URL != "http://127.0.0.1:8080/" || got[0].Label != "GET / · web:80" {
		t.Errorf("service target = %+v", got[0])
	}
	if got[1].NodeID != "Ingress/default/web" || got[1].HTTP.URL != "http://127.0.0.1:61080/" || got[1].HTTP.Headers["Host"] != "web.shop.localhost" {
		t.Errorf("ingress target = %+v", got[1])
	}
	if got[2].HTTP.Headers != nil || got[2].Label != "GET /api · ingress any" {
		t.Errorf("hostless ingress = %+v", got[2])
	}
	st.Cluster.State = model.ClusterStopped
	if got := SuggestKube(st); len(got) != 0 {
		t.Errorf("stopped cluster targets = %+v", got)
	}
}

// kubeLikeProbe labels services by workload name and maps them to node IDs.
type kubeLikeProbe struct{ fakeProbe }

func (p *kubeLikeProbe) NodeID(service string) string { return "Deployment/default/" + service }
func (p *kubeLikeProbe) Note() string                 { return "from cgroups" }

func TestDescribedProbeResult(t *testing.T) {
	s, tl := synthetic("api", 1, true, 0, []float64{10, 20, 30}, nil)
	rr := &resourceRecorder{probe: &kubeLikeProbe{}, samples: s}
	res := rr.result(tl, &model.LoadSummary{})
	if res == nil || res.Note != "from cgroups" || res.Services[0].NodeID != "Deployment/default/api" {
		t.Fatalf("resources = %+v", res)
	}
	// Compose probes keep compose node IDs and the docker stats note.
	rr = &resourceRecorder{probe: &fakeProbe{}, samples: s}
	if res := rr.result(tl, &model.LoadSummary{}); res.Services[0].NodeID != "service:api" || res.Note != resourcesNote {
		t.Fatalf("compose resources = %+v", res)
	}
}
