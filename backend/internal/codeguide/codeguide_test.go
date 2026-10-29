package codeguide

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// combos yields every option combination Get accepts, with the error it
// must return for the ones that don't exist.
func combos(yield func(o Options, wantErr bool) bool) {
	for _, mode := range Modes {
		for _, lang := range append([]string{""}, Languages...) {
			for _, style := range Styles {
				for _, platform := range Platforms {
					for _, target := range Targets {
						local := platform == "docker" || platform == "kubernetes"
						wantErr := local && target != TargetContainer ||
							mode == ModeFull && target == TargetFunction && platform != "aws" && lang != "" && !slices.Contains(nonAWSFunctionLanguages, lang)
						if !yield(Options{Mode: mode, Language: lang, Style: style, Platform: platform, Target: target}, wantErr) {
							return
						}
					}
				}
			}
		}
	}
}

var lastStage = regexp.MustCompile(`(?im)^FROM\s+.*$`)

func TestGetEveryCombination(t *testing.T) {
	n := 0
	for o, wantErr := range combos {
		g, err := Get(o)
		if wantErr {
			if err == nil {
				t.Errorf("%+v: no error", o)
			}
			continue
		}
		if err != nil {
			t.Errorf("%+v: %v", o, err)
			continue
		}
		n++
		if g.Mode == "" || g.Style == "" || g.Platform == "" || g.Target == "" || len(g.Principles) == 0 || len(g.Layout) == 0 ||
			len(g.Compute) == 0 || len(g.Testing) == 0 || len(g.Security) == 0 || len(g.Observability) == 0 || len(g.Sources) == 0 ||
			g.Packaging.Kind == "" || g.Packaging.How == "" || len(g.Spec.Questions) == 0 || len(g.Spec.Templates) == 0 || len(g.Spec.Gate) == 0 {
			t.Errorf("%+v: empty required field: %+v", o, g)
			continue
		}
		if o.Mode == ModeFull && o.Language == "" {
			if len(g.Languages) == 0 || len(g.Files) != 0 {
				t.Errorf("%+v: want language options and no files", o)
			}
			continue
		}
		if len(g.Files) == 0 || g.Language == "" {
			t.Errorf("%+v: no files", o)
			continue
		}
		checkFiles(t, o, g)
		checkPackaging(t, o, g)
	}
	if n < 300 {
		t.Fatalf("only %d combinations checked", n)
	}
}

func checkFiles(t *testing.T, o Options, g Guide) {
	t.Helper()
	svc := "services/" + serviceName(o.Style) + "/"
	dockerfile := ""
	for _, f := range g.Files {
		if !strings.HasPrefix(f.Path, svc) || strings.Contains(f.Path, "..") || f.Content == "" || f.Purpose == "" {
			t.Errorf("%+v: bad file %s (purpose %q)", o, f.Path, f.Purpose)
		}
		if strings.HasPrefix(f.Path, svc+"dist/") {
			t.Errorf("%+v: %s is build output", o, f.Path)
		}
		if f.Path == svc+"Dockerfile" {
			dockerfile = f.Content
		}
	}
	stages := lastStage.FindAllString(dockerfile, -1)
	last := ""
	if len(stages) > 0 {
		last = strings.TrimSpace(stages[len(stages)-1])
	}
	switch g.Packaging.Kind {
	case "build-zip":
		if last != "FROM scratch AS package" {
			t.Errorf("%+v: build Dockerfile ends with %q", o, last)
		}
	case "zip":
		if dockerfile != "" {
			t.Errorf("%+v: zip packaging ships a Dockerfile", o)
		}
	default:
		if !strings.HasSuffix(last, "AS runtime") {
			t.Errorf("%+v: %s Dockerfile ends with %q, want a runtime stage", o, g.Packaging.Kind, last)
		}
	}
	if o.Target == TargetVM && !slices.ContainsFunc(g.Files, func(f File) bool { return f.Path == svc+"deploy/cloud-init.yaml.tftpl" }) {
		t.Errorf("%+v: no cloud-init template", o)
	}
}

func checkPackaging(t *testing.T, o Options, g Guide) {
	t.Helper()
	tf := g.Packaging.Terraform
	if o.Platform == "docker" || o.Platform == "kubernetes" {
		if tf != "" || !strings.Contains(g.Packaging.How, "services/") {
			t.Errorf("%+v: packaging %+v", o, g.Packaging)
		}
		return
	}
	if diags := hclcodec.CheckSyntax("main.tf", []byte(tf)); len(diags) > 0 {
		t.Errorf("%+v: snippet does not parse: %v\n%s", o, diags, tf)
	}
	svc := "services/" + serviceName(o.Style)
	switch g.Packaging.Kind {
	case "zip", "build-zip":
		want := svc + "/src"
		if g.Packaging.Kind == "build-zip" {
			want = svc + "/dist"
		}
		if !strings.Contains(tf, `source_dir  = "${path.module}/`+want+`"`) {
			t.Errorf("%+v: snippet does not zip %s:\n%s", o, want, tf)
		}
		var dockerfile []byte
		for _, f := range g.Files {
			if f.Path == svc+"/Dockerfile" {
				dockerfile = []byte(f.Content)
			}
		}
		builds := hclcodec.ServiceBuilds(map[string][]byte{"main.tf": []byte(tf), svc + "/Dockerfile": dockerfile})
		if built := slices.Contains(builds, svc); built != (g.Packaging.Kind == "build-zip") {
			t.Errorf("%+v: Nimbo builds %v for %s packaging", o, builds, g.Packaging.Kind)
		}
	}
}

func TestSourcesAreHTTPS(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range sources {
		if s.Title == "" || !strings.HasPrefix(s.URL, "https://") || seen[s.URL] {
			t.Errorf("source %+v", s)
		}
		seen[s.URL] = true
	}
}

