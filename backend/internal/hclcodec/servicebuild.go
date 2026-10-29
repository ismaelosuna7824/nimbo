package hclcodec

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Application code lives in service folders services/<svc>/. A service
// that needs a build (TypeScript, Go, Java, ...) has a Dockerfile whose
// stage named "package" holds the deployable files at its root; the build
// exports that stage into services/<svc>/dist, which the Terraform zips
// (archive_file source_dir) or uploads (fileset) for any cloud's functions
// or storage. The Nimbo sandbox builds before every plan, apply and
// destroy; an exported project builds with ServiceBuildCommand (README,
// Makefile, CI/CD pipeline).

// ServiceBuildCommand is the command that exports the package stage of
// the service folder dir (slash path) into dir/dist with BuildKit.
func ServiceBuildCommand(dir string) string {
	return "docker build --target package --output type=local,dest=" + dir + "/dist " + dir
}

// ServiceBuildScript builds every service folder of dirs from scratch:
// dist is removed first, since BuildKit's local exporter never deletes
// files a previous build left there.
func ServiceBuildScript(dirs []string) string {
	lines := make([]string, 0, 2*len(dirs))
	for _, d := range dirs {
		lines = append(lines, "rm -rf "+d+"/dist", ServiceBuildCommand(d))
	}
	return strings.Join(lines, "\n")
}

var packageStage = regexp.MustCompile(`(?im)^[ \t]*FROM[ \t].*[ \t]AS[ \t]+package[ \t]*(#.*)?$`)

// HasPackageStage reports whether a Dockerfile has a stage named
// "package" (case-insensitive, like Docker).
func HasPackageStage(dockerfile []byte) bool { return packageStage.Match(dockerfile) }

// ServiceBuilds returns the service folders (slash paths relative to the
// root module, sorted) that must be built before Terraform runs:
// <...>/services/<svc> folders with a Dockerfile that has a package stage
// whose dist (or a path inside it) a string of the Terraform refers to.
// files maps slash paths, relative to the root module, to the contents of
// at least every .tf file and every services/<svc>/Dockerfile; other
// files are ignored. In those strings path.module is the file's folder,
// path.root and path.cwd the root module; any other interpolation matches
// one path segment (services/${each.key}/dist), and one that starts the
// string matches any leading folders. Files that don't parse are skipped.
func ServiceBuilds(files map[string][]byte) []string {
	var services []string
	for p, data := range files {
		if path.Base(p) == "Dockerfile" && path.Base(path.Dir(path.Dir(p))) == "services" && HasPackageStage(data) {
			services = append(services, path.Dir(p))
		}
	}
	if len(services) == 0 {
		return nil
	}
	var patterns []distPattern
	for p, data := range files {
		if strings.HasSuffix(p, ".tf") {
			patterns = append(patterns, distRefs(data, p)...)
		}
	}
	var out []string
	for _, svc := range services {
		if slices.ContainsFunc(patterns, func(p distPattern) bool { return p.match(svc + "/dist") }) {
			out = append(out, svc)
		}
	}
	slices.Sort(out)
	return out
}

// Builds are what a project builds before Terraform runs.
type Builds struct {
	Dists  []string       // service folders whose dist the Terraform packages (ServiceBuilds)
	Images []ServiceImage // nimbo.local images the Terraform runs (ServiceImages)
}

// Empty reports a project with nothing to build.
func (b Builds) Empty() bool { return len(b.Dists) == 0 && len(b.Images) == 0 }

// DetectBuilds is ServiceBuilds and ServiceImages of files; images that
// can't be built (ServiceImages problems) are left out.
func DetectBuilds(files map[string][]byte) Builds {
	images, _ := ServiceImages(files)
	return Builds{Dists: ServiceBuilds(files), Images: images}
}

// Container images (C5): an image reference nimbo.local/<svc>[:tag] in the
// Terraform means "the image of services/<svc>/Dockerfile". The Nimbo
// sandbox builds it as nimbo.local/<svc>:latest before Terraform runs, so
// the emulators run it without a registry; an export pushes it to the
// user's registry and sets the image variable (ImageVariable) instead.

