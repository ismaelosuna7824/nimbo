//go:build docker

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// A Go function with a multi-stage Dockerfile: the package stage is
// exported with BuildKit as dist/bootstrap (provided.al2023 layout), and an
// unchanged service is not built again.
func TestBuildServicesGoFunction(t *testing.T) {
	m, _ := newTestManager(t)
	work := t.TempDir()
	writeTree(t, work, map[string]string{
		"main.tf": `
data "archive_file" "fn" {
  type        = "zip"
  source_dir  = "${path.module}/services/fn/dist"
  output_path = "${path.module}/build/fn.zip"
}
`,
		"services/fn/go.mod":  "module fn\n\ngo 1.25\n",
		"services/fn/main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"ok\") }\n",
		"services/fn/Dockerfile": `FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/bootstrap .

FROM scratch AS package
COPY --from=build /out/bootstrap /bootstrap
`,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	rs := &runState{}
	start := time.Now()
	if err := m.buildArtifacts(ctx, rs, work); err != nil {
		t.Fatalf("build: %v\nlog: %+v", err, rs.run.Log)
	}
	t.Logf("build took %s", time.Since(start))
	bin, err := os.ReadFile(filepath.Join(work, "services", "fn", "dist", "bootstrap"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(bin, []byte("\x7fELF")) {
		t.Fatalf("dist/bootstrap is not a Linux binary (%d bytes)", len(bin))
	}
	entries, _ := os.ReadDir(filepath.Join(work, "services", "fn", "dist"))
	if len(entries) != 1 {
		t.Errorf("dist holds %d entries, want only bootstrap", len(entries))
	}
	if info, err := os.Stat(filepath.Join(work, "services", "fn", "dist", "bootstrap")); err != nil || info.Mode()&0o111 == 0 {
		t.Errorf("bootstrap is not executable: %v", err)
	}

	rs = &runState{}
	start = time.Now()
	if err := m.buildArtifacts(ctx, rs, work); err != nil {
		t.Fatal(err)
	}
	t.Logf("cached run took %s", time.Since(start))
	if !logged(rs, "Sources unchanged") {
		t.Errorf("unchanged service rebuilt: %+v", rs.run.Log)
	}
}

// A nimbo.local image is built from the runtime stage (not the package
// stage after it), reused while its sources don't change, and removed
// with the workflow's images.
func TestBuildImagesRuntimeStage(t *testing.T) {
	m, _ := newTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	name, wfID := fmt.Sprintf("itest%d", time.Now().UnixNano()), testWorkflowID("img")
	ref := "nimbo.local/" + name + ":latest"
	t.Cleanup(func() { m.removeImages(context.Background(), wfID) })
	work := t.TempDir()
	writeTree(t, work, map[string]string{
		"main.tf": `variable "img" {
  default = "` + ref + `"
}
`,
		"services/" + name + "/Dockerfile": "FROM alpine:3 AS runtime\nCOPY msg /msg\nCMD [\"cat\", \"/msg\"]\n\nFROM scratch AS package\nCOPY msg /\n",
		"services/" + name + "/msg":        "runtime stage",
	})
	run := func() *runState {
		rs := &runState{run: model.Run{WorkflowID: wfID}}
		if err := m.buildArtifacts(ctx, rs, work); err != nil {
			t.Fatalf("build: %v\nlog: %+v", err, rs.run.Log)
		}
		return rs
	}
	start := time.Now()
	run()
	t.Logf("build took %s", time.Since(start))
	if out, err := m.docker(ctx, "run", "--rm", ref); err != nil || out != "runtime stage" {
		t.Fatalf("docker run %s = %q, %v", ref, out, err)
	}
	start = time.Now()
	if rs := run(); !logged(rs, "Sources unchanged") {
		t.Errorf("rebuilt: %+v", rs.run.Log)
	}
	t.Logf("cached run took %s", time.Since(start))

	m.removeImages(ctx, wfID)
	if out, _ := m.docker(ctx, "image", "ls", "-q", "nimbo.local/"+name); strings.TrimSpace(out) != "" {
		t.Errorf("images left: %s", out)
	}
}
