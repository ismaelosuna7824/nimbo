package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Build metrics of docker workflows. Compose runs with --progress plain, so
// BuildKit prints every vertex of a build as numbered lines:
//
//	#9 [api builder 3/5] RUN npm ci      (header; "[api …]" only when several services build)
//	#9 CACHED | #9 DONE 12.3s | #9 ERROR: … | #9 CANCELED
//	#12 naming to docker.io/library/nimbo-dk-<wf>-api done
//
// After a build or up run these lines (with the time Nimbo read each of
// them) give each service's steps, cache hits and build time; `docker image
// inspect` and `docker image history` then give the image's size and its
// biggest layers. The size is split into base and own layers with the
// number of history entries the Dockerfile adds (BuildHint.OwnLayers).

// BuildHint describes the Dockerfile of a service that builds an image.
type BuildHint struct {
	// BaseImage is the external FROM image of the final stage's chain.
	BaseImage string
	// OwnLayers is the number of image history entries the Dockerfile adds
	// on top of BaseImage (one per instruction, FROM excluded); <= 0 when
	// unknown.
	OwnLayers int
	// Stack are technology hints of the build context (ServiceFacts.Stack).
	Stack []string
}

const (
	maxBuildRecords = 20
	maxBuildLayers  = 8
)

var (
	vertexHeadRe = regexp.MustCompile(`^#(\d+) \[([^\]]*)\] (.+)$`)
	vertexLineRe = regexp.MustCompile(`^#(\d+) (.+)$`)
	vertexDoneRe = regexp.MustCompile(`^DONE (\d+(?:\.\d+)?)s$`)
	stepCountRe  = regexp.MustCompile(`^\d+/\d+$`)
	namingRe     = regexp.MustCompile(`^naming to (\S+)`)
)

// buildVertex is one BuildKit vertex read from plain progress output.
type buildVertex struct {
	service     string
	bracket     string
	stage       string
	instruction string
	internal    bool
	from        bool
	cached      bool
	failed      bool
	finished    bool
	start, end  time.Time
	duration    time.Duration
}

// parsedBuild is what a run log says about a build.
type parsedBuild struct {
	vertices []*buildVertex // in order of appearance
	images   map[string]string
	start    time.Time
	end      time.Time
}

// parseBuildLog reads BuildKit's plain progress lines of a run log. built
// are the services that have a build section: a vertex is attributed to the
// service named first in its brackets ("[api builder 2/5]"), or to the only
// built service when one builds (BuildKit then omits the name).
func parseBuildLog(lines []model.LogLine, built []string) parsedBuild {
	pb := parsedBuild{images: map[string]string{}}
	byID := map[string]*buildVertex{}
	isBuilt := map[string]bool{}
	for _, s := range built {
		isBuilt[s] = true
	}
	single := ""
	if len(built) == 1 {
		single = built[0]
	}
	for _, ln := range lines {
		text := strings.TrimSpace(ln.Text)
		m := vertexLineRe.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		id, rest := m[1], m[2]
		v := byID[id]
		if v == nil {
			v = &buildVertex{service: single, start: ln.T, internal: true}
			if h := vertexHeadRe.FindStringSubmatch(text); h != nil {
				v.bracket, v.instruction = h[2], h[3]
				words := strings.Fields(h[2])
				if len(built) > 1 {
					v.service = ""
					if len(words) > 0 && isBuilt[words[0]] {
						v.service, words = words[0], words[1:]
					}
				}
				switch {
				case len(words) >= 2 && stepCountRe.MatchString(words[len(words)-1]):
					v.stage, v.internal = words[0], false
				case len(words) == 1 && stepCountRe.MatchString(words[0]):
					v.internal = false
				}
			} else {
				v.instruction = rest
			}
			v.from = !v.internal && strings.HasPrefix(strings.ToUpper(v.instruction), "FROM ")
			byID[id] = v
			pb.vertices = append(pb.vertices, v)
			if pb.start.IsZero() || ln.T.Before(pb.start) {
				pb.start = ln.T
			}
			continue
		}
		switch {
		case rest == "CACHED":
			v.cached, v.finished, v.end = true, true, ln.T
		case vertexDoneRe.MatchString(rest):
			secs, _ := strconv.ParseFloat(vertexDoneRe.FindStringSubmatch(rest)[1], 64)
			v.duration = time.Duration(secs * float64(time.Second))
			v.finished, v.end = true, ln.T
			if s := ln.T.Add(-v.duration); s.Before(v.start) {
				v.start = s
			}
		case strings.HasPrefix(rest, "ERROR"), rest == "CANCELED":
			v.failed, v.finished, v.end = true, true, ln.T
			if v.duration == 0 {
				v.duration = ln.T.Sub(v.start)
			}
		default:
			if n := namingRe.FindStringSubmatch(rest); n != nil && v.service != "" {
				pb.images[v.service] = n[1]
			}
			continue
		}
		if v.end.After(pb.end) {
			pb.end = v.end
		}
		if v.start.Before(pb.start) {
			pb.start = v.start
		}
	}
	return pb
}

