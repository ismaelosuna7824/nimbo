package kube

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// RBAC editors (GET/PUT /workflows/{id}/kubernetes/access): the rules of a
// Role or ClusterRole, the roleRef and subjects of a binding, a
// ServiceAccount's token automount and a workload's identity, read from
// the rendered objects and written back field by field through the Editor
// (comments and formatting kept). Objects whose RBAC fields don't come
// verbatim from a manifest document (Helm templates, Kustomize generators,
// patches and transformers) are read-only: Editable=false with the reason.

const rbacGroup = "rbac.authorization.k8s.io"

// Builtin ClusterRoles that grant broad rights (user-facing roles of
// https://kubernetes.io/docs/reference/access-authn-authz/rbac/#user-facing-roles).
var broadClusterRoles = map[string]string{
	"cluster-admin": "full control of every object",
	"admin":         "read and write access to most objects, Secrets and RBAC included",
	"edit":          "read and write access to most objects, Secrets included",
}

func isRBAC(o *Object, kinds ...string) bool {
	return apiGroup(o.APIVersion) == rbacGroup && slices.Contains(kinds, o.Kind)
}

func isRole(o *Object) bool    { return isRBAC(o, "Role", "ClusterRole") }
func isBinding(o *Object) bool { return isRBAC(o, "RoleBinding", "ClusterRoleBinding") }
func isServiceAccount(o *Object) bool {
	return o.Kind == "ServiceAccount" && apiGroup(o.APIVersion) == ""
}

// HasRBAC reports whether o has an RBAC view: a Role, ClusterRole,
// binding, ServiceAccount or workload.
func HasRBAC(o *Object) bool {
	return isRole(o) || isBinding(o) || isServiceAccount(o) || isWorkload(o)
}

// workloadSA is the ServiceAccount a workload's pods run as ("default"
// when the pod spec names none).
func workloadSA(o *Object) (name string, explicit bool) {
	spec, _ := podSpec(o)
	if n := firstNonEmpty(str(spec, "serviceAccountName"), str(spec, "serviceAccount")); n != "" {
		return n, true
	}
	return "default", false
}

func boolField(v any) *bool {
	if b, ok := v.(bool); ok {
		return &b
	}
	return nil
}

// ---------------------------------------------------------------------------
// Parsing

var (
	ruleKeys    = []string{"apiGroups", "resources", "resourceNames", "verbs", "nonResourceURLs"}
	subjectKeys = []string{"kind", "name", "namespace", "apiGroup"}
)

