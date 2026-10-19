//go:build corpus

package tfqa

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/netcheck"
)

var localRef = regexp.MustCompile(`\blocal\.([A-Za-z0-9_]+)`)

// exampleInputs turns the `module "vpc"` call of a terraform-aws-vpc
// example into variable values for the module itself: the example's
// locals are substituted textually (azs pinned to three zones, which a
// deploy reads from data.aws_availability_zones); arguments that still
// read data sources, resources or other modules are left out.
func exampleInputs(t *testing.T, mainTF string) map[string]model.Value {
	src, err := os.ReadFile(mainTF)
	if err != nil {
		t.Fatal(err)
	}
	f, diags := hclsyntax.ParseConfig(src, mainTF, hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	text := func(e hclsyntax.Expression) string { return string(e.Range().SliceBytes(src)) }
	locals := map[string]string{}
	var call *hclsyntax.Body
	for _, b := range f.Body.(*hclsyntax.Body).Blocks {
		switch {
		case b.Type == "locals":
			for name, a := range b.Body.Attributes {
				locals[name] = text(a.Expr)
			}
		case b.Type == "module" && call == nil:
			if s, ok := b.Body.Attributes["source"]; ok && strings.Contains(text(s.Expr), `"../..`) {
				call = b.Body
			}
		}
	}
	locals["azs"] = `["eu-west-1a", "eu-west-1b", "eu-west-1c"]`
	subst := func(s string) string {
		for range 4 {
			s = localRef.ReplaceAllStringFunc(s, func(m string) string {
				if v, ok := locals[strings.TrimPrefix(m, "local.")]; ok {
					return "(" + v + ")"
				}
				return m
			})
		}
		return s
	}
	out := map[string]model.Value{}
	if call == nil {
		return out
	}
	for name, a := range call.Attributes {
		if name == "source" || name == "version" || name == "providers" {
			continue
		}
		v := subst(text(a.Expr))
		if strings.Contains(v, "local.") || strings.Contains(v, "data.") || strings.Contains(v, "module.") || regexp.MustCompile(`\baws_[a-z0-9_]+\.`).MatchString(v) {
			continue
		}
		out[name] = model.Value{Expr: v}
	}
	return out
}

// netcheckModuleRoots are network modules analyzed as roots (their
// variable defaults decide count/for_each), on top of every corpus root.
var netcheckModuleRoots = []corpusRoot{
	{"terraform-aws-vpc", ".", "."},
	{"terraform-aws-security-group", ".", "."},
	{"terraform-google-network", ".", "."},
	{"terraform-google-network", ".", "modules/subnets"},
	{"terraform-google-network", ".", "modules/firewall-rules"},
}

// vpcExamples feed the terraform-aws-vpc module with each example's
// arguments, so its subnets, route tables, NAT gateways, NACLs and subnet
// groups get real values.
var vpcExamples = []string{"simple", "complete", "ipv6-dualstack", "network-acls", "secondary-cidr-blocks", "separate-route-tables", "block-public-access", "flow-log", "issues"}

// TestCorpusNetworkChecks runs the network checks over every public corpus
// root plus the network modules themselves and reports the findings: the
// false-positive measurement of docs/ARCHITECTURE.md ("netcheck"). Correct
// upstream examples must have no error findings. TFQA_NET_FINDINGS
// receives every finding (for review).
//
//	go test -tags corpus ./internal/tfqa/ -run TestCorpusNetworkChecks -v
func TestCorpusNetworkChecks(t *testing.T) {
	base := corpusDir()
	for _, r := range repos {
		fetch(t, r, filepath.Join(base, r.Name))
	}
	perCode := map[string]int{}
	var all []string
	roots, withNetwork := 0, 0
	type job struct {
		name, dir string
		env       map[string]model.Value
	}
	var jobs []job
	for _, cr := range append(append([]corpusRoot{}, corpusRoots...), netcheckModuleRoots...) {
		name := cr.Repo + "/" + strings.TrimPrefix(filepath.ToSlash(filepath.Join(cr.Scan, cr.Project)), "./")
		jobs = append(jobs, job{name: name, dir: filepath.Join(base, cr.Repo, filepath.FromSlash(cr.Scan), filepath.FromSlash(cr.Project))})
	}
	vpcDir := filepath.Join(base, "terraform-aws-vpc")
	for _, ex := range vpcExamples {
		jobs = append(jobs, job{name: "terraform-aws-vpc with examples/" + ex + " inputs", dir: vpcDir, env: exampleInputs(t, filepath.Join(vpcDir, "examples", ex, "main.tf"))})
	}
	for _, j := range jobs {
		name, dir := j.name, j.dir
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
		if j.env != nil {
			wf.Environments = append(wf.Environments, model.Environment{Name: "example", Values: j.env})
			wf.ActiveEnvironment = "example"
		}
		hclcodec.Normalize(wf)
		roots++
		a := netcheck.Analyze(wf, nil)
		s := a.Summary()
		if len(s.Networks)+len(s.Subnets)+len(s.SecurityGroups)+len(s.Placements) > 0 {
			withNetwork++
			classes := map[string]int{}
			for _, sn := range s.Subnets {
				classes[sn.Classification]++
			}
			t.Logf("%s: %d networks, %d subnets %v, %d route tables, %d security groups, %d placements, %d findings, notes: %s",
				name, len(s.Networks), len(s.Subnets), classes, len(s.RouteTables), len(s.SecurityGroups), len(s.Placements), len(s.Findings), strings.Join(s.Notes, " | "))
		}
		addrs := map[string]string{}
		for i := range wf.Nodes {
			addrs[wf.Nodes[i].ID] = hclcodec.Address(&wf.Nodes[i])
		}
		for _, d := range a.Findings() {
			if d.Severity == model.SeverityError {
				t.Errorf("%s: error finding on a correct upstream example: %s %s: %s", name, d.Code, addrs[d.NodeID], d.Message)
			}
			perCode[d.Code]++
			all = append(all, fmt.Sprintf("%s\t%s\t%s\t%s\t%s", d.Code, d.Severity, name, addrs[d.NodeID], d.Message))
		}
	}
	sort.Strings(all)
	t.Logf("%d roots, %d with network resources, findings per code: %v\n%s", roots, withNetwork, perCode, strings.Join(all, "\n"))
	if p := os.Getenv("TFQA_NET_FINDINGS"); p != "" {
		if err := os.WriteFile(p, []byte(strings.Join(all, "\n")+"\n"), 0o644); err != nil {
			t.Error(err)
		}
	}
}
