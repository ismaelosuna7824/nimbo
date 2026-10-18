//go:build corpus

package tfqa

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
)

// Deployment equivalence: apply the ORIGINAL configuration to a throwaway
// Floci emulator, swap the configuration for Nimbo's export in the same
// working directory (state kept) and require `terraform plan
// -detailed-exitcode` to report no changes (exit 0).
//
//	go test -tags corpus -timeout 2h ./internal/tfqa/ -run TestDeployEquivalence -v
//
// Needs docker (images floci/floci and alpine) and host terraform. Every
// container and network it creates is named nimbo-tfqa-<n>* and removed at
// the end. TFQA_DEPLOY_REPORT receives the results table.

const flociProviderOverride = `provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}
`

// overrideName is the emulator wiring file added to the working directory;
// it is not part of either configuration under test.
const overrideName = "zz_tfqa_floci_override.tf"

// localBackendOverride replaces a remote backend or HCP Terraform `cloud`
// block so the emulator run keeps its state locally.
const localBackendOverride = `
terraform {
  backend "local" {}
}
`

// hasRemoteState reports whether the configuration in dir declares a
// backend or cloud block.
func hasRemoteState(t *testing.T, dir string) bool {
	m, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range m.Blocks {
		if b.Type != "terraform" {
			continue
		}
		for _, nb := range b.Blocks {
			if nb.Type == "backend" || nb.Type == "cloud" {
				return true
			}
		}
	}
	return false
}

func deployRoots(t *testing.T) []root {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	var roots []root
	for _, tpl := range cat.Templates() {
		if tpl.Cloud == "aws" {
			roots = append(roots, root{Name: "template/" + tpl.ID, Template: tpl.ID})
		}
	}
	base := corpusDir()
	for _, r := range []struct{ repo, project string }{
		{"terraform-aws-sqs", "examples/complete"},
		{"terraform-aws-dynamodb-table", "examples/basic"},
		{"terraform-aws-sns", "examples/complete"},
		{"terraform-aws-eventbridge", "examples/default-bus"},
		{"learn-terraform-lambda-api-gateway", "."},
		{"terraform-aws-s3-bucket", "examples/object"},
		{"terraform-aws-lambda", "examples/simple"},
	} {
		roots = append(roots, root{Name: r.repo + "/" + r.project, Scan: filepath.Join(base, r.repo), Project: r.project})
	}
	return roots
}

func TestDeployEquivalence(t *testing.T) {
	if terraformBin() == "" {
		t.Skip("terraform not on PATH")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker not available")
	}
	for _, r := range repos { // the corpus roots below need their checkouts
		fetch(t, r, filepath.Join(corpusDir(), r.Name))
	}
	endpoint := startFloci(t)
	env := []string{"AWS_ENDPOINT_URL=" + endpoint, "AWS_ACCESS_KEY_ID=test", "AWS_SECRET_ACCESS_KEY=test", "AWS_REGION=us-east-1", "AWS_DEFAULT_REGION=us-east-1"}
	base := nimbo(t)

	var table strings.Builder
	table.WriteString("| root | original apply | plan after swapping in the export | result |\n|---|---|---|---|\n")
	for _, r := range deployRoots(t) {
		t.Run(r.Name, func(t *testing.T) {
			apply, plan, result := deployEquivalence(t, base, r, env)
			fmt.Fprintf(&table, "| %s | %s | %s | %s |\n", r.Name, apply, plan, result)
		})
	}
	t.Log("\n" + table.String())
	if p := os.Getenv("TFQA_DEPLOY_REPORT"); p != "" {
		if err := os.WriteFile(p, []byte(table.String()), 0o644); err != nil {
			t.Error(err)
		}
	}
}

// deployEquivalence returns the report cells for one root.
func deployEquivalence(t *testing.T, base string, r root, env []string) (apply, plan, result string) {
	wf, err := importRoot(base, r)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	exp := filepath.Join(t.TempDir(), "export")
	if err := exportTo(base, wf.ID, exp); err != nil {
		t.Fatalf("export: %v", err)
	}

	// The working directory: a copy of the scan folder (relative module
	// paths resolve) or of the template.
	work := t.TempDir()
	target := work
	if r.Scan != "" {
		copyTree(t, r.Scan, work)
		target = filepath.Join(work, filepath.FromSlash(r.Project))
	} else {
		copyTree(t, r.originalDir(t), work)
	}
	override := flociProviderOverride
	if hasRemoteState(t, target) {
		override += localBackendOverride
	}
	if err := os.WriteFile(filepath.Join(target, overrideName), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		args = append(args, "-no-color")
		if args[0] != "show" {
			args = append(args, "-input=false")
		}
		return terraform(target, env, args...)
	}
	if out, err := initRetry(run); err != nil {
		t.Fatalf("init (original): %v\n%s", err, lastLines(out, 30))
	}
	out, err := run("apply", "-auto-approve")
	if err != nil {
		// Not deployable on the emulator: nothing to compare.
		t.Logf("original apply fails on Floci:\n%s", lastLines(out, 25))
		run("destroy", "-auto-approve")
		return "fails on Floci: " + firstError(out), "–", "n/a"
	}
	apply = "ok"
	defer func() {
		if out, err := run("destroy", "-auto-approve"); err != nil {
			t.Logf("destroy: %v\n%s", err, lastLines(out, 15))
		}
	}()
	// The emulator may not round-trip every attribute, so the original can
	// plan changes right after its own apply. That drift is the baseline
	// the export must reproduce exactly.
	origCode, origPlan := planChanges(t, run)

	// Swap the configuration: root *.tf and variable files out, the export
	// in. State, .terraform and the emulator wiring stay.
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && n != overrideName && (strings.HasSuffix(n, ".tf") || strings.HasSuffix(n, ".tfvars") || strings.HasSuffix(n, ".tf.json")) {
			if err := os.Remove(filepath.Join(target, n)); err != nil {
				t.Fatal(err)
			}
		}
	}
	copyTree(t, exp, target)
	if out, err := initRetry(run); err != nil {
		t.Errorf("init (export): %v\n%s", err, lastLines(out, 30))
		return apply, "init failed", statusFail
	}
	code, expPlan := planChanges(t, run)
	plan = fmt.Sprintf("exit %d", code)
	switch {
	case code == 0 && origCode == 0:
		return apply, plan + " (no changes)", statusPass
	case code == origCode && slices.Equal(expPlan, origPlan):
		return apply, fmt.Sprintf("%s: same %d change(s) the original plans after its own apply (emulator drift)", plan, len(expPlan)), statusPass
	}
	t.Errorf("plan after swapping in the export: exit %d, original %d\nexport plans:\n%s\noriginal plans:\n%s",
		code, origCode, strings.Join(expPlan, "\n"), strings.Join(origPlan, "\n"))
	return apply, fmt.Sprintf("%s (original %d)", plan, origCode), statusFail
}

