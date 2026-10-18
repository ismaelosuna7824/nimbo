package tfqa

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/api"
	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/events"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

// root is one Terraform root module under test.
type root struct {
	Name string
	// Scan is the folder given to the folder import (POST /import) and
	// Project the project path inside it ("." for Scan itself).
	Scan, Project string
	// Template creates the workflow from a catalog template instead.
	Template string
}

// Check names, in report column order, and their statuses.
const (
	checkSemantic = "semantic"
	checkComments = "comments"
	checkModel    = "reimport"
	checkStable   = "stable"
	checkFiles    = "files"
	checkVars     = "tfvars"
	checkFmt      = "fmt"
	checkValidate = "validate"

	statusPass     = "pass"
	statusFail     = "FAIL"
	statusOrigFail = "orig-fails"
)

var checkOrder = []string{checkSemantic, checkComments, checkModel, checkStable, checkFiles, checkVars, checkFmt, checkValidate}

// report collects the outcome of every check for one root.
type report struct {
	Name     string
	Status   map[string]string
	Failures []string
}

func (r *report) set(check string, failures ...string) {
	r.Status[check] = statusPass
	if len(failures) > 0 {
		r.Status[check] = statusFail
	}
	for _, f := range failures {
		r.Failures = append(r.Failures, check+": "+f)
	}
}

// ---------------------------------------------------------------------------
// Nimbo API

var (
	serverOnce sync.Once
	serverURL  string
	serverErr  error
	serverHome string
)

// nimbo starts one in-process API server (own temp home) per test binary.
func nimbo(t *testing.T) string {
	t.Helper()
	serverOnce.Do(func() {
		serverHome, serverErr = os.MkdirTemp("", "nimbo-tfqa-home-")
		if serverErr != nil {
			return
		}
		st, err := store.Open(serverHome)
		if err != nil {
			serverErr = err
			return
		}
		cat, err := catalog.Load()
		if err != nil {
			serverErr = err
			return
		}
		srv := api.New(api.Config{Version: "tfqa", Home: serverHome, Store: st, Catalog: cat, Events: events.NewHub()})
		serverURL = httptest.NewServer(srv.Handler()).URL + "/api/v1"
	})
	if serverErr != nil {
		t.Fatal(serverErr)
	}
	return serverURL
}

// pluginCacheDir is the provider cache shared by every terraform call of
// the test binary: TFQA_PLUGIN_CACHE, or a directory TestMain creates
// before the tests and removes after all of them.
var pluginCacheDir string

func TestMain(m *testing.M) {
	owned := false
	if pluginCacheDir = os.Getenv("TFQA_PLUGIN_CACHE"); pluginCacheDir != "" {
		if err := os.MkdirAll(pluginCacheDir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "plugin cache:", err)
			os.Exit(1)
		}
	} else {
		dir, err := os.MkdirTemp("", "nimbo-tfqa-plugins-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "plugin cache:", err)
			os.Exit(1)
		}
		pluginCacheDir, owned = dir, true
	}
	code := m.Run()
	if serverHome != "" {
		os.RemoveAll(serverHome)
	}
	if owned {
		os.RemoveAll(pluginCacheDir)
	}
	os.Exit(code)
}

func call(base, method, p string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, base+p, bytes.NewReader(data))
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, p, res.StatusCode, raw)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// importRoot runs the folder import (or template creation) and returns the
// stored workflow.
func importRoot(base string, r root) (*model.Workflow, error) {
	if r.Template != "" {
		var wf model.Workflow
		err := call(base, "POST", "/workflows", map[string]string{"name": r.Name, "templateId": r.Template}, &wf)
		return &wf, err
	}
	var res model.ImportResult
	if err := call(base, "POST", "/import", map[string]any{"dir": r.Scan, "projects": []string{r.Project}}, &res); err != nil {
		return nil, err
	}
	if len(res.Workflows) != 1 {
		return nil, fmt.Errorf("import returned %d workflows", len(res.Workflows))
	}
	return &res.Workflows[0], nil
}

func exportTo(base, id, dir string) error {
	return call(base, "POST", "/workflows/"+id+"/export", map[string]any{"dir": dir, "environments": true}, nil)
}

// originalDir returns the directory holding the root's original files.
func (r root) originalDir(t *testing.T) string {
	if r.Template == "" {
		return filepath.Join(r.Scan, filepath.FromSlash(r.Project))
	}
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	files, ok := cat.TemplateFiles(r.Template)
	if !ok {
		t.Fatalf("template %s not found", r.Template)
	}
	dir := t.TempDir()
	writeTree(t, dir, files)
	return dir
}

