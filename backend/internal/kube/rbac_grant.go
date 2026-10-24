package kube

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Grant recipes (ConnectRecipe.Kind RecipeAccess): an identity (a workload
// or a ServiceAccount) gets least-privilege RBAC.
//
//   - access:read|write|manage (identity -> a namespaced object): a rule
//     scoped to that one object (resourceNames) with get, list, watch
//     (Read), + update, patch (Write), + delete (Manage). create and
//     deletecollection can't be limited to resourceNames, so even Manage
//     leaves them out. The rule extends the Role already bound only to the
//     identity's ServiceAccount in that namespace, else goes to a new Role
//     <identity>-<level>-<object> with its RoleBinding.
//   - access:bind (identity -> Role/ClusterRole): a RoleBinding (namespaced
//     even for a ClusterRole, whose rules then apply in that namespace only).
//   - access:role (identity alone): an empty Role and its RoleBinding, whose
//     rules are then picked in the Role's editor.
//
// A workload running as the namespace's default ServiceAccount first gets
// its own (named after it) and runs as it; new objects go to the
// identity's source (next to it for manifests).
const (
	RecipeAccess = "access"
	recipeBind   = model.AccessRecipePrefix + "bind"
	recipeRole   = model.AccessRecipePrefix + "role"
)

// levelVerbs are the verbs a grant level gives on one object.
var levelVerbs = map[model.AccessLevel][]string{
	model.AccessRead:   {"get", "list", "watch"},
	model.AccessWrite:  {"get", "list", "watch", "update", "patch"},
	model.AccessManage: {"get", "list", "watch", "update", "patch", "delete"},
}

// grantPlan is what a grant recipe writes.
type grantPlan struct {
	id            string
	level         model.AccessLevel
	ident         *Object
	saNS, saName  string
	newSA, runAs  bool
	rule          *model.KubeRule // access:<level>
	extend        *Object         // Role the rule is added to
	ns            string          // namespace of the new Role/RoleBinding
	roleKind      string
	roleName      string
	newRole       bool
	bindName      string
	alreadyAllows bool
}

func isIdentity(o *Object) bool { return isWorkload(o) || isServiceAccount(o) }

// identityOf is the ServiceAccount an identity grants go to: the
// ServiceAccount itself, or the workload's own one (created and set when it
// runs as the default ServiceAccount).
func (p *Project) identityOf(o *Object) (ns, sa string, newSA, runAs bool) {
	if isServiceAccount(o) {
		return o.Namespace, o.Name, false, false
	}
	sa, explicit := workloadSA(o)
	if !explicit || sa == "default" {
		sa, runAs = o.Name, true
	}
	return o.Namespace, sa, p.serviceAccount(o.Namespace, sa) == nil, runAs
}

// grantTarget is the API group and resource of an object grants can scope
// to (ok=false for cluster-scoped, unknown and RBAC objects).
func (p *Project) grantTarget(o *Object) (group, resource string, ok bool) {
	if o.Namespace == "" || isServiceAccount(o) || isRole(o) || isBinding(o) {
		return "", "", false
	}
	group = apiGroup(o.APIVersion)
	resource, namespaced := p.rbacResourceOf(group, o.Kind)
	return group, resource, resource != "" && namespaced
}

// objectExists reports an object of kind ns/name.
func (p *Project) objectExists(kind, ns, name string) bool {
	return slices.ContainsFunc(p.Objects, func(o *Object) bool {
		return o.Kind == kind && o.Name == name && o.Namespace == ns
	})
}

// freeName is name, or name-2, name-3... when kind ns/name exists.
func (p *Project) freeName(kind, ns, name string) string {
	out := name
	for n := 2; p.objectExists(kind, ns, out); n++ {
		out = fmt.Sprintf("%s-%d", name, n)
	}
	return out
}

// bindsOnly reports whether every subject of b is the ServiceAccount
// ns/name.
func bindsOnly(b *Object, ns, name string) bool {
	subjects := list(dig(b.Obj, "subjects"))
	for _, s := range subjects {
		if str(s, "kind") != "ServiceAccount" || str(s, "name") != name || firstNonEmpty(str(s, "namespace"), b.Namespace) != ns {
			return false
		}
	}
	return len(subjects) > 0
}

// ownRole is the editable Role of namespace ns bound (by RoleBindings) to
// the ServiceAccount saNS/sa only, which grants extend.
func (p *Project) ownRole(ns, saNS, sa string) *Object {
	for _, b := range p.Objects {
		if !isRBAC(b, "RoleBinding") || b.Namespace != ns || str(b.Obj, "roleRef", "kind") != "Role" || !bindsOnly(b, saNS, sa) {
			continue
		}
		r := p.roleOf(b)
		if r == nil {
			continue
		}
		if ok, _ := p.rbacEditable(r, "rules"); !ok {
			continue
		}
		if _, why := parseRules(r); why != "" || dig(r.Obj, "aggregationRule") != nil {
			continue
		}
		shared := slices.ContainsFunc(p.bindingsTo(r), func(x *Object) bool { return !bindsOnly(x, saNS, sa) })
		if !shared {
			return r
		}
	}
	return nil
}

// bound reports a RoleBinding of ns giving role kind/name to saNS/sa.
func (p *Project) bound(ns, kind, name, saNS, sa string) bool {
	return slices.ContainsFunc(p.Objects, func(b *Object) bool {
		return isRBAC(b, "RoleBinding") && b.Namespace == ns && str(b.Obj, "roleRef", "kind") == kind && str(b.Obj, "roleRef", "name") == name &&
			slices.ContainsFunc(list(dig(b.Obj, "subjects")), func(s any) bool {
				return str(s, "kind") == "ServiceAccount" && str(s, "name") == sa && firstNonEmpty(str(s, "namespace"), b.Namespace) == saNS
			})
	})
}

// grantPlans are the grant recipes of src and dst (dst nil: access:role).
func (p *Project) grantPlans(src, dst *Object) []grantPlan {
	ident, target := src, dst
	reversed := false
	if dst != nil && !isIdentity(src) && isIdentity(dst) {
		ident, target, reversed = dst, src, true
	}
	if !isIdentity(ident) {
		return nil
	}
	// Object grants follow the gesture "drag the identity onto the
	// resource": dragged the other way (a Service onto a Deployment, a
	// ConfigMap onto a workload) the wiring recipes stay the only options.
	// Binding a Role works both ways.
	if reversed && !isRole(target) {
		return nil
	}
	ns, sa, newSA, runAs := p.identityOf(ident)
	base := grantPlan{ident: ident, saNS: ns, saName: sa, newSA: newSA, runAs: runAs}
	switch {
	case target == nil:
		pl := base
		pl.id, pl.ns, pl.roleKind, pl.newRole = recipeRole, ns, "Role", true
		pl.roleName = p.freeName("Role", ns, sa)
		pl.bindName = p.freeName("RoleBinding", ns, sa)
		return []grantPlan{pl}
	case isRole(target):
		pl := base
		pl.id, pl.roleKind, pl.roleName, pl.ns = recipeBind, target.Kind, target.Name, target.Namespace
		if target.Kind == "ClusterRole" {
			pl.ns = ns
		}
		if !newSA && p.bound(pl.ns, pl.roleKind, pl.roleName, ns, sa) {
			return nil
		}
		pl.bindName = p.freeName("RoleBinding", pl.ns, sa+"-"+target.Name)
		return []grantPlan{pl}
	}
	group, resource, ok := p.grantTarget(target)
	if !ok || target == ident {
		return nil
	}
	var out []grantPlan
	for _, level := range model.AccessLevels {
		pl := base
		pl.id, pl.level, pl.ns, pl.roleKind = model.AccessRecipePrefix+string(level), level, target.Namespace, "Role"
		pl.rule = &model.KubeRule{APIGroups: []string{group}, Resources: []string{resource}, ResourceNames: []string{target.Name}, Verbs: slices.Clone(levelVerbs[level])}
		if !newSA {
			pl.extend = p.ownRole(target.Namespace, ns, sa)
		}
		if pl.extend != nil {
			rules, _ := parseRules(pl.extend)
			merged := mergeRule(rules, *pl.rule)
			pl.alreadyAllows = sameItems(ruleKeys, ruleItems(rules), ruleItems(merged))
			pl.roleName = pl.extend.Name
		} else {
			pl.newRole = true
			pl.roleName = p.freeName("Role", target.Namespace, ident.Name+"-"+string(level)+"-"+target.Name)
			pl.bindName = p.freeName("RoleBinding", target.Namespace, pl.roleName)
		}
		out = append(out, pl)
	}
	return out
}

// mergeRule adds r to rules: into a rule on the same resources (its verbs,
// or its resourceNames when the verbs match), else as a new rule.
func mergeRule(rules []model.KubeRule, r model.KubeRule) []model.KubeRule {
	out := slices.Clone(rules)
	same := func(a, b []string) bool {
		return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
	}
	union := func(a, b []string) []string {
		out := slices.Clone(a)
		for _, x := range b {
			if !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
		return out
	}
	for i, x := range out {
		if len(x.NonResourceURLs) > 0 || !same(x.APIGroups, r.APIGroups) || !same(x.Resources, r.Resources) {
			continue
		}
		switch {
		case same(x.ResourceNames, r.ResourceNames):
			out[i].Verbs = union(x.Verbs, r.Verbs)
			return out
		case same(x.Verbs, r.Verbs) && len(x.ResourceNames) > 0:
			out[i].ResourceNames = union(x.ResourceNames, r.ResourceNames)
			return out
		}
	}
	return append(out, r)
}

// ruleText describes a grant rule: `get, list, watch on configmaps "settings"`.
func ruleText(r model.KubeRule) string {
	res := strings.Join(r.Resources, ", ")
	if g := strings.Join(r.APIGroups, ""); g != "" {
		res += " (" + g + ")"
	}
	names := make([]string, len(r.ResourceNames))
	for i, n := range r.ResourceNames {
		names[i] = fmt.Sprintf("%q", n)
	}
	return strings.TrimSpace(fmt.Sprintf("%s on %s %s", strings.Join(r.Verbs, ", "), res, strings.Join(names, ", ")))
}

// recipe is the ConnectRecipe of a plan; its Description is exactly what
// Apply writes.
func (pl grantPlan) recipe() ConnectRecipe {
	rc := ConnectRecipe{ID: pl.id, Kind: RecipeAccess, Level: pl.level, Description: pl.describe()}
	switch pl.id {
	case recipeBind:
		rc.Label = "Run with this role"
	case recipeRole:
		rc.Label = "Give it its own Role"
	default:
		rc.Label = strings.ToUpper(string(pl.level[:1])) + string(pl.level[1:]) // Read, Write, Manage
	}
	return rc
}

func (pl grantPlan) describe() string {
	var steps []string
	if pl.newSA {
		steps = append(steps, fmt.Sprintf("create ServiceAccount %s", pl.saName))
	}
	if pl.runAs {
		steps = append(steps, fmt.Sprintf("run %s's pods as ServiceAccount %s", pl.ident.Name, pl.saName))
	}
	switch {
	case pl.extend != nil && pl.alreadyAllows:
		steps = append(steps, fmt.Sprintf("nothing else: Role %s already allows %s", pl.roleName, ruleText(*pl.rule)))
	case pl.extend != nil:
		steps = append(steps, fmt.Sprintf("add %s to Role %s (already bound to ServiceAccount %s)", ruleText(*pl.rule), pl.roleName, pl.saName))
	case pl.rule != nil:
		steps = append(steps, fmt.Sprintf("create Role %s in %s allowing %s", pl.roleName, pl.ns, ruleText(*pl.rule)))
	case pl.newRole:
		steps = append(steps, fmt.Sprintf("create Role %s in %s with no permissions yet (pick them in its inspector)", pl.roleName, pl.ns))
	}
	if pl.bindName != "" {
		b := fmt.Sprintf("bind %s %s to ServiceAccount %s with RoleBinding %s in %s", pl.roleKind, pl.roleName, pl.saName, pl.bindName, pl.ns)
		if pl.roleKind == "ClusterRole" {
			b += " (its rules apply in " + pl.ns + " only)"
		}
		steps = append(steps, b)
	}
	s := strings.Join(steps, "; ") + "."
	return strings.ToUpper(s[:1]) + s[1:]
}

// accessRecipes lists the grant recipes of src and dst.
func (p *Project) accessRecipes(src, dst *Object) []ConnectRecipe {
	plans := p.grantPlans(src, dst)
	out := make([]ConnectRecipe, len(plans))
	for i, pl := range plans {
		out[i] = pl.recipe()
	}
	return out
}

// applyGrant writes the plan of recipe id; it returns the added objects.
func (e *Editor) applyGrant(src, dst *Object, id string) ([]string, error) {
	p := e.p
	i := slices.IndexFunc(p.grantPlans(src, dst), func(pl grantPlan) bool { return pl.id == id })
	if i < 0 {
		return nil, editErrorf("unknown grant %q for these objects", id)
	}
	pl := p.grantPlans(src, dst)[i]
	if pl.runAs {
		if ok, why := p.rbacEditable(pl.ident, PodSpecPath(pl.ident)+".serviceAccountName"); !ok {
			return nil, editErrorf("%s %s can't run as its own ServiceAccount: %s", pl.ident.Kind, pl.ident.Name, why)
		}
		if err := e.runAs(pl.ident, pl.saName); err != nil {
			return nil, err
		}
	}
	if pl.extend != nil {
		rules, _ := parseRules(pl.extend)
		if err := e.syncList(pl.extend, "rules", ruleKeys, ruleItems(rules), ruleItems(mergeRule(rules, *pl.rule))); err != nil {
			return nil, err
		}
	}
	var docs []string
	if pl.newSA {
		docs = append(docs, serviceAccountYAML(pl.saName, p.nsLine(pl.saNS, pl.ident.Source)))
	}
	if pl.newRole {
		doc := fmt.Sprintf("apiVersion: %s/v1\nkind: Role\nmetadata:\n  name: %s%s\n", rbacGroup, yamlScalar(pl.roleName), p.nsLine(pl.ns, pl.ident.Source))
		if pl.rule != nil {
			doc += "rules:\n  - apiGroups: " + flowList(pl.rule.APIGroups) + "\n    resources: " + flowList(pl.rule.Resources) +
				"\n    resourceNames: " + flowList(pl.rule.ResourceNames) + "\n    verbs: " + flowList(pl.rule.Verbs) + "\n"
		}
		docs = append(docs, doc)
	}
	if pl.bindName != "" {
		docs = append(docs, fmt.Sprintf("apiVersion: %s/v1\nkind: RoleBinding\nmetadata:\n  name: %s%s\nroleRef:\n  apiGroup: %s\n  kind: %s\n  name: %s\nsubjects:\n  - kind: ServiceAccount\n    name: %s\n    namespace: %s\n",
			rbacGroup, yamlScalar(pl.bindName), p.nsLine(pl.ns, pl.ident.Source), rbacGroup, pl.roleKind, yamlScalar(pl.roleName), yamlScalar(pl.saName), yamlScalar(pl.saNS)))
	}
	if len(docs) == 0 {
		return nil, nil
	}
	return e.addFor(pl.ident, strings.Join(docs, "---\n"))
}

// flowList renders strings as a YAML flow list ([get, list]).
func flowList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = yamlScalar(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}
