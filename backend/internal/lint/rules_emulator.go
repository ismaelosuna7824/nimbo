package lint

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// emulatorEnvTypes are the compute resources whose environment reaches app
// code running in a container or function.
var emulatorEnvTypes = []string{
	"google_cloud_run_v2_service", "google_cloud_run_v2_job",
	"google_cloudfunctions2_function",
	"aws_lambda_function", "aws_ecs_task_definition",
	"azurerm_container_app", "azurerm_linux_function_app",
}

var emulatorHostVars = []string{
	"PUBSUB_EMULATOR_HOST", "STORAGE_EMULATOR_HOST", "FIRESTORE_EMULATOR_HOST",
	"DATASTORE_EMULATOR_HOST", "SPANNER_EMULATOR_HOST", "BIGTABLE_EMULATOR_HOST",
}

func isEmulatorVar(name string) bool {
	return slices.Contains(emulatorHostVars, name) || strings.HasPrefix(name, "AWS_ENDPOINT_URL")
}

// loopbackHost reports whether addr ("localhost:8085", "http://[::1]:4566",
// "s3.localhost.floci.io") points at the local host.
func loopbackHost(addr string) bool {
	h := strings.ToLower(strings.TrimSpace(addr))
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if h == "localhost" || h == "localhost.floci.io" || strings.HasSuffix(h, ".localhost.floci.io") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// emulatorHit is one loopback emulator address: the variable and its value.
type emulatorHit struct{ key, value string }

// emulatorHits finds loopback emulator addresses in v: map entries
// (KEY = "addr"), name/value objects ({ name = "KEY", value = "addr" }) and
// the same inside JSON strings (ECS container_definitions heredocs).
func emulatorHits(v model.Value) []emulatorHit {
	e := parse(v)
	if e == nil {
		return nil
	}
	var out []emulatorHit
	hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		switch x := n.(type) {
		case *hclsyntax.ObjectConsExpr:
			fields := map[string]string{}
			for _, item := range x.Items {
				k, ok := constString(item.KeyExpr)
				if !ok {
					continue
				}
				val, ok := constString(item.ValueExpr)
				if !ok {
					continue
				}
				fields[k] = val
				if isEmulatorVar(k) && loopbackHost(val) {
					out = append(out, emulatorHit{k, val})
				}
			}
			if isEmulatorVar(fields["name"]) && loopbackHost(fields["value"]) {
				out = append(out, emulatorHit{fields["name"], fields["value"]})
			}
		case *hclsyntax.TemplateExpr:
			if s, ok := constString(x); ok {
				out = append(out, jsonEmulatorHits(s)...)
			}
		}
		return nil
	})
	return out
}

func constString(e hclsyntax.Expression) (string, bool) {
	if len(e.Variables()) > 0 {
		return "", false
	}
	v, diags := e.Value(nil)
	if diags.HasErrors() || !v.IsKnown() || v.IsNull() || v.Type() != cty.String {
		return "", false
	}
	return v.AsString(), true
}

// jsonEmulatorHits scans a JSON document string.
func jsonEmulatorHits(s string) []emulatorHit {
	s = strings.TrimSpace(s)
	if s == "" || s[0] != '[' && s[0] != '{' {
		return nil
	}
	var doc any
	if json.Unmarshal([]byte(s), &doc) != nil {
		return nil
	}
	var out []emulatorHit
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case []any:
			for _, item := range t {
				walk(item)
			}
		case map[string]any:
			name, _ := t["name"].(string)
			value, _ := t["value"].(string)
			if isEmulatorVar(name) && loopbackHost(value) {
				out = append(out, emulatorHit{name, value})
			}
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if s, ok := t[k].(string); ok && isEmulatorVar(k) && loopbackHost(s) {
					out = append(out, emulatorHit{k, s})
				}
				walk(t[k])
			}
		}
	}
	walk(doc)
	return out
}

// bodyEmulatorHits scans every attribute of b and its nested blocks; blocks
// such as Cloud Run's env { name value } are seen as name/value pairs.
func bodyEmulatorHits(b *model.Body, prefix string, fn func(path string, h emulatorHit)) {
	name, _ := attrString(b, "name")
	if value, ok := attrString(b, "value"); ok && isEmulatorVar(name) && loopbackHost(value) {
		fn(strings.TrimSuffix(prefix, "."), emulatorHit{name, value})
	}
	for i := range b.Attrs {
		for _, h := range emulatorHits(b.Attrs[i].Value) {
			fn(prefix+b.Attrs[i].Name, h)
		}
	}
	idx := map[string]int{}
	for i := range b.Blocks {
		bl := &b.Blocks[i]
		switch bl.Type {
		case "lifecycle", "provisioner", "connection":
			continue
		}
		bodyEmulatorHits(&bl.Body, fmt.Sprintf("%s%s[%d].", prefix, bl.Type, idx[bl.Type]), fn)
		idx[bl.Type]++
	}
}

const emulatorWhy = "Inside a container or function, localhost is the container itself, not the machine running the emulator, so the app's client can't reach it and publishing or reads hang until they time out."

func emulatorFix(key string) string {
	if strings.HasPrefix(key, "AWS_ENDPOINT_URL") {
		return "Remove " + key + ": Floci injects AWS_ENDPOINT_URL into Lambda and container runtimes itself, and in real AWS the SDK must use the default endpoints."
	}
	return `Declare variable "gcp_emulator_host" { type = string, default = "" } and pass PUBSUB_EMULATOR_HOST/STORAGE_EMULATOR_HOST only when it is set (e.g. merge(var.gcp_emulator_host == "" ? {} : { PUBSUB_EMULATOR_HOST = var.gcp_emulator_host }, ...) or a dynamic "env" block). Nimbo fills it in the sandbox (TF_VAR_gcp_emulator_host = nimbo-<workflow>-pod:4588), so exports stay clean for real clouds.`
}

func checkEmulatorHostLoopback(p *pass) {
	for _, n := range p.ofType(emulatorEnvTypes...) {
		bodyEmulatorHits(&n.Body, "", func(path string, h emulatorHit) {
			p.report(finding{
				node: n, attribute: path,
				message: fmt.Sprintf("%s sets %s to the loopback address %q in %s", addr(n), h.key, h.value, path),
				why:     emulatorWhy,
				fix:     emulatorFix(h.key),
			})
		})
	}
	for _, env := range p.wf.Environments {
		names := make([]string, 0, len(env.Values))
		for name := range env.Values {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			for _, h := range emulatorHits(env.Values[name]) {
				p.report(finding{
					message: fmt.Sprintf("Environment %q sets %s to the loopback address %q in variable %q", env.Name, h.key, h.value, name),
					why:     emulatorWhy,
					fix:     emulatorFix(h.key),
				})
			}
		}
	}
}
