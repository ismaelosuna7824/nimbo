//go:build corpus

package tfqa

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// TestCorpusBestPractices runs the best-practice checks over every public
// corpus root (root .tf files, as the folder import reads them) and reports
// the findings per rule: the noise measurement of docs/ARCHITECTURE.md
// ("lint"). TFQA_BP_REPORT receives the table, TFQA_BP_FINDINGS every
// finding (for review).
//
//	go test -tags corpus ./internal/tfqa/ -run TestCorpusBestPractices -v
func TestCorpusBestPractices(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	base := corpusDir()
	for _, r := range repos {
		fetch(t, r, filepath.Join(base, r.Name))
	}
	perRule := map[string]int{}
	perRuleRoots := map[string]map[string]bool{}
	var all []string
	roots := 0
	for _, cr := range corpusRoots {
		name := cr.Repo + "/" + strings.TrimPrefix(filepath.ToSlash(filepath.Join(cr.Scan, cr.Project)), "./")
		dir := filepath.Join(base, cr.Repo, filepath.FromSlash(cr.Scan), filepath.FromSlash(cr.Project))
		files := map[string][]byte{}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if !e.IsDir() && (strings.HasSuffix(e.Name(), ".tf") || strings.HasSuffix(e.Name(), ".tfvars")) {
				b, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err != nil {
					t.Fatal(err)
				}
				files[e.Name()] = b
			}
		}
		wf, _, err := hclcodec.Import(name, files)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		hclcodec.Normalize(wf)
		wf.NodeFiles = hclcodec.NodeFiles(wf, cat.ResourceFile)
		roots++
		addrs := map[string]string{}
		for i := range wf.Nodes {
			addrs[wf.Nodes[i].ID] = hclcodec.Address(&wf.Nodes[i])
		}
		scan := filepath.Join(base, cr.Repo, filepath.FromSlash(cr.Scan))
		mods, err := lint.LocalModules(wf, cr.Project, func(d string) (map[string][]byte, error) {
			return tfFiles(filepath.Join(scan, filepath.FromSlash(d)))
		})
		if err != nil {
			t.Fatalf("%s: modules: %v", name, err)
		}
		for _, d := range lint.Check(wf, cat, model.BestPracticeSettings{}, mods) {
			if d.Severity == model.SeverityError {
				t.Errorf("%s: error-severity finding %+v", name, d)
			}
			perRule[d.Code]++
			if perRuleRoots[d.Code] == nil {
				perRuleRoots[d.Code] = map[string]bool{}
			}
			perRuleRoots[d.Code][name] = true
			all = append(all, fmt.Sprintf("%s\t%s\t%s\t%s\t%s", d.Code, d.Severity, name, addrs[d.NodeID], d.Message))
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "| rule | findings | roots (of %d) |\n|---|---|---|\n", roots)
	total := 0
	for _, r := range lint.Rules() {
		fmt.Fprintf(&sb, "| `%s` | %d | %d |\n", r.Code, perRule[r.Code], len(perRuleRoots[r.Code]))
		total += perRule[r.Code]
	}
	fmt.Fprintf(&sb, "| **total** | %d | |\n", total)
	t.Log("\n" + sb.String())
	if p := os.Getenv("TFQA_BP_REPORT"); p != "" {
		if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
			t.Error(err)
		}
	}
	if p := os.Getenv("TFQA_BP_FINDINGS"); p != "" {
		sort.Strings(all)
		if err := os.WriteFile(p, []byte(strings.Join(all, "\n")+"\n"), 0o644); err != nil {
			t.Error(err)
		}
	}
}

// tfFiles reads the top-level *.tf files of dir (none when it is missing).
func tfFiles(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".tf") {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			files[e.Name()] = b
		}
	}
	return files, nil
}
