package loadtest

import (
	"fmt"
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// SuggestKube suggests a GET on every forwarded HTTP Service port and on
// every Ingress URL of a Kubernetes workflow's running cluster (the
// Endpoints cards' URLs). Ingress targets call the ingress port on
// 127.0.0.1 with the rule's Host header, so they work without DNS.
func SuggestKube(st model.KubeStatus) []model.LoadTarget {
	out := []model.LoadTarget{}
	if st.Cluster.State != model.ClusterRunning {
		return out
	}
	for _, f := range st.Forwards {
		if f.URL == "" || f.HostPort == 0 {
			continue
		}
		out = append(out, model.LoadTarget{
			Kind: model.TargetHTTP, NodeID: f.NodeID,
			Label: fmt.Sprintf("GET / · %s:%d", f.Service, f.Port),
			HTTP:  &model.HTTPTarget{Method: http.MethodGet, URL: f.URL},
		})
	}
	for _, u := range st.Ingresses {
		if u.Address == "" {
			continue
		}
		t := model.LoadTarget{
			Kind: model.TargetHTTP, NodeID: u.NodeID,
			Label: fmt.Sprintf("GET %s%s · ingress %s", u.Host, u.Path, u.Ingress),
			HTTP:  &model.HTTPTarget{Method: http.MethodGet, URL: u.Address},
		}
		if u.Host != "" {
			t.HTTP.Headers = map[string]string{"Host": u.Host}
		}
		out = append(out, t)
	}
	return out
}
