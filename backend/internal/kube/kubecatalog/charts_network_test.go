//go:build network

package kubecatalog

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Every pinned chart downloads, is the version listed, renders through the
// wrapper chart and has no k8s_* errors (go test -tags network). The log
// lists the advice found per chart: popular charts are the rules' noise
// benchmark.
func TestChartsFetchAndRender(t *testing.T) {
	for _, c := range charts {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			cs, err := AddChart(context.Background(), http.DefaultClient, "chart:"+c.id, "", "")
			if err != nil {
				t.Fatal(err)
			}
			dir := writeFiles(t, cs.Files)
			wf := &model.Workflow{ID: "t", Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{cs.Source}}}
			p, diags := kube.Derive(context.Background(), dir, wf)
			if len(p.Objects) == 0 {
				t.Fatalf("rendered nothing: %v", diags)
			}
			counts := map[string]int{}
			for _, d := range append(diags, kuberules.Check(p, wf, model.BestPracticeSettings{})...) {
				if os.Getenv("NIMBO_SHOW") == d.Code {
					t.Logf("%s:%d %s", d.File, d.Line, d.Message)
				}
				if d.Severity == model.SeverityError {
					t.Errorf("%s %s:%d %s", d.Code, d.File, d.Line, d.Message)
				}
				counts[string(d.Severity)+" "+d.Code]++
			}
			var keys []string
			for k, n := range counts {
				keys = append(keys, k+"="+strconv.Itoa(n))
			}
			sort.Strings(keys)
			t.Logf("%s %s: %d objects; %s", c.chart.Name, c.chart.Version, len(p.Objects), strings.Join(keys, ", "))
		})
	}
}
