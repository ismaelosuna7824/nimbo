package lint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func checkVariableType(p *pass) {
	for _, v := range p.wf.Variables {
		if getAttr(&v.Body, "type") == nil {
			p.report(finding{
				message: fmt.Sprintf("Variable %q has no type", v.Name),
				why:     "Without a type Terraform accepts any value, so mistakes surface later as confusing errors inside resources.",
				fix:     fmt.Sprintf("Add type = string (or number, bool, list(string), object({...})) to variable %q.", v.Name),
			})
		}
	}
}

func checkVariableDescription(p *pass) {
	for _, v := range p.wf.Variables {
		if !hasText(&v.Body, "description") {
			p.report(finding{
				message: fmt.Sprintf("Variable %q has no description", v.Name),
				why:     "Descriptions are the documentation of a configuration's inputs: plans, generated docs and the Nimbo forms show them.",
				fix:     fmt.Sprintf("Add description = \"…\" to variable %q saying what it controls and its expected format.", v.Name),
			})
		}
	}
}

func checkOutputDescription(p *pass) {
	for _, o := range p.wf.Outputs {
		if !hasText(&o.Body, "description") {
			p.report(finding{
				message: fmt.Sprintf("Output %q has no description", o.Name),
				why:     "Outputs are the interface other tools and modules consume; a description says what the value is and how to use it.",
				fix:     fmt.Sprintf("Add description = \"…\" to output %q.", o.Name),
			})
		}
	}
}

// secretWords mark a name segment (snake/kebab case) as secret-looking;
// secretPairs are two-segment names.
var (
	secretWords     = map[string]bool{"password": true, "passwd": true, "secret": true, "token": true, "apikey": true, "credential": true, "credentials": true, "privatekey": true, "passphrase": true}
	secretPairs     = map[string]bool{"api_key": true, "private_key": true, "access_key": true, "secret_key": true, "auth_key": true, "connection_string": true, "client_secret": true, "signing_key": true, "master_key": true}
	notSecretSuffix = map[string]bool{"arn": true, "arns": true, "id": true, "ids": true, "name": true, "names": true, "length": true, "ttl": true, "version": true, "versions": true, "enabled": true, "count": true, "days": true, "rotation": true, "type": true, "path": true, "policy": true, "kms": true, "url": true, "uri": true, "endpoint": true, "header": true, "parameter": true, "manager": true, "rule": true, "file": true, "wo": true, "fingerprint": true, "status": true, "expiration": true, "created": true}
	notSecretPrefix = map[string]bool{"create": true, "enable": true, "manage": true, "use": true, "is": true, "has": true, "attach": true, "generate": true, "random": true, "store": true}
	nameSplit       = regexp.MustCompile(`[_\-.]+`)
)

// secretLooking reports whether a variable/output name looks like a secret
// value. Values encrypted for a recipient (PGP-encrypted IAM secrets) are
// safe to print.
func secretLooking(name string) bool {
	parts := nameSplit.Split(strings.ToLower(name), -1)
	if len(parts) == 0 || notSecretSuffix[parts[len(parts)-1]] || notSecretPrefix[parts[0]] || slices.Contains(parts, "encrypted") {
		return false
	}
	for i, part := range parts {
		if secretWords[part] {
			return true
		}
		if i > 0 && secretPairs[parts[i-1]+"_"+part] {
			return true
		}
	}
	return false
}

// isSensitive: sensitive is set to anything but a literal false.
func isSensitive(b *model.Body) bool {
	return getAttr(b, "sensitive") != nil && !hasLiteralFalse(b, "sensitive")
}

// hasText: the attribute is set and not a blank literal string.
func hasText(b *model.Body, name string) bool {
	if getAttr(b, name) == nil {
		return false
	}
	s, isString := attrString(b, name)
	return !isString || strings.TrimSpace(s) != ""
}

func hasLiteralFalse(b *model.Body, name string) bool {
	v, set := attrBool(b, name)
	return set && !v
}

