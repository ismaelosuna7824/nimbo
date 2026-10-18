// Package endpoints explains how to use what is deployed in a workflow's
// sandbox: for every resource a plain-language summary, host-reachable URLs
// and identifiers, snippets that run as-is against the sandbox, "Try it"
// actions executed by the daemon, and a Postman collection.
package endpoints

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// Resource kinds (model.ResourceAccess.Kind).
const (
	KindHTTPAPI      = "http-api"
	KindFunctionURL  = "function-url"
	KindFunction     = "function"
	KindQueue        = "queue"
	KindTopic        = "topic"
	KindBucket       = "bucket"
	KindTable        = "table"
	KindStream       = "stream"
	KindEventBus     = "event-bus"
	KindStateMachine = "state-machine"
	KindSecret       = "secret"
	KindParameter    = "parameter"
	KindUserPool     = "user-pool"
	KindDatabase     = "database"
	KindCache        = "cache"
	KindWebService   = "web-service" // URL-serving compute: Cloud Run, Container Apps, Function Apps
	KindKey          = "key"         // KMS/Vault encryption keys
	KindKubernetes   = "kubernetes"  // Kubernetes clusters
	KindGeneric      = "generic"
)

// Try action IDs.
const (
	ActRequest = "request"
	ActInvoke  = "invoke"
	ActSend    = "send"
	ActReceive = "receive"
	ActPublish = "publish"
	ActList    = "list"
	ActPut     = "put"
	ActGet     = "get"
	ActScan    = "scan"
	ActStart   = "start"
	ActEncrypt = "encrypt"
	ActDecrypt = "decrypt"
)

const (
	emulatorAccount = "000000000000"
	defaultRegion   = "us-east-1"
	helloMessage    = `{"message":"Hello from Nimbo"}`
	sampleJSONBody  = `{"name":"example"}`
)

// Sandbox is what Build needs to know about the workflow's sandbox.
type Sandbox struct {
	// Bases maps clouds to host base URLs ("http://127.0.0.1:45000"): live
	// ports while the sandbox runs, else the fixed ports it will reuse.
	Bases map[model.Cloud]string
	// Running marks emulators that are up and answering.
	Running map[model.Cloud]bool
	// Region is the sandbox's default AWS region (settings).
	Region string
	// Sources are texts of the workflow's code assets (Lambda sources),
	// scanned to pick a sample path for catch-all API routes.
	Sources []string
}

// Build describes how to reach the sandbox and every deployed resource.
func Build(wf *model.Workflow, st *model.DeployedState, sb Sandbox) model.EndpointsInfo {
	b := newBuilder(wf, st, sb)
	info := model.EndpointsInfo{
		WorkflowID: b.wfID,
		Sandbox:    b.sandboxAccess(),
		Resources:  make([]model.ResourceAccess, 0, len(b.entries)),
	}
	if st != nil {
		info.UpdatedAt = st.UpdatedAt
	}
	for _, e := range b.entries {
		info.Resources = append(info.Resources, e.ResourceAccess)
	}
	return info
}

// entry is one ResourceAccess plus what the Try executor needs.
type entry struct {
	model.ResourceAccess
	t target
}

type target struct {
	cloud    model.Cloud
	region   string
	name     string // function, queue, bucket, table, stream, bus, secret, parameter
	arn      string
	url      string // SQS queue URL (host form); Pub/Sub topic path
	httpBase string // base of relative paths in HTTP requests
	fifo     bool
	dedup    bool   // FIFO content-based deduplication
	item     string // DynamoDB example item in typed JSON
	parts    int    // OCI stream partitions
}

type resource = model.DeployedResource

type builder struct {
	wf      *model.Workflow
	wfID    string
	sb      Sandbox
	aws     string
	gcp     string
	azure   string
	oci     string
	region  string
	byType  map[string][]*resource
	ordered []*resource
	done    map[*resource]bool
	entries []entry
	sample  string // sample segment for catch-all routes
}

