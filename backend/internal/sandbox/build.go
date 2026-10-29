package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
)

// Application code lives in workflow asset folders services/<svc>/ (see
// hclcodec.ServiceBuilds). Before Terraform plans, applies or destroys,
// buildServices exports each service's package stage into its dist in the
// workdir copy (never the user's assets) with BuildKit, so `terraform
// plan` zips what the sources build today.

// buildsFile records, per built service folder, the digest of the inputs
// the current dist was built from and of dist itself.
const buildsFile = ".nimbo/builds.json"

// buildOutDir receives a build before it replaces dist.
const buildOutDir = ".nimbo/build-out"

// buildVersion is part of the input digest: bump it when the build
// command changes what a build produces.
const buildVersion = "package-v1"

type buildRecord struct {
	Inputs string `json:"inputs"`
	Output string `json:"output"`
}

// buildInputs reads what the build detection looks at in work: every
// .tf and .tfvars file and every services/<svc>/Dockerfile, by slash path.
func buildInputs(work string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(work, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(work, p)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if base := d.Name(); strings.HasPrefix(base, ".") || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && (strings.HasSuffix(rel, ".tf") || strings.HasSuffix(rel, ".tfvars") ||
			path.Base(rel) == "Dockerfile" && path.Base(path.Dir(path.Dir(rel))) == "services") {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			files[rel] = data
		}
		return nil
	})
	return files, err
}

// buildArtifacts builds what the configuration in work needs before
// Terraform runs: the service dists it packages (hclcodec.ServiceBuilds)
// and the nimbo.local images it runs (hclcodec.ServiceImages).
func (m *Manager) buildArtifacts(ctx context.Context, rs *runState, work string) error {
	files, err := buildInputs(work)
	if err != nil {
		return fmt.Errorf("find service builds: %w", err)
	}
	if err := m.buildServices(ctx, rs, work, hclcodec.ServiceBuilds(files)); err != nil {
		return err
	}
	images, problems := hclcodec.ServiceImages(files)
	if len(problems) > 0 {
		return fmt.Errorf("container images: %s", strings.Join(problems, "; "))
	}
	return m.buildImages(ctx, rs, work, images)
}

// treeDigest hashes the regular files under root (slash path, executable
// bit, content), skipping the top-level entry skip. A missing root hashes
// to "".
func treeDigest(root, skip string) (string, error) {
	h := sha256.New()
	files := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == skip {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintf(h, "%s\x00%t\x00", rel, info.Mode()&0o111 != 0)
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		h.Write([]byte{0})
		files++
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		if _, serr := os.Stat(root); errors.Is(serr, fs.ErrNotExist) {
			return "", nil
		}
	}
	if err != nil || files == 0 {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readBuildRecords(work string) map[string]buildRecord {
	recs := map[string]buildRecord{}
	if data, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(buildsFile))); err == nil {
		_ = json.Unmarshal(data, &recs)
	}
	return recs
}

func writeBuildRecords(work string, recs map[string]buildRecord) error {
	for dir := range recs {
		if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(dir))); err != nil {
			delete(recs, dir)
		}
	}
	data, err := json.Marshal(recs)
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(work, filepath.FromSlash(buildsFile)), data)
}

