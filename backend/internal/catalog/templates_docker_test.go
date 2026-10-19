//go:build docker

package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// These tests deploy every AWS template into a throwaway Floci pod using the
// sandbox pattern (alpine pod owning the network namespace, floci and
// terraform joining it) and validate the non-AWS templates.

const flociOverride = `provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}
`

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeTemplate(t *testing.T, c *Catalog, id string, extra map[string]string) string {
	t.Helper()
	files, ok := c.TemplateFiles(id)
	if !ok {
		t.Fatalf("template %s not found", id)
	}
	dir, err := os.MkdirTemp("", "nimbo-tpl-"+id+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, s := range extra {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func terraformArgs(network, dir string, env []string, args ...string) []string {
	full := []string{"run", "--rm", "-v", dir + ":/work", "-w", "/work",
		"-v", "nimbo-tf-plugins:/plugins", "-e", "TF_PLUGIN_CACHE_DIR=/plugins", "-e", "TF_IN_AUTOMATION=1"}
	if network != "" {
		full = append(full, "--network", "container:"+network)
	}
	for _, e := range env {
		full = append(full, "-e", e)
	}
	full = append(full, "hashicorp/terraform:latest")
	return append(full, args...)
}

func runTerraform(t *testing.T, network, dir string, env []string, args ...string) string {
	t.Helper()
	// init retries: the shared plugin cache volume may be busy while another
	// terraform process installs or executes the same provider binary.
	attempts := 1
	if args[0] == "init" {
		attempts = 6
	}
	var out []byte
	var err error
	for i := range attempts {
		if i > 0 {
			time.Sleep(10 * time.Second)
		}
		out, err = exec.Command("docker", terraformArgs(network, dir, env, args...)...).CombinedOutput()
		if err == nil {
			return string(out)
		}
	}
	t.Fatalf("terraform %s: %v\n%s", strings.Join(args, " "), err, out)
	return ""
}

func TestTemplatesValidate(t *testing.T) {
	c := mustLoad(t)
	for _, tpl := range c.Templates() {
		t.Run(tpl.ID, func(t *testing.T) {
			dir := writeTemplate(t, c, tpl.ID, nil)
			runTerraform(t, "", dir, nil, "init", "-input=false", "-backend=false", "-no-color")
			runTerraform(t, "", dir, nil, "validate", "-no-color")
		})
	}
}

// startFloci starts a throwaway pod + floci container (removed on cleanup)
// and returns the pod name, the host base URL and the Terraform env.
func startFloci(t *testing.T) (pod, base string, env []string) {
	t.Helper()
	name := fmt.Sprintf("nimbo-cattpl%d", time.Now().UnixNano()%1_000_000)
	pod = name + "-pod"
	docker(t, "network", "create", name)
	t.Cleanup(func() {
		// Floci-spawned containers attach to the network; floci itself only
		// shares the pod's namespace, so it is matched by name.
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
	docker(t, "run", "-d", "--name", pod, "--network", name, "-p", "127.0.0.1::4566", "alpine:3", "sleep", "infinity")
	docker(t, "run", "-d", "--name", name+"-aws", "--network", "container:"+pod,
		"-v", "/var/run/docker.sock:/var/run/docker.sock", "-u", "root",
		"-e", "FLOCI_SERVICES_DOCKER_NETWORK="+name, "floci/floci:latest")
	hostPort := docker(t, "port", pod, "4566")
	base = "http://" + hostPort[strings.LastIndex(hostPort, "\n")+1:]
	waitReady(t, base+"/_floci/init")
	return pod, base, []string{"AWS_ENDPOINT_URL=http://localhost:4566", "AWS_ACCESS_KEY_ID=test", "AWS_SECRET_ACCESS_KEY=test", "AWS_REGION=us-east-1"}
}

func TestAWSTemplatesApplyOnFloci(t *testing.T) {
	c := mustLoad(t)
	pod, base, env := startFloci(t)
	for _, tpl := range c.Templates() {
		if tpl.Cloud != "aws" {
			continue
		}
		t.Run(tpl.ID, func(t *testing.T) {
			dir := writeTemplate(t, c, tpl.ID, map[string]string{"floci_override.tf": flociOverride})
			runTerraform(t, pod, dir, env, "init", "-input=false", "-no-color")
			start := time.Now()
			runTerraform(t, pod, dir, env, "apply", "-auto-approve", "-input=false", "-no-color")
			t.Logf("apply took %s", time.Since(start).Round(time.Millisecond))
			if check := applyChecks[tpl.ID]; check != nil {
				outputs := map[string]struct {
					Value any `json:"value"`
				}{}
				raw := runTerraform(t, pod, dir, env, "output", "-json")
				if err := json.Unmarshal([]byte(raw), &outputs); err != nil {
					t.Fatalf("outputs: %v\n%s", err, raw)
				}
				vals := map[string]string{}
				for k, v := range outputs {
					vals[k] = fmt.Sprint(v.Value)
				}
				check(t, base, vals)
			}
			planNoChanges(t, pod, dir, env)
			runTerraform(t, pod, dir, env, "destroy", "-auto-approve", "-input=false", "-no-color")
		})
	}
}

// TestPopularAWSStartersApplyOnFloci builds one workflow with every popular
// Floci-supported AWS starter (with Nimbo's conventions: name_prefix names,
// default_tags, encryption, log retention), exports it and applies it.
func TestPopularAWSStartersApplyOnFloci(t *testing.T) {
	c := mustLoad(t)
	wf := &model.Workflow{Name: "Starters"}
	for _, r := range c.Search(Query{Cloud: model.CloudAWS, Kind: model.KindResource, PopularOnly: true, SupportedOnly: true}) {
		if _, err := c.AddNode(wf, model.KindResource, r.Type, "", model.Position{}); err != nil {
			t.Fatalf("add %s: %v", r.Type, err)
		}
	}
	hclcodec.Normalize(wf)
	files, err := hclcodec.Export(wf, hclcodec.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	extra := map[string]string{"floci_override.tf": flociOverride}
	for name, b := range files {
		extra[name] = string(b)
	}
	dir, err := os.MkdirTemp("", "nimbo-starters-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for name, s := range extra {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pod, _, env := startFloci(t)
	runTerraform(t, pod, dir, env, "init", "-input=false", "-no-color")
	start := time.Now()
	runTerraform(t, pod, dir, env, "apply", "-auto-approve", "-input=false", "-no-color")
	t.Logf("apply of %d nodes took %s", len(wf.Nodes), time.Since(start).Round(time.Millisecond))
	planNoChanges(t, pod, dir, env)
	runTerraform(t, pod, dir, env, "destroy", "-auto-approve", "-input=false", "-no-color")
}

// planNoChanges fails when a plan right after apply wants changes.
func planNoChanges(t *testing.T, pod, dir string, env []string) {
	t.Helper()
	out, err := exec.Command("docker", terraformArgs(pod, dir, env, "plan", "-detailed-exitcode", "-input=false", "-no-color")...).CombinedOutput()
	if err != nil {
		t.Errorf("plan after apply is not empty: %v\n%s", err, out)
	}
}

// applyChecks exercise a deployed template through the Floci host port.
var applyChecks = map[string]func(t *testing.T, base string, out map[string]string){
	"aws-serverless-api": func(t *testing.T, base string, out map[string]string) {
		url := base + "/execute-api/" + out["api_id"] + "/$default/items"
		created := httpJSON(t, http.MethodPost, url, `{"name":"first"}`, http.StatusCreated)
		id, _ := created["id"].(string)
		if id == "" || created["name"] != "first" {
			t.Fatalf("POST /items returned %v", created)
		}
		got := httpJSON(t, http.MethodGet, url+"/"+id, "", http.StatusOK)
		if got["name"] != "first" {
			t.Fatalf("GET /items/%s returned %v", id, got)
		}
	},
}

func httpJSON(t *testing.T, method, url, body string, want int) map[string]any {
	t.Helper()
	var resp *http.Response
	var err error
	// The first invocation cold-starts a Lambda container.
	deadline := time.Now().Add(90 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
		req.Header.Set("content-type", "application/json")
		resp, err = http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode == want || time.Now().After(deadline) {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, url, resp.StatusCode, want, raw)
	}
	var v map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &v); err != nil {
		t.Fatalf("%s %s: %v: %s", method, url, err, raw)
	}
	return v
}

func waitReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			var v struct {
				Completed struct {
					Ready bool `json:"ready"`
				} `json:"completed"`
			}
			json.NewDecoder(resp.Body).Decode(&v)
			resp.Body.Close()
			if v.Completed.Ready {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("floci not ready at %s", url)
}