func newBuilder(wf *model.Workflow, st *model.DeployedState, sb Sandbox) *builder {
	b := &builder{wf: wf, sb: sb, byType: map[string][]*resource{}, done: map[*resource]bool{}}
	if st != nil {
		b.wfID = st.WorkflowID
	}
	if wf != nil && wf.ID != "" {
		b.wfID = wf.ID
	}
	b.aws = strings.TrimSuffix(sb.Bases[model.CloudAWS], "/")
	b.gcp = strings.TrimSuffix(sb.Bases[model.CloudGCP], "/")
	b.azure = strings.TrimSuffix(sb.Bases[model.CloudAzure], "/")
	b.oci = strings.TrimSuffix(sb.Bases[model.CloudOCI], "/")
	b.region = sb.Region
	if st != nil {
		for i := range st.Resources {
			r := &st.Resources[i]
			if r.Mode != "" && r.Mode != "managed" {
				continue
			}
			b.byType[r.Type] = append(b.byType[r.Type], r)
			b.ordered = append(b.ordered, r)
		}
	}
	// The sandbox region is the one resources were created in.
	for _, r := range b.ordered {
		if reg := arnRegion(str(r, "arn")); reg != "" && strings.HasPrefix(r.Type, "aws_") {
			b.region = reg
			break
		}
	}
	if b.region == "" {
		b.region = defaultRegion
	}
	b.sample = sampleSegment(wf, sb.Sources)
	b.build()
	return b
}

func (b *builder) build() {
	if b.aws != "" {
		b.httpAPIs()
		b.restAPIs()
		b.functionURLs()
		b.functions()
		b.queues()
		b.topics()
		b.buckets()
		b.tables()
		b.streams()
		b.eventBuses()
		b.stateMachines()
		b.secrets()
		b.parameters()
		b.userPools()
	}
	b.databases()
	b.caches()
	if b.gcp != "" {
		b.gcpResources()
	}
	if b.azure != "" {
		b.azureResources()
	}
	if b.oci != "" {
		b.ociResources()
	}
	for _, r := range b.ordered {
		if !b.done[r] && cloudOf(r.Type) != "" {
			b.generic(r)
		}
	}
}

// add records a built resource.
func (b *builder) add(r *resource, kind, summary string, t target) *entry {
	b.done[r] = true
	b.entries = append(b.entries, entry{
		ResourceAccess: model.ResourceAccess{
			NodeID: b.nodeID(r), Address: r.Address, Type: r.Type, Label: b.label(r, nameKey(r.Type)),
			Kind: kind, Summary: summary, Supporting: supporting(r, kind),
			Fields: []model.AccessField{}, Snippets: []model.Snippet{}, Actions: []model.TryAction{},
		},
		t: t,
	})
	return &b.entries[len(b.entries)-1]
}

// supporting reports whether r is plumbing: something Terraform created so
// the resources people use work together (permissions, triggers, routing,
// settings) with nothing to call itself. Resources with a dedicated kind
// never are; generic ones are when their type names plumbing, or when they
// have no address and their type names nothing people use either.
func supporting(r *resource, kind string) bool {
	if kind != KindGeneric {
		return false
	}
	t := r.Type
	if strings.Contains(t, "_iam_") || slices.ContainsFunc(plumbingPrefixes, func(p string) bool { return strings.HasPrefix(t, p) && !primaryTypes[t] }) ||
		slices.ContainsFunc(plumbingSuffixes, func(s string) bool { return strings.HasSuffix(t, s) }) {
		return true
	}
	if primaryTypes[t] || slices.ContainsFunc(primarySuffixes, func(s string) bool { return strings.HasSuffix(t, s) }) {
		return false
	}
	return !hasAddress(r)
}

// Plumbing types: prefixes cover a service's sub-resources (the parent
// itself stays primary), suffixes the wiring shared by every cloud.
var (
	plumbingPrefixes = []string{"aws_apigatewayv2_", "aws_api_gateway_", "aws_s3_bucket_", "aws_cloudwatch_log_", "google_project_service", "oci_identity_"}
	plumbingSuffixes = []string{
		"_policy", "_attachment", "_association", "_permission", "_permissions", "_mapping",
		"_subscription", "_binding", "_member", "_role_assignment", "_role_definition", "_instance_profile",
		"_notification", "_route", "_integration", "_stage", "_deployment", "_authorizer", "_rule", "_target",
	}
	primaryTypes    = map[string]bool{"aws_apigatewayv2_api": true, "aws_api_gateway_rest_api": true}
	primarySuffixes = []string{
		"_api", "_function", "_function_url", "_function_app", "_queue", "_topic", "_bucket", "_table", "_stream",
		"_event_bus", "_state_machine", "_secret", "_parameter", "_user_pool", "_db_instance", "_database",
		"_cluster", "_cache", "_replication_group", "_container",
	}
)

// hasAddress reports whether r has a URL, endpoint or host name to call.
func hasAddress(r *resource) bool {
	for k, v := range r.Values {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		switch {
		case k == "url", k == "endpoint", k == "address", k == "dns_name", k == "domain_name", k == "fqdn", k == "hostname",
			strings.HasSuffix(k, "_url"), strings.HasSuffix(k, "_endpoint"), strings.Contains(s, "://"):
			return true
		}
	}
	return false
}

