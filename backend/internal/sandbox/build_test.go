package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(name)), content)
	}
}

const packageDockerfile = "FROM node:22-alpine AS build\nRUN true\nFROM scratch AS package\nCOPY --from=build /out/ /\n"

func TestBuildTargets(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  []string
	}{
		"static paths and modules": {
			files: map[string]string{
				"main.tf": `
data "archive_file" "api" {
  type        = "zip"
  source_dir  = "${path.module}/services/api/dist"
  output_path = "${path.module}/build/api.zip"
}
data "archive_file" "py" {
  source_dir = "${path.module}/services/py/src"
}
data "archive_file" "plain" {
  source_dir = "services/plain/dist"
}
resource "aws_s3_object" "o" {
  source = "services/unused/src/index.mjs"
}
`,
				"modules/m/main.tf": `
data "archive_file" "worker" {
  source_dir = "${path.module}/../../services/worker/dist"
}
data "archive_file" "tail" {
  source_dir = "${var.root}/services/tail/dist"
}
`,
				"broken.tf":                  `data "archive_file" "x" {`,
				"services/api/Dockerfile":    packageDockerfile,
				"services/py/Dockerfile":     packageDockerfile,
				"services/plain/Dockerfile":  "FROM node:22 AS build\nCMD [\"node\"]\n",
				"services/worker/Dockerfile": packageDockerfile,
				"services/tail/Dockerfile":   packageDockerfile,
				"services/unused/Dockerfile": packageDockerfile,
			},
			want: []string{"services/api", "services/tail", "services/worker"},
		},
		"for_each over services": {
			files: map[string]string{
				"main.tf": `
data "archive_file" "fn" {
  for_each   = toset(["a", "b"])
  source_dir = "${path.module}/services/${each.key}/dist"
}
`,
				"services/a/Dockerfile": "from --platform=$BUILDPLATFORM golang:1.25 as build\nFROM scratch as Package # deployable\n",
				"services/b/Dockerfile": "FROM golang:1.25\n",
			},
			want: []string{"services/a"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			writeTree(t, work, tc.files)
			files, err := buildInputs(work)
			if err != nil {
				t.Fatal(err)
			}
			if got := hclcodec.ServiceBuilds(files); !slices.Equal(got, tc.want) {
				t.Fatalf("targets = %v, want %v", got, tc.want)
			}
		})
	}
}

