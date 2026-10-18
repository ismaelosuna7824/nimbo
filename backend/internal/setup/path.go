package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// AugmentPath prepends the well-known directories where Docker engines and
// package managers install the docker CLI (and its credential helpers) to
// this process's PATH. An app launched from the macOS Finder or Dock gets
// PATH=/usr/bin:/bin:/usr/sbin:/sbin, which contains none of them. Only
// existing directories that are not already on PATH are added, so a shell
// user's PATH order is preserved. It returns the new PATH.
func AugmentPath() string {
	home, _ := os.UserHomeDir()
	path := augmentPath(os.Getenv("PATH"), wellKnownDirs(runtime.GOOS, home), dirExists)
	os.Setenv("PATH", path)
	return path
}

// wellKnownDirs lists, in priority order, where docker CLIs live on goos.
func wellKnownDirs(goos, home string) []string {
	switch goos {
	case "windows":
		dirs := []string{`C:\Program Files\Docker\Docker\resources\bin`}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, `AppData\Local\Programs\Rancher Desktop\resources\resources\win32\bin`))
		}
		return dirs
	case "linux":
		dirs := []string{"/usr/local/bin", "/usr/bin", "/snap/bin"}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, ".docker", "bin"), filepath.Join(home, ".rd", "bin"))
		}
		return dirs
	}
	dirs := []string{"/usr/local/bin", "/opt/homebrew/bin"}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".orbstack", "bin"))
	}
	dirs = append(dirs, "/Applications/Docker.app/Contents/Resources/bin")
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".docker", "bin"),
			filepath.Join(home, ".rd", "bin"),
			filepath.Join(home, ".colima", "bin"))
	}
	return append(dirs, "/usr/bin")
}

// augmentPath returns current with the existing dirs that it lacks prepended
// (in dirs order) and duplicate or empty entries removed.
func augmentPath(current string, dirs []string, exists func(string) bool) string {
	sep := string(os.PathListSeparator)
	var existing []string
	for p := range strings.SplitSeq(current, sep) {
		if p != "" {
			existing = append(existing, p)
		}
	}
	seen := make(map[string]bool, len(existing)+len(dirs))
	for _, p := range existing {
		seen[pathKey(p)] = true
	}
	var out []string
	for _, d := range dirs {
		if k := pathKey(d); !seen[k] && exists(d) {
			seen[k] = true
			out = append(out, d)
		}
	}
	added := make(map[string]bool, len(existing))
	for _, p := range existing {
		if k := pathKey(p); !added[k] {
			added[k] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// pathKey identifies a PATH entry: trailing separators are ignored, and
// case too on Windows.
func pathKey(p string) string {
	if len(p) > 1 {
		p = strings.TrimRight(p, `/\`)
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