func (e *entry) field(label, value, hint string) {
	if value != "" {
		e.Fields = append(e.Fields, model.AccessField{Label: label, Value: value, Hint: hint})
	}
}

func (e *entry) snippet(label, lang, code string) {
	e.Snippets = append(e.Snippets, model.Snippet{Label: label, Language: lang, Code: code})
}

func (e *entry) action(a model.TryAction) { e.Actions = append(e.Actions, a) }

// ---------------------------------------------------------------------------
// Sandbox access

func (b *builder) sandboxAccess() []model.SandboxAccess {
	out := []model.SandboxAccess{}
	for _, c := range model.AllClouds {
		base := strings.TrimSuffix(b.sb.Bases[c], "/")
		if !b.sb.Running[c] || base == "" {
			continue
		}
		switch c {
		case model.CloudAWS:
			out = append(out, model.SandboxAccess{
				Cloud: c, URL: base, Region: b.region,
				Credentials: []model.AccessField{
					{Label: "Access key ID", Value: "test"},
					{Label: "Secret access key", Value: "test", Hint: "Any value works; the sandbox doesn't check credentials."},
					{Label: "Account ID", Value: emulatorAccount},
				},
				EnvSnippet: fmt.Sprintf("export AWS_ENDPOINT_URL=%s AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=%s AWS_DEFAULT_REGION=%s", base, b.region, b.region),
				Notes: []string{
					"Any AWS SDK or the AWS CLI works with these settings.",
					"AWS CLI v2.13+ and current SDKs read AWS_ENDPOINT_URL; older tools need --endpoint-url (CLI) or an endpoint option (SDKs).",
					"S3 needs path-style addressing: forcePathStyle: true in JavaScript, addressing_style \"path\" in Python.",
					"This address stays the same when you stop and start the sandbox.",
				},
			})
		case model.CloudGCP:
			out = append(out, b.gcpSandboxAccess(base))
		case model.CloudAzure:
			out = append(out, b.azureSandboxAccess(base))
		case model.CloudOCI:
			out = append(out, b.ociSandboxAccess(base))
		}
	}
	return out
}

func (b *builder) gcpProject() string {
	for _, r := range b.ordered {
		if strings.HasPrefix(r.Type, "google_") {
			if p := str(r, "project"); p != "" {
				return p
			}
		}
	}
	return sandbox.SandboxIdentities().GCPProject
}

// ---------------------------------------------------------------------------
// Names and values

func (b *builder) node(r *resource) *model.Node {
	if b.wf == nil {
		return nil
	}
	for i := range b.wf.Nodes {
		n := &b.wf.Nodes[i]
		if r.NodeID != "" && n.ID == r.NodeID {
			return n
		}
		if r.NodeID == "" && !strings.HasPrefix(r.Address, "module.") && n.Kind == model.KindResource && n.Type == r.Type && n.Name == r.Name {
			return n
		}
	}
	return nil
}

func (b *builder) nodeID(r *resource) string {
	if r.NodeID != "" {
		return r.NodeID
	}
	if n := b.node(r); n != nil {
		return n.ID
	}
	return ""
}

// label is the friendliest name for r: its canvas label (resources of a
// module show their own cloud name), else the cloud name from nameKey,
// else the Terraform name; instances get their index appended.
func (b *builder) label(r *resource, nameKey string) string {
	name := ""
	if n := b.node(r); n != nil && !strings.HasPrefix(r.Address, "module.") {
		name = n.Label
	}
	if name == "" && nameKey != "" {
		name = str(r, nameKey)
	}
	if name == "" {
		name = r.Name
	}
	switch idx := r.Index.(type) {
	case float64:
		name += "[" + strconv.FormatFloat(idx, 'f', -1, 64) + "]"
	case int:
		name += "[" + strconv.Itoa(idx) + "]"
	case string:
		name += "[" + strconv.Quote(idx) + "]"
	}
	return name
}

// nameKey is the attribute holding the cloud name of a resource type.
func nameKey(typ string) string {
	switch typ {
	case "aws_s3_bucket":
		return "bucket"
	case "aws_lambda_function", "aws_lambda_function_url":
		return "function_name"
	case "aws_db_instance":
		return "identifier"
	case "aws_rds_cluster":
		return "cluster_identifier"
	case "aws_elasticache_cluster":
		return "cluster_id"
	case "aws_elasticache_replication_group":
		return "replication_group_id"
	case "oci_queue_queue", "oci_kms_vault", "oci_kms_key", "oci_functions_application", "oci_functions_function":
		return "display_name"
	case "oci_vault_secret":
		return "secret_name"
	}
	return "name"
}

