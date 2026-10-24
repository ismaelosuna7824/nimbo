package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Access tools: read and edit permissions structurally (AWS policy
// statements, Azure role assignments, GCP IAM members/bindings, OCI policy
// statements, Kubernetes RBAC) and grant an identity Read/Write/Manage on
// a resource the way the canvas drag does.

const groupAccess = "Access"

func registerAccessTools(r *registry, t *tools) {
	r.group = groupAccess
	addTool(r, "get_access", "Get access", "Read the permissions of one node as structured data. Terraform: AWS policy documents (per policy attribute or aws_iam_policy_document: statements with sid, effect, actions, resources as HCL expressions, principals; editable=false parts are kept verbatim), Azure role assignment (scope, role, principal), GCP IAM member/binding (resource, role, members), OCI policy statements (subject, verb, resource type, compartment, condition). Kubernetes: a Role/ClusterRole's rules, a binding's roleRef/subjects, a workload's or ServiceAccount's service account and token automount. Includes least-privilege warnings (wildcards, admin/basic roles, tenancy-wide or cluster-wide grants).", t.getAccess)
	addTool(r, "set_access", "Set access", "Write the permissions of one node from the structure get_access returned (change what you need and send it back). Terraform: pass exactly one of aws (one document, matched by path; send every statement: omitted ones are deleted, statements keep their source to stay byte-identical when unchanged), azure, gcp or oci. Kubernetes: rules, role_ref, subjects, service_account and/or automount_token (omitted fields are unchanged). Everything the editor doesn't represent is kept.", t.setAccess)
	addTool(r, "grant_access", "Grant access", "Give an identity least-privilege access to a resource, like dragging one onto the other on the canvas and answering \"What can it do?\". level: read, write or manage. Terraform: identity is e.g. an aws_iam_role or Lambda function, an Azure web app/managed identity, a google_service_account or an OCI group/dynamic group; the provider's model is written (policy statement, azurerm_role_assignment, google_*_iam_member, oci_identity_policy statement). Kubernetes: identity is a workload (or ServiceAccount) and resource a Role or an object (ConfigMap, Secret, ...): creates the ServiceAccount, Role and RoleBinding and sets serviceAccountName. Without level it lists the options with what each writes.", t.grantAccess)
	addTool(r, "list_access_catalog", "List access catalog", "Official reference data the access editors pick from (pinned in Nimbo with source URL and date): aws (services; with service=<prefix> its actions with access level and resource types), azure (built-in roles), gcp (predefined roles), oci (verbs and resource types), kubernetes (verbs and resources). resource_type=<terraform type> puts the roles suggested for that resource first (azure, gcp).", t.listAccessCatalog)
}

type accessNodeIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Node     string `json:"node" jsonschema:"node address or ID (Terraform), or object node id like Role/shop/reader (Kubernetes)"`
}

func (t *tools) getAccess(ctx context.Context, in accessNodeIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if wf.IsKubernetes() {
		var out model.KubeAccessView
		err := t.c.get(ctx, kubePath(wf, "/access?node="+url.QueryEscape(in.Node)), &out)
		return out, err
	}
	n, err := findNode(wf, in.Node)
	if err != nil {
		return nil, err
	}
	var out model.AccessView
	err = t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/access?node="+url.QueryEscape(n.ID), &out)
	return out, err
}

type setAccessIn struct {
	accessNodeIn
	AWS            *model.AWSPolicyDocument   `json:"aws,omitempty" jsonschema:"Terraform AWS: one policy document from get_access (path selects it)"`
	Azure          *model.AzureRoleAssignment `json:"azure,omitempty" jsonschema:"Terraform Azure role assignment"`
	GCP            *model.GCPIAMBinding       `json:"gcp,omitempty" jsonschema:"Terraform GCP IAM member or binding"`
	OCI            *model.OCIPolicy           `json:"oci,omitempty" jsonschema:"Terraform OCI policy statements"`
	Rules          []model.KubeRule           `json:"rules,omitempty" jsonschema:"Kubernetes Role/ClusterRole rules (the whole list)"`
	RoleRef        *model.KubeRoleRef         `json:"role_ref,omitempty" jsonschema:"Kubernetes binding roleRef"`
	Subjects       []model.KubeSubject        `json:"subjects,omitempty" jsonschema:"Kubernetes binding subjects (the whole list)"`
	ServiceAccount *string                    `json:"service_account,omitempty" jsonschema:"Kubernetes workload: serviceAccountName"`
	AutomountToken *bool                      `json:"automount_token,omitempty" jsonschema:"Kubernetes workload or ServiceAccount: automountServiceAccountToken"`
}