// LocalImageRegistry is the registry host of images Nimbo builds.
const LocalImageRegistry = "nimbo.local"

// ServiceImage is one image the Terraform refers to as nimbo.local/<Name>.
type ServiceImage struct {
	Name string // repository under nimbo.local (the service folder's name)
	Dir  string // the service folder (slash path relative to the root module)
	// Target is the Dockerfile stage to build: "runtime" when there is one,
	// else "" (the last stage).
	Target string
}

// Ref is the reference the Terraform uses locally.
func (s ServiceImage) Ref() string { return LocalImageRegistry + "/" + s.Name + ":latest" }

// ImageVariable is the Terraform variable that holds the image of service
// name (default nimbo.local/<name>:latest): <name>_image, with '-' and '.'
// as '_' so TF_VAR_<variable> is a valid environment variable name.
func ImageVariable(name string) string {
	return strings.NewReplacer("-", "_", ".", "_").Replace(name) + "_image"
}

// ImageBuildCommand builds s tagged ref.
func ImageBuildCommand(s ServiceImage, ref string) string {
	target := ""
	if s.Target != "" {
		target = "--target " + s.Target + " "
	}
	return "docker build " + target + "-t " + ref + " " + s.Dir
}

var (
	localImageRef = regexp.MustCompile(`nimbo\.local/([a-z0-9]+(?:[._-][a-z0-9]+)*)`)
	fromStage     = regexp.MustCompile(`(?im)^[ \t]*FROM[ \t]+(?:--\S+[ \t]+)*\S+(?:[ \t]+AS[ \t]+(\S+))?`)
)

