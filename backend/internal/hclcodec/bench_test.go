package hclcodec

import (
	"encoding/json"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model/modeltest"
)

const benchNodes = 500

// normalizedLarge returns the 500-node workflow after a first Normalize, as
// stored and sent back by clients.
func normalizedLarge(b *testing.B) *model.Workflow {
	b.Helper()
	wf := modeltest.LargeWorkflow(benchNodes)
	if d := Normalize(wf); len(d) > 0 {
		b.Fatalf("diagnostics: %v", d)
	}
	if len(wf.Nodes) != benchNodes || len(wf.Edges) != 600 {
		b.Fatalf("got %d nodes, %d edges", len(wf.Nodes), len(wf.Edges))
	}
	return wf
}

func BenchmarkNormalize500(b *testing.B) {
	data, err := json.Marshal(normalizedLarge(b))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		var wf model.Workflow
		if err := json.Unmarshal(data, &wf); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		Normalize(&wf)
	}
}

func BenchmarkExport500(b *testing.B) {
	wf := normalizedLarge(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Export(wf, ExportOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
