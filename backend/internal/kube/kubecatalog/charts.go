package kubecatalog

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// Chart is a pinned Helm chart of the palette.
type Chart struct {
	// Repository is an https chart repository or an oci:// registry path.
	Repository string `json:"repository"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	AppVersion string `json:"appVersion,omitempty"`
}

type chartEntry struct {
	id, label, description, category, icon string
	chart                                  Chart
	// values are the wrapper chart's values for the dependency: small,
	// local-friendly defaults (the chart's own values.yaml documents the rest).
	values string
	kinds  []string
}

func (c chartEntry) item() Item {
	ch := c.chart
	return Item{ID: "chart:" + c.id, Group: GroupChart, Label: c.label, Description: c.description, Category: c.category, Icon: c.icon, Kinds: c.kinds, Chart: &ch}
}

func chartByID(id string) (chartEntry, bool) {
	for _, c := range charts {
		if "chart:"+c.id == id {
			return c, true
		}
	}
	return chartEntry{}, false
}

const bitnami = "oci://registry-1.docker.io/bitnamicharts"

var charts = []chartEntry{
	{"postgresql", "PostgreSQL (Bitnami)", "PostgreSQL primary with persistence and a generated Secret", "database", "postgres",
		Chart{bitnami, "postgresql", "18.12.14", "18.6.0"}, `postgresql:
  auth:
    username: app
    password: change-me
    database: app
  primary:
    persistence:
      size: 2Gi
    resourcesPreset: small
`, []string{"StatefulSet", "Service", "Secret"}},
	{"redis", "Redis (Bitnami)", "Redis standalone with persistence", "cache", "redis",
		Chart{bitnami, "redis", "28.3.1", "8.10.2"}, `redis:
  architecture: standalone
  auth:
    password: change-me
  master:
    persistence:
      size: 1Gi
    resourcesPreset: nano
`, []string{"StatefulSet", "Service", "Secret"}},
	{"rabbitmq", "RabbitMQ (Bitnami)", "RabbitMQ broker with the management UI", "messaging", "rabbitmq",
		Chart{bitnami, "rabbitmq", "16.0.14", "4.1.3"}, `rabbitmq:
  auth:
    username: app
    password: change-me
  persistence:
    size: 1Gi
  resourcesPreset: small
`, []string{"StatefulSet", "Service", "Secret"}},
	{"mongodb", "MongoDB (Bitnami)", "MongoDB standalone with persistence", "database", "mongodb",
		Chart{bitnami, "mongodb", "20.0.5", "9.0.4"}, `mongodb:
  architecture: standalone
  auth:
    rootPassword: change-me
  persistence:
    size: 2Gi
  resourcesPreset: small
`, []string{"Deployment", "Service", "Secret"}},
	{"mysql", "MySQL (Bitnami)", "MySQL primary with persistence", "database", "mysql",
		Chart{bitnami, "mysql", "14.0.3", "9.4.0"}, `mysql:
  auth:
    rootPassword: change-me
    username: app
    password: change-me
    database: app
  primary:
    persistence:
      size: 2Gi
    resourcesPreset: small
`, []string{"StatefulSet", "Service", "Secret"}},
	{"keycloak", "Keycloak (Bitnami)", "Keycloak identity provider with its PostgreSQL", "auth", "keycloak",
		Chart{bitnami, "keycloak", "25.2.0", "26.3.3"}, `keycloak:
  auth:
    adminUser: admin
    adminPassword: change-me
  resourcesPreset: medium
`, []string{"StatefulSet", "Service", "Secret"}},
	{"ingress-nginx", "ingress-nginx", "The NGINX Ingress controller (IngressClass nginx; the local cluster already routes Ingress through Traefik)", "gateway", "nginx",
		Chart{"https://kubernetes.github.io/ingress-nginx", "ingress-nginx", "4.15.1", "1.15.1"}, `ingress-nginx:
  controller:
    ingressClassResource:
      default: false
    service:
      type: ClusterIP
`, []string{"Deployment", "Service", "IngressClass"}},
	{"traefik", "Traefik", "Traefik proxy and Ingress/Gateway API controller", "gateway", "traefik",
		Chart{"https://traefik.github.io/charts", "traefik", "42.1.3", "v3.7.16"}, `traefik:
  ingressClass:
    enabled: true
    isDefaultClass: false
    name: traefik-app
  service:
    type: ClusterIP
`, []string{"Deployment", "Service", "IngressClass"}},
	{"cert-manager", "cert-manager", "Issues and renews TLS certificates (installs its CRDs)", "security", "cert-manager",
		Chart{"https://charts.jetstack.io", "cert-manager", "v1.21.2", "v1.21.2"}, `cert-manager:
  crds:
    enabled: true
`, []string{"Deployment", "Service", "CustomResourceDefinition"}},
	{"external-secrets", "External Secrets Operator", "Syncs Secrets from Vault, AWS, GCP, Azure… into Kubernetes Secrets", "security", "vault",
		Chart{"https://charts.external-secrets.io", "external-secrets", "2.13.0", "v2.13.0"}, `external-secrets:
  installCRDs: true
`, []string{"Deployment", "CustomResourceDefinition"}},
	{"kube-prometheus-stack", "kube-prometheus-stack", "Prometheus Operator, Prometheus, Alertmanager, Grafana and the Kubernetes dashboards", "observability", "prometheus",
		Chart{"https://prometheus-community.github.io/helm-charts", "kube-prometheus-stack", "92.11.0", "v0.94.1"}, `kube-prometheus-stack:
  grafana:
    adminPassword: change-me
  alertmanager:
    enabled: false
  # k3s runs these inside the server process: nothing to scrape separately.
  kubeControllerManager:
    enabled: false
  kubeScheduler:
    enabled: false
  kubeProxy:
    enabled: false
  kubeEtcd:
    enabled: false
`, []string{"Deployment", "StatefulSet", "CustomResourceDefinition"}},
	{"grafana", "Grafana", "Dashboards over Prometheus, Loki and other data sources", "observability", "grafana",
		Chart{"https://grafana-community.github.io/helm-charts", "grafana", "14.0.1", "13.2.4"}, `grafana:
  adminPassword: change-me
  persistence:
    enabled: false
`, []string{"Deployment", "Service", "Secret"}},
	{"argo-cd", "Argo CD", "GitOps continuous delivery: syncs the cluster with Git repositories", "devtools", "argo",
		Chart{"https://argoproj.github.io/argo-helm", "argo-cd", "10.10.5", "v3.5.5"}, `argo-cd:
  dex:
    enabled: false
  notifications:
    enabled: false
`, []string{"Deployment", "StatefulSet", "CustomResourceDefinition"}},
	{"opentelemetry-collector", "OpenTelemetry Collector", "Receives OTLP traces, metrics and logs and exports them", "observability", "otel",
		Chart{"https://open-telemetry.github.io/opentelemetry-helm-charts", "opentelemetry-collector", "0.177.0", "0.163.2"}, `opentelemetry-collector:
  mode: deployment
  image:
    repository: otel/opentelemetry-collector-k8s
`, []string{"Deployment", "Service", "ConfigMap"}},
}

// ChartSource is what adding a chart writes: a wrapper chart under
// charts/<name>/ depending on the pinned chart (vendored in its charts/
// folder, so rendering and `up` work offline) and the helm source.
type ChartSource struct {
	Files  map[string][]byte
	Source model.KubeSource
}

// fetchTimeout bounds downloading a chart.
const fetchTimeout = 2 * time.Minute

// AddChart downloads the pinned chart of palette entry id and returns the
// wrapper chart files (under dir, e.g. "charts/postgresql") and its helm
// source (release name, namespace ns).
func AddChart(ctx context.Context, client *http.Client, id, name, ns string) (*ChartSource, error) {
	c, ok := chartByID(id)
	if !ok {
		return nil, fmt.Errorf("unknown chart %q", id)
	}
	name = orDefault(ObjectName(name), c.id)
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	tgz, err := FetchChart(ctx, client, c.chart)
	if err != nil {
		return nil, fmt.Errorf("download %s %s: %w", c.chart.Name, c.chart.Version, err)
	}
	dir := "charts/" + name
	wrapper := map[string]any{
		"apiVersion":  "v2",
		"name":        name,
		"description": fmt.Sprintf("%s %s (%s), vendored in charts/", c.chart.Name, c.chart.Version, c.chart.Repository),
		"type":        "application",
		"version":     "0.1.0",
		"dependencies": []map[string]any{{
			"name": c.chart.Name, "version": c.chart.Version, "repository": c.chart.Repository,
		}},
	}
	chartYAML, err := yaml.Marshal(wrapper)
	if err != nil {
		return nil, err
	}
	values := fmt.Sprintf("# Values of %s %s (the dependency's values go under %q).\n# Every option: helm show values %s\n%s",
		c.chart.Name, c.chart.Version, c.chart.Name, chartRef(c.chart), c.values)
	files := map[string][]byte{
		dir + "/Chart.yaml":  chartYAML,
		dir + "/values.yaml": []byte(values),
		dir + "/charts/" + c.chart.Name + "-" + c.chart.Version + ".tgz": tgz,
	}
	src := model.KubeSource{Kind: model.KubeHelm, Path: dir, Release: name, Namespace: orDefault(ns, name)}
	return &ChartSource{Files: files, Source: src}, nil
}

func chartRef(c Chart) string {
	if strings.HasPrefix(c.Repository, "oci://") {
		return c.Repository + "/" + c.Name + " --version " + c.Version
	}
	return c.Name + " --repo " + c.Repository + " --version " + c.Version
}

// FetchChart downloads a chart archive (.tgz) from an https repository
// (through its index.yaml) or an OCI registry (anonymous pull), and
// checks it is the chart and version asked for.
func FetchChart(ctx context.Context, client *http.Client, c Chart) ([]byte, error) {
	var data []byte
	var err error
	if strings.HasPrefix(c.Repository, "oci://") {
		data, err = fetchOCI(ctx, client, strings.TrimPrefix(c.Repository, "oci://")+"/"+c.Name, c.Version)
	} else {
		data, err = fetchHTTPRepo(ctx, client, c)
	}
	if err != nil {
		return nil, err
	}
	name, version, err := archiveChart(data)
	if err != nil {
		return nil, err
	}
	if name != c.Name || version != c.Version {
		return nil, fmt.Errorf("got chart %s %s, want %s %s", name, version, c.Name, c.Version)
	}
	return data, nil
}

// maxChartSize bounds a chart archive (kube-prometheus-stack is ~1 MB).
const maxChartSize = 32 << 20

func get(ctx context.Context, client *http.Client, u string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	return client.Do(req)
}

func readOK(resp *http.Response, u string) ([]byte, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxChartSize+1))
	if err == nil && len(data) > maxChartSize {
		err = errors.New("chart archive too large")
	}
	return data, err
}

func fetchHTTPRepo(ctx context.Context, client *http.Client, c Chart) ([]byte, error) {
	indexURL := strings.TrimSuffix(c.Repository, "/") + "/index.yaml"
	resp, err := get(ctx, client, indexURL, nil)
	if err != nil {
		return nil, err
	}
	raw, err := readOK(resp, indexURL)
	if err != nil {
		return nil, err
	}
	var index struct {
		Entries map[string][]struct {
			Version string   `yaml:"version"`
			URLs    []string `yaml:"urls"`
		} `yaml:"entries"`
	}
	if err := yaml.Unmarshal(raw, &index); err != nil {
		return nil, fmt.Errorf("%s: %w", indexURL, err)
	}
	for _, e := range index.Entries[c.Name] {
		if e.Version != c.Version || len(e.URLs) == 0 {
			continue
		}
		base, _ := url.Parse(strings.TrimSuffix(c.Repository, "/") + "/")
		ref, err := url.Parse(e.URLs[0])
		if err != nil {
			return nil, err
		}
		u := base.ResolveReference(ref).String()
		resp, err := get(ctx, client, u, nil)
		if err != nil {
			return nil, err
		}
		return readOK(resp, u)
	}
	return nil, fmt.Errorf("%s has no %s %s", c.Repository, c.Name, c.Version)
}

// fetchOCI pulls a Helm chart from an OCI registry anonymously (Docker
// Hub, GHCR and Quay hand out pull tokens for public repositories).
func fetchOCI(ctx context.Context, client *http.Client, ref, tag string) ([]byte, error) {
	host, repo, ok := strings.Cut(ref, "/")
	if !ok {
		return nil, fmt.Errorf("bad OCI reference %q", ref)
	}
	manifestURL := "https://" + host + "/v2/" + repo + "/manifests/" + tag
	header := http.Header{"Accept": {"application/vnd.oci.image.manifest.v1+json"}}
	resp, err := get(ctx, client, manifestURL, header)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close()
		token, err := registryToken(ctx, client, challenge, repo)
		if err != nil {
			return nil, err
		}
		header.Set("Authorization", "Bearer "+token)
		if resp, err = get(ctx, client, manifestURL, header); err != nil {
			return nil, err
		}
	}
	raw, err := readOK(resp, manifestURL)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Layers []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	for _, l := range manifest.Layers {
		if l.MediaType != "application/vnd.cncf.helm.chart.content.v1.tar+gzip" {
			continue
		}
		blobURL := "https://" + host + "/v2/" + repo + "/blobs/" + l.Digest
		resp, err := get(ctx, client, blobURL, http.Header{"Authorization": header["Authorization"]})
		if err != nil {
			return nil, err
		}
		return readOK(resp, blobURL)
	}
	return nil, fmt.Errorf("%s:%s is not a Helm chart", ref, tag)
}

var challengeParam = regexp.MustCompile(`(\w+)="([^"]*)"`)

