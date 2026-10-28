package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/monitoring"
)

// Live traffic (GET /workflows/{id}/traffic): the canvas polls it while a
// load test runs to show the requests each service or workload receives,
// read from the workflow's own Prometheus. Nothing is estimated: a node is
// listed only when Prometheus measures its requests.

// trafficTimeout bounds the Prometheus queries of one poll.
const trafficTimeout = 4 * time.Second

var promClient = &http.Client{Timeout: trafficTimeout}

// liveSeriesCache keeps the live series of each workflow's content
// version: generating them renders the whole project (every environment
// on Kubernetes), and the canvas polls every few seconds.
type liveSeriesCache struct {
	mu   sync.Mutex
	byWF map[string]liveSeriesEntry
}

type liveSeriesEntry struct {
	updatedAt time.Time
	series    []monitoring.LiveSeries
	// namespace is the Kubernetes monitoring's namespace.
	namespace string
}

func (c *liveSeriesCache) get(wf *model.Workflow, build func() ([]monitoring.LiveSeries, string, error)) ([]monitoring.LiveSeries, string, error) {
	c.mu.Lock()
	e, ok := c.byWF[wf.ID]
	c.mu.Unlock()
	if ok && e.updatedAt.Equal(wf.UpdatedAt) {
		return e.series, e.namespace, nil
	}
	series, ns, err := build()
	if err != nil {
		return nil, "", err
	}
	c.mu.Lock()
	if c.byWF == nil {
		c.byWF = map[string]liveSeriesEntry{}
	}
	c.byWF[wf.ID] = liveSeriesEntry{updatedAt: wf.UpdatedAt, series: series, namespace: ns}
	c.mu.Unlock()
	return series, ns, nil
}

// liveTraffic is GET /workflows/{id}/traffic.
func (s *Server) liveTraffic(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*trafficTimeout)
	defer cancel()
	out := model.LiveTraffic{Window: monitoring.LiveWindowNote, Nodes: []model.NodeTraffic{}}
	var series []monitoring.LiveSeries
	var prom string
	switch {
	case wf.IsDocker() && wf.Compose != nil:
		p := s.openCompose(wf, nil)
		if !slices.Contains(wf.Compose.Files, monitoring.ComposeFile(p.ProjectDir)) {
			out.Status = model.TrafficNoMonitoring
			return ok(w, out)
		}
		if series, _, err = s.liveSeries.get(wf, func() ([]monitoring.LiveSeries, string, error) {
			plan, err := s.planDockerMonitoring(wf, p, nil)
			if err != nil {
				return nil, "", err
			}
			return plan.Live, "", nil
		}); err != nil {
			return err
		}
		if st, err := s.cfg.Sandbox.ComposeStatus(ctx, wf.ID, composeServices(wf)); err == nil {
			prom = dockerPrometheusURL(st)
		}
	case wf.IsKubernetes() && wf.Kubernetes != nil:
		if chart, man := kubeMonitoringSources(wf); chart < 0 || man < 0 {
			out.Status = model.TrafficNoMonitoring
			return ok(w, out)
		}
		var ns string
		if series, ns, err = s.liveSeries.get(wf, func() ([]monitoring.LiveSeries, string, error) {
			plan, _, _, ns, err := s.planKubeMonitoring(wf, s.openKube(wf, nil), nil)
			return plan.Live, ns, err
		}); err != nil {
			return err
		}
		for _, l := range s.kubeMonitoringLinks(ctx, wf.ID, ns) {
			if l.Name == "Prometheus" && l.Running {
				prom = l.URL
			}
		}
	default:
		return errorf(http.StatusBadRequest, "live traffic is for docker and Kubernetes workflows; %s is a %s workflow", wf.Name, model.KindLabel(wf.Kind))
	}
	if prom == "" {
		out.Status = model.TrafficNotRunning
		return ok(w, out)
	}
	nodes, err := monitoring.QueryLive(ctx, promClient, prom, series)
	if err != nil {
		out.Status, out.Message = model.TrafficUnreachable, err.Error()
		return ok(w, out)
	}
	out.Status, out.Nodes = model.TrafficOK, nodes
	return ok(w, out)
}

// dockerPrometheusURL is the host URL of the running Prometheus container
// ("" when it does not run).
func dockerPrometheusURL(st model.ComposeStatus) string {
	sum := model.MonitoringSummary{Links: []model.MonitoringLink{{Name: "Prometheus"}}}
	dockerMonitoringLinks(&sum, st)
	if !sum.Links[0].Running {
		return ""
	}
	return strings.TrimSuffix(sum.Links[0].URL, "/")
}