// ServiceImages returns the images (sorted by name) the strings of the
// .tf and .tfvars files in files refer to as nimbo.local/<svc>, each with
// its folder: services/<svc> holding a Dockerfile (the shallowest such
// folder; the root's services/<svc> first). files: see ServiceBuilds.
// problems explain references that can't be built: no such folder, or a
// Dockerfile whose last stage is package without a runtime stage.
func ServiceImages(files map[string][]byte) (images []ServiceImage, problems []string) {
	names := map[string]bool{}
	for p, data := range files {
		if !strings.HasSuffix(p, ".tf") && !strings.HasSuffix(p, ".tfvars") {
			continue
		}
		for _, s := range stringLiterals(data, p) {
			for _, m := range localImageRef.FindAllStringSubmatch(s, -1) {
				names[m[1]] = true
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		dir := ""
		for p := range files {
			d := path.Dir(p)
			if path.Base(p) == "Dockerfile" && path.Base(d) == name && path.Base(path.Dir(d)) == "services" &&
				(dir == "" || strings.Count(d, "/") < strings.Count(dir, "/") || strings.Count(d, "/") == strings.Count(dir, "/") && d < dir) {
				dir = d
			}
		}
		if dir == "" {
			problems = append(problems, fmt.Sprintf("%s/%s: no services/%s/Dockerfile to build it from", LocalImageRegistry, name, name))
			continue
		}
		var stages []string
		for _, m := range fromStage.FindAllSubmatch(files[dir+"/Dockerfile"], -1) {
			stages = append(stages, strings.ToLower(string(m[1])))
		}
		img := ServiceImage{Name: name, Dir: dir}
		switch {
		case slices.Contains(stages, "runtime"):
			img.Target = "runtime"
		case len(stages) > 0 && stages[len(stages)-1] == "package":
			problems = append(problems, fmt.Sprintf("%s/Dockerfile: its last stage is package (the function files); add a stage named runtime for the %s/%s image", dir, LocalImageRegistry, name))
			continue
		}
		images = append(images, img)
	}
	return images, problems
}

// stringLiterals returns the literal string parts of an HCL file (template
// parts included; interpolations left out). Files that don't parse give
// none.
func stringLiterals(src []byte, name string) []string {
	f, diags := hclsyntax.ParseConfig(src, name, hcl.InitialPos)
	if diags.HasErrors() {
		return nil
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil
	}
	var out []string
	_ = hclsyntax.VisitAll(body, func(n hclsyntax.Node) hcl.Diagnostics {
		if e, ok := n.(*hclsyntax.LiteralValueExpr); ok && e.Val.Type() == cty.String && e.Val.IsKnown() && !e.Val.IsNull() {
			out = append(out, e.Val.AsString())
		}
		return nil
	})
	return out
}

// distPattern is a path to a services/<svc>/dist folder as a slash path
// pattern relative to the root module: parts not known before Terraform
// runs are "*". Without anchored the string starts with such a part, so
// only the pattern's segments after the first are matched, against the
// end of a path.
type distPattern struct {
	glob     string
	anchored bool
}

func (p distPattern) match(dist string) bool {
	if p.anchored {
		ok, _ := path.Match(p.glob, dist)
		return ok
	}
	want := strings.Split(p.glob, "/")[1:]
	have := strings.Split(dist, "/")
	if len(want) == 0 || len(want) > len(have) {
		return false
	}
	ok, _ := path.Match(strings.Join(want, "/"), strings.Join(have[len(have)-len(want):], "/"))
	return ok
}

// distRefs returns the dist folders the strings of the Terraform file rel
// refer to.
func distRefs(src []byte, rel string) []distPattern {
	f, diags := hclsyntax.ParseConfig(src, rel, hcl.InitialPos)
	if diags.HasErrors() {
		return nil
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil
	}
	moduleDir := path.Dir(rel)
	var templates []*hclsyntax.TemplateExpr
	var literals []*hclsyntax.LiteralValueExpr
	_ = hclsyntax.VisitAll(body, func(n hclsyntax.Node) hcl.Diagnostics {
		switch e := n.(type) {
		case *hclsyntax.TemplateExpr:
			templates = append(templates, e)
		case *hclsyntax.LiteralValueExpr:
			literals = append(literals, e)
		}
		return nil
	})
	var out []distPattern
	parts := map[hclsyntax.Expression]bool{}
	for _, t := range templates {
		for _, p := range t.Parts {
			parts[p] = true
		}
		if p, ok := distPatternOf(t.Parts, moduleDir); ok {
			out = append(out, p)
		}
	}
	for _, l := range literals {
		if !parts[l] {
			if p, ok := distPatternOf([]hclsyntax.Expression{l}, moduleDir); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// distPatternOf joins a string's parts into a distPattern cut after its
// services/<svc>/dist segments; false when the string names no such
// folder.
func distPatternOf(parts []hclsyntax.Expression, moduleDir string) (distPattern, bool) {
	var sb strings.Builder
	anchored := true
	for i, part := range parts {
		s, known := templatePart(part, moduleDir)
		if !known {
			if i == 0 {
				anchored = false
			}
			s = "*"
		}
		sb.WriteString(s)
	}
	s := sb.String()
	if !strings.Contains(s, "services/") || path.IsAbs(s) {
		return distPattern{}, false
	}
	if anchored {
		s = path.Clean(s)
		if s == ".." || strings.HasPrefix(s, "../") {
			return distPattern{}, false
		}
	}
	segs := strings.Split(s, "/")
	for i := 2; i < len(segs); i++ {
		if segs[i] == "dist" && segs[i-2] == "services" {
			glob := strings.Join(segs[:i+1], "/")
			if !anchored && (len(segs[:i+1]) < 4 || slices.Contains(segs[1:i+1], "..")) {
				return distPattern{}, false
			}
			return distPattern{glob: glob, anchored: anchored}, true
		}
	}
	return distPattern{}, false
}

func templatePart(e hclsyntax.Expression, moduleDir string) (string, bool) {
	switch e := e.(type) {
	case *hclsyntax.LiteralValueExpr:
		if e.Val.Type() == cty.String && e.Val.IsKnown() && !e.Val.IsNull() {
			return e.Val.AsString(), true
		}
	case *hclsyntax.ScopeTraversalExpr:
		if len(e.Traversal) == 2 && e.Traversal.RootName() == "path" {
			if attr, ok := e.Traversal[1].(hcl.TraverseAttr); ok {
				switch attr.Name {
				case "module":
					return moduleDir, true
				case "root", "cwd":
					return ".", true
				}
			}
		}
	}
	return "", false
}
