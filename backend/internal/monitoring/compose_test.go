package monitoring

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

const shopCompose = `services:
  api:
    image: ghcr.io/stefanprodan/podinfo:6.15.0
    labels:
      prometheus.io/scrape: "true"
      prometheus.io/port: "9898"
    ports:
      - "127.0.0.1:3000:9898"
    networks: [front, data]
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    networks: [data]
  migrate:
    image: postgres:16-alpine
    command: ["true"]
    restart: "no"
    networks: [data]
networks:
  front:
  data:
`

func openCompose(t *testing.T, dir string, files ...string) *compose.Project {
	t.Helper()
	p := compose.Open(context.Background(), dir, files, nil, compose.Options{})
	if p.Model == nil {
		t.Fatalf("compose project does not load: %v", p.Diagnostics)
	}
	return p
}

func TestComposeStack(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"compose.yaml": shopCompose, ".env": "POSTGRES_PASSWORD=x\n"})
	out, err := Compose(ComposeInput{Project: openCompose(t, dir, "compose.yaml"), ProjectName: "nimbo-dk-shop"})
	if err != nil {
		t.Fatal(err)
	}
	if out.File != "monitoring/compose.yaml" || !out.NewPassword || len(out.Password) != 24 {
		t.Fatalf("file %q, password %q (new %v)", out.File, out.Password, out.NewPassword)
	}
	// 3000 is taken by api: Grafana moves to the next free port.
	if out.GrafanaPort != 3001 || out.PrometheusPort != 9090 {
		t.Fatalf("ports grafana %d prometheus %d", out.GrafanaPort, out.PrometheusPort)
	}
	var prom struct {
		ScrapeConfigs []struct {
			JobName      string `yaml:"job_name"`
			DNSSDConfigs []struct {
				Names []string
				Port  int
			} `yaml:"dns_sd_configs"`
			MetricRelabel []struct{ Regex string } `yaml:"metric_relabel_configs"`
		} `yaml:"scrape_configs"`
	}
	if err := yaml.Unmarshal(out.Files["monitoring/prometheus/prometheus.yml"], &prom); err != nil {
		t.Fatal(err)
	}
	var jobs []string
	for _, sc := range prom.ScrapeConfigs {
		jobs = append(jobs, sc.JobName)
		if sc.JobName == "cadvisor" && (len(sc.MetricRelabel) != 1 || sc.MetricRelabel[0].Regex != "nimbo-dk-shop") {
			t.Errorf("cadvisor keeps other projects' containers: %+v", sc.MetricRelabel)
		}
		if sc.JobName == "api" && (len(sc.DNSSDConfigs) != 1 || sc.DNSSDConfigs[0].Port != 9898) {
			t.Errorf("api scrape %+v", sc.DNSSDConfigs)
		}
	}
	// Only the labelled service is scraped besides the stack.
	if !slices.Equal(jobs, []string{"prometheus", "cadvisor", "api"}) {
		t.Fatalf("jobs %v", jobs)
	}
	// Prometheus joins the networks it scrapes on.
	if !strings.Contains(string(out.Files[out.File]), "networks: [data, front]") {
		t.Errorf("prometheus networks:\n%s", out.Files[out.File])
	}
	// A down alert per long-running service; the one-shot migration has none.
	var down []string
	for _, a := range out.Summary.Alerts {
		if a.Name == "ContainerDown" {
			down = append(down, a.Target)
		}
	}
	if !slices.Equal(down, []string{"api", "db"}) {
		t.Fatalf("ContainerDown for %v", down)
	}

	// The stack loads with the user's file and adds no warnings or errors.
	for f, data := range out.Files {
		writeFiles(t, dir, map[string]string{f: string(data)})
	}
	writeFiles(t, dir, map[string]string{".env": "POSTGRES_PASSWORD=x\n" + GrafanaPasswordVar + "=" + out.Password + "\n"})
	p := openCompose(t, dir, "compose.yaml", out.File)
	for _, d := range p.Diagnostics {
		ours := strings.HasPrefix(d.File, ComposeDir+"/") || slices.Contains([]string{"service:" + PrometheusService, "service:" + GrafanaService, "service:" + CadvisorService}, d.NodeID)
		if d.Severity != model.SeverityInfo && ours {
			t.Errorf("%s %s %s:%d %s", d.Severity, d.Code, d.File, d.Line, d.Message)
		}
	}
	for _, s := range []string{PrometheusService, GrafanaService, CadvisorService} {
		if _, ok := p.Model.Services[s]; !ok {
			t.Errorf("service %s missing", s)
		}
	}

	// Again: same files, same ports and password, nothing new.
	again, err := Compose(ComposeInput{Project: p, ProjectName: "nimbo-dk-shop", Password: p.Env[GrafanaPasswordVar]})
	if err != nil {
		t.Fatal(err)
	}
	if again.NewPassword || again.GrafanaPort != 3001 || again.PrometheusPort != 9090 {
		t.Fatalf("second run: new password %v, ports %d/%d", again.NewPassword, again.GrafanaPort, again.PrometheusPort)
	}
	for f, data := range out.Files {
		if string(again.Files[f]) != string(data) {
			t.Errorf("%s changed on the second run", f)
		}
	}
}

func TestComposeServiceNameConflict(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"compose.yaml": "services:\n  grafana:\n    image: grafana/grafana:11.2.0\n"})
	_, err := Compose(ComposeInput{Project: openCompose(t, dir, "compose.yaml"), ProjectName: "p"})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Service != GrafanaService {
		t.Fatalf("err = %v", err)
	}
}
