//go:build docker

package kubeconvert

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// TestDeployConvertedTemplates converts every docker template in every
// format, renders it (prod for Kustomize/Helm: 2 replicas + PDBs) and
// applies it to a throwaway k3s cluster, waiting until every workload is
// rolled out and the Ingress answers. Needs Docker and kubectl:
//
//	go test -tags docker -run TestDeployConvertedTemplates -timeout 90m ./internal/kube/kubeconvert
//
// NIMBO_CONVERT_TEMPLATES / NIMBO_CONVERT_FORMATS (comma lists) narrow it.
func TestDeployConvertedTemplates(t *testing.T) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not installed")
	}
	cl := startCluster(t)
	formats := []string{model.ConvertManifests, model.ConvertKustomize, model.ConvertHelm}
	if f := os.Getenv("NIMBO_CONVERT_FORMATS"); f != "" {
		formats = strings.Split(f, ",")
	}
	only := map[string]bool{}
	if s := os.Getenv("NIMBO_CONVERT_TEMPLATES"); s != "" {
		for _, id := range strings.Split(s, ",") {
			only[id] = true
		}
	}
	for _, tmpl := range compose.Templates() {
		if len(only) > 0 && !only[tmpl.ID] {
			continue
		}
		for _, f := range formats {
			t.Run(tmpl.ID+"/"+f, func(t *testing.T) {
				ns := strings.TrimPrefix(tmpl.ID, "docker-")
				if len(ns) > 40 {
					ns = ns[:40]
				}
				ns = strings.TrimRight(ns, "-") + "-" + f[:4]
				p := openTemplate(t, tmpl.ID)
				res, err := Convert(p, Options{Format: f, Name: "app", Namespace: ns})
				if err != nil {
					t.Fatal(err)
				}
				env := ""
				if f != model.ConvertManifests {
					env = "prod"
				}
				kp := renderConverted(t, res, env)
				for _, d := range kp.Diagnostics {
					if d.Severity == model.SeverityError {
						t.Fatalf("render: %s %s", d.Code, d.Message)
					}
				}
				for _, b := range res.Model.Builds {
					cl.loadBuild(t, p.Dir, b)
				}
				var yaml bytes.Buffer
				for _, r := range kp.Sources {
					yaml.Write(r.YAML)
					yaml.WriteString("\n---\n")
				}
				cl.kubectl(t, nil, "create", "namespace", ns)
				defer cl.kubectl(t, nil, "delete", "namespace", ns, "--wait=false")
				cl.kubectl(t, yaml.Bytes(), "apply", "--server-side", "-n", ns, "-f", "-")
				start := time.Now()
				for _, o := range kp.Objects {
					switch o.Kind {
					case "Deployment", "StatefulSet":
						if out, err := cl.try(nil, "rollout", "status", "-n", ns, strings.ToLower(o.Kind)+"/"+o.Name, "--timeout=600s"); err != nil {
							pods, _ := cl.try(nil, "get", "pods,events", "-n", ns, "-o", "wide")
							logs, _ := cl.try(nil, "logs", "-n", ns, "-l", "app.kubernetes.io/name="+o.Name, "--all-containers", "--tail=40")
							t.Fatalf("%s %s not rolled out: %v\n%s\n%s\n%s", o.Kind, o.Name, err, out, pods, logs)
						}
					}
				}
				t.Logf("healthy in %s", time.Since(start).Round(time.Second))
				for _, o := range kp.Objects {
					if o.Kind != "Ingress" {
						continue
					}
					rules, _ := o.Obj["spec"].(map[string]any)["rules"].([]any)
					for _, r := range rules {
						host := r.(map[string]any)["host"].(string)
						status := cl.get(t, host)
						if status >= 500 || status == 404 {
							t.Errorf("Ingress %s answered %d", host, status)
						} else {
							t.Logf("Ingress http://%s → %d", host, status)
						}
					}
				}
			})
		}
	}
}

type cluster struct {
	name, kubeconfig string
	ingressPort      string
}