// buildServices builds the dist of every service folder of dirs whose
// sources or dist changed since its last build, streaming the build into
// the run log. A failed build fails the run and leaves no dist behind, so
// Terraform never zips an outdated build.
func (m *Manager) buildServices(ctx context.Context, rs *runState, work string, dirs []string) error {
	if len(dirs) == 0 {
		return nil
	}
	recs := readBuildRecords(work)
	built := map[string]int64{}
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		prefix := "[build " + dir + "]"
		svc := filepath.Join(work, filepath.FromSlash(dir))
		dist := filepath.Join(svc, "dist")
		inputs, err := treeDigest(svc, "dist")
		if err != nil {
			return fmt.Errorf("build %s: hash sources: %w", dir, err)
		}
		inputs = buildVersion + ":" + inputs
		output, err := treeDigest(dist, "")
		if err != nil {
			return fmt.Errorf("build %s: hash dist: %w", dir, err)
		}
		rec, seen := recs[dir]
		switch {
		case seen && rec.Inputs == inputs && output != "" && rec.Output == output:
			m.logLine(rs, "system", prefix+" Sources unchanged: reusing dist")
			continue
		case !seen || output == "":
			m.logLine(rs, "system", prefix+" Building the package stage into "+dir+"/dist")
		case rec.Inputs != inputs:
			m.logLine(rs, "system", prefix+" Sources changed: rebuilding "+dir+"/dist")
		default:
			m.logLine(rs, "system", prefix+" "+dir+"/dist was modified outside the build (do not keep build output in the workflow files): rebuilding it")
		}

		// The old dist goes first: it is stale, must not end up in the
		// build context, and a failed build must leave nothing to zip.
		delete(recs, dir)
		if err := writeBuildRecords(work, recs); err != nil {
			return err
		}
		if err := os.RemoveAll(dist); err != nil {
			return fmt.Errorf("build %s: remove the old dist: %w", dir, err)
		}
		out := filepath.Join(work, filepath.FromSlash(buildOutDir), strings.ReplaceAll(dir, "/", "_"))
		if err := os.RemoveAll(out); err != nil {
			return err
		}
		m.logLine(rs, "system", prefix+" $ "+hclcodec.ServiceBuildCommand(dir))
		start := time.Now()
		if err := m.dockerBuild(ctx, rs, prefix, "--target", "package", "--output", "type=local,dest="+out, svc); err != nil {
			_ = os.RemoveAll(out)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("build %s: %w", dir, err)
		}
		if err := os.Rename(out, dist); err != nil {
			return fmt.Errorf("build %s: %w", dir, err)
		}
		output, err = treeDigest(dist, "")
		if err != nil {
			return fmt.Errorf("build %s: hash dist: %w", dir, err)
		}
		if output == "" {
			return fmt.Errorf("build %s: the Dockerfile's package stage holds no files: copy the deployable files into it (e.g. COPY --from=build /out/ /)", dir)
		}
		recs[dir] = buildRecord{Inputs: inputs, Output: output}
		if err := writeBuildRecords(work, recs); err != nil {
			return err
		}
		if err := filepath.WalkDir(dist, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(work, p)
			built[filepath.ToSlash(rel)] = info.Size()
			return nil
		}); err != nil {
			return err
		}
		m.logLine(rs, "system", fmt.Sprintf("%s Built in %s", prefix, time.Since(start).Round(100*time.Millisecond)))
	}
	// A rebuilt dist replaces files Terraform's container may have seen
	// before (see settleWorkdir).
	if err := m.settleWorkdir(ctx, work, built); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m.logLine(rs, "system", "Warning: "+err.Error())
	}
	return nil
}

// imageVersion is part of an image's cache key: bump it when the build
// command changes what a build produces.
const imageVersion = "image-v1"

// buildImages makes sure every image of images exists as
// nimbo.local/<svc>:latest, the reference the Terraform uses: a build
// tagged with its cache key too (nimbo.local/<svc>:<key>, from the
// workflow, the target and the folder's files without dist/), or, when
// that tag exists already, only the latest tag moved onto it. Builds are
// labelled with the workflow (Remove deletes them); a rebuild drops the
// workflow's older keys of that image.
func (m *Manager) buildImages(ctx context.Context, rs *runState, work string, images []hclcodec.ServiceImage) error {
	wfID := rs.run.WorkflowID
	for _, img := range images {
		if err := ctx.Err(); err != nil {
			return err
		}
		prefix := "[image " + img.Dir + "]"
		dir := filepath.Join(work, filepath.FromSlash(img.Dir))
		digest, err := treeDigest(dir, "dist")
		if err != nil {
			return fmt.Errorf("image %s: hash sources: %w", img.Dir, err)
		}
		sum := sha256.Sum256([]byte(imageVersion + "\x00" + wfID + "\x00" + img.Target + "\x00" + digest))
		latest, keyed := img.Ref(), hclcodec.LocalImageRegistry+"/"+img.Name+":"+hex.EncodeToString(sum[:6])
		if _, err := m.docker(ctx, "image", "inspect", "--format", "{{.Id}}", keyed); err == nil {
			if _, err := m.docker(ctx, "tag", keyed, latest); err != nil {
				return fmt.Errorf("image %s: %w", latest, err)
			}
			m.logLine(rs, "system", prefix+" Sources unchanged: reusing "+keyed+" as "+latest)
			continue
		}
		args := []string{}
		if img.Target != "" {
			args = append(args, "--target", img.Target)
		}
		args = append(args, "-t", latest, "-t", keyed, "--label", labelWorkflow+"="+wfID, dir)
		m.logLine(rs, "system", prefix+" $ "+hclcodec.ImageBuildCommand(img, latest))
		start := time.Now()
		if err := m.dockerBuild(ctx, rs, prefix, args...); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("image %s: %w", img.Dir, err)
		}
		m.logLine(rs, "system", fmt.Sprintf("%s Built %s in %s", prefix, latest, time.Since(start).Round(100*time.Millisecond)))
		m.pruneImages(ctx, img.Name, wfID, keyed)
	}
	return nil
}

