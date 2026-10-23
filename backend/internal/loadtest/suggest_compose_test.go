package loadtest

import (
	"strconv"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestSuggestCompose(t *testing.T) {
	port := func(container, host int) model.ComposePort {
		return model.ComposePort{Container: container, Protocol: "tcp", HostIP: "0.0.0.0", Host: host, URL: "http://127.0.0.1:" + strconv.Itoa(host) + "/"}
	}
	st := model.ComposeStatus{Project: "nimbo-dk-wf", Services: []model.ComposeServiceStatus{
		{Name: "web", NodeID: "service:web", State: model.ComposeRunning, Containers: []model.ComposeContainer{
			{Name: "nimbo-dk-wf-web-1", State: "running", Ports: []model.ComposePort{port(80, 8080), port(80, 8080)}},
			{Name: "nimbo-dk-wf-web-2", State: "running", Ports: []model.ComposePort{port(80, 8081)}},
		}},
		{Name: "db", NodeID: "service:db", State: model.ComposeRunning, Containers: []model.ComposeContainer{
			{Name: "nimbo-dk-wf-db-1", State: "running", Ports: []model.ComposePort{port(5432, 5432)}},
		}},
		{Name: "old", NodeID: "service:old", State: model.ComposeStopped, Containers: []model.ComposeContainer{
			{Name: "nimbo-dk-wf-old-1", State: "exited", Ports: []model.ComposePort{port(80, 9090)}},
		}},
	}}
	got := SuggestCompose(st)
	if len(got) != 2 {
		t.Fatalf("targets = %+v", got)
	}
	if t0 := got[0]; t0.Kind != model.TargetHTTP || t0.NodeID != "service:web" || t0.Label != "GET / · web-1:80" || t0.HTTP.URL != "http://127.0.0.1:8080/" || t0.HTTP.Method != "GET" {
		t.Errorf("target = %+v", t0)
	}
	if got[1].HTTP.URL != "http://127.0.0.1:8081/" || got[1].Label != "GET / · web-2:80" {
		t.Errorf("second replica = %+v", got[1])
	}

	single := model.ComposeStatus{Project: "nimbo-dk-wf", Services: []model.ComposeServiceStatus{
		{Name: "api", NodeID: "service:api", State: model.ComposeStarting, Containers: []model.ComposeContainer{
			{Name: "nimbo-dk-wf-api-1", State: "running", Ports: []model.ComposePort{port(3000, 3000)}},
		}},
	}}
	if got := SuggestCompose(single); len(got) != 1 || got[0].Label != "GET / · api:3000" {
		t.Errorf("single = %+v", got)
	}
	if SuggestCompose(model.ComposeStatus{}) == nil {
		t.Error("nil instead of empty")
	}
}
