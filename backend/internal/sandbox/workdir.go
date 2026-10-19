package sandbox

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const manifestFile = ".nimbo/manifest.json"

// skipAsset reports whether a top-level assets entry must not be copied
// (local Terraform metadata and state from wherever the assets came from).
func skipAsset(name string) bool {
	return strings.HasPrefix(name, ".terraform") || strings.HasPrefix(name, "terraform.tfstate") || name == ".nimbo"
}

// isRootConfig reports whether a root-level file is Terraform configuration
// or an auto-loaded variables file.
func isRootConfig(name string) bool {
	for _, suffix := range []string{".tf", ".tf.json", ".auto.tfvars", ".auto.tfvars.json"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// syncWorkdir writes assets and then files (except the environments/
// folder) into work. Files written by the previous sync that are no longer
// part of the input, and stale root configuration files, are deleted.
// Everything else stays: provider installs, the lock file, Terraform state
// and files Terraform itself produced (e.g. archive_file outputs). Files
// are overwritten in place rather than recreated so container bind mounts
// never observe a directory vanishing between runs. It returns the
// existing files whose size changed (slash path -> new size): see
// settleWorkdir.
func syncWorkdir(work string, files map[string][]byte, assetsDir string) (map[string]int64, error) {
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}
	want := map[string]bool{}
	clean := make(map[string][]byte, len(files))
	for name, content := range files {
		p := path.Clean(filepath.ToSlash(name))
		if p == "." || path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, ".nimbo/") {
			return nil, fmt.Errorf("invalid file path %q", name)
		}
		if strings.HasPrefix(p, "environments/") {
			continue
		}
		clean[p] = content
		want[p] = true
	}
	assets, err := listAssets(assetsDir)
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	for _, a := range assets {
		want[a] = true
	}

	var stale []string
	if data, err := os.ReadFile(filepath.Join(work, manifestFile)); err == nil {
		_ = json.Unmarshal(data, &stale)
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Type().IsRegular() && isRootConfig(e.Name()) {
			stale = append(stale, e.Name())
		}
	}
	for _, p := range stale {
		p = path.Clean(p)
		if want[p] || p == "." || path.IsAbs(p) || strings.HasPrefix(p, "..") {
			continue
		}
		if err := os.Remove(filepath.Join(work, filepath.FromSlash(p))); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}

	resized := map[string]int64{}
	track := func(rel string, write func(dst string) error) error {
		dst := filepath.Join(work, filepath.FromSlash(rel))
		before, statErr := os.Stat(dst)
		if err := write(dst); err != nil {
			return err
		}
		if after, err := os.Stat(dst); statErr == nil && err == nil && after.Size() != before.Size() {
			resized[rel] = after.Size()
		}
		return nil
	}
	for _, a := range assets {
		if err := track(a, func(dst string) error { return copyFile(filepath.Join(assetsDir, filepath.FromSlash(a)), dst) }); err != nil {
			return nil, fmt.Errorf("copy asset %s: %w", a, err)
		}
	}
	for p, content := range clean {
		if err := track(p, func(dst string) error { return writeFile(dst, content) }); err != nil {
			return nil, err
		}
	}

	manifest := make([]string, 0, len(want))
	for p := range want {
		manifest = append(manifest, p)
	}
	slices.Sort(manifest)
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	return resized, writeFile(filepath.Join(work, filepath.FromSlash(manifestFile)), data)
}

// settleScript exits once every "path size" argument pair has that size
// under /work, or with status 1 after about 10 seconds.
const settleScript = `i=0
while [ $i -lt 100 ]; do
  ok=1 f=
  for a in "$@"; do
    if [ -z "$f" ]; then f=$a; continue; fi
    [ "$(stat -c %s "/work/$f" 2>/dev/null)" = "$a" ] || ok=0
    f=
  done
  [ $ok = 1 ] && exit 0
  i=$((i+1))
  sleep 0.1
done
exit 1`

// settleWorkdir waits until containers see the new size of the files
// syncWorkdir rewrote. VM-backed Docker file sharing (Docker Desktop,
// OrbStack on macOS) can serve a rewritten file with its old size for a
// moment, so Terraform would read it cut short ("Unclosed configuration
// block"); verified on OrbStack. Contents of unchanged size and created or
// deleted files are seen right away.
func (m *Manager) settleWorkdir(ctx context.Context, work string, resized map[string]int64) error {
	if len(resized) == 0 {
		return nil
	}
	args := []string{"run", "--rm", "--network", "none", "-v", work + ":/work", podImage, "sh", "-c", settleScript, "sh"}
	for _, p := range slices.Sorted(maps.Keys(resized)) {
		args = append(args, p, strconv.FormatInt(resized[p], 10))
	}
	if _, err := m.docker(ctx, args...); err != nil {
		return fmt.Errorf("containers still see an old version of the rewritten files: %w", err)
	}
	return nil
}

// listAssets returns the regular files under dir as slash paths.
func listAssets(dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !strings.Contains(rel, "/") && skipAsset(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

func writeFile(p string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, content, 0o644)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// writeOCIKey writes a fresh RSA key for the OCI provider; floci-oci parses
// request signatures but does not verify them.
func writeOCIKey(work string) error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	p := filepath.Join(work, ".nimbo", "oci_api_key.pem")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, pemBytes, 0o600)
}
