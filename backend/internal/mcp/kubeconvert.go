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
		"Convert a docker workflow into a NEW Kubernetes workflow (the docker one is kept): format manifests (k8s/*.yaml), kustomize (k8s/base + overlays/dev,prod) or helm (chart/ with values-dev/prod.yaml). Each service becomes a Deployment (StatefulSet + volumeClaimTemplates for data services with named volumes, Job for one-shot services), a Service named like the compose service, a ConfigMap + Secret for its environment (credentials in the Secret), probes from its healthcheck, requests/limits from deploy.resources, a non-root securityContext when the image's UID is known, init containers waiting for its depends_on, and an Ingress rule (<service>.<name>.localhost) for published HTTP ports; built images keep their build contexts (<service>:dev). Returns the report: mappings, objects and what couldn't be mapped (with why/what to do). dry_run previews the report and files without creating anything. Then deploy the new workflow with deploy.",
		t.convertToKubernetes)
}

type convertToKubernetesIn struct {
	Workflow  string `json:"workflow,omitempty" jsonschema:"docker workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Format    string `json:"format,omitempty" jsonschema:"manifests (default), kustomize or helm"`
	Name      string `json:"name,omitempty" jsonschema:"application name (DNS label): labels, chart/release name, Ingress hosts; default from the workflow name"`
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace (default: name)"`
	DryRun    bool   `json:"dry_run,omitempty" jsonschema:"preview the report and generated files without creating the workflow"`
}

func (t *tools) convertToKubernetes(ctx context.Context, in convertToKubernetesIn) (any, error) {
	wf, err := t.dockerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"format": in.Format, "name": in.Name, "namespace": in.Namespace, "dryRun": in.DryRun}
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