func run(ctx context.Context, stdin []byte, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func startCluster(t *testing.T) *cluster {
	t.Helper()
	ctx := context.Background()
	c := &cluster{name: "nimbo-k8s-convert-it"}
	_, _ = run(ctx, nil, "docker", "rm", "-f", "-v", c.name)
	if out, err := run(ctx, nil, "docker", "run", "-d", "--name", c.name, "--hostname", c.name, "--privileged",
		"--label", "com.nimbo.workflow=convert-it", "--tmpfs", "/run", "--tmpfs", "/var/run",
		"-p", "127.0.0.1::6443", "-p", "127.0.0.1::80",
		"rancher/k3s:v1.36.5-k3s1", "server", "--node-name", c.name, "--tls-san", "127.0.0.1"); err != nil {
		t.Fatalf("start k3s: %v %s", err, out)
	}
	t.Cleanup(func() {
		if os.Getenv("NIMBO_CONVERT_KEEP") == "" {
			_, _ = run(context.Background(), nil, "docker", "rm", "-f", "-v", c.name)
		}
	})
	port := func(p string) string {
		out, err := run(ctx, nil, "docker", "port", c.name, p)
		if err != nil {
			t.Fatalf("docker port: %v %s", err, out)
		}
		line := strings.Split(strings.TrimSpace(out), "\n")[0]
		return line[strings.LastIndex(line, ":")+1:]
	}
	api := port("6443/tcp")
	c.ingressPort = port("80/tcp")
	var kc string
	for range 120 {
		out, err := run(ctx, nil, "docker", "exec", c.name, "cat", "/etc/rancher/k3s/k3s.yaml")
		if err == nil && strings.Contains(out, "server:") {
			kc = strings.ReplaceAll(out, "https://127.0.0.1:6443", "https://127.0.0.1:"+api)
			break
		}
		time.Sleep(time.Second)
	}
	if kc == "" {
		t.Fatal("no kubeconfig")
	}
	c.kubeconfig = filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(c.kubeconfig, []byte(kc), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		out, err := c.try(nil, "-n", "kube-system", "rollout", "status", "deploy/traefik", "--timeout=10s")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("traefik not ready: %s", out)
		}
		time.Sleep(3 * time.Second)
	}
	// The "nginx" class prod overlays use: Traefik under another name, as
	// Nimbo's runtime sets it up.
	c.kubectl(t, []byte(`apiVersion: networking.k8s.io/v1
kind: IngressClass
metadata:
  name: nginx
spec:
  controller: traefik.io/ingress-controller
`), "apply", "-f", "-")
	// Images the templates pull, from the local Docker cache when there.
	for _, img := range []string{"postgres:16-alpine", "mysql:8.4", "redis:7-alpine", "rabbitmq:3.13-management-alpine", "nginx:1.27-alpine", "wordpress:6-apache", waitImage} {
		if _, err := run(ctx, nil, "docker", "image", "inspect", img); err == nil {
			c.importImage(t, img, "")
		}
	}
	return c
}

func (c *cluster) try(stdin []byte, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	return run(ctx, stdin, "kubectl", append([]string{"--kubeconfig", c.kubeconfig}, args...)...)
}

func (c *cluster) kubectl(t *testing.T, stdin []byte, args ...string) string {
	t.Helper()
	out, err := c.try(stdin, args...)
	if err != nil {
		t.Fatalf("kubectl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// importImage loads a local image into the node (as name when set).
func (c *cluster) importImage(t *testing.T, img, name string) {
	t.Helper()
	ctx := context.Background()
	save := exec.CommandContext(ctx, "docker", "save", img)
	imp := exec.CommandContext(ctx, "docker", "exec", "-i", c.name, "ctr", "-n", "k8s.io", "images", "import", "-")
	pipe, err := save.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	imp.Stdin = pipe
	var out bytes.Buffer
	imp.Stdout, imp.Stderr = &out, &out
	if err := save.Start(); err != nil {
		t.Fatal(err)
	}
	if err := imp.Run(); err != nil {
		t.Fatalf("import %s: %v %s", img, err, out.String())
	}
	_ = save.Wait()
	if name != "" {
		full := func(s string) string { return "docker.io/library/" + s }
		_, _ = run(ctx, nil, "docker", "exec", c.name, "ctr", "-n", "k8s.io", "images", "rm", full(name))
		if o, err := run(ctx, nil, "docker", "exec", c.name, "ctr", "-n", "k8s.io", "images", "tag", full(img), full(name)); err != nil {
			t.Fatalf("tag %s: %v %s", name, err, o)
		}
	}
}

// loadBuild builds an image of the workflow under a private tag and loads
// it into the node under the name the manifests use.
func (c *cluster) loadBuild(t *testing.T, dir string, b model.KubeBuild) {
	t.Helper()
	tag := "nimbo-convert-it-" + strings.ReplaceAll(b.Image, ":", "-") + ":it"
	args := []string{"build", "-q", "-t", tag}
	if b.Dockerfile != "" {
		args = append(args, "-f", filepath.Join(dir, b.Context, b.Dockerfile))
	}
	if b.Target != "" {
		args = append(args, "--target", b.Target)
	}
	for k, v := range b.Args {
		args = append(args, "--build-arg", k+"="+v)
	}
	args = append(args, filepath.Join(dir, filepath.FromSlash(b.Context)))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if out, err := run(ctx, nil, "docker", args...); err != nil {
		t.Fatalf("build %s: %v\n%s", b.Image, err, out)
	}
	t.Cleanup(func() { _, _ = run(context.Background(), nil, "docker", "rmi", tag) })
	c.importImage(t, tag, b.Image)
}

// get requests / on the Ingress with host, retrying while the route
// propagates.
func (c *cluster) get(t *testing.T, host string) int {
	t.Helper()
	cli := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	status := 0
	for range 20 {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%s/", c.ingressPort), nil)
		req.Host = host
		resp, err := cli.Do(req)
		if err == nil {
			status = resp.StatusCode
			resp.Body.Close()
			if status < 500 && status != 404 {
				return status
			}
		}
		time.Sleep(2 * time.Second)
	}
	return status
}