// planChanges runs terraform plan -detailed-exitcode and lists the planned
// resource and output changes (address, actions, planned values).
func planChanges(t *testing.T, run func(args ...string) (string, error)) (int, []string) {
	t.Helper()
	out, err := run("plan", "-detailed-exitcode", "-out=tfqa.plan")
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code == 1 {
		t.Fatalf("plan failed:\n%s", lastLines(out, 30))
	}
	raw, err := run("show", "-json", "tfqa.plan")
	if err != nil {
		t.Fatalf("show: %v\n%s", err, raw)
	}
	var p struct {
		ResourceChanges []struct {
			Address string `json:"address"`
			Change  struct {
				Actions []string        `json:"actions"`
				After   json.RawMessage `json:"after"`
			} `json:"change"`
		} `json:"resource_changes"`
		OutputChanges map[string]struct {
			Actions []string        `json:"actions"`
			After   json.RawMessage `json:"after"`
		} `json:"output_changes"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("plan JSON: %v", err)
	}
	var changes []string
	for _, rc := range p.ResourceChanges {
		if !slices.Equal(rc.Change.Actions, []string{"no-op"}) && !slices.Equal(rc.Change.Actions, []string{"read"}) {
			changes = append(changes, fmt.Sprintf("%s %v %s", rc.Address, rc.Change.Actions, rc.Change.After))
		}
	}
	for name, oc := range p.OutputChanges {
		if !slices.Equal(oc.Actions, []string{"no-op"}) {
			changes = append(changes, fmt.Sprintf("output.%s %v %s", name, oc.Actions, oc.After))
		}
	}
	slices.Sort(changes)
	return code, changes
}

func initRetry(run func(args ...string) (string, error)) (string, error) {
	var out string
	var err error
	for range 3 {
		if out, err = run("init"); err == nil {
			return out, nil
		}
		time.Sleep(5 * time.Second)
	}
	return out, err
}

func firstError(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if s, ok := strings.CutPrefix(strings.TrimSpace(l), "Error: "); ok {
			return strings.ReplaceAll(s, "|", "/")
		}
	}
	return "error"
}

func firstLine(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			return strings.TrimSpace(l)
		}
	}
	return "see log"
}

// startFloci runs Floci the way Nimbo's sandbox does (an alpine pod owning
// the network namespace, Floci joining it) and returns the host endpoint.
func startFloci(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("nimbo-tfqa-%d", time.Now().UnixNano()%1_000_000)
	pod := name + "-pod"
	docker := func(args ...string) string {
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	t.Cleanup(func() {
		var ids []string
		for _, filter := range []string{"network=" + name, "name=^" + name + "-"} {
			out, _ := exec.Command("docker", "ps", "-aq", "--filter", filter).Output()
			ids = append(ids, strings.Fields(string(out))...)
		}
		for _, id := range ids {
			exec.Command("docker", "rm", "-f", id).Run()
		}
		exec.Command("docker", "network", "rm", name).Run()
	})
	docker("network", "create", name)
	docker("run", "-d", "--name", pod, "--network", name, "-p", "127.0.0.1::4566", "alpine:3", "sleep", "infinity")
	docker("run", "-d", "--name", name+"-aws", "--network", "container:"+pod,
		"-v", "/var/run/docker.sock:/var/run/docker.sock", "-u", "root",
		"-e", "FLOCI_SERVICES_DOCKER_NETWORK="+name, "floci/floci:latest")
	hostPort := docker("port", pod, "4566")
	endpoint := "http://" + hostPort[strings.LastIndex(hostPort, "\n")+1:]
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(endpoint + "/_floci/init"); err == nil {
			var v struct {
				Completed struct {
					Ready bool `json:"ready"`
				} `json:"completed"`
			}
			json.NewDecoder(resp.Body).Decode(&v)
			resp.Body.Close()
			if v.Completed.Ready {
				return endpoint
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("floci not ready at %s", endpoint)
	return ""
}
