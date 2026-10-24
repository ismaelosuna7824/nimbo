package kube

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// RBAC reference data: the built-in resources of the pinned Kubernetes
// release (rbac/resources.json, written by
// `go run ./internal/access/gen/kubernetes`) and the request verbs of
// https://kubernetes.io/docs/reference/access-authn-authz/authorization/#determine-the-request-verb
// plus the RBAC-specific ones of
// https://kubernetes.io/docs/reference/access-authn-authz/rbac/#privilege-escalation-prevention-and-bootstrapping
// (both retrieved 2026-10-23).

//go:embed rbac/resources.json
var rbacResourcesJSON []byte

// rbacResource is one resource (or subresource) of the API.
type rbacResource struct {
	Group      string   `json:"group"`
	Resource   string   `json:"resource"`
	Kind       string   `json:"kind,omitempty"`
	Namespaced bool     `json:"namespaced"`
	Verbs      []string `json:"verbs"`
}

type rbacData struct {
	Version   string             `json:"version"`
	Source    model.AccessSource `json:"source"`
	Resources []rbacResource     `json:"resources"`
}

var loadRBACData = sync.OnceValue(func() *rbacData {
	var d rbacData
	if err := json.Unmarshal(rbacResourcesJSON, &d); err != nil {
		panic(fmt.Sprintf("kube: embedded rbac/resources.json: %v", err))
	}
	return &d
})

// rbacVerbs are the verbs RBAC rules grant, in the editor's order.
var rbacVerbs = []model.AccessVerb{
	{Name: "get", Description: "Read one object by name."},
	{Name: "list", Description: "Read every object of the resource (their full contents, Secrets included)."},
	{Name: "watch", Description: "Stream changes to the objects."},
	{Name: "create", Description: "Create objects (can't be limited to resourceNames)."},
	{Name: "update", Description: "Replace an existing object."},
	{Name: "patch", Description: "Change part of an existing object."},
	{Name: "delete", Description: "Delete one object."},
	{Name: "deletecollection", Description: "Delete every object of the resource at once (can't be limited to resourceNames)."},
	{Name: "use", Description: "Use a resource an admission plugin checks it for (policies, IP pools)."},
	{Name: "bind", Description: "Create bindings to a role without holding its permissions (privilege escalation)."},
	{Name: "escalate", Description: "Create or update roles with permissions it doesn't hold (privilege escalation)."},
	{Name: "impersonate", Description: "Act as another user, group or ServiceAccount."},
	{Name: "approve", Description: "Approve CertificateSigningRequests."},
	{Name: "sign", Description: "Sign CertificateSigningRequests."},
	{Name: "*", Description: "Every verb, including ones added later."},
}

// rbacResourceOf is the resource plural of a kind in an API group ("" when
// unknown): built-ins from the pinned API, custom resources from a
// CustomResourceDefinition of the project.
func (p *Project) rbacResourceOf(group, kind string) (resource string, namespaced bool) {
	for _, r := range loadRBACData().Resources {
		if r.Group == group && r.Kind == kind {
			return r.Resource, r.Namespaced
		}
	}
	for _, o := range p.Objects {
		if o.Kind == "CustomResourceDefinition" && str(o.Obj, "spec", "group") == group && str(o.Obj, "spec", "names", "kind") == kind {
			return str(o.Obj, "spec", "names", "plural"), str(o.Obj, "spec", "scope") != "Cluster"
		}
	}
	return "", false
}

// RBACCatalog is the Kubernetes reference data of the RBAC editors: verbs
// and built-in resources with their API group. group ("core" for the core
// group) keeps one group's resources; search filters both.
func RBACCatalog(group, search string) *model.AccessCatalog {
	d := loadRBACData()
	out := &model.AccessCatalog{Provider: "kubernetes", Source: d.Source, Verbs: []model.AccessVerb{}, ResourceTypes: []model.AccessResourceType{}}
	search = strings.ToLower(strings.TrimSpace(search))
	for _, v := range rbacVerbs {
		if search == "" || strings.Contains(strings.ToLower(v.Name+" "+v.Description), search) {
			out.Verbs = append(out.Verbs, v)
		}
	}
	only := group != ""
	if group == "core" {
		group = ""
	}
	res := slices.Clone(d.Resources)
	slices.SortStableFunc(res, func(a, b rbacResource) int {
		return strings.Compare(a.Group+"\x00"+a.Resource, b.Group+"\x00"+b.Resource)
	})
	for _, r := range res {
		scope := "cluster-wide"
		if r.Namespaced {
			scope = "namespaced"
		}
		kind := r.Kind
		if kind == "" {
			kind = "subresource"
		}
		desc := fmt.Sprintf("%s · %s · %s", kind, scope, strings.Join(r.Verbs, ", "))
		if search != "" && !strings.Contains(strings.ToLower(r.Resource+" "+r.Group+" "+desc), search) {
			continue
		}
		if only && r.Group != group {
			continue
		}
		out.ResourceTypes = append(out.ResourceTypes, model.AccessResourceType{Name: r.Resource, Group: r.Group, Description: desc})
	}
	return out
}
