package mcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// convert_to_kubernetes: a docker workflow → a new Kubernetes workflow
// (POST /workflows/{id}/convert/kubernetes).

func registerConvertTool(r *registry, t *tools) {
	addTool(r, "convert_to_kubernetes", "Convert to Kubernetes",
		"Convert a docker workflow into a NEW Kubernetes workflow (the docker one is kept): format manifests (k8s/*.yaml), kustomize (k8s/base + one overlay per environment) or helm (chart/ with values-<env>.yaml). Environments: the docker workflow's own (compose.<env>.yaml and/or its env file .env.<env> / <env>.env, see list_environments) each become overlays/<env> / values-<env>.yaml carrying what they change from the base (replicas, image, resources, ConfigMap values; credentials through Secret generators/secretEnv, never in manifests); without any, dev and prod are written. The base files are the base whatever environment is active. Each service becomes a Deployment (StatefulSet + volumeClaimTemplates for data services with named volumes, Job for one-shot services), a Service named like the compose service, a ConfigMap + Secret for its environment (credentials in the Secret), probes from its healthcheck, a non-root securityContext when the image's UID is known, init containers waiting for its depends_on, and an Ingress rule (<service>.<name>.localhost) for published HTTP ports; built images keep their build contexts (<service>:dev). "+
			"Requests/limits: explicit deploy.resources/cpus/mem_limit win; the rest is sized from the newest finished load test of the docker workflow (run_load_test first for production-grade sizing: CPU request = p90 usage × 1.15, CPU limit = max(peak × 1.5, 2 × request), memory request/limit = peak × 1.2 / × 1.5; services that hit their Docker limit are sized above it), else defaulted; report.sizing gives each service's numbers and provenance. "+
			"Prod-like environments (prod, production, staging, …; or the default prod) scale stateless non-data services with an autoscaling/v2 HorizontalPodAutoscaler on CPU (min = max(the environment's replicas, 2), fast scale-up, 5 min scale-down stabilization; the Deployment leaves spec.replicas to the HPA), a PodDisruptionBudget, RollingUpdate maxUnavailable 0/maxSurge 1 and topology spread over nodes; plain manifests mirror the base compose files (no HPA, no environments). "+
			"Returns the report: mappings, objects, sizing, autoscaling, the load test used and what couldn't be mapped (with why/what to do). dry_run previews the report and files without creating anything. Then deploy the new workflow with deploy.",
		t.convertToKubernetes)
}

type convertToKubernetesIn struct {
	Workflow         string `json:"workflow,omitempty" jsonschema:"docker workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Format           string `json:"format,omitempty" jsonschema:"manifests (default), kustomize or helm"`
	Name             string `json:"name,omitempty" jsonschema:"application name (DNS label): labels, chart/release name, Ingress hosts; default from the workflow name"`
	Namespace        string `json:"namespace,omitempty" jsonschema:"namespace (default: name)"`
	DryRun           bool   `json:"dry_run,omitempty" jsonschema:"preview the report and generated files without creating the workflow"`
	SizeFromLoadTest *bool  `json:"size_from_load_test,omitempty" jsonschema:"size requests/limits from the newest usable load test of the docker workflow (default true when there is one); false keeps compose values and defaults"`
	LoadTestRunID    string `json:"load_test_run_id,omitempty" jsonschema:"size from this load test run instead of the newest (must have measured current services of the workflow)"`
	Autoscaling      *bool  `json:"autoscaling,omitempty" jsonschema:"write HorizontalPodAutoscalers in prod (kustomize/helm) for stateless non-data services (default true)"`
	MaxReplicas      int    `json:"max_replicas,omitempty" jsonschema:"HPA maxReplicas, 2-100 (default 5; raised to the service's compose replicas when those are more)"`
	TargetCPU        int    `json:"target_cpu,omitempty" jsonschema:"HPA average CPU utilization target, % of the CPU request, 10-95 (default 70)"`
}

func (t *tools) convertToKubernetes(ctx context.Context, in convertToKubernetesIn) (any, error) {
	wf, err := t.dockerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"format": in.Format, "name": in.Name, "namespace": in.Namespace, "dryRun": in.DryRun, "loadTestRunId": in.LoadTestRunID,
		"autoscaling": map[string]any{"enabled": in.Autoscaling, "maxReplicas": in.MaxReplicas, "targetCpu": in.TargetCPU}}
	if in.SizeFromLoadTest != nil {
		body["sizeFromLoadTest"] = *in.SizeFromLoadTest
	}
	var res struct {
		Workflow *model.Workflow         `json:"workflow"`
		Report   model.KubeConvertReport `json:"report"`
		Files    map[string]string       `json:"files"`
	}
	if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/convert/kubernetes", body, &res); err != nil {
		return nil, err
	}
	out := map[string]any{"report": res.Report}
	if in.DryRun {
		out["files"] = res.Files
		return out, nil
	}
	if res.Workflow != nil {
		out["workflow"] = map[string]any{"id": res.Workflow.ID, "name": res.Workflow.Name, "kind": res.Workflow.Kind, "diagnostics": convertDiagLines(res.Workflow.Diagnostics)}
		out["next"] = fmt.Sprintf("Deploy it with deploy workflow %q (builds the images, creates the local cluster, applies the %s source).", res.Workflow.ID, res.Report.Format)
	}
	return out, nil
}

// convertDiagLines formats warnings and errors as "severity code file:line message".
func convertDiagLines(ds []model.Diagnostic) []string {
	out := []string{}
	for _, d := range ds {
		if d.Severity == model.SeverityInfo {
			continue
		}
		at := ""
		if d.File != "" {
			at = fmt.Sprintf(" %s:%d", d.File, d.Line)
		}
		out = append(out, fmt.Sprintf("%s %s%s %s", d.Severity, d.Code, at, d.Message))
	}
	return out
}
