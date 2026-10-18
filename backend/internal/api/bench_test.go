package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model/modeltest"
)

// benchWorkflow stores the 500-node workflow and returns the handler, its ID
// and the PUT body a client would send (the normalized workflow).
func benchWorkflow(b *testing.B) (http.Handler, string, []byte) {
	b.Helper()
	e := newEnv(b)
	created := e.createEmpty("Bench")
	wf := modeltest.LargeWorkflow(500)
	hclcodec.Normalize(wf)
	wf.ID, wf.Name, wf.Icon = created.ID, created.Name, created.Icon
	body, err := json.Marshal(wf)
	if err != nil {
		b.Fatal(err)
	}
	var saved model.Workflow
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+created.ID, json.RawMessage(body), &saved)
	if len(saved.Nodes) != 500 || len(saved.Edges) != 600 || len(errorDiags(&saved)) > 0 {
		b.Fatalf("saved %d nodes, %d edges, errors %v", len(saved.Nodes), len(saved.Edges), errorDiags(&saved))
	}
	b.Logf("PUT body %d bytes", len(body))
	return e.srv.Config.Handler, created.ID, body
}

// serve runs one request through the full handler (host/origin checks,
// routing, handler) without the network.
func serve(b *testing.B, h http.Handler, method, path string, body []byte) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, rd)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		b.Fatalf("%s %s = %d: %s", method, path, rec.Code, rec.Body.Bytes())
	}
}

func BenchmarkPutWorkflow500(b *testing.B) {
	h, id, body := benchWorkflow(b)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		serve(b, h, "PUT", "/workflows/"+id, body)
	}
}

func BenchmarkGetWorkflow500(b *testing.B) {
	h, id, _ := benchWorkflow(b)
	b.ReportAllocs()
	for b.Loop() {
		serve(b, h, "GET", "/workflows/"+id, nil)
	}
}

func BenchmarkGetTerraform500(b *testing.B) {
	h, id, _ := benchWorkflow(b)
	b.ReportAllocs()
	for b.Loop() {
		serve(b, h, "GET", "/workflows/"+id+"/terraform", nil)
	}
}