// localImages lists the nimbo.local images (repository:tag) wfID built,
// of repository name ("" = any).
func (m *Manager) localImages(ctx context.Context, wfID, name string) []string {
	ref := hclcodec.LocalImageRegistry + "/*"
	if name != "" {
		ref = hclcodec.LocalImageRegistry + "/" + name
	}
	out, err := m.docker(ctx, "image", "ls", "--filter", "reference="+ref, "--filter", "label="+labelWorkflow+"="+wfID, "--format", "{{.Repository}}:{{.Tag}}")
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

// pruneImages drops the workflow's tags of image name other than keep and
// latest. Best effort: an image a container still uses stays.
func (m *Manager) pruneImages(ctx context.Context, name, wfID, keep string) {
	var old []string
	for _, ref := range m.localImages(ctx, wfID, name) {
		if ref != keep && !strings.HasSuffix(ref, ":latest") {
			old = append(old, ref)
		}
	}
	if len(old) > 0 {
		_, _ = m.docker(ctx, append([]string{"image", "rm"}, old...)...)
	}
}

// removeImages deletes the nimbo.local images wfID built. Best effort: an
// image a container of another workflow still uses stays.
func (m *Manager) removeImages(ctx context.Context, wfID string) {
	if refs := m.localImages(ctx, wfID, ""); len(refs) > 0 {
		_, _ = m.docker(ctx, append([]string{"image", "rm"}, refs...)...)
	}
}

// dockerBuild runs `docker build --progress plain args...` with BuildKit,
// each output line logged after prefix. A canceled ctx interrupts the
// build (SIGINT, so BuildKit stops its steps) and kills the CLI 15 s
// later.
func (m *Manager) dockerBuild(ctx context.Context, rs *runState, prefix string, args ...string) error {
	cmd := exec.CommandContext(ctx, m.dockerBin(), append([]string{"build", "--progress", "plain"}, args...)...)
	cmd.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")
	cmd.Cancel = func() error { return interrupt(cmd.Process) }
	cmd.WaitDelay = 15 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var (
		mu      sync.Mutex
		failure string // BuildKit's last "ERROR: ..." line, else the last line
		last    string
	)
	var wg sync.WaitGroup
	for stream, r := range map[string]io.Reader{"stdout": stdout, "stderr": stderr} {
		wg.Go(func() {
			scanLines(r, func(line string) {
				m.logLine(rs, stream, prefix+" "+line)
				if line = strings.TrimSpace(line); line != "" {
					mu.Lock()
					last = line
					if strings.HasPrefix(line, "ERROR") {
						failure = line
					}
					mu.Unlock()
				}
			})
		})
	}
	wg.Wait()
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		msg := fmt.Sprintf("docker build exited with code %d", exitErr.ExitCode())
		if failure == "" {
			failure = last
		}
		if failure != "" {
			msg += ": " + failure
		}
		return errors.New(msg)
	}
	return err
}