func checkSensitive(p *pass) {
	sensitiveVars := map[string]bool{}
	for _, v := range p.wf.Variables {
		if isSensitive(&v.Body) {
			sensitiveVars[v.Name] = true
			continue
		}
		if !secretLooking(v.Name) || nonSecretType(&v.Body) {
			continue
		}
		p.report(finding{
			message: fmt.Sprintf("Variable %q looks like a secret but is not marked sensitive", v.Name),
			why:     "Terraform prints non-sensitive values in plans and logs, so the secret ends up in CI output and terminal history.",
			fix:     fmt.Sprintf("Add sensitive = true to variable %q (and pass the value through an environment, never a committed default).", v.Name),
		})
	}
	for _, o := range p.wf.Outputs {
		if isSensitive(&o.Body) {
			continue
		}
		reason := ""
		if secretLooking(o.Name) {
			reason = "its name looks like a secret"
		} else if a := getAttr(&o.Body, "value"); a != nil {
			reason = p.sensitiveSource(a.Value, sensitiveVars)
		}
		if reason == "" {
			continue
		}
		p.report(finding{
			message: fmt.Sprintf("Output %q is not marked sensitive but %s", o.Name, reason),
			why:     "Outputs are printed after every apply and stored in plain text by tools that read them; secrets must stay redacted.",
			fix:     fmt.Sprintf("Add sensitive = true to output %q.", o.Name),
		})
	}
}

// nonSecretType: bool/number variables are flags or sizes, not secrets.
func nonSecretType(b *model.Body) bool {
	if a := getAttr(b, "type"); a != nil {
		t := strings.TrimSpace(source(a.Value))
		return t == "bool" || t == "number"
	}
	switch literal(valueOf(b, "default")).(type) {
	case bool, float64:
		return true
	}
	return false
}

func valueOf(b *model.Body, name string) model.Value {
	if a := getAttr(b, name); a != nil {
		return a.Value
	}
	return model.Value{}
}

// sensitiveSource explains why v carries a sensitive value: a sensitive
// variable or a sensitive attribute of a resource.
func (p *pass) sensitiveSource(v model.Value, sensitiveVars map[string]bool) string {
	for _, r := range references(v) {
		parts := strings.Split(r, ".")
		if parts[0] == "var" && len(parts) > 1 && sensitiveVars[parts[1]] {
			return fmt.Sprintf("it returns the sensitive variable %q", parts[1])
		}
		kind, typ, attr := model.KindResource, "", ""
		switch {
		case parts[0] == "data" && len(parts) >= 4:
			kind, typ, attr = model.KindData, parts[1], parts[3]
		case len(parts) >= 3 && strings.Contains(parts[0], "_"):
			typ, attr = parts[0], parts[2]
		default:
			continue
		}
		s, ok := p.cat.Schema(kind, typ)
		if !ok {
			continue
		}
		for _, a := range s.Block.Attributes {
			if a.Name == attr && a.Sensitive {
				return fmt.Sprintf("it returns %s, a sensitive attribute", r)
			}
		}
	}
	return ""
}

func checkRequiredVersion(p *pass) {
	if len(p.wf.Nodes) == 0 && len(p.wf.Providers) == 0 {
		return
	}
	if p.wf.Terraform != nil && getAttr(p.wf.Terraform, "required_version") != nil {
		return
	}
	p.report(finding{
		message: "The terraform block has no required_version",
		why:     "Without it any Terraform release runs the code; a much older or newer one can parse or plan it differently.",
		fix:     "Add required_version = \">= 1.6\" to the terraform block (versions.tf).",
	})
}

// builtinProviders need no required_providers entry.
var builtinProviders = map[string]bool{"terraform": true}

func checkProviderVersion(p *pass) {
	used := map[string]bool{}
	var order []string
	use := func(name string) {
		if name != "" && !builtinProviders[name] && !used[name] {
			used[name] = true
			order = append(order, name)
		}
	}
	for _, pb := range p.wf.Providers {
		use(pb.Name)
	}
	for i := range p.wf.Nodes {
		n := &p.wf.Nodes[i]
		if n.Kind == model.KindModule {
			continue
		}
		name, _, _ := strings.Cut(providerKey(n), ".")
		use(name)
	}
	var rp *model.Body
	if p.wf.Terraform != nil {
		if b := blocks(p.wf.Terraform, "required_providers"); len(b) > 0 {
			rp = &b[0].Body
		}
	}
	for _, name := range order {
		var a *model.Attr
		if rp != nil {
			a = getAttr(rp, name)
		}
		msg := ""
		switch {
		case a == nil:
			msg = fmt.Sprintf("Provider %q is not listed in required_providers", name)
		default:
			switch v := literal(a.Value).(type) {
			case string:
				msg = fmt.Sprintf("Provider %q has no version constraint", name)
			case map[string]any:
				if s, _ := v["version"].(string); strings.TrimSpace(s) == "" {
					msg = fmt.Sprintf("Provider %q has no version constraint", name)
				}
			}
		}
		if msg == "" {
			continue
		}
		fix := fmt.Sprintf("In terraform { required_providers { … } } set %s = { source = \"<namespace>/%s\", version = \"~> X.0\" } with the major version you tested.", name, name)
		if pc, ok := p.cat.ProviderCompat(name); ok {
			fix = fmt.Sprintf("In terraform { required_providers { … } } set %s = { source = %q, version = %q } (the constraint Nimbo writes).", name, pc.Source, pc.Recommended)
		}
		p.report(finding{
			message: msg,
			why:     "An unpinned provider upgrades to the newest release on the next init, and a new major version can change or remove resources.",
			fix:     fix,
		})
	}
}

