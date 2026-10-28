package monitoring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestComposeLiveSeries(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"compose.yaml": shopCompose, ".env": "POSTGRES_PASSWORD=x\n"})
	out, err := Compose(ComposeInput{Project: openCompose(t, dir, "compose.yaml"), ProjectName: "nimbo-dk-shop"})
	if err != nil {
		t.Fatal(err)
	}
	// Only the scraped service has request metrics.
	if len(out.Live) != 1 || out.Live[0].Node != "service:api" || out.Live[0].Source != SourceOTel {
		t.Fatalf("live series %+v", out.Live)
	}
	if want := `sum(irate(http_server_request_duration_seconds_count{job="api"}[1m]))`; out.Live[0].Requests != want {
		t.Errorf("requests %s, want %s", out.Live[0].Requests, want)
	}
}

func TestKubeLiveSeries(t *testing.T) {
	out := httpKube(t, HTTPThresholds{})
	got := map[string]LiveSeries{}
	for _, s := range out.Live {
		got[s.Node] = s
	}
	want := map[string]string{
		"Deployment/shop/web":     SourceTraefik,
		"Deployment/shop/api":     SourceTraefik,
		"Deployment/shop/metrics": SourceOTel,
	}
	if len(got) != len(want) {
		t.Fatalf("live series for %d workloads, want %d: %+v", len(got), len(want), out.Live)
	}
	for node, src := range want {
		s, ok := got[node]
		if !ok || s.Source != src {
			t.Errorf("%s: %+v, want source %s", node, s, src)
			continue
		}
		if !strings.Contains(s.Requests, "irate(") || !strings.Contains(s.Buckets, "[1m]") {
			t.Errorf("%s reads %s / %s, want irate over 1m", node, s.Requests, s.Buckets)
		}
	}
	// The recording rules keep their 5m rate.
	if rules := string(out.Files[KubeRulesFile]); strings.Contains(rules, "irate(") || !strings.Contains(rules, "[5m]") {
		t.Error("recording rules changed window")
	}
}

func TestQueryLive(t *testing.T) {
	series := []LiveSeries{
		{Node: "service:api", Source: SourceOTel, Requests: "req_api", Errors: "err_api", Buckets: "b_api"},
		{Node: "service:idle", Source: SourceOTel, Requests: "req_idle", Errors: "err_idle", Buckets: "b_idle"},
		{Node: "service:gone", Source: SourceOTel, Requests: "req_gone", Errors: "err_gone", Buckets: "b_gone"},
	}
	var queries []string
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.FormValue("query")
		queries = append(queries, q)
		type sample struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		}
		at := func(node, v string) sample {
			return sample{Metric: map[string]string{"nimbo_node": node}, Value: [2]any{1.7e9, v}}
		}
		var result []sample
		switch {
		case strings.Contains(q, "histogram_quantile"):
			result = []sample{at("service:api", "0.12"), at("service:idle", "NaN")}
		case strings.Contains(q, "err_"):
			result = []sample{at("service:api", "2")} // no 5xx series for idle
		default:
			result = []sample{at("service:api", "40"), at("service:idle", "0")}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": result}})
	}))
	defer prom.Close()

	got, err := QueryLive(context.Background(), prom.Client(), prom.URL, series)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.NodeTraffic{
		{NodeID: "service:api", Source: SourceOTel, RPS: 40, ErrorRate: 0.05, P95Ms: 120},
		{NodeID: "service:idle", Source: SourceOTel},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("node %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// One query per measure, every series labelled with its node.
	if len(queries) != 3 || !strings.Contains(queries[0], `label_replace(req_api, "nimbo_node", "service:api", "", "") or label_replace(req_idle`) {
		t.Errorf("queries %q", queries)
	}
}

func TestQueryLiveError(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
	}))
	defer prom.Close()
	_, err := QueryLive(context.Background(), prom.Client(), prom.URL, []LiveSeries{{Node: "service:api", Requests: "x", Errors: "y", Buckets: "z"}})
	if err == nil || !strings.Contains(err.Error(), "parse error") {
		t.Fatalf("error %v", err)
	}
}