// registryToken gets an anonymous pull token from a Bearer challenge.
func registryToken(ctx context.Context, client *http.Client, challenge, repo string) (string, error) {
	params := map[string]string{}
	for _, m := range challengeParam.FindAllStringSubmatch(challenge, -1) {
		params[m[1]] = m[2]
	}
	if params["realm"] == "" {
		return "", fmt.Errorf("registry asks for credentials (%s)", challenge)
	}
	u, err := url.Parse(params["realm"])
	if err != nil {
		return "", err
	}
	q := u.Query()
	if params["service"] != "" {
		q.Set("service", params["service"])
	}
	scope := params["scope"]
	if scope == "" {
		scope = "repository:" + repo + ":pull"
	}
	q.Set("scope", scope)
	u.RawQuery = q.Encode()
	resp, err := get(ctx, client, u.String(), nil)
	if err != nil {
		return "", err
	}
	raw, err := readOK(resp, u.String())
	if err != nil {
		return "", err
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return "", err
	}
	return orDefault(tok.Token, tok.AccessToken), nil
}

// archiveChart reads the name and version of a chart archive's Chart.yaml.
func archiveChart(data []byte) (string, string, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", "", fmt.Errorf("not a chart archive: %w", err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			return "", "", fmt.Errorf("chart archive without Chart.yaml: %w", err)
		}
		if path.Base(h.Name) != "Chart.yaml" || strings.Count(strings.Trim(h.Name, "/"), "/") != 1 {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(tr, 1<<20))
		if err != nil {
			return "", "", err
		}
		var meta struct {
			Name    string `yaml:"name"`
			Version string `yaml:"version"`
		}
		if err := yaml.Unmarshal(raw, &meta); err != nil {
			return "", "", err
		}
		return meta.Name, meta.Version, nil
	}
}