// serviceBuilds groups the parsed vertices per service.
func (pb parsedBuild) serviceBuilds() map[string]*model.ServiceBuildStats {
	out := map[string]*model.ServiceBuildStats{}
	spans := map[string][2]time.Time{}
	for _, v := range pb.vertices {
		if v.service == "" {
			continue
		}
		sbs := out[v.service]
		if sbs == nil {
			sbs = &model.ServiceBuildStats{Service: v.service, NodeID: model.ComposeServiceID(v.service), Steps: []model.BuildStep{}, Layers: []model.ImageLayer{}, Findings: []model.BuildFinding{}}
			out[v.service] = sbs
		}
		name := v.instruction
		if v.bracket != "" {
			name = "[" + v.bracket + "] " + v.instruction
		}
		sbs.Steps = append(sbs.Steps, model.BuildStep{
			Name: name, Stage: v.stage, Instruction: v.instruction, Internal: v.internal,
			Cached: v.cached, DurationMs: v.duration.Milliseconds(), Error: v.failed,
		})
		if v.failed {
			sbs.Failed = true
		}
		if !v.internal && !v.from && v.finished && !v.failed {
			if v.cached {
				sbs.CacheHits++
			} else {
				sbs.CacheMisses++
			}
		}
		end := v.end
		if end.IsZero() {
			end = v.start
		}
		sp, ok := spans[v.service]
		if !ok {
			sp = [2]time.Time{v.start, end}
		}
		if v.start.Before(sp[0]) {
			sp[0] = v.start
		}
		if end.After(sp[1]) {
			sp[1] = end
		}
		spans[v.service] = sp
	}
	for name, sp := range spans {
		out[name].DurationMs = sp[1].Sub(sp[0]).Milliseconds()
	}
	return out
}

// historyEntry is one line of `docker image history --format '{{json .}}'`.
type historyEntry struct {
	CreatedBy string `json:"CreatedBy"`
	Size      string `json:"Size"`
}

// parseHistory parses `docker image history --no-trunc --human=false
// --format '{{json .}}'` (newest entry first; sizes in bytes).
func parseHistory(out string) []model.ImageLayer {
	var layers []model.ImageLayer
	for line := range strings.Lines(out) {
		var e historyEntry
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &e) != nil {
			continue
		}
		size, err := strconv.ParseInt(strings.TrimSpace(e.Size), 10, 64)
		if err != nil {
			size = parseSize(e.Size)
		}
		layers = append(layers, model.ImageLayer{CreatedBy: cleanCreatedBy(e.CreatedBy), SizeBytes: size})
	}
	return layers
}

// cleanCreatedBy turns a history command into its Dockerfile instruction.
func cleanCreatedBy(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, " # buildkit")
	if rest, ok := strings.CutPrefix(s, "/bin/sh -c #(nop) "); ok {
		return strings.TrimSpace(rest)
	}
	if rest, ok := strings.CutPrefix(s, "RUN /bin/sh -c "); ok {
		return "RUN " + rest
	}
	if rest, ok := strings.CutPrefix(s, "/bin/sh -c "); ok {
		return "RUN " + rest
	}
	return s
}

// splitLayers marks the base image's entries of history (newest first)
// given the number of entries the Dockerfile added, and returns the base
// size. ok is false when the split is unknown.
func splitLayers(history []model.ImageLayer, own int) (int64, bool) {
	if own <= 0 || own > len(history) {
		return 0, false
	}
	var base int64
	for i := own; i < len(history); i++ {
		history[i].Base = true
		base += history[i].SizeBytes
	}
	return base, true
}