// ---------------------------------------------------------------------------
// Checks

// options selects the checks that need the terraform binary.
type options struct {
	fmt      bool // terraform fmt -check (needs terraform on PATH)
	validate bool // terraform init -backend=false + validate (downloads providers)
}

// checkRoot imports r through the API, exports it and runs every check.
// It returns the report, the original directory and the export directory.
func checkRoot(t *testing.T, r root, opts options) (*report, string, string) {
	t.Helper()
	base := nimbo(t)
	rep := &report{Name: r.Name, Status: map[string]string{}}
	orig := r.originalDir(t)

	wf, err := importRoot(base, r)
	if err != nil {
		rep.set(checkSemantic, "import: "+err.Error())
		t.Error(rep.Failures[0])
		return rep, orig, ""
	}
	exp := filepath.Join(t.TempDir(), "export")
	if err := exportTo(base, wf.ID, exp); err != nil {
		rep.set(checkSemantic, "export: "+err.Error())
		t.Error(rep.Failures[0])
		return rep, orig, ""
	}
	if keep := os.Getenv("TFQA_KEEP"); keep != "" { // keep exports for inspection
		copyTree(t, exp, filepath.Join(keep, strings.ReplaceAll(r.Name, "/", "__")))
	}

	// Semantic equality (and comments) of the root and every local module.
	cmp, err := CompareDirs(orig, exp)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	rep.set(checkSemantic, cmp.Semantic...)
	rep.set(checkComments, cmp.Comments...)

	// Exporting twice gives byte-identical trees.
	again := filepath.Join(t.TempDir(), "again")
	if err := exportTo(base, wf.ID, again); err != nil {
		t.Fatalf("second export: %v", err)
	}
	rep.set(checkStable, diffTrees(exp, again)...)

	// Import(Export(x)) == x at the model level.
	var back model.ImportResult
	if err := call(base, "POST", "/import", map[string]any{"dir": exp}, &back); err != nil {
		rep.set(checkModel, "re-import: "+err.Error())
	} else {
		rep.set(checkModel, diffModels(wf, &back.Workflows[0])...)
	}

	if r.Template == "" {
		rep.set(checkFiles, checkAssets(orig, exp, cmp)...)
		rep.set(checkVars, checkVarFiles(orig, exp)...)
	} else {
		rep.set(checkFiles)
		rep.set(checkVars)
	}

	if opts.fmt {
		rep.set(checkFmt, checkFormatted(t, orig, exp, cmp)...)
	}
	if opts.validate {
		origOK, origOut := validate(t, r, orig)
		expOK, expOut := validate(t, root{}, exp)
		switch {
		case !origOK && !expOK:
			rep.Status[checkValidate] = statusOrigFail
			t.Logf("validate fails for the original too:\n%s", lastLines(origOut, 15))
		case origOK && !expOK:
			rep.set(checkValidate, "export does not validate:\n"+lastLines(expOut, 30))
		default:
			rep.set(checkValidate)
		}
	}
	for _, f := range rep.Failures {
		t.Error(f)
	}
	return rep, orig, exp
}

// diffModels compares the Terraform content of two workflows.
func diffModels(a, b *model.Workflow) []string {
	pick := func(wf *model.Workflow) map[string]any {
		return map[string]any{
			"nodes": wf.Nodes, "variables": wf.Variables, "outputs": wf.Outputs, "locals": wf.Locals,
			"localsComments": wf.LocalsComments, "providers": wf.Providers, "terraform": wf.Terraform,
			"other": wf.Other, "environments": wf.Environments, "edges": wf.Edges, "clouds": wf.Clouds,
		}
	}
	var out []string
	pa, pb := pick(a), pick(b)
	keys := make([]string, 0, len(pa))
	for k := range pa {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		ja, _ := json.MarshalIndent(pa[k], "", " ")
		jb, _ := json.MarshalIndent(pb[k], "", " ")
		if !bytes.Equal(ja, jb) {
			out = append(out, fmt.Sprintf("re-imported %s differ:\n%s", k, lineDiff(string(ja), string(jb))))
		}
	}
	return out
}