// strList reads a list of strings (ok=false when it holds anything else).
func strList(v any) ([]string, bool) {
	if v == nil {
		return nil, true
	}
	l, isList := v.([]any)
	if !isList {
		return nil, false
	}
	out := make([]string, 0, len(l))
	for _, it := range l {
		s, isStr := it.(string)
		if !isStr {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// parseRules reads o's rules; why is set when one can't be represented.
func parseRules(o *Object) (rules []model.KubeRule, why string) {
	rules = []model.KubeRule{}
	raw := dig(o.Obj, "rules")
	if raw != nil && list(raw) == nil {
		return rules, "rules is not a list"
	}
	for i, it := range list(raw) {
		m, ok := it.(map[string]any)
		if !ok {
			return rules, fmt.Sprintf("rule %d is not a mapping", i+1)
		}
		for k := range m {
			if !slices.Contains(ruleKeys, k) {
				return rules, fmt.Sprintf("rule %d has the unknown key %q", i+1, k)
			}
		}
		var r model.KubeRule
		fields := []*[]string{&r.APIGroups, &r.Resources, &r.ResourceNames, &r.Verbs, &r.NonResourceURLs}
		for j, k := range ruleKeys {
			l, ok := strList(m[k])
			if !ok {
				return rules, fmt.Sprintf("rule %d: %s is not a list of strings", i+1, k)
			}
			*fields[j] = l
		}
		r.APIGroups = nonNilStrings(r.APIGroups)
		r.Verbs = nonNilStrings(r.Verbs)
		rules = append(rules, r)
	}
	return rules, ""
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// parseBinding reads a binding's roleRef and subjects (with each subject's
// apiGroup, which the view doesn't show but writes keep).
func parseBinding(o *Object) (ref *model.KubeRoleRef, subjects []model.KubeSubject, groups []string, why string) {
	if r := dig(o.Obj, "roleRef"); r != nil {
		if _, ok := r.(map[string]any); !ok {
			return nil, nil, nil, "roleRef is not a mapping"
		}
		ref = &model.KubeRoleRef{Kind: str(r, "kind"), Name: str(r, "name")}
	}
	raw := dig(o.Obj, "subjects")
	if raw != nil && list(raw) == nil {
		return ref, nil, nil, "subjects is not a list"
	}
	for i, it := range list(raw) {
		m, ok := it.(map[string]any)
		if !ok {
			return ref, nil, nil, fmt.Sprintf("subject %d is not a mapping", i+1)
		}
		for k, v := range m {
			if _, isStr := v.(string); !slices.Contains(subjectKeys, k) || !isStr {
				return ref, nil, nil, fmt.Sprintf("subject %d has the key %q Nimbo can't show", i+1, k)
			}
		}
		subjects = append(subjects, model.KubeSubject{Kind: str(m, "kind"), Name: str(m, "name"), Namespace: str(m, "namespace")})
		groups = append(groups, str(m, "apiGroup"))
	}
	return ref, subjects, groups, ""
}

// ---------------------------------------------------------------------------
// Relations

// bindsServiceAccount reports whether binding b grants its role to the
// ServiceAccount ns/name (directly, or through the ServiceAccount groups
// and users Kubernetes puts it in).
func bindsServiceAccount(b *Object, ns, name string) bool {
	for _, s := range list(dig(b.Obj, "subjects")) {
		switch str(s, "kind") {
		case "ServiceAccount":
			sns := str(s, "namespace")
			if sns == "" && b.Kind == "RoleBinding" {
				sns = b.Namespace
			}
			if str(s, "name") == name && sns == ns {
				return true
			}
		case "Group":
			switch str(s, "name") {
			case "system:serviceaccounts", "system:serviceaccounts:" + ns, "system:authenticated":
				return true
			}
		case "User":
			if str(s, "name") == "system:serviceaccount:"+ns+":"+name {
				return true
			}
		}
	}
	return false
}

// bindingsOf are the bindings granting a role to the ServiceAccount ns/name.
func (p *Project) bindingsOf(ns, name string) []*Object {
	var out []*Object
	for _, b := range p.Objects {
		if isBinding(b) && (b.Kind == "ClusterRoleBinding" || b.Namespace == ns) && bindsServiceAccount(b, ns, name) {
			out = append(out, b)
		}
	}
	return out
}

// roleOf is the Role or ClusterRole a binding refers to (nil when the
// project doesn't declare it: a built-in or cluster role).
func (p *Project) roleOf(b *Object) *Object {
	return p.roleNamed(str(b.Obj, "roleRef", "kind"), b.Namespace, str(b.Obj, "roleRef", "name"))
}

// roleNamed finds a Role in ns or a ClusterRole.
func (p *Project) roleNamed(kind, ns, name string) *Object {
	for _, o := range p.Objects {
		if isRole(o) && o.Kind == kind && o.Name == name && (kind == "ClusterRole" || o.Namespace == ns) {
			return o
		}
	}
	return nil
}

// bindingsTo are the bindings referring to role r.
func (p *Project) bindingsTo(r *Object) []*Object {
	var out []*Object
	for _, b := range p.Objects {
		if isBinding(b) && str(b.Obj, "roleRef", "kind") == r.Kind && str(b.Obj, "roleRef", "name") == r.Name &&
			(r.Kind == "ClusterRole" || b.Kind == "RoleBinding" && b.Namespace == r.Namespace) {
			out = append(out, b)
		}
	}
	return out
}

// serviceAccount finds the ServiceAccount ns/name.
func (p *Project) serviceAccount(ns, name string) *Object {
	for _, o := range p.Objects {
		if isServiceAccount(o) && o.Namespace == ns && o.Name == name {
			return o
		}
	}
	return nil
}

// workloadsOf are the workloads running as the ServiceAccount ns/name.
func (p *Project) workloadsOf(ns, name string) []*Object {
	var out []*Object
	for _, o := range p.Objects {
		if isWorkload(o) && o.Namespace == ns {
			if sa, _ := workloadSA(o); sa == name {
				out = append(out, o)
			}
		}
	}
	return out
}

func related(o *Object, label string) model.AccessRelated {
	return model.AccessRelated{NodeID: o.ID, Address: o.Kind + " " + o.Name, Label: label}
}

// rulesSummary describes rules in a few words ("get, list, watch
// configmaps (settings)").
func rulesSummary(rules []model.KubeRule) string {
	if len(rules) == 0 {
		return "no permissions"
	}
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		what := strings.Join(r.Resources, ", ")
		if len(r.NonResourceURLs) > 0 {
			what = strings.TrimPrefix(what+", "+strings.Join(r.NonResourceURLs, ", "), ", ")
		}
		s := strings.Join(r.Verbs, ", ") + " " + what
		if len(r.ResourceNames) > 0 {
			s += " (" + strings.Join(r.ResourceNames, ", ") + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// grantSummary describes what binding b gives.
func (p *Project) grantSummary(b *Object) string {
	kind, name := str(b.Obj, "roleRef", "kind"), str(b.Obj, "roleRef", "name")
	scope := "in " + b.Namespace
	if b.Kind == "ClusterRoleBinding" {
		scope = "in every namespace"
	}
	if r := p.roleOf(b); r != nil {
		rules, _ := parseRules(r)
		return fmt.Sprintf("%s %s %s: %s", kind, name, scope, rulesSummary(rules))
	}
	if what, ok := broadClusterRoles[name]; ok && kind == "ClusterRole" {
		return fmt.Sprintf("built-in ClusterRole %s %s: %s", name, scope, what)
	}
	return fmt.Sprintf("%s %s %s (not declared in this workflow)", kind, name, scope)
}

// ---------------------------------------------------------------------------
// Editability

// rbacEditable reports whether the fields at paths of o are written
// verbatim in a manifest document Nimbo can edit.
func (p *Project) rbacEditable(o *Object, paths ...string) (bool, string) {
	r := p.Source(o)
	switch {
	case r == nil:
		return false, "the object's source is unknown"
	case r.Source.Kind == model.KubeHelm:
		return false, fmt.Sprintf("rendered by the Helm chart's %s: edit the template (or its values) in code", firstNonEmpty(o.Template, o.File))
	case o.generated:
		return false, "generated by a kustomization generator: edit " + o.File + " in code"
	case o.Doc < 0:
		return false, fmt.Sprintf("can't be located in %s: edit it in code", o.File)
	case strings.HasSuffix(o.File, ".json"):
		return false, "a JSON manifest: edit it in the Code tab"
	case r.Source.Kind != model.KubeKustomize:
		return true, ""
	}
	data, err := p.fs.read(o.File)
	if err != nil {
		return false, o.File + " can't be read"
	}
	doc := docValue(ParseFile(o.File, data), o.Doc)
	for _, fp := range paths {
		segs, err := ParsePath(fp)
		if err != nil {
			continue
		}
		if !sameAt(o.Obj, doc, segs) {
			return false, fmt.Sprintf("the kustomization changes %s (a patch, the namespace, a name prefix or a replacement): edit it in code", fp)
		}
	}
	return true, ""
}

// sameAt reports whether a and b hold the same value at segs.
func sameAt(a, b any, segs []seg) bool {
	val := func(v any) string {
		keys, missing, ok := concrete(v, segs)
		if !ok || missing {
			return ""
		}
		x, found := at(v, keys)
		if !found {
			return ""
		}
		out, _ := json.Marshal(x)
		return string(out)
	}
	return val(a) == val(b)
}

// ---------------------------------------------------------------------------
// View

// RBACView reads o's RBAC (see model.KubeAccessView).
func (p *Project) RBACView(o *Object) (*model.KubeAccessView, error) {
	if !HasRBAC(o) {
		return nil, &EditError{Msg: fmt.Sprintf("%s has no access editor: pick a Role, ClusterRole, RoleBinding, ClusterRoleBinding, ServiceAccount or workload", o.ID), NotFound: true}
	}
	v := &model.KubeAccessView{NodeID: o.ID, Kind: o.Kind, Warnings: []model.AccessWarning{}}
	switch {
	case isRole(o):
		var why string
		v.Rules, why = parseRules(o)
		v.Editable, v.Reason = p.rbacEditable(o, "rules")
		switch {
		case dig(o.Obj, "aggregationRule") != nil:
			v.Editable, v.Reason = false, "an aggregated ClusterRole: the control plane fills its rules from the ClusterRoles its aggregationRule selects; edit those"
		case why != "":
			v.Editable, v.Reason = false, why+": edit it in code"
		}
		for _, b := range p.bindingsTo(o) {
			v.Related = append(v.Related, related(b, "Given by "+b.Kind+" "+b.Name))
		}
		v.Warnings = append(v.Warnings, ruleWarnings(v.Rules)...)
	case isBinding(o):
		var why string
		v.RoleRef, v.Subjects, _, why = parseBinding(o)
		v.Editable, v.Reason = p.rbacEditable(o, "roleRef", "subjects")
		if why != "" {
			v.Editable, v.Reason = false, why+": edit it in code"
		}
		if r := p.roleOf(o); r != nil {
			rules, _ := parseRules(r)
			v.Related = append(v.Related, related(r, "Grants: "+rulesSummary(rules)))
		}
		for _, s := range v.Subjects {
			if s.Kind != "ServiceAccount" {
				continue
			}
			if sa := p.serviceAccount(firstNonEmpty(s.Namespace, o.Namespace), s.Name); sa != nil {
				v.Related = append(v.Related, related(sa, "Subject"))
			}
		}
		v.Warnings = append(v.Warnings, p.bindingWarnings(o, v.RoleRef, v.Subjects)...)
	case isServiceAccount(o):
		v.AutomountToken = boolField(dig(o.Obj, "automountServiceAccountToken"))
		v.Editable, v.Reason = p.rbacEditable(o, "automountServiceAccountToken")
		bindings := p.bindingsOf(o.Namespace, o.Name)
		for _, b := range bindings {
			v.Related = append(v.Related, related(b, p.grantSummary(b)))
		}
		users := p.workloadsOf(o.Namespace, o.Name)
		for _, w := range users {
			v.Related = append(v.Related, related(w, "Runs as it"))
		}
		v.Warnings = append(v.Warnings, p.grantWarnings(bindings, o.Namespace, o.Name)...)
		if len(bindings) == 0 && (v.AutomountToken == nil || *v.AutomountToken) && len(users) > 0 {
			v.Warnings = append(v.Warnings, model.AccessWarning{
				Code: "k8s_token_automount", Severity: "info",
				Message: "Pods running as " + o.Name + " get a Kubernetes API token mounted, but it has no permissions.",
				Fix:     "Turn off “Mount API token” (automountServiceAccountToken: false) unless its pods call the API.",
			})
		}
	default: // workload
		base := PodSpecPath(o)
		spec, _ := podSpec(o)
		sa, _ := workloadSA(o)
		v.ServiceAccount = sa
		v.AutomountToken = boolField(spec["automountServiceAccountToken"])
		v.Editable, v.Reason = p.rbacEditable(o, base+".serviceAccountName", base+".serviceAccount", base+".automountServiceAccountToken")
		saObj := p.serviceAccount(o.Namespace, sa)
		if saObj != nil {
			v.Related = append(v.Related, related(saObj, "Runs as this ServiceAccount"))
		}
		bindings := p.bindingsOf(o.Namespace, sa)
		for _, b := range bindings {
			v.Related = append(v.Related, related(b, p.grantSummary(b)))
			if r := p.roleOf(b); r != nil && !slices.ContainsFunc(v.Related, func(x model.AccessRelated) bool { return x.NodeID == r.ID }) {
				rules, _ := parseRules(r)
				v.Related = append(v.Related, related(r, "Can: "+rulesSummary(rules)))
			}
		}
		v.Warnings = append(v.Warnings, p.grantWarnings(bindings, o.Namespace, sa)...)
		if sa == "default" && len(bindings) > 0 {
			v.Warnings = append(v.Warnings, model.AccessWarning{
				Code: "k8s_default_sa", Severity: "warning",
				Message: "Runs as the namespace's default ServiceAccount, which has grants: every pod without its own ServiceAccount gets them too.",
				Fix:     "Give it its own ServiceAccount (Identity) and grant the permissions there.",
			})
		}
		mounted := v.AutomountToken == nil || *v.AutomountToken
		if v.AutomountToken == nil && saObj != nil {
			if b := boolField(dig(saObj.Obj, "automountServiceAccountToken")); b != nil {
				mounted = *b
			}
		}
		if mounted && len(bindings) == 0 {
			v.Warnings = append(v.Warnings, model.AccessWarning{
				Code: "k8s_token_automount", Severity: "info",
				Message: "Its pods get a Kubernetes API token mounted, but ServiceAccount " + sa + " has no permissions.",
				Fix:     "Turn off “Mount API token” (automountServiceAccountToken: false) unless it calls the API.",
			})
		}
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Warnings

// ruleWarnings flags wildcards and Secret reads (path rules/<i>).
func ruleWarnings(rules []model.KubeRule) []model.AccessWarning {
	var out []model.AccessWarning
	for i, r := range rules {
		path := fmt.Sprintf("rules/%d", i)
		var wild []string
		for _, f := range []struct {
			label string
			vals  []string
		}{{"verbs", r.Verbs}, {"resources", r.Resources}, {"API groups", r.APIGroups}, {"URLs", r.NonResourceURLs}} {
			if slices.ContainsFunc(f.vals, func(s string) bool { return s == "*" || strings.HasSuffix(s, "/*") }) {
				wild = append(wild, f.label)
			}
		}
		if len(wild) > 0 {
			out = append(out, model.AccessWarning{
				Code: "k8s_wildcard", Severity: "warning", Path: path,
				Message: "This rule uses * in its " + strings.Join(wild, " and ") + ": it also covers everything added later (new resources, custom resources, subresources).",
				Fix:     "List the exact verbs and resources it needs.",
			})
		}
		core := slices.Contains(r.APIGroups, "") || slices.Contains(r.APIGroups, "*")
		secrets := slices.Contains(r.Resources, "secrets") || slices.Contains(r.Resources, "*")
		reads := slices.ContainsFunc(r.Verbs, func(v string) bool { return v == "list" || v == "watch" || v == "*" })
		if core && secrets && reads {
			out = append(out, model.AccessWarning{
				Code: "k8s_secrets_access", Severity: "warning", Path: path,
				Message: "This rule can list or watch Secrets: that returns the contents of every Secret it covers.",
				Fix:     "Grant get on the Secrets it needs by name (resourceNames), or mount them in its pods instead.",
			})
		}
	}
	return out
}

// bindingWarnings flags broad built-in roles (path roleRef) and
// cluster-wide grants to ServiceAccounts (path subjects/<i>).
func (p *Project) bindingWarnings(b *Object, ref *model.KubeRoleRef, subjects []model.KubeSubject) []model.AccessWarning {
	var out []model.AccessWarning
	if ref != nil && ref.Kind == "ClusterRole" {
		if what, ok := broadClusterRoles[ref.Name]; ok {
			scope := "in namespace " + b.Namespace
			if b.Kind == "ClusterRoleBinding" {
				scope = "in every namespace"
			}
			out = append(out, model.AccessWarning{
				Code: "k8s_cluster_admin", Severity: "warning", Path: "roleRef",
				Message: fmt.Sprintf("Grants the built-in ClusterRole %s (%s) %s.", ref.Name, what, scope),
				Fix:     "Bind a Role listing only the verbs and resources it needs.",
			})
		}
	}
	if b.Kind == "ClusterRoleBinding" {
		role := "its role"
		if ref != nil {
			role = ref.Kind + " " + ref.Name
		}
		for i, s := range subjects {
			if s.Kind != "ServiceAccount" {
				continue
			}
			who := s.Name
			if users := p.workloadsOf(s.Namespace, s.Name); len(users) > 0 {
				names := make([]string, len(users))
				for j, w := range users {
					names[j] = w.Name
				}
				who += " (used by " + strings.Join(names, ", ") + ")"
			}
			out = append(out, model.AccessWarning{
				Code: "k8s_cluster_scope", Severity: "warning", Path: fmt.Sprintf("subjects/%d", i),
				Message: fmt.Sprintf("A ClusterRoleBinding grants %s to ServiceAccount %s in every namespace.", role, who),
				Fix:     "Use a RoleBinding in the namespace it works in (a RoleBinding can also refer to a ClusterRole).",
			})
		}
	}
	return out
}

// grantWarnings are the binding warnings of what a ServiceAccount is given
// (whole-node warnings of a ServiceAccount or workload).
func (p *Project) grantWarnings(bindings []*Object, ns, name string) []model.AccessWarning {
	var out []model.AccessWarning
	for _, b := range bindings {
		ref, subjects, _, _ := parseBinding(b)
		for _, w := range p.bindingWarnings(b, ref, subjects) {
			if w.Code == "k8s_cluster_scope" {
				i := 0
				fmt.Sscanf(w.Path, "subjects/%d", &i)
				if i >= len(subjects) || subjects[i].Name != name || subjects[i].Namespace != ns {
					continue
				}
			}
			w.Path = ""
			w.Message = b.Kind + " " + b.Name + ": " + w.Message
			out = append(out, w)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Edit

var dnsSubdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)

// ApplyRBAC writes an RBAC edit of o (nil fields unchanged).
func (e *Editor) ApplyRBAC(o *Object, edit model.KubeAccessEdit) error {
	p := e.p
	v, err := p.RBACView(o)
	if err != nil {
		return err
	}
	refuse := func(field string, set bool) error {
		if set {
			return editErrorf("%s %s has no %s to edit", o.Kind, o.Name, field)
		}
		return nil
	}
	switch {
	case isRole(o):
		if err := errorsJoin(refuse("roleRef", edit.RoleRef != nil), refuse("subjects", edit.Subjects != nil),
			refuse("ServiceAccount", edit.ServiceAccount != nil), refuse("token automount", edit.AutomountToken != nil)); err != nil {
			return err
		}
	case isBinding(o):
		if err := errorsJoin(refuse("rules", edit.Rules != nil), refuse("ServiceAccount", edit.ServiceAccount != nil), refuse("token automount", edit.AutomountToken != nil)); err != nil {
			return err
		}
	case isServiceAccount(o):
		if err := errorsJoin(refuse("rules", edit.Rules != nil), refuse("roleRef", edit.RoleRef != nil), refuse("subjects", edit.Subjects != nil), refuse("ServiceAccount", edit.ServiceAccount != nil)); err != nil {
			return err
		}
	default:
		if err := errorsJoin(refuse("rules", edit.Rules != nil), refuse("roleRef", edit.RoleRef != nil), refuse("subjects", edit.Subjects != nil)); err != nil {
			return err
		}
	}
	if !v.Editable {
		return editErrorf("%s %s can't be edited here: %s", o.Kind, o.Name, v.Reason)
	}
	if edit.Rules != nil {
		rules, err := cleanRules(o.Kind, edit.Rules, v.Rules)
		if err != nil {
			return err
		}
		return e.syncList(o, "rules", ruleKeys, ruleItems(v.Rules), ruleItems(rules))
	}
	if edit.RoleRef != nil {
		ref := model.KubeRoleRef{Kind: strings.TrimSpace(edit.RoleRef.Kind), Name: strings.TrimSpace(edit.RoleRef.Name)}
		switch {
		case ref.Kind != "Role" && ref.Kind != "ClusterRole":
			return editErrorf("roleRef.kind must be Role or ClusterRole")
		case ref.Kind == "Role" && o.Kind == "ClusterRoleBinding":
			return editErrorf("a ClusterRoleBinding can only refer to a ClusterRole")
		case !dnsSubdomain.MatchString(ref.Name) && !strings.Contains(ref.Name, ":"):
			return editErrorf("%q is not a role name", ref.Name)
		}
		if v.RoleRef == nil || *v.RoleRef != ref || str(o.Obj, "roleRef", "apiGroup") != rbacGroup {
			for _, f := range [][2]string{{"apiGroup", rbacGroup}, {"kind", ref.Kind}, {"name", ref.Name}} {
				if err := e.Set(o, "roleRef."+f[0], f[1]); err != nil {
					return err
				}
			}
		}
	}
	if edit.Subjects != nil {
		_, _, groups, _ := parseBinding(o)
		old := make([]map[string]any, len(v.Subjects))
		for i, s := range v.Subjects {
			old[i] = subjectItem(s, groups[i])
		}
		want := make([]map[string]any, 0, len(edit.Subjects))
		for i, s := range edit.Subjects {
			s = model.KubeSubject{Kind: strings.TrimSpace(s.Kind), Name: strings.TrimSpace(s.Name), Namespace: strings.TrimSpace(s.Namespace)}
			switch {
			case s.Kind != "ServiceAccount" && s.Kind != "User" && s.Kind != "Group":
				return editErrorf("subject %d: kind must be ServiceAccount, User or Group", i+1)
			case s.Name == "":
				return editErrorf("subject %d: a name is required", i+1)
			case s.Kind == "ServiceAccount" && s.Namespace == "" && o.Kind == "ClusterRoleBinding":
				return editErrorf("subject %d: a ServiceAccount of a ClusterRoleBinding needs its namespace", i+1)
			case s.Kind != "ServiceAccount" && s.Namespace != "":
				return editErrorf("subject %d: only ServiceAccounts have a namespace", i+1)
			}
			group := ""
			if s.Kind != "ServiceAccount" {
				group = rbacGroup
			}
			want = append(want, subjectItem(s, group))
		}
		if err := e.syncList(o, "subjects", subjectKeys, old, want); err != nil {
			return err
		}
	}
	if edit.AutomountToken != nil {
		path := "automountServiceAccountToken"
		if !isServiceAccount(o) {
			path = PodSpecPath(o) + "." + path
		}
		if v.AutomountToken == nil || *v.AutomountToken != *edit.AutomountToken {
			if err := e.Set(o, path, *edit.AutomountToken); err != nil {
				return err
			}
		}
	}
	if edit.ServiceAccount != nil {
		return e.setServiceAccount(o, strings.TrimSpace(*edit.ServiceAccount))
	}
	return nil
}

func errorsJoin(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// setServiceAccount makes workload o run as the ServiceAccount name ("" or
// "default": the namespace's default), creating it when the project has
// none of that name.
func (e *Editor) setServiceAccount(o *Object, name string) error {
	base := PodSpecPath(o)
	if name == "" || name == "default" {
		for _, f := range []string{"serviceAccountName", "serviceAccount"} {
			if err := e.Unset(o, base+"."+f); err != nil {
				return err
			}
		}
		return nil
	}
	if !dnsSubdomain.MatchString(name) {
		return editErrorf("%q is not a ServiceAccount name (lowercase letters, digits, “-” and “.”)", name)
	}
	if err := e.runAs(o, name); err != nil {
		return err
	}
	if e.p.serviceAccount(o.Namespace, name) == nil {
		_, err := e.addFor(o, serviceAccountYAML(name, e.p.nsLine(o.Namespace, o.Source)))
		return err
	}
	return nil
}

// runAs sets the serviceAccountName of workload o's pods (dropping the
// deprecated serviceAccount field).
func (e *Editor) runAs(o *Object, name string) error {
	base := PodSpecPath(o)
	spec, _ := podSpec(o)
	if str(spec, "serviceAccountName") != name {
		if err := e.Set(o, base+".serviceAccountName", name); err != nil {
			return err
		}
	}
	if spec["serviceAccount"] != nil {
		return e.Unset(o, base+".serviceAccount")
	}
	return nil
}

// nsLine is the metadata.namespace line of a new object of namespace ns in
// source si ("" when the source's default namespace applies).
func (p *Project) nsLine(ns string, si int) string {
	def := "default"
	if r := p.sourceAt(si); r != nil {
		def = firstNonEmpty(r.Source.Namespace, "default")
	}
	if ns == "" || ns == def {
		return ""
	}
	return "\n  namespace: " + yamlScalar(ns)
}

func serviceAccountYAML(name, nsLine string) string {
	return fmt.Sprintf("apiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: %s%s\n", yamlScalar(name), nsLine)
}

// cleanRules trims and checks an edit's rules; rules identical to one of
// old (untouched) are kept as written.
func cleanRules(kind string, in, old []model.KubeRule) ([]model.KubeRule, error) {
	oldItems := ruleItems(old)
	clean := func(s []string, keepEmpty bool) []string {
		out := []string{}
		for _, x := range s {
			x = strings.TrimSpace(x)
			if (x != "" || keepEmpty) && !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
		return out
	}
	out := make([]model.KubeRule, 0, len(in))
	for i, r := range in {
		c := model.KubeRule{
			APIGroups: clean(r.APIGroups, true), Resources: clean(r.Resources, false), ResourceNames: clean(r.ResourceNames, false),
			Verbs: clean(r.Verbs, false), NonResourceURLs: clean(r.NonResourceURLs, false),
		}
		item := ruleItems([]model.KubeRule{c})[0]
		if slices.ContainsFunc(oldItems, func(o map[string]any) bool { return sameItem(ruleKeys, o, item) }) {
			out = append(out, c)
			continue
		}
		switch {
		case len(c.Verbs) == 0:
			return nil, editErrorf("rule %d: pick at least one verb", i+1)
		case len(c.Resources) == 0 && len(c.NonResourceURLs) == 0:
			return nil, editErrorf("rule %d: pick at least one resource", i+1)
		case len(c.NonResourceURLs) > 0 && kind != "ClusterRole":
			return nil, editErrorf("rule %d: only a ClusterRole can grant URLs (nonResourceURLs)", i+1)
		case len(c.NonResourceURLs) > 0 && (len(c.Resources) > 0 || len(c.ResourceNames) > 0):
			return nil, editErrorf("rule %d: a rule grants either resources or URLs, not both", i+1)
		case len(c.Resources) > 0 && len(c.APIGroups) == 0:
			return nil, editErrorf("rule %d: pick an API group (\"\" is the core group of ConfigMaps, Secrets, Pods…)", i+1)
		case len(c.ResourceNames) > 0 && slices.ContainsFunc(c.Verbs, func(v string) bool { return v == "create" || v == "deletecollection" }):
			return nil, editErrorf("rule %d: create and deletecollection can't be limited to resourceNames: put them in a rule without names", i+1)
		}
		out = append(out, c)
	}
	return out, nil
}

func ruleItems(rules []model.KubeRule) []map[string]any {
	out := make([]map[string]any, len(rules))
	for i, r := range rules {
		out[i] = map[string]any{"apiGroups": r.APIGroups, "resources": r.Resources, "resourceNames": r.ResourceNames, "verbs": r.Verbs, "nonResourceURLs": r.NonResourceURLs}
	}
	return out
}

func subjectItem(s model.KubeSubject, group string) map[string]any {
	return map[string]any{"kind": s.Kind, "name": s.Name, "namespace": s.Namespace, "apiGroup": group}
}

// emptyField reports a field an item doesn't write.
func emptyField(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []string:
		return len(t) == 0
	}
	return false
}

func sameField(a, b any) bool {
	if emptyField(a) || emptyField(b) {
		return emptyField(a) == emptyField(b)
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func sameItems(keys []string, a, b []map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameItem(keys, a[i], b[i]) {
			return false
		}
	}
	return true
}

func sameItem(keys []string, a, b map[string]any) bool {
	for _, k := range keys {
		if !sameField(a[k], b[k]) {
			return false
		}
	}
	return true
}

// itemNode renders a new list item: its fields in keys order, lists of
// strings in flow style ([get, list]).
func itemNode(keys []string, it map[string]any) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range keys {
		v := it[k]
		if emptyField(v) {
			continue
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, valueNode(v))
	}
	return m
}

func valueNode(v any) *yaml.Node {
	if l, ok := v.([]string); ok {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, s := range l {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s})
		}
		return seq
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
}

func fieldValue(v any) any {
	if l, ok := v.([]string); ok {
		out := make([]any, len(l))
		for i, s := range l {
			out[i] = s
		}
		return out
	}
	return v
}

// syncList rewrites the list at path of o from old to want, touching only
// what changed: unchanged items keep their bytes, changed ones are edited
// field by field, removed ones deleted and new ones appended.
func (e *Editor) syncList(o *Object, path string, keys []string, old, want []map[string]any) error {
	if sameItems(keys, old, want) {
		return nil
	}
	if len(old) == 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for _, it := range want {
			seq.Content = append(seq.Content, itemNode(keys, it))
		}
		return e.Set(o, path, seq)
	}
	pairs, drop, add := listPlan(keys, old, want)
	for _, pr := range pairs {
		i, j := pr[0], pr[1]
		for _, k := range keys {
			if sameField(old[i][k], want[j][k]) {
				continue
			}
			fp := fmt.Sprintf("%s[%d].%s", path, i, k)
			var err error
			if emptyField(want[j][k]) {
				err = e.Unset(o, fp)
			} else {
				err = e.Set(o, fp, fieldValue(want[j][k]))
			}
			if err != nil {
				return err
			}
		}
	}
	for x := len(drop) - 1; x >= 0; x-- {
		if err := e.Unset(o, fmt.Sprintf("%s[%d]", path, drop[x])); err != nil {
			return err
		}
	}
	for _, j := range add {
		if err := e.Append(o, path, itemNode(keys, want[j])); err != nil {
			return err
		}
	}
	return nil
}

// listPlan matches old items to wanted ones: pairs (old index, want index)
// edited in place (identical ones untouched), old items to delete (in
// order) and wanted items to append. Items stay where a longest common
// subsequence keeps them; new items can only be appended, so an insertion
// before a kept item falls back to editing the items in place.
func listPlan(keys []string, old, want []map[string]any) (pairs [][2]int, drop, add []int) {
	anchors := append(lcsPairs(keys, old, want), [2]int{len(old), len(want)})
	pi, pj := 0, 0
	for k, a := range anchors {
		last := k == len(anchors)-1
		no, nw := a[0]-pi, a[1]-pj
		n := min(no, nw)
		for x := range n {
			pairs = append(pairs, [2]int{pi + x, pj + x})
		}
		for x := n; x < no; x++ {
			drop = append(drop, pi+x)
		}
		if nw > n {
			if !last {
				return positionalPlan(len(old), len(want))
			}
			for x := n; x < nw; x++ {
				add = append(add, pj+x)
			}
		}
		if !last {
			pairs = append(pairs, a)
		}
		pi, pj = a[0]+1, a[1]+1
	}
	return pairs, drop, add
}

// positionalPlan edits item i into wanted item i.
func positionalPlan(nOld, nWant int) (pairs [][2]int, drop, add []int) {
	for i := range min(nOld, nWant) {
		pairs = append(pairs, [2]int{i, i})
	}
	for i := nWant; i < nOld; i++ {
		drop = append(drop, i)
	}
	for j := nOld; j < nWant; j++ {
		add = append(add, j)
	}
	return pairs, drop, add
}

// lcsPairs is a longest common subsequence of old and want (index pairs).
func lcsPairs(keys []string, old, want []map[string]any) [][2]int {
	n, m := len(old), len(want)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if sameItem(keys, old[i], want[j]) {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var out [][2]int
	for i, j := 0, 0; i < n && j < m; {
		switch {
		case sameItem(keys, old[i], want[j]):
			out = append(out, [2]int{i, j})
			i, j = i+1, j+1
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return out
}
