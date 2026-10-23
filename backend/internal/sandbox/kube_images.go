package sandbox

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// KubeBuildPlan lists the images an up builds: the workflow's explicit
// builds, plus every image of the rendered objects that has no registry
// host and whose repository is the name of a folder (under sourceDir,
// at most four levels deep) holding a Dockerfile — "api:dev" builds
// api/Dockerfile. Explicit builds win; the result is sorted by image.
func KubeBuildPlan(sourceDir string, builds []model.KubeBuild, images []string) []model.KubeBuild {
	byImage := map[string]model.KubeBuild{}
	for _, b := range builds {
		if b.Image != "" {
			byImage[normalizeImage(b.Image)] = b
		}
	}
	dirs := dockerfileDirs(sourceDir)
	for _, img := range images {
		key := normalizeImage(img)
		if _, ok := byImage[key]; ok || registryHost(img) {
			continue
		}
		repo := strings.TrimSuffix(key[:strings.LastIndex(key, ":")], "/")
		base := path.Base(repo)
		for _, d := range dirs {
			if path.Base(d) == base {
				byImage[key] = model.KubeBuild{Image: img, Context: d}
				break
			}
		}
	}
	out := slices.Collect(maps.Values(byImage))
	slices.SortFunc(out, func(a, b model.KubeBuild) int { return strings.Compare(a.Image, b.Image) })
	return out
}

// registryHost reports whether an image reference names a registry
// ("ghcr.io/x", "localhost:5000/x"): those are pulled, never built.
func registryHost(ref string) bool {
	first, _, ok := strings.Cut(ref, "/")
	return ok && (strings.ContainsAny(first, ".:") || first == "localhost")
}

// dockerfileDirs lists the folders (slash paths, "." for the root) holding
// a file named Dockerfile, shallowest first.
func dockerfileDirs(root string) []string {
	if root == "" {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || strings.Count(rel, "/") >= 4) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "Dockerfile" {
			out = append(out, path.Dir(rel))
		}
		return nil
	})
	slices.SortStableFunc(out, func(a, b string) int { return strings.Count(a, "/") - strings.Count(b, "/") })
	return out
}

// safeRel validates a slash path relative to the workflow files.
func safeRel(p string) (string, error) {
	if p == "" {
		return ".", nil
	}
	c := path.Clean(p)
	if path.IsAbs(p) || filepath.IsAbs(p) || c == ".." || strings.HasPrefix(c, "../") {
		return "", invalidKube("invalid build path %q: it must stay inside the workflow's files", p)
	}
	return c, nil
}

// buildAndLoad builds each image with docker build (labelled for the
// workflow, so cluster-delete removes it) and imports it into the node's
// containerd, where pods with imagePullPolicy IfNotPresent find it.
func (m *Manager) buildAndLoad(ctx context.Context, rs *runState, wfID, node, sourceDir string, builds []model.KubeBuild) error {
	for _, b := range builds {
		if err := ctx.Err(); err != nil {
			return err
		}
		ctxDir, err := safeRel(b.Context)
		if err != nil {
			return err
		}
		dockerfile := b.Dockerfile
		if dockerfile == "" {
			dockerfile = "Dockerfile"
		}
		df, err := safeRel(path.Join(ctxDir, dockerfile))
		if err != nil {
			return err
		}
		abs := filepath.Join(sourceDir, filepath.FromSlash(ctxDir))
		if _, err := os.Stat(filepath.Join(sourceDir, filepath.FromSlash(df))); err != nil {
			return invalidKube("build of %s: %s does not exist", b.Image, df)
		}
		args := []string{"build", "--progress", "plain", "-t", b.Image, "-f", filepath.Join(sourceDir, filepath.FromSlash(df)),
			"--label", ComposeLabel + "=" + wfID}
		if b.Target != "" {
			args = append(args, "--target", b.Target)
		}
		for _, k := range slices.Sorted(maps.Keys(b.Args)) {
			args = append(args, "--build-arg", k+"="+b.Args[k])
		}
		args = append(args, abs)
		m.logLine(rs, "system", fmt.Sprintf("$ docker build -t %s %s", b.Image, ctxDir))
		start := time.Now()
		code, err := m.composeExec(ctx, rs, args)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("docker build of %s exited with code %d", b.Image, code)
		}
		m.logLine(rs, "system", fmt.Sprintf("Built %s in %s; loading it into the cluster", b.Image, time.Since(start).Round(100*time.Millisecond)))
		if err := m.loadImage(ctx, node, b.Image); err != nil {
			return fmt.Errorf("load %s into the cluster: %w", b.Image, err)
		}
	}
	return nil
}

// loadImage streams `docker save` into `ctr images import` inside the node.
func (m *Manager) loadImage(ctx context.Context, node, image string) error {
	save := exec.CommandContext(ctx, m.dockerBin(), "save", image)
	imp := exec.CommandContext(ctx, m.dockerBin(), "exec", "-i", node, "ctr", "-n", "k8s.io", "images", "import", "-")
	pipe, err := save.StdoutPipe()
	if err != nil {
		return err
	}
	imp.Stdin = pipe
	var saveErr, impOut strings.Builder
	save.Stderr = &saveErr
	imp.Stdout, imp.Stderr = &impOut, &impOut
	if err := save.Start(); err != nil {
		return err
	}
	if err := imp.Start(); err != nil {
		_ = save.Process.Kill()
		_ = save.Wait()
		return err
	}
	ierr := imp.Wait()
	serr := save.Wait()
	switch {
	case serr != nil:
		return fmt.Errorf("docker save: %s", firstNonEmpty(strings.TrimSpace(saveErr.String()), serr.Error()))
	case ierr != nil:
		return fmt.Errorf("ctr import: %s", firstNonEmpty(lastLines(impOut.String(), 3), ierr.Error()))
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// removeBuiltImages deletes the images built for the workflow (labelled).
func (m *Manager) removeBuiltImages(ctx context.Context, wfID string) error {
	out, err := m.docker(ctx, "image", "ls", "--format", "{{.ID}} {{.Repository}}:{{.Tag}}", "--filter", "label="+ComposeLabel+"="+wfID)
	if err != nil {
		return err
	}
	var refs []string
	for line := range strings.Lines(out) {
		id, ref, _ := strings.Cut(strings.TrimSpace(line), " ")
		if strings.Contains(ref, "<none>") {
			ref = id
		}
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	if refs = slices.Compact(slices.Sorted(slices.Values(refs))); len(refs) == 0 {
		return nil
	}
	if _, err := m.docker(ctx, append([]string{"image", "rm", "-f"}, refs...)...); err != nil && !isNoSuch(err) {
		return err
	}
	return nil
}