// diffTrees compares two directory trees byte-wise.
func diffTrees(a, b string) []string {
	ta, tb := readTree(a), readTree(b)
	var out []string
	for name, data := range ta {
		if other, ok := tb[name]; !ok || !bytes.Equal(data, other) {
			out = append(out, name+" differs between two exports")
		}
	}
	for name := range tb {
		if _, ok := ta[name]; !ok {
			out = append(out, name+" only in the second export")
		}
	}
	slices.Sort(out)
	return out
}

func readTree(dir string) map[string][]byte {
	out := map[string][]byte{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.Contains(p, string(filepath.Separator)+".terraform"+string(filepath.Separator)) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err == nil {
			rel, _ := filepath.Rel(dir, p)
			out[filepath.ToSlash(rel)] = data
		}
		return nil
	})
	return out
}

// skipDir mirrors the import's skipped folders.
func skipDir(name string) bool {
	switch name {
	case "node_modules", "vendor", ".terragrunt-cache":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// ownFiles lists the files of a Terraform directory that the import must
// carry: everything except its root *.tf/*.tfvars (rendered from the model),
// environments/*.tfvars, skipped folders, Terraform working data and nested
// Terraform directories.
func ownFiles(dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p != dir && (skipDir(d.Name()) || hasTF(p)) {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !d.Type().IsRegular() || name == ".DS_Store" || strings.Contains(name, ".tfstate") ||
			(strings.HasPrefix(name, ".terraform") && name != ".terraform.lock.hcl") {
			return nil
		}
		dirPart := path.Dir(rel)
		switch {
		case dirPart == "." && strings.HasSuffix(name, ".tf") && !IsOverrideFile(name):
			return nil
		case dirPart == "." && strings.HasSuffix(name, ".tfvars"):
			return nil
		case dirPart == "environments" && (strings.HasSuffix(name, ".tfvars") || strings.HasSuffix(name, ".env.example")):
			return nil
		}
		out = append(out, rel)
		return nil
	})
	return out
}

func hasTF(dir string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
	return len(m) > 0
}

// checkAssets verifies that every asset of the root and of its local
// modules is in the export, byte-identical, at the same relative path.
func checkAssets(orig, exp string, cmp *Result) []string {
	var out []string
	pairs := map[string]string{orig: exp}
	for o, e := range cmp.Modules {
		pairs[o] = e
	}
	for o, e := range pairs {
		isModule := o != orig
		for _, rel := range ownFiles(o) {
			if isModule && path.Dir(rel) == "." && strings.HasSuffix(rel, ".tf") {
				continue // compared semantically
			}
			want, _ := os.ReadFile(filepath.Join(o, filepath.FromSlash(rel)))
			got, err := os.ReadFile(filepath.Join(e, filepath.FromSlash(rel)))
			switch {
			case err != nil:
				out = append(out, fmt.Sprintf("%s missing from export", filepath.Join(o, rel)))
			case !bytes.Equal(want, got):
				out = append(out, fmt.Sprintf("%s changed in export", filepath.Join(o, rel)))
			}
		}
	}
	slices.Sort(out)
	return out
}

// checkVarFiles verifies that each variable file of the original exists in
// the export at a path Terraform treats the same way, with equal values.
func checkVarFiles(orig, exp string) []string {
	var out []string
	var files []string
	for _, pattern := range []string{"*.tfvars", "environments/*.tfvars", "environments/*.env.example"} {
		m, _ := filepath.Glob(filepath.Join(orig, pattern))
		files = append(files, m...)
	}
	for _, p := range files {
		rel, _ := filepath.Rel(orig, p)
		got, err := os.ReadFile(filepath.Join(exp, rel))
		if err != nil {
			out = append(out, rel+" missing from export")
			continue
		}
		want, _ := os.ReadFile(p)
		if strings.HasSuffix(rel, ".env.example") {
			continue // rewritten as KEY=VALUE; covered by unit tests
		}
		a, errA := LoadVars(rel, want)
		b, errB := LoadVars(rel, got)
		if errA != nil || errB != nil {
			out = append(out, fmt.Sprintf("%s: %v %v", rel, errA, errB))
			continue
		}
		if !mapsEqual(a, b) {
			out = append(out, fmt.Sprintf("%s values differ: %v vs %v", rel, a, b))
		}
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// terraform

// terraformBin returns the host terraform binary, or "" when absent.
func terraformBin() string {
	p, _ := exec.LookPath("terraform")
	return p
}

// terraformCmd prepares a terraform command in dir; env entries override
// the defaults.
func terraformCmd(dir string, env []string, args ...string) *exec.Cmd {
	cmd := exec.Command(terraformBin(), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TF_PLUGIN_CACHE_DIR="+pluginCacheDir, "TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=true",
		"TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1", "TF_INPUT=0")
	cmd.Env = append(cmd.Env, env...)
	return cmd
}

// terraform runs terraform and returns its combined output.
func terraform(dir string, env []string, args ...string) (string, error) {
	out, err := terraformCmd(dir, env, args...).CombinedOutput()
	return string(out), err
}

// checkFormatted runs terraform fmt -check -recursive on the export. Files
// copied verbatim (module copies, override files) may only fail when their
// original fails too.
func checkFormatted(t *testing.T, orig, exp string, cmp *Result) []string {
	// stdout lists the unformatted files; warnings and errors go to stderr.
	var stdout, stderr strings.Builder
	cmd := terraformCmd(exp, nil, "fmt", "-check", "-recursive", "-list=true", ".")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var failures []string
	back := map[string]string{exp: orig}
	for o, e := range cmp.Modules {
		back[e] = o
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		rel := strings.TrimSpace(line)
		if rel == "" {
			continue
		}
		p := filepath.Join(exp, rel)
		generated := path.Dir(filepath.ToSlash(rel)) == "." && !IsOverrideFile(rel) || strings.HasPrefix(filepath.ToSlash(rel), "environments/")
		if !generated {
			if o, ok := back[filepath.Dir(p)]; ok {
				if _, err := terraform(o, nil, "fmt", "-check", filepath.Base(p)); err != nil {
					continue // not formatted in the original either
				}
			}
		}
		failures = append(failures, rel+" is not formatted")
	}
	if len(failures) == 0 && strings.TrimSpace(stdout.String()) == "" {
		failures = append(failures, fmt.Sprintf("terraform fmt failed: %v: %s", err, stderr.String()))
	}
	return failures
}

// validate runs terraform init -backend=false and validate in a copy of the
// root (the whole scan folder for folder imports, so relative module paths
// resolve).
func validate(t *testing.T, r root, dir string) (bool, string) {
	work := t.TempDir()
	target := work
	if r.Scan != "" {
		copyTree(t, r.Scan, work)
		target = filepath.Join(work, filepath.FromSlash(r.Project))
	} else {
		copyTree(t, dir, work)
	}
	// A lock file whose checksums do not cover this platform cannot be
	// satisfied from the plugin cache: install those providers directly.
	var env []string
	if _, err := os.Stat(filepath.Join(target, ".terraform.lock.hcl")); err == nil {
		env = []string{"TF_PLUGIN_CACHE_DIR="}
	}
	var log strings.Builder
	for _, args := range [][]string{{"init", "-backend=false", "-input=false", "-no-color"}, {"validate", "-no-color"}} {
		var out string
		var err error
		for attempt := range 3 { // registry hiccups and plugin cache races
			if out, err = terraform(target, env, args...); err == nil || args[0] != "init" || attempt == 2 {
				break
			}
		}
		log.WriteString(out)
		if err != nil {
			return false, log.String()
		}
	}
	return true, log.String()
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// File helpers

func writeTree(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// copyTree copies src into dst, skipping VCS metadata and Terraform working
// directories.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".terraform") {
			return fs.SkipDir
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, _ := d.Info()
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// formatTable renders reports as a Markdown table.
func formatTable(reps []*report, extra ...string) string {
	cols := append(slices.Clone(checkOrder), extra...)
	var sb strings.Builder
	sb.WriteString("| root | " + strings.Join(cols, " | ") + " |\n|---|" + strings.Repeat("---|", len(cols)) + "\n")
	for _, r := range reps {
		sb.WriteString("| " + r.Name)
		for _, c := range cols {
			s := r.Status[c]
			if s == "" {
				s = "–"
			}
			sb.WriteString(" | " + s)
		}
		sb.WriteString(" |\n")
	}
	totals := map[string]int{}
	for _, r := range reps {
		for _, c := range cols {
			if s := r.Status[c]; s == statusPass {
				totals[c]++
			}
		}
	}
	sb.WriteString("| **pass count**")
	for _, c := range cols {
		fmt.Fprintf(&sb, " | %d/%d", totals[c], len(reps))
	}
	sb.WriteString(" |\n")
	return sb.String()
}
