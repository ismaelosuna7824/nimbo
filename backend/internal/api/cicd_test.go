package api

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The pipeline option adds the pipeline file and its README section to
// both the folder export and the zip.
func TestExportWithPipeline(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Orders")
	e.addNode(wf.ID, "aws_sqs_queue")

	out := filepath.Join(t.TempDir(), "exported")
	var res struct {
		Files []string `json:"files"`
	}
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/export", map[string]any{"dir": out, "ci": "gitlab", "smoke": true}, &res)
	if !slices.Contains(res.Files, ".gitlab-ci.yml") {
		t.Fatalf("export files %v lack .gitlab-ci.yml", res.Files)
	}
	pipeline, _ := os.ReadFile(filepath.Join(out, ".gitlab-ci.yml"))
	if !strings.Contains(string(pipeline), "smoke-default:") || !strings.Contains(string(pipeline), "AWS_ID_TOKEN") {
		t.Errorf(".gitlab-ci.yml:\n%s", pipeline)
	}
	readme, _ := os.ReadFile(filepath.Join(out, "README.md"))
	if !strings.Contains(string(readme), "\n## GitLab CI pipeline\n") || !strings.Contains(string(readme), "`SMOKE_URL`") {
		t.Errorf("README.md:\n%s", readme)
	}

	resp, err := http.Get(e.srv.URL + "/api/v1/workflows/" + wf.ID + "/export.zip?ci=github")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip: %v (%s)", err, data)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if !slices.Contains(names, ".github/workflows/orders.yml") || slices.Contains(names, ".gitlab-ci.yml") {
		t.Errorf("zip entries = %v", names)
	}

	if got := e.call("POST", "/workflows/"+wf.ID+"/export", map[string]any{"dir": out, "ci": "jenkins"}, nil); got != http.StatusBadRequest {
		t.Errorf("unknown ci status %d", got)
	}
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/export", map[string]any{"dir": t.TempDir(), "ci": "none"}, &res)
	if slices.ContainsFunc(res.Files, func(f string) bool { return strings.Contains(f, "gitlab") || strings.HasPrefix(f, ".github/") }) {
		t.Errorf("ci none wrote %v", res.Files)
	}
}
