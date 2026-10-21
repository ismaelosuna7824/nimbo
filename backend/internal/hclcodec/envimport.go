package hclcodec

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// envRootDirs are the directories that hold one root module per environment.
var envRootDirs = []string{EnvironmentsDir, "envs"}

// IsEnvironmentsTree reports whether files (slash paths relative to the
// project) hold environment roots: *.tf files in environments/<env>/ or
// envs/<env>/.
func IsEnvironmentsTree(files map[string][]byte) bool {
	for p := range files {
		parts := strings.Split(path.Clean(strings.ReplaceAll(p, `\`, "/")), "/")
		if len(parts) == 3 && slices.Contains(envRootDirs, parts[0]) && strings.HasSuffix(parts[2], ".tf") {
			return true
		}
	}
	return false
}

// moduleCallMeta are module call arguments that are not module inputs.
var moduleCallMeta = map[string]bool{
	"source": true, "version": true, "providers": true, "count": true, "for_each": true, "depends_on": true,
}

// ImportEnvironments parses a tree in the environments layout (as written
// by ExportEnvironments; keys are slash paths relative to the project) into
// one workflow with Layout model.LayoutEnvironments. Every directory
// environments/<env>/ (or envs/<env>/) with *.tf files is an environment
// root that only calls modules/<service> (the same one for all); the
// service module holds the workflow's blocks. Provider configuration and
// terraform settings come from the first root; import, removed and moved
// blocks of the roots lose their module.<service>. prefix; the state
// backend keeps the workflow-level key (or is dropped when it is the
// cloud's default). Each root's terraform.tfvars and *.auto.tfvars, its
// module call arguments and .env.example give the environment's values and
// environment variables. Local module sources ("../x") point at
// ./modules/x again. Files of the service module other than *.tf, of other
// modules and of the roots (lock file, backend.hcl) are the caller's.
func ImportEnvironments(name string, files map[string][]byte) (*model.Workflow, []model.Diagnostic, error) {
	roots := map[string]map[string][]byte{}
	rootDir := map[string]string{}
	rest := map[string][]byte{}
	for p, data := range files {
		clean := path.Clean(strings.ReplaceAll(p, `\`, "/"))
		parts := strings.Split(clean, "/")
		if len(parts) == 3 && slices.Contains(envRootDirs, parts[0]) {
			if roots[parts[1]] == nil {
				roots[parts[1]] = map[string][]byte{}
			}
			roots[parts[1]][parts[2]] = data
			rootDir[parts[1]] = parts[0] + "/" + parts[1]
			continue
		}
		rest[clean] = data
	}
	var envs []string
	for env, rf := range roots {
		for base := range rf {
			if strings.HasSuffix(base, ".tf") {
				envs = append(envs, env)
				break
			}
		}
	}
	sort.Strings(envs)
	if len(envs) == 0 {
		return nil, nil, fmt.Errorf("%w: no environments/<env>/*.tf root", errNotEnvironments)
	}

	parsed := map[string]parsedRoot{}
	var diags []model.Diagnostic
	svc := ""
	for _, env := range envs {
		tf := map[string][]byte{}
		for base, data := range roots[env] {
			if strings.HasSuffix(base, ".tf") {
				tf[base] = data
			}
		}
		rw, rd, err := Import(env, tf)
		if err != nil {
			return nil, rd, fmt.Errorf("%s: %w", rootDir[env], err)
		}
		for _, d := range rd {
			switch d.Code {
			case "parse_error", "unsupported_file", "invalid_block":
				d.Detail = rootDir[env] + "/" + d.Detail
				diags = append(diags, d)
			}
		}
		var call *model.Node
		for i := range rw.Nodes {
			n := &rw.Nodes[i]
			if n.Kind != model.KindModule || call != nil {
				return nil, diags, fmt.Errorf("%w: %s must only call the service module (found %s)", errNotEnvironments, rootDir[env], Address(n))
			}
			call = n
		}
		if call == nil {
			return nil, diags, fmt.Errorf("%w: %s calls no module", errNotEnvironments, rootDir[env])
		}
		src, ok := constStringExpr(getAttrValue(&call.Body, "source"))
		target := ResolveLocalSource(rootDir[env], src)
		if !ok || !IsLocalModuleSource(src) || path.Dir(target) != ModulesDir {
			return nil, diags, fmt.Errorf("%w: %s calls %q, not a module under modules/", errNotEnvironments, rootDir[env], src)
		}
		if svc == "" {
			svc = path.Base(target)
		} else if path.Base(target) != svc {
			return nil, diags, fmt.Errorf("%w: environment roots call different modules (%s, %s)", errNotEnvironments, svc, path.Base(target))
		}
		parsed[env] = parsedRoot{wf: rw, call: call}
	}

	svcFiles := map[string][]byte{}
	prefix := ModulesDir + "/" + svc + "/"
	for p, data := range rest {
		if base, ok := strings.CutPrefix(p, prefix); ok && !strings.Contains(base, "/") && strings.HasSuffix(base, ".tf") {
			svcFiles[base] = data
		}
	}
	wf, sd, err := Import(name, svcFiles)
	if err != nil {
		return nil, append(diags, sd...), fmt.Errorf("%s: %w", prefix, err)
	}
	diags = append(diags, sd...)

	// Variables and locals only the roots read (provider configuration),
	// and defaults the roots hold for inputs they always pass, come back
	// from the first root.
	for _, rv := range parsed[envs[0]].wf.Variables {
		i := slices.IndexFunc(wf.Variables, func(v model.NamedBlock) bool { return v.Name == rv.Name })
		if i < 0 {
			rv.File, rv.Order = "", 0
			wf.Variables = append(wf.Variables, rv)
			continue
		}
		body := &wf.Variables[i].Body
		if attrExpr(body, "default") != "" {
			continue
		}
		for j, a := range rv.Body.Attrs {
			if a.Name == "default" {
				body.Attrs = slices.Insert(body.Attrs, min(j, len(body.Attrs)), a)
				break
			}
		}
	}
	for _, a := range parsed[envs[0]].wf.Locals {
		if !slices.ContainsFunc(wf.Locals, func(b model.Attr) bool { return b.Name == a.Name }) {
			a.File, a.Order = "", 0
			wf.Locals = append(wf.Locals, a)
		}
	}
	first := parsed[envs[0]].wf
	wf.Providers = make([]model.NamedBlock, 0, len(first.Providers))
	for _, p := range first.Providers {
		p.File, p.Order = "", 0
		wf.Providers = append(wf.Providers, p)
	}
	for _, env := range envs[1:] {
		if !sameProviders(first, parsed[env].wf) {
			diags = append(diags, model.Diagnostic{
				Severity: model.SeverityWarning,
				Code:     "environment_providers_differ",
				Message:  fmt.Sprintf("Provider configuration of %s differs from %s; the workflow keeps the one of %s", rootDir[env], rootDir[envs[0]], rootDir[envs[0]]),
				Detail:   "Every environment root gets the same provider configuration; set what differs per environment with variables.",
			})
		}
	}

	// Terraform settings: the first root's, its state backend generalized.
	wf.Terraform, wf.TerraformBlocks = nil, nil
	var backend *model.Block
	if first.Terraform != nil {
		tf := &model.Body{Attrs: []model.Attr{}, Blocks: []model.Block{}}
		for _, a := range first.Terraform.Attrs {
			a.File, a.Order = "", 0
			tf.Attrs = append(tf.Attrs, a)
		}
		for _, b := range first.Terraform.Blocks {
			b.File, b.Order = "", 0
			if (b.Type == "backend" || b.Type == "cloud") && backend == nil {
				blk := cloneBlock(b)
				backend = &blk
				continue
			}
			tf.Blocks = append(tf.Blocks, b)
		}
		wf.Terraform = tf
	}

	// Root-only and moved blocks return to the workflow without the
	// module prefix.
	for _, b := range first.Other {
		b.File, b.Order = "", 0
		if b.Type == "import" || b.Type == "removed" || b.Type == "moved" {
			for j := range b.Body.Attrs {
				if a := &b.Body.Attrs[j]; a.Name == "to" || a.Name == "from" {
					if expr, err := exprOf(a.Value); err == nil {
						if rest, ok := strings.CutPrefix(strings.TrimSpace(expr), "module."+svc+"."); ok {
							a.Value = model.Value{Expr: rest}
						}
					}
				}
			}
		}
		wf.Other = append(wf.Other, b)
	}

	// Local module calls of the service module point at ./modules again.
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.Kind != model.KindModule {
			continue
		}
		src, ok := constStringExpr(getAttrValue(&n.Body, "source"))
		if !ok || !IsLocalModuleSource(src) {
			continue
		}
		target := ResolveLocalSource(ModulesDir+"/"+svc, src)
		switch {
		case path.Dir(target) == ModulesDir:
			setAttr(&n.Body, "source", stringValue("./"+target), false)
		case strings.HasPrefix(target, prefix):
			setAttr(&n.Body, "source", stringValue("./"+strings.TrimPrefix(target, prefix)), false)
		}
	}

	// Environments.
	wf.Environments = make([]model.Environment, 0, len(envs))
	for _, env := range envs {
		e, d := rootEnvironment(env, roots[env], parsed[env].wf, parsed[env].call)
		for i := range d {
			d[i].Detail = rootDir[env] + "/" + d[i].Detail
		}
		diags = append(diags, d...)
		wf.Environments = append(wf.Environments, e)
	}
	wf.ActiveEnvironment = envs[0]
	if slices.Contains(envs, "dev") {
		wf.ActiveEnvironment = "dev"
	}
	wf.Layout, wf.Service = model.LayoutEnvironments, svc
	wf.Name = name

	if backend != nil {
		diags = append(diags, restoreBackend(wf, backend, envs, parsed, rootDir)...)
	}
	diags = append(diags, Normalize(wf)...)
	return wf, dedupDiags(diags), nil
}

// rootEnvironment reads one environment: variable files, module call
// arguments (var.<name> resolved through them) and .env.example.
func rootEnvironment(env string, files map[string][]byte, root *model.Workflow, call *model.Node) (model.Environment, []model.Diagnostic) {
	e := model.Environment{Name: env, Values: map[string]model.Value{}, EnvVars: []model.EnvVar{}}
	var diags []model.Diagnostic
	var varFiles []string
	for base := range files {
		if base == "terraform.tfvars" || strings.HasSuffix(base, ".auto.tfvars") {
			varFiles = append(varFiles, base)
		}
	}
	// terraform.tfvars first, then *.auto.tfvars in name order, as
	// Terraform loads them (later files win).
	sort.Slice(varFiles, func(i, j int) bool {
		if (varFiles[i] == "terraform.tfvars") != (varFiles[j] == "terraform.tfvars") {
			return varFiles[i] == "terraform.tfvars"
		}
		return varFiles[i] < varFiles[j]
	})
	values := map[string]model.Value{}
	for _, f := range varFiles {
		vf, d := importTfvars(f, files[f])
		diags = append(diags, d...)
		for k, v := range vf.Values {
			values[k] = v
		}
	}
	// Every value of the variable files is the environment's (inputs the
	// root passes on, or reads itself, as provider settings); a module
	// argument other than var.<same name> sets that input directly.
	maps.Copy(e.Values, values)
	for _, a := range call.Body.Attrs {
		if moduleCallMeta[a.Name] {
			continue
		}
		expr, err := exprOf(a.Value)
		if err != nil {
			continue
		}
		if ref, ok := strings.CutPrefix(strings.TrimSpace(expr), "var."); ok && isIdentifier(ref) {
			if v, ok := values[ref]; ok && ref != a.Name {
				e.Values[a.Name] = v
			}
			continue
		}
		e.Values[a.Name] = model.Value{Expr: expr}
	}
	if data, ok := files[".env.example"]; ok {
		vars, d := importEnvExample(".env.example", data)
		diags = append(diags, d...)
		e.EnvVars = vars
	}
	return e, diags
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && (r == '-' || r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// renderBlocks renders top-level blocks (without their comments) as
// formatted HCL, to compare configurations.
func renderBlocks(blocks []model.Block) string {
	w := &writer{}
	for _, b := range blocks {
		body := cloneBody(b.Body)
		body.Comments = nil
		w.block("block", b.Type, b.Labels, &body)
	}
	return string(hclwrite.Format([]byte(w.buf.String())))
}

// sameProviders reports whether two roots configure the same providers.
func sameProviders(a, b *model.Workflow) bool {
	render := func(wf *model.Workflow) string {
		blocks := make([]model.Block, len(wf.Providers))
		for i, p := range wf.Providers {
			blocks[i] = model.Block{Type: "provider", Labels: []string{p.Name}, Body: p.Body}
		}
		return renderBlocks(blocks)
	}
	return render(a) == render(b)
}

// stateHolder is the body of a backend (or cloud) block holding its state
// key, with the key's spec (nil when the type has none).
func stateHolder(b *model.Block) (*model.Body, *stateKey) {
	kind := b.Type
	if b.Type == "backend" && len(b.Labels) == 1 {
		kind = b.Labels[0]
	}
	k, ok := stateKeys[kind]
	if !ok {
		return nil, nil
	}
	if k.block == "" {
		return &b.Body, &k
	}
	for i := range b.Body.Blocks {
		if b.Body.Blocks[i].Type == k.block {
			return &b.Body.Blocks[i].Body, &k
		}
	}
	return nil, nil
}

// rootBackend is the backend or cloud block of a root (nil without one).
func rootBackend(root *model.Workflow) *model.Block {
	if root.Terraform == nil {
		return nil
	}
	for i := range root.Terraform.Blocks {
		if t := root.Terraform.Blocks[i].Type; t == "backend" || t == "cloud" {
			return &root.Terraform.Blocks[i]
		}
	}
	return nil
}

// restoreBackend puts the roots' state backend back into the workflow with
// its workflow-level key (the inverse of backendPlan.forEnv), unless it is
// the default of the workflow's cloud, which export generates anyway.
func restoreBackend(wf *model.Workflow, backend *model.Block, envs []string, parsed map[string]parsedRoot, rootDir map[string]string) []model.Diagnostic {
	var diags []model.Diagnostic
	if holder, k := stateHolder(backend); holder != nil {
		base, consistent := "", true
		for i, env := range envs {
			rb := rootBackend(parsed[env].wf)
			var g string
			ok := rb != nil
			if ok {
				h, _ := stateHolder(rb)
				var v string
				if h != nil {
					v, ok = constStringExpr(getAttrValue(h, k.attr))
				} else {
					ok = false
				}
				if ok {
					g, ok = k.general(v, env)
				}
			}
			if !ok || (i > 0 && g != base) {
				consistent = false
				break
			}
			base = g
		}
		if consistent {
			setAttr(holder, k.attr, stringValue(base), true)
		} else {
			removeAttr(holder, k.attr)
			diags = append(diags, model.Diagnostic{
				Severity: model.SeverityWarning,
				Code:     "environment_state_key",
				Message:  fmt.Sprintf("The %s of the environments' state backends does not follow one pattern; check the state keys before the next init", k.attr),
				Detail: fmt.Sprintf("Export writes %s with the environment name as a path segment (%s); the imported roots (%s, ...) use other values.",
					k.attr, k.perEnv("<"+k.attr+">", "<env>"), rootDir[envs[0]]),
			})
		}
	}
	if def := defaultBackend(primaryCloud(wf), ServiceName(wf)); def != nil {
		d := cloneBlock(def.block)
		setAttr(&d.Body, def.key.attr, stringValue(def.base), true)
		if renderBlocks([]model.Block{*backend}) == renderBlocks([]model.Block{d}) {
			return diags
		}
	}
	if wf.Terraform == nil {
		wf.Terraform = &model.Body{Attrs: []model.Attr{}, Blocks: []model.Block{}}
	}
	wf.Terraform.Blocks = append(wf.Terraform.Blocks, *backend)
	return diags
}

// parsedRoot is one imported environment root and its service module call.
type parsedRoot struct {
	wf   *model.Workflow
	call *model.Node
}
