package codeguide

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// _files holds real, buildable source trees as layers: a bundle is the
// union of its layers, later layers overriding earlier ones, each path
// relative to the service folder. The leading underscore keeps the Go
// tool from compiling the Go skeleton inside it.
//
//go:embed all:_files
var filesFS embed.FS

// serviceName is the skeleton's service folder: one deployable for a
// single service or a modular monolith, one per bounded context (shown for
// "items") with microservices.
func serviceName(style string) string {
	if style == StyleMicroservices {
		return "items"
	}
	return "api"
}

// layers lists the bundle's layer folders under _files for o.
func layers(o Options) []string {
	if o.Mode == ModeArchitecture {
		base := "smoke/" + o.Language + "/"
		if o.Target == TargetFunction {
			return []string{base + "core", base + o.Platform + "-function"}
		}
		deps := o.Platform
		if deps != "aws" && deps != "azure" && deps != "gcp" {
			deps = "generic"
		}
		ls := []string{base + "core", base + "container", base + "deps-" + deps}
		if o.Target == TargetVM {
			ls = append(ls, "vm")
		}
		return ls
	}
	base := "full/" + o.Language + "/"
	store := "postgres"
	if o.Platform == "aws" {
		store = "dynamodb"
	}
	switch o.Target {
	case TargetFunction:
		return []string{base + "common", base + store, base + o.Platform + "-function"}
	case TargetVM:
		return []string{base + "common", base + store, base + "container", "vm"}
	}
	return []string{base + "common", base + store, base + "container"}
}

func files(o Options) ([]File, error) {
	byPath := map[string]string{}
	for _, layer := range layers(o) {
		root := "_files/" + layer
		if _, err := fs.Stat(filesFS, root); err != nil {
			return nil, fmt.Errorf("no %s files for %s/%s/%s", o.Mode, o.Language, o.Platform, o.Target)
		}
		err := fs.WalkDir(filesFS, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := filesFS.ReadFile(p)
			rel := strings.TrimPrefix(p, root+"/")
			if path.Base(rel) == "go.mod.txt" {
				// A go.mod would make the embedded tree a separate module,
				// which embed refuses.
				rel = path.Join(path.Dir(rel), "go.mod")
			}
			byPath[rel] = string(b)
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	prefix := "services/" + serviceName(o.Style) + "/"
	out := make([]File, 0, len(byPath))
	for rel, content := range byPath {
		out = append(out, File{Path: prefix + rel, Purpose: purpose(rel, content), Content: content})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// purpose explains a skeleton file from its path (and, for Dockerfiles,
// its stages). Every embedded file must match a rule (tested).
func purpose(rel, content string) string {
	base := path.Base(rel)
	lower := strings.ToLower(rel)
	switch {
	case base == "Dockerfile" && strings.Contains(content, " AS package"):
		return "Build: the last stage, package, holds the deployable files at its root; Nimbo builds it into dist before Terraform runs (" + "docker build --target package --output type=local,dest=dist .)."
	case base == "Dockerfile" && strings.Contains(content, "fnproject/"):
		return "OCI Functions image: build stage plus the Fn runtime stage; push it to OCIR."
	case base == "Dockerfile":
		return "Container image: build and test stage, then a small non-root runtime stage."
	case base == "cloud-init.yaml.tftpl":
		return "VM boot (cloud-init): installs Docker and runs the service image under systemd; rendered by Terraform templatefile()."
	case base == "smoke.mjs" || base == "smoke.py":
		return "Smoke checks: /health plus a write/read round trip on every store whose env var is set (SDKs load lazily)."
	case base == "host.json":
		return "Azure Functions host settings (extension bundle)."
	case base == "func.yaml":
		return "OCI Functions (Fn) function definition; runtime docker uses the Dockerfile."
	case base == "__init__.py":
		return "Python package marker."
	case base == "package.json" || base == "tsconfig.json" || base == "go.mod" || base == "pom.xml" ||
		strings.HasSuffix(base, ".csproj") || strings.HasPrefix(base, "requirements"):
		return "Dependencies and build settings."
	case strings.Contains(lower, "test"):
		return "Unit tests: domain rules and use cases through the in-memory adapter (no mocks, no network)."
	case strings.Contains(lower, "domain"):
		return "Domain: entity, value object and typed errors; imports no SDK, HTTP or database code."
	case strings.Contains(lower, "application"):
		return "Application: use cases and the repository port they own; returns plain views."
	case strings.Contains(lower, "memory"):
		return "Driven adapter: in-memory repository for tests and local runs."
	case strings.Contains(lower, "inbound") || strings.Contains(lower, "httpapi") || base == "http.py":
		return "Driving adapter: HTTP request to use case; maps typed domain errors to status codes and never leaks internals."
	case strings.Contains(lower, "store") || strings.Contains(lower, "repositor"):
		return "Driven adapter: the repository implementation for the platform's database (idempotent create)."
	case isEntry(base):
		return "Entry point and composition root: reads the env, builds the adapters, wires them to the use cases and translates the platform's request/response."
	}
	return ""
}

func isEntry(base string) bool {
	name := strings.TrimSuffix(base, path.Ext(base))
	switch name {
	case "index", "handler", "main", "server", "func", "function_app", "api", "smoke", "Handler", "Function", "Main", "Program":
		return true
	}
	return false
}