func cloudOf(typ string) model.Cloud {
	switch {
	case strings.HasPrefix(typ, "aws_"):
		return model.CloudAWS
	case strings.HasPrefix(typ, "google_"):
		return model.CloudGCP
	case strings.HasPrefix(typ, "azurerm_"), strings.HasPrefix(typ, "azapi_"):
		return model.CloudAzure
	case strings.HasPrefix(typ, "oci_"):
		return model.CloudOCI
	}
	return ""
}

func str(r *resource, key string) string {
	switch v := r.Values[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	}
	return ""
}

func first(r *resource, keys ...string) string {
	for _, k := range keys {
		if v := str(r, k); v != "" {
			return v
		}
	}
	return ""
}

// list returns the objects of a nested block/list attribute.
func list(r *resource, key string) []map[string]any {
	var out []map[string]any
	if l, ok := r.Values[key].([]any); ok {
		for _, x := range l {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

func mstr(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func boolean(r *resource, key string) bool {
	v, _ := r.Values[key].(bool)
	return v
}

// arnParts splits "arn:partition:service:region:account:resource".
func arnParts(s string) (service, region, account, res string, ok bool) {
	p := strings.SplitN(s, ":", 6)
	if len(p) != 6 || p[0] != "arn" {
		return "", "", "", "", false
	}
	return p[2], p[3], p[4], p[5], true
}

func arnRegion(s string) string {
	_, region, _, _, _ := arnParts(s)
	return region
}

// regionOf is the region of a resource from its ARN, else the sandbox's.
func (b *builder) regionOf(arn string) string {
	if r := arnRegion(arn); r != "" {
		return r
	}
	return b.region
}

// ---------------------------------------------------------------------------
// Sample paths for catch-all routes

var routeLiteral = regexp.MustCompile("[\"'`](/[A-Za-z][A-Za-z0-9_-]{0,40})[\"'`/?]")

var notRoutes = map[string]bool{
	"tmp": true, "var": true, "opt": true, "usr": true, "etc": true, "bin": true, "dev": true, "proc": true,
	"home": true, "build": true, "app": true, "src": true, "lib": true, "work": true, "node_modules": true,
	"assets": true, "dist": true, "static": true, "public": true,
}

// sampleSegment guesses the first path segment the workflow's code serves
// (e.g. "items" for code comparing the path with "/items"), else "hello".
func sampleSegment(wf *model.Workflow, sources []string) string {
	counts := map[string]int{}
	var order []string
	scan := func(s string) {
		for _, m := range routeLiteral.FindAllStringSubmatch(s, -1) {
			seg := strings.TrimPrefix(m[1], "/")
			if notRoutes[strings.ToLower(seg)] {
				continue
			}
			if counts[seg] == 0 {
				order = append(order, seg)
			}
			counts[seg]++
		}
	}
	if wf != nil {
		var walk func(body model.Body)
		walk = func(body model.Body) {
			for _, a := range body.Attrs {
				scan(a.Value.Expr)
			}
			for _, blk := range body.Blocks {
				walk(blk.Body)
			}
		}
		for _, n := range wf.Nodes {
			if n.Type == "archive_file" || n.Type == "aws_lambda_function" || n.Type == "local_file" {
				walk(n.Body)
			}
		}
	}
	for _, s := range sources {
		scan(s)
	}
	if len(order) == 0 {
		return "hello"
	}
	// Most frequent wins; ties keep the first seen.
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	return order[0]
}

// fillPath turns a route path into a concrete one: {param} becomes 1 and a
// greedy {param+} becomes the sample segment. proxy reports a greedy match.
func fillPath(p, sample string) (path string, proxy bool) {
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		switch {
		case strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "+}"):
			out = append(out, sample)
			proxy = true
		case strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}"):
			out = append(out, "1")
		default:
			out = append(out, seg)
		}
		if proxy {
			break
		}
	}
	return "/" + strings.Join(out, "/"), proxy
}

func hasParams(p string) bool { return strings.Contains(p, "{") && !strings.Contains(p, "+}") }

// ---------------------------------------------------------------------------
// Ordering helpers

func uniq(s []string) []string {
	out := []string{}
	for _, v := range s {
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