func TestSpecTemplates(t *testing.T) {
	for _, f := range specGuide.Templates {
		if !strings.HasPrefix(f.Path, "openspec/changes/") || f.Content == "" {
			t.Errorf("template %+v", f)
		}
	}
	proposal := specGuide.Templates[0].Content
	if !strings.Contains(proposal, "[NEEDS CLARIFICATION:") {
		t.Error("proposal template lacks the clarification marker")
	}
	if spec := specGuide.Templates[1].Content; !strings.Contains(spec, "### Requirement:") || !strings.Contains(spec, "#### Scenario:") {
		t.Error("spec template lacks the OpenSpec requirement/scenario format")
	}
}

func TestErrors(t *testing.T) {
	for _, o := range []Options{
		{Mode: "draft"}, {Language: "cobol"}, {Style: "soa"}, {Platform: "heroku"}, {Target: "edge"},
		{Platform: "docker", Target: "function"}, {Platform: "k8s", Target: "vm"},
		{Mode: "full", Language: "go", Platform: "azure", Target: "function"},
	} {
		if _, err := Get(o); err == nil {
			t.Errorf("%+v: no error", o)
		}
	}
	g, err := Get(Options{Mode: " FULL ", Language: "TS", Platform: "k8s"})
	if err != nil || g.Language != "typescript" || g.Platform != "kubernetes" || g.Target != TargetContainer || g.Packaging.Kind != "k8s-image" {
		t.Fatalf("aliases: %+v %v", g, err)
	}
	g, _ = Get(Options{})
	if g.Mode != ModeArchitecture || g.Language != "javascript" || g.Platform != "aws" || g.Target != TargetFunction || g.Packaging.Kind != "zip" {
		t.Fatalf("defaults: %+v", g)
	}
	g, _ = Get(Options{Mode: "full", Platform: "gcp", Target: "function"})
	if len(g.Languages) != 3 {
		t.Fatalf("gcp function languages = %+v", g.Languages)
	}
}

// Every embedded file is reachable from some combination and has a purpose.
func TestEveryEmbeddedFileIsUsed(t *testing.T) {
	used := map[string]bool{}
	for o, wantErr := range combos {
		if wantErr || o.Mode == ModeFull && o.Language == "" {
			continue
		}
		for _, layer := range layers(mustNormalize(t, o)) {
			used["_files/"+layer] = true
		}
	}
	err := filepath.WalkDir("_files", func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || strings.Count(p, "/") != 3 && p != "_files/vm" {
			return err
		}
		if !used[filepath.ToSlash(p)] {
			t.Errorf("layer %s is never used", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func mustNormalize(t *testing.T, o Options) Options {
	t.Helper()
	n, err := normalize(o)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The JavaScript and Python sources compile (when node/python3 exist).
func TestSourcesCompile(t *testing.T) {
	node, _ := exec.LookPath("node")
	python, _ := exec.LookPath("python3")
	if node == "" && python == "" {
		t.Skip("node and python3 not installed")
	}
	err := filepath.WalkDir("_files", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		var cmd *exec.Cmd
		switch path.Ext(p) {
		case ".mjs":
			if node != "" {
				cmd = exec.Command(node, "--check", p)
			}
		case ".py":
			if python != "" {
				// Compile to a throwaway location: never write __pycache__ into the tree.
				cmd = exec.Command(python, "-c", "import sys; compile(open(sys.argv[1]).read(), sys.argv[1], 'exec')", p)
			}
		}
		if cmd != nil {
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s: %v\n%s", p, err, out)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The Terraform snippets match the provider schemas, and each compute
// option's Local agrees with the catalog's Floci support.
func TestTerraformAgainstCatalog(t *testing.T) {
	c, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for o, wantErr := range combos {
		if wantErr || o.Mode == ModeFull && o.Language == "" || o.Platform == "docker" || o.Platform == "kubernetes" {
			continue
		}
		g, _ := Get(o)
		if seen[g.Packaging.Terraform] {
			continue
		}
		seen[g.Packaging.Terraform] = true
		wf, diags, err := hclcodec.Import("main.tf", map[string][]byte{"main.tf": []byte(g.Packaging.Terraform)})
		if err != nil || hasError(diags) {
			t.Errorf("%+v: import: %v %v", o, err, diags)
			continue
		}
		for _, d := range c.Validate(wf) {
			if d.Severity == model.SeverityError {
				t.Errorf("%+v: %s: %s", o, d.Message, d.Detail)
			}
		}
	}
	for platform, opts := range compute {
		for _, opt := range opts {
			if opt.ID == "" || opt.Name == "" || opt.When == "" || opt.AvoidWhen == "" || !slices.Contains(Targets, opt.Target) {
				t.Errorf("%s: %+v", platform, opt)
			}
			local := true
			for _, typ := range opt.TerraformTypes {
				s, ok := c.Schema(model.KindResource, typ)
				if !ok || s == nil {
					t.Errorf("%s/%s: unknown type %s", platform, opt.ID, typ)
					continue
				}
				local = local && s.FlociSupported
			}
			// Catalog support means the resources deploy; whether workloads
			// run was verified separately (runsLocally, controlPlane, unverified).
			if (opt.Local == exportOnly) == local || !slices.Contains([]string{runsLocally, controlPlane, unverified, exportOnly}, opt.Local) {
				t.Errorf("%s/%s: Local = %q, catalog Floci support %v", platform, opt.ID, opt.Local, local)
			}
		}
	}
}

func hasError(diags []model.Diagnostic) bool {
	return slices.ContainsFunc(diags, func(d model.Diagnostic) bool { return d.Severity == model.SeverityError })
}