func (t *tools) setAccess(ctx context.Context, in setAccessIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var out model.Workflow
	if wf.IsKubernetes() {
		edit := model.KubeAccessEdit{NodeID: in.Node, Rules: in.Rules, RoleRef: in.RoleRef, Subjects: in.Subjects, ServiceAccount: in.ServiceAccount, AutomountToken: in.AutomountToken}
		if err := t.c.do(ctx, http.MethodPut, kubePath(wf, "/access"), edit, &out); err != nil {
			return nil, err
		}
		return kubeResult(wf, &out), nil
	}
	n, err := findNode(wf, in.Node)
	if err != nil {
		return nil, err
	}
	edit := model.AccessEdit{NodeID: n.ID, AWS: in.AWS, Azure: in.Azure, GCP: in.GCP, OCI: in.OCI}
	if err := t.c.do(ctx, http.MethodPut, "/workflows/"+pathEscape(wf.ID)+"/access", edit, &out); err != nil {
		return nil, err
	}
	if after := findNodeByID(&out, n.ID); after != nil {
		return mutationResult(wf, &out, map[string]any{"node": viewNode(&out, after)}), nil
	}
	return mutationResult(wf, &out, nil), nil
}

type grantAccessIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Identity string `json:"identity" jsonschema:"who gets access: node address/ID (Terraform) or object node id (Kubernetes workload or ServiceAccount)"`
	Resource string `json:"resource" jsonschema:"what it gets access to: node address/ID, or Kubernetes object node id (Role, ConfigMap, Secret, ...)"`
	Level    string `json:"level,omitempty" jsonschema:"read, write or manage; omit to list the options"`
}

func (t *tools) grantAccess(ctx context.Context, in grantAccessIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if wf.IsKubernetes() {
		if in.Level == "" {
			var options []map[string]any
			err := t.c.get(ctx, kubePath(wf, "/connect?from="+url.QueryEscape(in.Identity)+"&to="+url.QueryEscape(in.Resource)), &options)
			return map[string]any{"options": accessOptions(options), "hint": "call grant_access again with level"}, err
		}
		var out model.Workflow
		body := map[string]any{"from": in.Identity, "to": in.Resource, "recipeId": model.AccessRecipePrefix + in.Level}
		if err := t.c.do(ctx, http.MethodPost, kubePath(wf, "/connect"), body, &out); err != nil {
			return nil, err
		}
		return kubeResult(wf, &out), nil
	}
	id, err := findNode(wf, in.Identity)
	if err != nil {
		return nil, err
	}
	res, err := findNode(wf, in.Resource)
	if err != nil {
		return nil, err
	}
	var recipes []model.ConnectionRecipe
	q := url.Values{"source": {id.ID}, "target": {res.ID}}
	if err := t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/connections?"+q.Encode(), &recipes); err != nil {
		return nil, err
	}
	var grants []model.ConnectionRecipe
	for _, r := range recipes {
		if r.Group == model.AccessRecipeGroup {
			grants = append(grants, r)
		}
	}
	if len(grants) == 0 {
		return nil, fmt.Errorf("Nimbo can't grant %s access to %s: no identity/resource pair it knows (use set_access or update_node)", address(id), address(res))
	}
	if in.Level == "" {
		return map[string]any{"options": grants, "hint": "call grant_access again with level"}, nil
	}
	return t.connect(ctx, connectIn{connectionIn: connectionIn{Workflow: wf.ID, Source: id.ID, Target: res.ID}, Recipe: model.AccessRecipePrefix + in.Level})
}

// accessOptions keeps the grant options of a Kubernetes recipe listing.
func accessOptions(options []map[string]any) []map[string]any {
	var out []map[string]any
	for _, o := range options {
		if o["kind"] == model.AccessRecipeGroup {
			out = append(out, o)
		}
	}
	return out
}

type accessCatalogIn struct {
	Provider     string `json:"provider" jsonschema:"aws, azure, gcp, oci or kubernetes"`
	Service      string `json:"service,omitempty" jsonschema:"aws: service prefix (dynamodb, s3, logs, ...) to list its actions"`
	ResourceType string `json:"resource_type,omitempty" jsonschema:"azure/gcp: Terraform resource type to put its suggested roles first"`
	Query        string `json:"query,omitempty" jsonschema:"case-insensitive filter"`
}

func (t *tools) listAccessCatalog(ctx context.Context, in accessCatalogIn) (any, error) {
	q := url.Values{}
	if in.Service != "" {
		q.Set("service", in.Service)
	}
	if in.ResourceType != "" {
		q.Set("resourceType", in.ResourceType)
	}
	if in.Query != "" {
		q.Set("q", in.Query)
	}
	var out model.AccessCatalog
	err := t.c.get(ctx, "/access/catalog/"+pathEscape(in.Provider)+"?"+q.Encode(), &out)
	return out, err
}

func findNodeByID(wf *model.Workflow, id string) *model.Node {
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == id {
			return &wf.Nodes[i]
		}
	}
	return nil
}
