package loadtest

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// SuggestCompose suggests a GET / on every published HTTP port of the
// running services of a docker workflow (the Endpoints cards' Host URLs).
// The URLs are on 127.0.0.1, so the preflight warmup applies while a
// service behind a published port is still starting.
func SuggestCompose(st model.ComposeStatus) []model.LoadTarget {
	out := []model.LoadTarget{}
	for _, svc := range st.Services {
		if svc.State != model.ComposeRunning && svc.State != model.ComposeStarting {
			continue
		}
		seen := map[int]bool{}
		for _, c := range svc.Containers {
			if c.State != "running" {
				continue
			}
			// Replicas are told apart by their container ("web-2").
			name := svc.Name
			if len(svc.Containers) > 1 {
				name = strings.TrimPrefix(c.Name, st.Project+"-")
			}
			for _, p := range c.Ports {
				if !sandbox.ComposeHTTPPort(p) || seen[p.Host] {
					continue
				}
				seen[p.Host] = true
				out = append(out, model.LoadTarget{
					Kind: model.TargetHTTP, NodeID: svc.NodeID,
					Label: fmt.Sprintf("GET / · %s:%d", name, p.Container),
					HTTP:  &model.HTTPTarget{Method: http.MethodGet, URL: p.URL},
				})
			}
		}
	}
	return out
}