// biggestLayers returns the n largest non-empty layers, largest first.
func biggestLayers(history []model.ImageLayer, n int) []model.ImageLayer {
	out := slices.DeleteFunc(slices.Clone(history), func(l model.ImageLayer) bool { return l.SizeBytes == 0 })
	slices.SortStableFunc(out, func(a, b model.ImageLayer) int {
		switch {
		case a.SizeBytes > b.SizeBytes:
			return -1
		case a.SizeBytes < b.SizeBytes:
			return 1
		}
		return 0
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// imageFacts reads a built image's ID, size and history.
func (m *Manager) imageFacts(ctx context.Context, ref string) (id string, size int64, history []model.ImageLayer, err error) {
	out, err := m.docker(ctx, "image", "inspect", "--format", "{{.Id}} {{.Size}}", ref)
	if err != nil {
		return "", 0, nil, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return "", 0, nil, fmt.Errorf("unexpected docker image inspect output %q", out)
	}
	id = f[0]
	size, _ = strconv.ParseInt(f[1], 10, 64)
	hist, err := m.docker(ctx, "image", "history", "--no-trunc", "--human=false", "--format", "{{json .}}", ref)
	if err != nil {
		return id, size, nil, err
	}
	return id, size, parseHistory(hist), nil
}

// ---------------------------------------------------------------------------
// Findings

// stackSize is the size above which an image is flagged for its stack.
type stackSize struct {
	limit   int64
	label   string
	typical string
	fix     string
	rules   []string
}

var stackSizes = map[string]stackSize{
	"node":   {1_000_000_000, "Node.js", "150–400 MB on node:*-alpine or *-slim", "Build in a full node stage, ship a node:*-alpine/slim (or distroless) runtime stage with production dependencies only (npm ci --omit=dev), and keep node_modules/.git out of the context with .dockerignore.", []string{"dk_heavy_base", "dk_single_stage", "dk_dockerignore"}},
	"python": {1_000_000_000, "Python", "100–400 MB on python:*-slim", "Use a python:*-slim runtime, install with pip --no-cache-dir (or a cache mount), and build wheels in a separate stage.", []string{"dk_heavy_base", "dk_pip_cache", "dk_single_stage"}},
	"ruby":   {1_000_000_000, "Ruby", "150–400 MB on ruby:*-slim", "Use a ruby:*-slim runtime stage and keep build tools in a builder stage.", []string{"dk_heavy_base", "dk_single_stage"}},
	"php":    {800_000_000, "PHP", "100–300 MB on php:*-fpm-alpine", "Use an alpine-based PHP image and install composer dependencies in a builder stage with --no-dev.", []string{"dk_heavy_base", "dk_single_stage"}},
	"java":   {600_000_000, "Java", "200–350 MB on a JRE runtime", "Build with Maven/Gradle in a JDK stage and run the jar on a JRE (eclipse-temurin:*-jre) or distroless java image.", []string{"dk_single_stage", "dk_heavy_base"}},
	"dotnet": {500_000_000, ".NET", "100–250 MB on mcr.microsoft.com/dotnet/aspnet or runtime", "Publish with the SDK image in a build stage and run on dotnet/aspnet (or runtime-deps for self-contained apps).", []string{"dk_single_stage", "dk_heavy_base"}},
	"go":     {100_000_000, "Go", "10–30 MB on scratch or distroless", "Compile in a golang stage (CGO_ENABLED=0) and copy only the binary into scratch, distroless/static or alpine.", []string{"dk_single_stage", "dk_heavy_base"}},
	"rust":   {100_000_000, "Rust", "10–50 MB on distroless or debian-slim", "Build with cargo in a rust stage and copy only the release binary into a slim runtime image.", []string{"dk_single_stage", "dk_heavy_base"}},
	"static": {200_000_000, "static site", "under 60 MB on nginx:alpine", "Serve the built files from nginx:alpine (or caddy:alpine) and build them in a separate node stage.", []string{"dk_heavy_base", "dk_single_stage", "dk_dockerignore"}},
}

var stackHints = map[string]string{
	"node": "node", "express": "node", "nextjs": "node", "nestjs": "node", "typescript": "node", "bun": "node", "deno": "node",
	"go": "go", "python": "python", "fastapi": "python", "django": "python", "flask": "python",
	"java": "java", "spring": "java", "kotlin": "java", "scala": "java",
	"dotnet": "dotnet", "csharp": "dotnet", "rust": "rust", "ruby": "ruby", "rails": "ruby", "php": "php", "laravel": "php",
}

// imageStack names the stack of a built image from the build context's
// hints, else from its base image.
func imageStack(base string, hints []string) string {
	for _, h := range hints {
		if s, ok := stackHints[strings.ToLower(h)]; ok {
			return s
		}
	}
	repo := strings.ToLower(base)
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	repo, _, _ = strings.Cut(repo, ":")
	repo, _, _ = strings.Cut(repo, "@")
	switch {
	case strings.HasPrefix(repo, "node"), repo == "bun", repo == "deno":
		return "node"
	case strings.HasPrefix(repo, "golang"):
		return "go"
	case strings.HasPrefix(repo, "python"), strings.HasPrefix(repo, "pypy"):
		return "python"
	case strings.HasPrefix(repo, "rust"):
		return "rust"
	case strings.HasPrefix(repo, "ruby"):
		return "ruby"
	case strings.HasPrefix(repo, "php"):
		return "php"
	case strings.Contains(repo, "jdk"), strings.Contains(repo, "jre"), strings.HasPrefix(repo, "eclipse-temurin"),
		strings.HasPrefix(repo, "amazoncorretto"), strings.HasPrefix(repo, "maven"), strings.HasPrefix(repo, "gradle"):
		return "java"
	case strings.Contains(base, "dotnet/"):
		return "dotnet"
	case repo == "nginx", repo == "httpd", repo == "caddy":
		return "static"
	}
	return ""
}

var depInstallRe = regexp.MustCompile(`(?i)\b(npm (ci|install|i)|yarn( install)?|pnpm (install|i)|pip3? install|poetry install|uv (sync|pip)|go (mod download|build)|mvn|gradle|dotnet (restore|publish)|bundle install|composer install|cargo (build|fetch)|apt-get install|apk add)\b`)

const slowStep = 20 * time.Second

// buildFindings flags an image too big for its stack, a base image that
// makes up most of it, and slow dependency installs that missed the cache.
func buildFindings(s *model.ServiceBuildStats) []model.BuildFinding {
	out := []model.BuildFinding{}
	if ss, ok := stackSizes[s.Stack]; ok && s.SizeBytes > ss.limit {
		out = append(out, model.BuildFinding{
			Code: "image_too_big", Severity: "warning",
			Message: fmt.Sprintf("%s image is %s — big for a %s service (typically %s; flagged above %s)", s.Service, decimalBytes(s.SizeBytes), ss.label, ss.typical, decimalBytes(ss.limit)),
			Fix:     ss.fix, Rules: slices.Clone(ss.rules),
		})
	}
	if s.BaseImage != "" && s.BaseSizeBytes > 300_000_000 && float64(s.BaseSizeBytes) >= 0.7*float64(s.SizeBytes) {
		out = append(out, model.BuildFinding{
			Code: "base_dominates", Severity: "info",
			Message: fmt.Sprintf("the base image %s is %s of the %s image (%d%%)", s.BaseImage, decimalBytes(s.BaseSizeBytes), decimalBytes(s.SizeBytes), s.BaseSizeBytes*100/max(s.SizeBytes, 1)),
			Fix:     "A slim, alpine or distroless variant of the base image (or a separate runtime stage) removes most of it.",
			Rules:   []string{"dk_heavy_base", "dk_single_stage"},
		})
	}
	if s.CacheHits > 0 {
		for _, st := range s.Steps {
			if st.Cached || st.Internal || st.Error || time.Duration(st.DurationMs)*time.Millisecond < slowStep || !depInstallRe.MatchString(st.Instruction) {
				continue
			}
			out = append(out, model.BuildFinding{
				Code: "slow_uncached_step", Severity: "info",
				Message: fmt.Sprintf("%q took %.0f s and was not cached", truncate(st.Instruction, 80), float64(st.DurationMs)/1000),
				Fix:     "Copy only the dependency manifests (package.json/lock, requirements.txt, go.mod/go.sum, …) before installing, so code changes keep this layer cached; a BuildKit cache mount (RUN --mount=type=cache) speeds up the misses.",
				Rules:   []string{"dk_layer_order", "dk_cache_mount"},
			})
		}
	}
	return out
}

// decimalBytes formats a size like docker images does ("1.21 GB").
func decimalBytes(n int64) string {
	switch {
	case n >= 1e9:
		return strconv.FormatFloat(float64(n)/1e9, 'f', 2, 64) + " GB"
	case n >= 1e6:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + " MB"
	case n >= 1e3:
		return strconv.FormatFloat(float64(n)/1e3, 'f', 1, 64) + " kB"
	}
	return strconv.FormatInt(n, 10) + " B"
}

// ---------------------------------------------------------------------------
// Recording

func (m *Manager) perfDir(wfID string) string { return filepath.Join(m.wfDir(wfID), "perf") }

var buildsMu sync.Mutex

// recordBuild turns the log of a finished build/up run into a ComposeBuild,
// stores it with the delta against each service's previous build and
// publishes compose.build. Runs that built nothing record nothing.
func (m *Manager) recordBuild(ctx context.Context, rs *runState, built []string, images map[string]string, hints map[string]BuildHint) {
	if len(built) == 0 {
		return
	}
	m.mu.Lock()
	lines := slices.Clone(rs.run.Log)
	run := m.snapshot(rs, false)
	m.mu.Unlock()
	pb := parseBuildLog(lines, built)
	stats := pb.serviceBuilds()
	if len(stats) == 0 {
		return
	}
	b := model.ComposeBuild{
		RunID: run.ID, Action: run.Action, At: pb.start.UTC(), DurationMs: pb.end.Sub(pb.start).Milliseconds(),
		Succeeded: run.Status == model.RunSucceeded, Services: []model.ServiceBuildStats{},
	}
	for _, name := range slices.Sorted(maps.Keys(stats)) {
		s := stats[name]
		hint := hints[name]
		s.BaseImage = hint.BaseImage
		s.Stack = imageStack(hint.BaseImage, hint.Stack)
		ref := pb.images[name]
		if ref == "" {
			ref = images[name]
		}
		s.Image = strings.TrimPrefix(ref, "docker.io/library/")
		if !s.Failed && ref != "" {
			if id, size, history, err := m.imageFacts(ctx, ref); err == nil {
				s.ImageID, s.SizeBytes = id, size
				if base, ok := splitLayers(history, hint.OwnLayers); ok {
					s.BaseSizeBytes = base
				} else {
					s.BaseImage = ""
				}
				s.Layers = biggestLayers(history, maxBuildLayers)
			}
		}
		s.Findings = buildFindings(s)
		b.Services = append(b.Services, *s)
	}
	buildsMu.Lock()
	defer buildsMu.Unlock()
	prev := m.loadBuilds(rs.run.WorkflowID)
	for i := range b.Services {
		b.Services[i].Delta = buildDelta(b.Services[i], prev)
	}
	all := append([]model.ComposeBuild{b}, prev...)
	if len(all) > maxBuildRecords {
		all = all[:maxBuildRecords]
	}
	if err := m.saveBuilds(rs.run.WorkflowID, all); err != nil {
		m.logLine(rs, "system", "Could not save build metrics: "+err.Error())
	}
	m.publish(model.EventComposeBuild, rs.run.WorkflowID, b)
}

// buildDelta compares s with the newest earlier build of the same service
// that produced an image.
func buildDelta(s model.ServiceBuildStats, prev []model.ComposeBuild) *model.BuildDelta {
	if s.Failed || s.SizeBytes == 0 {
		return nil
	}
	for _, b := range prev {
		for _, p := range b.Services {
			if p.Service != s.Service || p.Failed || p.SizeBytes == 0 {
				continue
			}
			return &model.BuildDelta{
				PrevRunID: b.RunID, DurationMs: s.DurationMs - p.DurationMs, SizeBytes: s.SizeBytes - p.SizeBytes,
				CacheHits: s.CacheHits - p.CacheHits, CacheMisses: s.CacheMisses - p.CacheMisses,
			}
		}
	}
	return nil
}

func (m *Manager) buildsFile(wfID string) string {
	return filepath.Join(m.perfDir(wfID), "builds.json")
}

// loadBuilds reads the recorded builds, newest first (none when missing or
// unreadable). Callers hold buildsMu.
func (m *Manager) loadBuilds(wfID string) []model.ComposeBuild {
	data, err := os.ReadFile(m.buildsFile(wfID))
	if err != nil {
		return nil
	}
	var builds []model.ComposeBuild
	if json.Unmarshal(data, &builds) != nil {
		return nil
	}
	return builds
}

func (m *Manager) saveBuilds(wfID string, builds []model.ComposeBuild) error {
	data, err := json.MarshalIndent(builds, "", "  ")
	if err != nil {
		return err
	}
	p := m.buildsFile(wfID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// ComposeBuilds returns the workflow's recorded builds, newest first, at
// most limit (0 = all kept).
func (m *Manager) ComposeBuilds(wfID string, limit int) (model.ComposeBuilds, error) {
	if err := checkID(wfID); err != nil {
		return model.ComposeBuilds{}, err
	}
	buildsMu.Lock()
	builds := m.loadBuilds(wfID)
	buildsMu.Unlock()
	if builds == nil {
		builds = []model.ComposeBuild{}
	}
	if limit > 0 && len(builds) > limit {
		builds = builds[:limit]
	}
	return model.ComposeBuilds{Builds: builds}, nil
}

// removePerf deletes the workflow's recorded performance data.
func (m *Manager) removePerf(wfID string) error {
	buildsMu.Lock()
	defer buildsMu.Unlock()
	if err := os.RemoveAll(m.perfDir(wfID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
