package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestComposePerfEndpoints(t *testing.T) {
	e := newEnv(t)
	wf := e.createDocker(map[string]string{"name": "perf", "templateId": "docker-node-postgres-redis"})

	var builds model.ComposeBuilds
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/compose/builds?limit=5", nil, &builds)
	if builds.Builds == nil || len(builds.Builds) != 0 {
		t.Errorf("builds before any run: %+v", builds)
	}
	e.mustCall(http.StatusBadRequest, "GET", "/workflows/"+wf.ID+"/compose/builds?limit=-1", nil, nil)

	var stats model.ComposeStats
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/compose/stats", nil, &stats)
	var names []string
	for _, s := range stats.Services {
		names = append(names, s.Service)
		if s.Current != nil || s.History == nil || s.NodeID != model.ComposeServiceID(s.Service) {
			t.Errorf("idle service %+v", s)
		}
	}
	if !slices.Equal(names, []string{"api", "cache", "db"}) || stats.WorkflowID != wf.ID || stats.IntervalMs != 1000 || stats.Streaming {
		t.Errorf("stats %+v", stats)
	}

	tf := e.createEmpty("tf")
	e.mustCall(http.StatusBadRequest, "GET", "/workflows/"+tf.ID+"/compose/stats", nil, nil)
	e.mustCall(http.StatusBadRequest, "GET", "/workflows/"+tf.ID+"/compose/builds", nil, nil)
}

func TestBuildHints(t *testing.T) {
	e := newEnv(t)
	wf := e.createDocker(map[string]string{"name": "perf", "templateId": "docker-node-postgres-redis"})
	s := &Server{cfg: Config{Store: e.st}}
	hints := s.buildHints(&wf)
	api, ok := hints["api"]
	if len(hints) != 1 || !ok {
		t.Fatalf("hints %+v", hints)
	}
	// FROM node:22-alpine + 8 instructions (ENV, WORKDIR, COPY, RUN, COPY,
	// USER, EXPOSE, CMD).
	if api.BaseImage != "node:22-alpine" || api.OwnLayers != 8 || !slices.Contains(api.Stack, "node") {
		t.Errorf("api hint %+v", api)
	}
}
