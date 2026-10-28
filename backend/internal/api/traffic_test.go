package api

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Live traffic says why there is nothing to show: no monitoring, then a
// Prometheus that is not running (no Docker here); Terraform has none.
func TestLiveTrafficStatus(t *testing.T) {
	e := newEnvDocker(t, filepath.Join(t.TempDir(), "no-docker"))
	wf := e.createDocker(map[string]string{"templateId": "docker-node-postgres-redis"})
	var got model.LiveTraffic
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/traffic", nil, &got)
	if got.Status != model.TrafficNoMonitoring || got.Nodes == nil {
		t.Fatalf("without monitoring: %+v", got)
	}
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/monitoring", nil, nil)
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/traffic", nil, &got)
	if got.Status != model.TrafficNotRunning || got.Window == "" {
		t.Fatalf("monitoring not deployed: %+v", got)
	}
	tf := e.createEmpty("tf")
	e.mustCall(http.StatusBadRequest, "GET", "/workflows/"+tf.ID+"/traffic", nil, nil)
}
