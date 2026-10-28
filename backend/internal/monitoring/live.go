package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Live traffic: the canvas shows, while a load test runs, how many requests
// each service or workload receives, the share answered 5xx and the p95
// latency, as measured by the workflow's own Prometheus. The expressions
// are the HTTP recording rules' (same sources, same selectors) read with
// irate over a short window instead of rate over 5m: irate uses the last
// two scrapes (15 s apart), so the numbers follow a test of a minute or two
// instead of trailing it by minutes.

// LiveWindow is the range irate reads (several scrapes, so two always fall
// inside it).
const LiveWindow = "1m"

// LiveWindowNote says how the live numbers are computed.
const LiveWindowNote = "between its last two scrapes, 15 s apart"

// LiveSeries is how one canvas node's requests are measured.
type LiveSeries struct {
	// Node is the canvas node id: "service:api", "Deployment/default/web".
	Node string
	// Source is SourceOTel, SourceTraefik or SourceIngressNginx.
	Source string
	// Requests and Errors are request and 5xx rates (per second); Buckets
	// the latency histogram's bucket rates summed by le.
	Requests, Errors, Buckets string
}

// composeLive is the live series of the scraped compose services.
func composeLive(scrapes []scrapeTarget) []LiveSeries {
	out := make([]LiveSeries, 0, len(scrapes))
	for _, s := range scrapes {
		total, errors, buckets := otelServiceExprs(s.service, rangeRate("irate", LiveWindow))
		out = append(out, LiveSeries{Node: "service:" + s.service, Source: SourceOTel, Requests: total, Errors: errors, Buckets: buckets})
	}
	return out
}

// kubeLive is the live series of every workload with request metrics.
// A workload measured by several sources (an Ingress through Traefik and
// its own metrics) reads one: its own metrics when scraped (they count
// every request, port-forwarded ones included), else the first ingress.
func kubeLive(workloads []*kubeWorkload, sources map[string][]httpSource) []LiveSeries {
	var out []LiveSeries
	for _, w := range workloads {
		srcs := sources[w.key()]
		if len(srcs) == 0 {
			continue
		}
		i := max(0, slices.IndexFunc(srcs, func(s httpSource) bool { return s.kind == SourceOTel }))
		total, errors, buckets := workloadExprs(w, srcs[i], rangeRate("irate", LiveWindow))
		out = append(out, LiveSeries{Node: w.key(), Source: srcs[i].kind, Requests: total, Errors: errors, Buckets: buckets})
	}
	return out
}

// liveQueries combines the series into one PromQL query per measure, each
// result labelled nimbo_node with its node.
func liveQueries(series []LiveSeries) (requests, errors, p95 string) {
	join := func(expr func(LiveSeries) string) string {
		parts := make([]string, len(series))
		for i, s := range series {
			parts[i] = fmt.Sprintf(`label_replace(%s, "nimbo_node", %q, "", "")`, expr(s), s.Node)
		}
		return strings.Join(parts, " or ")
	}
	return join(func(s LiveSeries) string { return s.Requests }),
		join(func(s LiveSeries) string { return s.Errors }),
		join(func(s LiveSeries) string { return "histogram_quantile(0.95, " + s.Buckets + ")" })
}

// QueryLive asks Prometheus at base (http://127.0.0.1:9090) for the live
// traffic of series: one node per series Prometheus has data for (a
// service that served nothing yet has none).
func QueryLive(ctx context.Context, client *http.Client, base string, series []LiveSeries) ([]model.NodeTraffic, error) {
	if len(series) == 0 {
		return []model.NodeTraffic{}, nil
	}
	reqQ, errQ, p95Q := liveQueries(series)
	requests, err := instantQuery(ctx, client, base, reqQ)
	if err != nil {
		return nil, err
	}
	errs, err := instantQuery(ctx, client, base, errQ)
	if err != nil {
		return nil, err
	}
	p95, err := instantQuery(ctx, client, base, p95Q)
	if err != nil {
		return nil, err
	}
	out := []model.NodeTraffic{}
	for _, s := range series {
		rps, ok := requests[s.Node]
		if !ok {
			continue
		}
		n := model.NodeTraffic{NodeID: s.Node, Source: s.Source, RPS: rps}
		// No 5xx series while every request succeeds.
		if e := errs[s.Node]; rps > 0 && e > 0 {
			n.ErrorRate = math.Min(1, e/rps)
		}
		// NaN (absent here) while no request completed in the window.
		if v, ok := p95[s.Node]; ok {
			n.P95Ms = v * 1000
		}
		out = append(out, n)
	}
	return out, nil
}

// promResponse is the body of GET /api/v1/query.
type promResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// instantQuery runs query and returns its finite values by nimbo_node.
func instantQuery(ctx context.Context, client *http.Client, base, query string) (map[string]float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(base, "/")+"/api/v1/query", strings.NewReader(url.Values{"query": {query}}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body promResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("prometheus answered %s: %w", resp.Status, err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s", body.Error)
	}
	out := map[string]float64{}
	for _, r := range body.Data.Result {
		s, _ := r.Value[1].(string)
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		out[r.Metric["nimbo_node"]] = v
	}
	return out, nil
}
