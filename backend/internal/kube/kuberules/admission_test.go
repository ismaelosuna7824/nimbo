package kuberules

import (
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const tlsService = `apiVersion: v1
kind: Service
metadata: {name: web, namespace: shop}
spec:
  selector: {app: web}
  ports: [{port: 80}]
`

func TestIngressTLS(t *testing.T) {
	plain := `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: web, namespace: shop}
spec:
  rules:
    - host: shop.example.com
      http:
        paths:
          - {path: /, pathType: Prefix, backend: {service: {name: web, port: {number: 80}}}}
`
	withTLS := strings.Replace(plain, "spec:\n", "spec:\n  tls: [{hosts: [shop.example.com], secretName: web-tls}]\n", 1)
	gateway := `apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: edge, namespace: shop}
spec:
  gatewayClassName: traefik
  listeners:
    - {name: http, protocol: HTTP, port: 80}
`
	gatewayTLS := gateway + "    - {name: https, protocol: HTTPS, port: 443, tls: {mode: Terminate, certificateRefs: [{name: edge-tls}]}}\n"
	cases := []struct {
		name, env, doc string
		want           []string // severity:message fragment
	}{
		{"ingress without tls", "dev", plain, []string{"info:Ingress web serves shop.example.com over plain HTTP"}},
		{"ingress without tls, production", "production", plain, []string{"info:Ingress web serves shop.example.com"}},
		{"ingress with tls", "production", withTLS, nil},
		{"local hosts only", "production", strings.Replace(plain, "shop.example.com", "shop.localhost", 1), nil},
		{"hostless ingress", "", strings.Replace(plain, "    - host: shop.example.com\n      http:", "    - http:", 1), []string{"info:Ingress web serves every host"}},
		{"gateway with only HTTP", "", gateway, []string{"info:Gateway edge only has plain HTTP listeners"}},
		{"gateway on localhost", "", strings.Replace(gateway, "port: 80}", "port: 80, hostname: shop.localhost}", 1), nil},
		{"gateway with HTTPS", "prod", gatewayTLS, nil},
		{"ingress without rules or backend", "prod", "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata: {name: empty, namespace: shop}\nspec: {}\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := only(checkFiles(t, c.env, map[string]string{"a.yaml": tlsService + "---\n" + c.doc}), "k8s_ingress_tls")
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i, w := range c.want {
				sev, msg, _ := strings.Cut(w, ":")
				if string(got[i].Severity) != sev || !strings.Contains(got[i].Message, msg) || got[i].Fix == "" {
					t.Errorf("got %s %q, want %s %q", got[i].Severity, got[i].Message, sev, msg)
				}
			}
		})
	}
}

func TestPodSecurityNamespaceLabel(t *testing.T) {
	ns := func(name, labels string) string {
		return "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: " + name + "\n" + labels + "---\n"
	}
	files := map[string]string{"a.yaml": ns("shop", "") +
		ns("billing", "  labels: {pod-security.kubernetes.io/enforce: restricted}\n") +
		ns("monitoring", "  labels: {pod-security.kubernetes.io/enforce: privileged}\n") +
		ns("audit-only", "  labels: {pod-security.kubernetes.io/audit: restricted}\n") +
		ns("kube-system", "")}
	diags := checkFiles(t, "", files)
	got := only(diags, "k8s_pss_namespace_label")
	if len(got) != 2 || !strings.Contains(got[0].Message, "Namespace shop") || !strings.Contains(got[1].Message, "Namespace audit-only") {
		t.Fatalf("got %v", got)
	}
	if !strings.Contains(got[0].Fix, "pod-security.kubernetes.io/enforce: baseline") {
		t.Errorf("fix: %s", got[0].Fix)
	}
	if hasCode(FilterSettings(diags, model.BestPracticeSettings{}), "k8s_pss_namespace_label") {
		t.Error("reported without opting in")
	}
}

func TestStartupProbe(t *testing.T) {
	pod := func(probes string) string {
		return `apiVersion: v1
kind: Pod
metadata: {name: api, namespace: shop}
spec:
  containers:
    - name: api
      image: api:1.0
      ports: [{containerPort: 8080}]
` + probes
	}
	cases := []struct {
		name, probes string
		want         string
	}{
		{"slow liveness delay", "      livenessProbe: {httpGet: {path: /healthz, port: 8080}, initialDelaySeconds: 90}\n", "failureThreshold: 9"},
		{"short delay", "      livenessProbe: {httpGet: {path: /healthz, port: 8080}, initialDelaySeconds: 10}\n", ""},
		{"no delay", "      livenessProbe: {httpGet: {path: /healthz, port: 8080}}\n", ""},
		{"startup probe present", "      livenessProbe: {httpGet: {path: /healthz, port: 8080}, initialDelaySeconds: 90}\n      startupProbe: {httpGet: {path: /healthz, port: 8080}, failureThreshold: 30, periodSeconds: 10}\n", ""},
		{"no liveness", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diags := checkFiles(t, "", map[string]string{"a.yaml": pod(c.probes)})
			got := only(diags, "k8s_startup_probe")
			if c.want == "" {
				if len(got) > 0 {
					t.Fatalf("unexpected: %v", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0].Fix, c.want) || got[0].Attribute != "spec.containers[0].livenessProbe.initialDelaySeconds" {
				t.Fatalf("got %v", got)
			}
			if hasCode(FilterSettings(diags, model.BestPracticeSettings{}), "k8s_startup_probe") {
				t.Error("reported without opting in")
			}
		})
	}
}