// fakeBuildDocker stands in for the docker CLI: `build` exports the
// context's src/ folder as the package stage (markers in the context make
// it fail, hang or export nothing); every other command succeeds.
func fakeBuildDocker(t *testing.T) (m *Manager, builds func() int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script docker stand-in")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "builds")
	bin := filepath.Join(dir, "docker")
	script := `#!/bin/sh
[ "$1" = build ] || exit 0
[ "$DOCKER_BUILDKIT" = 1 ] || { echo "ERROR: BuildKit is off" >&2; exit 3; }
echo "$*" >> '` + calls + `'
dest= ctx=
while [ $# -gt 0 ]; do
  case "$1" in
    build) ;;
    --output) shift; dest=${1#type=local,dest=} ;;
    --progress|--target) shift ;;
    *) ctx=$1 ;;
  esac
  shift
done
[ -e "$ctx/dist" ] && { echo "ERROR: the old dist is in the build context" >&2; exit 2; }
[ -f "$ctx/FAIL" ] && { echo "#5 [build 2/2] RUN tsc" >&2; echo "ERROR: failed to solve: boom" >&2; echo "#5 DONE" >&2; exit 1; }
[ -f "$ctx/SLOW" ] && exec sleep 30
mkdir -p "$dest"
[ -f "$ctx/EMPTY" ] || cp -R "$ctx/src/." "$dest/"
echo "#1 exporting to client directory" >&2
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Manager{cfg: Config{Docker: bin}}, func() int {
		b, _ := os.ReadFile(calls)
		return strings.Count(string(b), "\n")
	}
}

func buildWork(t *testing.T) string {
	work := t.TempDir()
	writeTree(t, work, map[string]string{
		"main.tf":                       `data "archive_file" "api" { source_dir = "${path.module}/services/api/dist" }`,
		"services/api/Dockerfile":       packageDockerfile,
		"services/api/src/index.mjs":    "v1",
		"services/api/src/lib/util.mjs": "util",
	})
	return work
}

func logged(rs *runState, text string) bool {
	return slices.ContainsFunc(rs.run.Log, func(l model.LogLine) bool { return strings.Contains(l.Text, text) })
}

func readDist(t *testing.T, work, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(work, "services", "api", "dist", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBuildServicesCache(t *testing.T) {
	m, builds := fakeBuildDocker(t)
	work := buildWork(t)
	ctx := context.Background()

	rs := &runState{}
	if _, err := m.buildArtifacts(ctx, rs, work); err != nil {
		t.Fatal(err)
	}
	if builds() != 1 || readDist(t, work, "index.mjs") != "v1" || readDist(t, work, "lib/util.mjs") != "util" {
		t.Fatalf("first build: %d builds, log %+v", builds(), rs.run.Log)
	}
	if !logged(rs, "[build services/api] #1 exporting to client directory") || !logged(rs, "[build services/api] Built in") {
		t.Errorf("build output not streamed with its prefix: %+v", rs.run.Log)
	}

	rs = &runState{}
	if _, err := m.buildArtifacts(ctx, rs, work); err != nil {
		t.Fatal(err)
	}
	if builds() != 1 || !logged(rs, "Sources unchanged") {
		t.Fatalf("unchanged sources rebuilt: %d builds, log %+v", builds(), rs.run.Log)
	}

	// A source change rebuilds dist from scratch: removed sources leave no
	// stale output behind.
	writeTree(t, work, map[string]string{"services/api/src/index.mjs": "v2"})
	if err := os.Remove(filepath.Join(work, "services", "api", "src", "lib", "util.mjs")); err != nil {
		t.Fatal(err)
	}
	rs = &runState{}
	if _, err := m.buildArtifacts(ctx, rs, work); err != nil {
		t.Fatal(err)
	}
	if builds() != 2 || readDist(t, work, "index.mjs") != "v2" || !logged(rs, "Sources changed") {
		t.Fatalf("source change: %d builds, log %+v", builds(), rs.run.Log)
	}
	if _, err := os.Stat(filepath.Join(work, "services", "api", "dist", "lib", "util.mjs")); !os.IsNotExist(err) {
		t.Errorf("stale dist file survived the rebuild: %v", err)
	}

	// dist edited behind the builder's back (e.g. a dist kept in the
	// workflow files and copied over it) is rebuilt.
	writeTree(t, work, map[string]string{"services/api/dist/index.mjs": "tampered"})
	rs = &runState{}
	if _, err := m.buildArtifacts(ctx, rs, work); err != nil {
		t.Fatal(err)
	}
	if builds() != 3 || readDist(t, work, "index.mjs") != "v2" || !logged(rs, "modified outside the build") {
		t.Fatalf("modified dist: %d builds, log %+v", builds(), rs.run.Log)
	}
}

func TestBuildServicesFailure(t *testing.T) {
	m, builds := fakeBuildDocker(t)
	work := buildWork(t)
	ctx := context.Background()
	if _, err := m.buildArtifacts(ctx, &runState{}, work); err != nil {
		t.Fatal(err)
	}

	writeTree(t, work, map[string]string{"services/api/FAIL": ""})
	rs := &runState{}
	_, err := m.buildArtifacts(ctx, rs, work)
	if err == nil || err.Error() != "build services/api: docker build exited with code 1: ERROR: failed to solve: boom" {
		t.Fatalf("err = %v", err)
	}
	if !logged(rs, "[build services/api] ERROR: failed to solve: boom") {
		t.Errorf("build error not in the log: %+v", rs.run.Log)
	}
	if _, err := os.Stat(filepath.Join(work, "services", "api", "dist")); !os.IsNotExist(err) {
		t.Errorf("a failed build left a dist for Terraform to zip: %v", err)
	}

	// Back to the sources of the last good build: dist is gone, so it is
	// built again.
	if err := os.Remove(filepath.Join(work, "services", "api", "FAIL")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.buildArtifacts(ctx, &runState{}, work); err != nil {
		t.Fatal(err)
	}
	if builds() != 3 || readDist(t, work, "index.mjs") != "v1" {
		t.Fatalf("recovery: %d builds", builds())
	}

	writeTree(t, work, map[string]string{"services/api/EMPTY": ""})
	if _, err := m.buildArtifacts(ctx, &runState{}, work); err == nil || !strings.Contains(err.Error(), "package stage holds no files") {
		t.Fatalf("empty package stage: %v", err)
	}
}

func TestBuildServicesCancel(t *testing.T) {
	m, _ := fakeBuildDocker(t)
	work := buildWork(t)
	writeTree(t, work, map[string]string{"services/api/SLOW": ""})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	_, err := m.buildArtifacts(ctx, &runState{}, work)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("cancel took %s", d)
	}
	if _, err := os.Stat(filepath.Join(work, "services", "api", "dist")); !os.IsNotExist(err) {
		t.Errorf("canceled build left a dist: %v", err)
	}
}

func TestBuildServicesNothingToBuild(t *testing.T) {
	m, builds := fakeBuildDocker(t)
	work := t.TempDir()
	writeTree(t, work, map[string]string{
		"main.tf":                    `data "archive_file" "py" { source_dir = "${path.module}/services/py/src" }`,
		"services/py/src/handler.py": "def handler(e, c): return 1",
	})
	if _, err := m.buildArtifacts(context.Background(), &runState{}, work); err != nil || builds() != 0 {
		t.Fatalf("err %v, %d builds", err, builds())
	}
}

// fakeImageDocker stands in for the docker CLI's image commands, keeping
// the tags it "built" in a file; calls returns the command lines so far.
func fakeImageDocker(t *testing.T) (m *Manager, calls func() []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script docker stand-in")
	}
	dir := t.TempDir()
	log, tags := filepath.Join(dir, "calls"), filepath.Join(dir, "tags")
	bin := filepath.Join(dir, "docker")
	script := `#!/bin/sh
echo "$*" >> '` + log + `'
tags='` + tags + `'
touch "$tags"
case "$1 $2" in
"image inspect") grep -qx "$5" "$tags" ;;
"image ls") sort -u "$tags" ;;
"image rm") shift 2; for r in "$@"; do grep -vx "$r" "$tags" > "$tags.new"; mv "$tags.new" "$tags"; done ;;
"tag "*) echo "$3" >> "$tags" ;;
"build "*)
  [ "$DOCKER_BUILDKIT" = 1 ] || exit 3
  for a in "$@"; do [ "$prev" = -t ] && echo "$a" >> "$tags"; prev=$a; done
  echo "#1 naming to docker.io/library/x done" >&2 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Manager{cfg: Config{Docker: bin}}, func() []string {
		b, _ := os.ReadFile(log)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

func countPrefix(lines []string, prefix string) int {
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func TestBuildImages(t *testing.T) {
	m, calls := fakeImageDocker(t)
	work := t.TempDir()
	writeTree(t, work, map[string]string{
		"main.tf": `variable "orders_image" {
  default = "nimbo.local/orders:latest"
}
resource "google_cloud_run_v2_service" "orders" {
  template {
    containers {
      image = var.orders_image
    }
  }
}
`,
		"services/orders/Dockerfile": "FROM golang:1.25 AS build\nRUN go build\nFROM alpine:3 AS runtime\nCMD [\"/app\"]\nFROM scratch AS package\n",
		"services/orders/main.go":    "package main",
	})
	runAs := func(wfID string) (*runState, []string) {
		t.Helper()
		rs := &runState{run: model.Run{WorkflowID: wfID}}
		vars, err := m.buildArtifacts(context.Background(), rs, work)
		if err != nil {
			t.Fatal(err)
		}
		return rs, vars
	}
	run := func() (*runState, []string) { return runAs("wf-img") }
	// The run's image variable names the workflow-scoped key tag the build
	// made, so Terraform sees a new reference after every source change.
	keyVar := func(vars []string) string {
		t.Helper()
		if len(vars) != 1 || !strings.HasPrefix(vars[0], "TF_VAR_orders_image=nimbo.local/orders:") || strings.HasSuffix(vars[0], ":latest") {
			t.Fatalf("image vars %q", vars)
		}
		return strings.TrimPrefix(vars[0], "TF_VAR_orders_image=")
	}

	rs, vars := run()
	v1 := keyVar(vars)
	builds := countPrefix(calls(), "build ")
	if builds != 1 || !slices.ContainsFunc(calls(), func(c string) bool {
		return strings.HasPrefix(c, "build --progress plain --target runtime -t nimbo.local/orders:latest -t "+v1+" ") &&
			strings.HasSuffix(c, " --label io.nimbo.workflow=wf-img "+filepath.Join(work, "services", "orders"))
	}) {
		t.Fatalf("calls: %q", calls())
	}
	if !logged(rs, "[image services/orders] #1 naming") || !logged(rs, "[image services/orders] Built nimbo.local/orders:latest") {
		t.Errorf("log: %+v", rs.run.Log)
	}

	rs, vars = run()
	if countPrefix(calls(), "build ") != 1 || countPrefix(calls(), "tag ") != 1 || !logged(rs, "Sources unchanged") || keyVar(vars) != v1 {
		t.Fatalf("unchanged sources: calls %q, vars %q, log %+v", calls(), vars, rs.run.Log)
	}

	// Another workflow with the same service and sources gets its own tag.
	if _, vars := runAs("wf-other"); keyVar(vars) == v1 {
		t.Fatalf("workflows share the image tag %s", v1)
	}

	removed := countPrefix(calls(), "image rm nimbo.local/orders:")
	writeTree(t, work, map[string]string{"services/orders/main.go": "package main // v2"})
	if _, vars = run(); keyVar(vars) == v1 {
		t.Fatalf("source change kept the image reference %s", v1)
	}
	if countPrefix(calls(), "build ") != 3 || countPrefix(calls(), "image rm nimbo.local/orders:") != removed+1 {
		t.Fatalf("source change: calls %q", calls())
	}

	m.removeImages(context.Background(), "wf-img")
	if last := calls()[len(calls())-1]; !strings.HasPrefix(last, "image rm ") || !strings.Contains(last, "nimbo.local/orders:latest") {
		t.Fatalf("remove: %q", last)
	}
}

func TestBuildImagesProblems(t *testing.T) {
	m, calls := fakeImageDocker(t)
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"no folder": {
			files: map[string]string{"main.tf": `locals { image = "nimbo.local/ghost:latest" }`},
			want:  "container images: nimbo.local/ghost: no services/ghost/Dockerfile to build it from",
		},
		"package stage last": {
			files: map[string]string{
				"main.tf":                `locals { image = "nimbo.local/fn" }`,
				"services/fn/Dockerfile": "FROM node:22 AS build\nFROM scratch AS package\n",
			},
			want: "container images: services/fn/Dockerfile: its last stage is package (the function files); add a stage named runtime for the nimbo.local/fn image",
		},
	} {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			writeTree(t, work, tc.files)
			_, err := m.buildArtifacts(context.Background(), &runState{}, work)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if n := countPrefix(calls(), "build "); n != 0 {
		t.Errorf("%d builds", n)
	}
}