// checkProviderLocalCompat flags required_providers constraints that miss
// the major of the schema Nimbo validates against, or allow releases older
// than the oldest verified on the local emulators or known to break some of
// the workflow's resources there (catalog.ProviderCompat). Emulator gaps
// that the catalog release itself hits are left to the run warnings: no
// constraint Nimbo recommends avoids them.
func checkProviderLocalCompat(p *pass) {
	types := catalog.ResourceTypes(p.wf)
	for _, rp := range catalog.RequiredProviders(p.wf) {
		if strings.TrimSpace(rp.Constraint) == "" {
			continue // bp_provider_version
		}
		pc, ok := p.cat.CompatFor(rp)
		if !ok {
			continue
		}
		pc = pc.WithoutGaps()
		cc := pc.CheckConstraint(rp.Constraint, types)
		if cc.Invalid || cc.OK() {
			continue
		}
		subject := fmt.Sprintf("%s version %q", pc.Source, rp.Constraint)
		schema := fmt.Sprintf("Nimbo validates attributes and blocks against the %s %s schema", pc.Source, pc.Catalog)
		var message string
		var why []string
		switch {
		case cc.OtherMajor:
			message = fmt.Sprintf("%s can't select a %d.x release, the major Nimbo's schema comes from (%s)", subject, pc.Major(), pc.Catalog)
			if len(cc.Issues) > 0 {
				message += fmt.Sprintf(", and allows releases known to break %s on the local emulators", issueServices(cc.Issues))
			}
			why = append(why, schema+": with another major, resources and attributes it accepts can be renamed, removed or missing, here and on the real cloud.")
		case len(cc.Issues) > 0 && cc.Broken:
			message = fmt.Sprintf("%s only allows releases known to break %s on the local emulators", subject, issueServices(cc.Issues))
		case len(cc.Issues) > 0:
			message = fmt.Sprintf("%s allows releases known to break %s on the local emulators", subject, issueServices(cc.Issues))
		default:
			message = fmt.Sprintf("%s allows releases older than %s, the oldest verified on the local emulators", subject, pc.LocalMinimum)
		}
		for _, is := range cc.Issues {
			why = append(why, is.Message+" ("+is.Versions+").")
		}
		if cc.BelowMinimum && !cc.OtherMajor {
			why = append(why, fmt.Sprintf("Releases before %s are not verified against the local emulators, and %s: an older release can lack attributes Nimbo accepts.", pc.LocalMinimum, schema))
		}
		why = append(why, "A lock file keeps whichever release init selected first, so an allowed old release stays until terraform init -upgrade (the sandbox upgrades its own copy when the lock no longer matches).")
		fix := fmt.Sprintf("Set version = %q for %s in required_providers (the constraint Nimbo writes), then run terraform init -upgrade.", pc.Recommended, rp.Local)
		for i := len(cc.Issues) - 1; i >= 0; i-- {
			if f := cc.Issues[i].Fix; f != "" && !strings.Contains(fix, f) {
				fix = f + ": " + strings.ToLower(fix[:1]) + fix[1:]
			}
		}
		p.report(finding{message: message, why: strings.Join(why, " "), fix: fix})
	}
}

// issueServices names what issues break: their resource types, or "every
// resource of the provider".
func issueServices(issues []catalog.CompatIssue) string {
	var out []string
	for _, is := range issues {
		if len(is.Services) == 0 {
			return "every resource of the provider"
		}
		for _, s := range is.Services {
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return strings.Join(out, ", ")
}
