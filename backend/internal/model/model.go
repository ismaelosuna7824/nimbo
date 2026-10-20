// Package model holds the domain types shared by every backend package, the
// HTTP API and the MCP server. JSON tags are the wire contract mirrored in
// apps/desktop/src/lib/api/types.ts; change both together.
package model

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"regexp"
	"time"
)

// Cloud identifies a cloud provider family and the Floci emulator serving it.
type Cloud string

const (
	CloudAWS   Cloud = "aws"
	CloudAzure Cloud = "azure"
	CloudGCP   Cloud = "gcp"
	CloudOCI   Cloud = "oci"
)

// AllClouds lists every cloud in display order.
var AllClouds = []Cloud{CloudAWS, CloudAzure, CloudGCP, CloudOCI}

// NodeKind is the Terraform block kind a canvas node represents.
type NodeKind string

const (
	KindResource NodeKind = "resource"
	KindData     NodeKind = "data"
	KindModule   NodeKind = "module"
)

// Value is one HCL expression. Expr is the canonical HCL source text and is
// always set after normalization. Literal is the JSON form of Expr when the
// expression is a constant (no references, functions or templates with
// interpolation); it is absent otherwise. Clients may send only one of the
// two; the backend fills the other on save.
type Value struct {
	Expr    string          `json:"expr,omitempty"`
	Literal json.RawMessage `json:"literal,omitempty"`
}

// Attr is a named attribute inside a Body, kept in source order.
type Attr struct {
	Name     string    `json:"name"`
	Value    Value     `json:"value"`
	Comments *Comments `json:"comments,omitempty"`
	// File and Order name the source block an item of Workflow.Locals or of
	// Workflow.Terraform's body was written in (see BlockPart). Unused
	// elsewhere.
	File  string `json:"file,omitempty"`
	Order int    `json:"order,omitempty"`
}

// Block is a nested HCL block (e.g. `attribute { ... }`, `lifecycle { ... }`,
// `dynamic "x" { ... }`).
type Block struct {
	Type   string   `json:"type"`
	Labels []string `json:"labels,omitempty"`
	Body   Body     `json:"body"`
	// File and Order place a top-level block (Workflow.Other) like
	// Node.File and Node.Order, and name the source block of an item of
	// Workflow.Terraform's body (required_providers, backend, ...) like
	// Attr.File/Order. Unused on other nested blocks.
	File  string `json:"file,omitempty"`
	Order int    `json:"order,omitempty"`
}

// BlockPart is one source `locals` or `terraform` block. The workflow keeps
// the items of every such block in one list (Workflow.Locals, the body of
// Workflow.Terraform); export writes one block per part with the items
// whose File and Order match, in their list order, and Comments as the
// block's own comments.
type BlockPart struct {
	File     string    `json:"file"`
	Order    int       `json:"order,omitempty"`
	Comments *Comments `json:"comments,omitempty"`
}

// Body is an ordered HCL body. Meta-arguments (count, for_each, provider,
// depends_on, lifecycle, source, version) live here like any other attribute
// or block, exactly as in HCL. Comments belong to the block that owns the
// body (resource, variable, nested block, ...).
type Body struct {
	Attrs    []Attr    `json:"attrs"`
	Blocks   []Block   `json:"blocks"`
	Comments *Comments `json:"comments,omitempty"`
}

// Comments are the source comments of an attribute or block, kept verbatim
// with their markers ("# x", "// y", "/* z */") so export writes them back.
// In Lead, Tail and After an empty string is a blank line between comment
// groups. Comments inside an expression are part of Value.Expr.
type Comments struct {
	// Lead are the comment lines directly above (for the first block of a
	// file, the file header).
	Lead []string `json:"lead,omitempty"`
	// Line is the comment on the same line: after the value of an
	// attribute, after the opening "{" of a block.
	Line string `json:"line,omitempty"`
	// Tail are the comment lines of a block after its last attribute or
	// nested block, before "}".
	Tail []string `json:"tail,omitempty"`
	// End is the comment after a block's closing "}" on the same line.
	End string `json:"end,omitempty"`
	// After are the comment lines that follow a top-level block at the end
	// of its file.
	After []string `json:"after,omitempty"`
}

// Position is the canvas position of a node (React Flow coordinates).
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Node is one resource, data source or module call on the canvas.
type Node struct {
	ID       string   `json:"id"`
	Kind     NodeKind `json:"kind"`
	Type     string   `json:"type"` // "aws_s3_bucket"; "module" for module calls
	Name     string   `json:"name"` // Terraform local name; unique per kind+type
	Label    string   `json:"label,omitempty"`
	Parent   string   `json:"parent,omitempty"` // visual grouping only (node ID)
	Position Position `json:"position"`
	// Positioned is false for nodes that have never been placed (fresh
	// import); the UI auto-lays them out and saves positions back.
	Positioned bool `json:"positioned"`
	Body       Body `json:"body"`
	// File is the root .tf file the block is written to ("network.tf"):
	// recorded by import so a project keeps its own layout, or chosen by
	// the user. Empty: export places it in Nimbo's recommended layout
	// (hclcodec.Layout), recomputed on every export.
	File string `json:"file,omitempty"`
	// Order is the block's position in File as imported (1-based; 0 =
	// none): export writes those blocks of a file in their source order,
	// whatever their kind, and the others after them.
	Order int `json:"order,omitempty"`
}

// EdgeKind distinguishes attribute references from explicit depends_on.
type EdgeKind string

const (
	EdgeReference EdgeKind = "reference"
	EdgeDependsOn EdgeKind = "depends_on"
)

// Edge is derived from expressions; it is never authored directly. Source is
// the referenced node, Target the node whose attribute holds the reference.
type Edge struct {
	ID         string   `json:"id"`
	Source     string   `json:"source"`
	Target     string   `json:"target"`
	Kind       EdgeKind `json:"kind"`
	Attribute  string   `json:"attribute"`            // path in target, e.g. "event_source_arn", "attribute[0].name"
	SourceAttr string   `json:"sourceAttr,omitempty"` // attribute read from source, e.g. "arn"
}

// NamedBlock is a top-level labelled block such as `variable "x"`,
// `output "y"` or `provider "aws"`.
type NamedBlock struct {
	Name  string `json:"name"`
	Body  Body   `json:"body"`
	File  string `json:"file,omitempty"`  // see Node.File
	Order int    `json:"order,omitempty"` // see Node.Order
}

// Workflow is the unit the user designs, tests and exports. It maps 1:1 to a
// Terraform root module plus app-only data (positions, environments).
type Workflow struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	// Archived hides the workflow from recents; its sandbox is stopped and
	// runs/load tests are refused until it is restored.
	Archived bool `json:"archived,omitempty"`
	// Icon is the pixel-art icon shown for the workflow. Every workflow gets a
	// random one when created; nil only on files from before icons existed,
	// which the daemon fills at startup. Glyph MascotGlyph is the Nimbo mascot.
	Icon *WorkflowIcon `json:"icon,omitempty"`

	Nodes     []Node       `json:"nodes"`
	Variables []NamedBlock `json:"variables"`
	Outputs   []NamedBlock `json:"outputs"`
	Locals    []Attr       `json:"locals"`
	// LocalsComments are the comments of the `locals` block holding the
	// items without a recorded part (new content); LocalsBlocks are the
	// imported locals blocks (see BlockPart).
	LocalsComments *Comments    `json:"localsComments,omitempty"`
	LocalsBlocks   []BlockPart  `json:"localsBlocks,omitempty"`
	Providers      []NamedBlock `json:"providers"` // Name = provider local name ("aws"); alias lives in Body
	// Terraform holds the items of every terraform block; its Comments
	// belong to the block of the items without a recorded part, and
	// TerraformBlocks are the imported terraform blocks (see BlockPart).
	Terraform       *Body       `json:"terraform,omitempty"`
	TerraformBlocks []BlockPart `json:"terraformBlocks,omitempty"`
	// Other keeps top-level blocks with no dedicated field (moved, import,
	// check, removed, ...) so import/export is lossless.
	Other []Block `json:"other"`

	Environments      []Environment `json:"environments"`
	ActiveEnvironment string        `json:"activeEnvironment,omitempty"`

	// Derived on every save (read-only for clients).
	Edges       []Edge       `json:"edges"`
	Clouds      []Cloud      `json:"clouds"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	// NodeFiles maps node ID -> the root .tf file export writes it to
	// (Node.File, or the recommended layout when it is empty).
	NodeFiles map[string]string `json:"nodeFiles,omitempty"`
}

// WorkflowSummary is the list view of a workflow.
type WorkflowSummary struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	UpdatedAt   time.Time     `json:"updatedAt"`
	NodeCount   int           `json:"nodeCount"`
	Clouds      []Cloud       `json:"clouds"`
	Archived    bool          `json:"archived,omitempty"`
	Icon        *WorkflowIcon `json:"icon,omitempty"`
}

// WorkflowIcon picks a pixel-art glyph and tile color by ID. The server only
// checks the format; clients render unknown IDs as the Nimbo mascot.
type WorkflowIcon struct {
	Glyph string `json:"glyph"`
	Color string `json:"color"`
}

// MascotGlyph is the explicit "Nimbo mascot" choice; its color is ignored.
const MascotGlyph = "nimbo"

// WorkflowIconGlyphs are the random-pick glyph IDs.
// Must match apps/desktop/src/components/workflow-icon/glyphs.ts.
var WorkflowIconGlyphs = []string{
	"cloud", "bolt", "database", "envelope", "bucket", "globe", "shield", "key", "gear", "server",
	"chip", "network", "flow", "broadcast", "clock", "rocket", "robot", "cat", "ghost", "alien",
	"heart", "star", "fire", "leaf", "crown", "coffee", "gamepad", "diamond", "mushroom", "potion",
}

// WorkflowIconColors are the tile color IDs.
// Must match apps/desktop/src/components/workflow-icon/colors.ts.
var WorkflowIconColors = []string{"violet", "cyan", "pink", "lime", "amber", "red", "blue", "slate"}

// RandomWorkflowIcon returns a random glyph and color. Glyphs used by avoid
// are skipped, and so is the color of avoid[0] (the most recent), while
// other choices remain.
func RandomWorkflowIcon(avoid ...*WorkflowIcon) *WorkflowIcon {
	usedGlyph := map[string]bool{}
	for _, a := range avoid {
		if a != nil {
			usedGlyph[a.Glyph] = true
		}
	}
	lastColor := ""
	if len(avoid) > 0 && avoid[0] != nil {
		lastColor = avoid[0].Color
	}
	return &WorkflowIcon{
		Glyph: pickExcept(WorkflowIconGlyphs, func(g string) bool { return usedGlyph[g] }),
		Color: pickExcept(WorkflowIconColors, func(c string) bool { return c == lastColor }),
	}
}

// pickExcept returns a random element not matched by skip, or any element
// when skip matches all of them.
func pickExcept(ids []string, skip func(string) bool) string {
	var free []string
	for _, id := range ids {
		if !skip(id) {
			free = append(free, id)
		}
	}
	if len(free) == 0 {
		free = ids
	}
	return free[rand.IntN(len(free))]
}

var iconID = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// Validate reports a malformed glyph or color ID.
func (i *WorkflowIcon) Validate() error {
	if i == nil {
		return nil
	}
	if !iconID.MatchString(i.Glyph) {
		return fmt.Errorf("invalid icon glyph %q", i.Glyph)
	}
	if !iconID.MatchString(i.Color) {
		return fmt.Errorf("invalid icon color %q", i.Color)
	}
	return nil
}

// Environment is a named set of Terraform variable values and process
// environment variables applied to plan/apply and exported as tfvars.
type Environment struct {
	Name    string           `json:"name"`
	Values  map[string]Value `json:"values"` // variable name -> value
	EnvVars []EnvVar         `json:"envVars"`
	// File is the root variable file the environment was imported from
	// ("terraform.tfvars", "prod.auto.tfvars"); export writes it back there
	// so Terraform keeps loading it the same way. Empty: the environment is
	// exported as environments/<name>.tfvars.
	File string `json:"file,omitempty"`
}

// EnvVar is a process environment variable passed to Terraform runs.
type EnvVar struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret,omitempty"`
}

// Severity of a diagnostic.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Diagnostic is a problem found by import, normalization, schema validation,
// the best-practice checks (codes "bp_*", info/warning only) or a Terraform
// run.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	// Detail explains the problem (best-practice findings: why it matters).
	Detail string `json:"detail,omitempty"`
	// Fix is a suggested change (best-practice findings).
	Fix       string `json:"fix,omitempty"`
	NodeID    string `json:"nodeId,omitempty"`
	Attribute string `json:"attribute,omitempty"`
	// File and Line locate code diagnostics (Terraform syntax errors) in
	// the file that was imported; Line is 1-based.
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
}

// ---------------------------------------------------------------------------
// Catalog

// ProviderInfo describes a supported Terraform provider and its emulator.
type ProviderInfo struct {
	Cloud         Cloud  `json:"cloud"`
	Name          string `json:"name"`   // provider local name: "aws", "azurerm", "google", "oci"
	Source        string `json:"source"` // "hashicorp/aws"
	Version       string `json:"version"`
	Label         string `json:"label"`
	Emulator      string `json:"emulator"` // docker image
	EmulatorPort  int    `json:"emulatorPort"`
	ResourceCount int    `json:"resourceCount"`
	DataCount     int    `json:"dataCount"`
}

// ResourceSummary is a palette entry.
type ResourceSummary struct {
	Kind           NodeKind `json:"kind"`
	Type           string   `json:"type"`
	Cloud          Cloud    `json:"cloud"`
	Service        string   `json:"service,omitempty"` // Floci service key, e.g. "sqs"
	Category       string   `json:"category"`
	Label          string   `json:"label"`
	Icon           string   `json:"icon"`
	Popular        bool     `json:"popular,omitempty"`
	FlociSupported bool     `json:"flociSupported"`
}

// ResourceSchema is the full schema of one resource/data type plus curated
// metadata used to build forms.
type ResourceSchema struct {
	ResourceSummary
	Provider    string      `json:"provider"` // "hashicorp/aws"
	Description string      `json:"description,omitempty"`
	DocsURL     string      `json:"docsUrl,omitempty"`
	Block       SchemaBlock `json:"block"`
}

// SchemaBlock mirrors a Terraform provider schema block.
type SchemaBlock struct {
	Attributes []SchemaAttribute   `json:"attributes"`
	Blocks     []SchemaNestedBlock `json:"blocks"`
}

// SchemaAttribute is one attribute of a schema block.
type SchemaAttribute struct {
	Name        string          `json:"name"`
	Type        json.RawMessage `json:"type"` // cty type JSON: "string", ["list","string"], ...
	Required    bool            `json:"required,omitempty"`
	Optional    bool            `json:"optional,omitempty"`
	Computed    bool            `json:"computed,omitempty"`
	Sensitive   bool            `json:"sensitive,omitempty"`
	Deprecated  bool            `json:"deprecated,omitempty"`
	Description string          `json:"description,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"` // curated suggestion, not provider default
	Enum        []string        `json:"enum,omitempty"`    // curated allowed values
	Basic       bool            `json:"basic,omitempty"`   // curated: show in beginner form
}

// SchemaNestedBlock is a nested block type of a schema block.
type SchemaNestedBlock struct {
	Name     string      `json:"name"`
	Nesting  string      `json:"nesting"` // single | list | set | map | group
	MinItems int         `json:"minItems,omitempty"`
	MaxItems int         `json:"maxItems,omitempty"`
	Block    SchemaBlock `json:"block"`
}

// ConnectionRecipe is one way to connect two node types. Generic recipes set
// a single reference attribute or depends_on; curated recipes may also add
// helper nodes (e.g. aws_lambda_event_source_mapping, aws_lambda_permission).
type ConnectionRecipe struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	SourceType  string `json:"sourceType"`
	TargetType  string `json:"targetType"`
	Curated     bool   `json:"curated"`
}

// Template is a starter workflow.
type Template struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Cloud       Cloud         `json:"cloud"`
	Difficulty  string        `json:"difficulty"` // beginner | intermediate | advanced
	Tags        []string      `json:"tags"`
	Icon        *WorkflowIcon `json:"icon,omitempty"`
}

// ---------------------------------------------------------------------------
// Sandbox (per-workflow Floci emulators) and Terraform runs

// SandboxState is the lifecycle state of a workflow sandbox.
type SandboxState string

const (
	SandboxAbsent   SandboxState = "absent"
	SandboxStarting SandboxState = "starting"
	SandboxRunning  SandboxState = "running"
	SandboxStopped  SandboxState = "stopped"
	SandboxError    SandboxState = "error"
)

// SandboxStatus describes the containers backing one workflow.
type SandboxStatus struct {
	WorkflowID string           `json:"workflowId"`
	State      SandboxState     `json:"state"`
	Network    string           `json:"network,omitempty"`
	Emulators  []EmulatorStatus `json:"emulators"`
	Message    string           `json:"message,omitempty"`
}

// EmulatorStatus describes one Floci emulator container.
type EmulatorStatus struct {
	Cloud     Cloud             `json:"cloud"`
	Container string            `json:"container"`
	Image     string            `json:"image"`
	Port      int               `json:"port"`     // in-sandbox port (4566, 4577, ...)
	HostPort  int               `json:"hostPort"` // published on 127.0.0.1
	URL       string            `json:"url"`      // host-reachable base URL
	State     SandboxState      `json:"state"`
	Version   string            `json:"version,omitempty"`
	Services  map[string]string `json:"services,omitempty"` // floci service key -> running|available
}

// RunAction is a Terraform command.
type RunAction string

const (
	ActionValidate RunAction = "validate"
	ActionPlan     RunAction = "plan"
	ActionApply    RunAction = "apply"
	ActionDestroy  RunAction = "destroy"
)

// RunStatus is the lifecycle of a Terraform run.
type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
	RunCanceled  RunStatus = "canceled"
)

// LogLine is one line of run output.
type LogLine struct {
	T      time.Time `json:"t"`
	Stream string    `json:"stream"` // stdout | stderr | system
	Text   string    `json:"text"`
}

// PlanSummary counts planned changes.
type PlanSummary struct {
	Add     int `json:"add"`
	Change  int `json:"change"`
	Destroy int `json:"destroy"`
}

// Run is one Terraform invocation against a sandbox.
type Run struct {
	ID          string       `json:"id"`
	WorkflowID  string       `json:"workflowId"`
	Action      RunAction    `json:"action"`
	Environment string       `json:"environment,omitempty"`
	Status      RunStatus    `json:"status"`
	CreatedAt   time.Time    `json:"createdAt"`
	StartedAt   *time.Time   `json:"startedAt,omitempty"`
	EndedAt     *time.Time   `json:"endedAt,omitempty"`
	ExitCode    int          `json:"exitCode"`
	Plan        *PlanSummary `json:"plan,omitempty"`
	Error       string       `json:"error,omitempty"`
	Log         []LogLine    `json:"log,omitempty"` // omitted in list responses
}

// DeployedState is the Terraform state of a sandbox mapped onto nodes.
type DeployedState struct {
	WorkflowID string             `json:"workflowId"`
	Resources  []DeployedResource `json:"resources"`
	Outputs    map[string]any     `json:"outputs"`
	UpdatedAt  time.Time          `json:"updatedAt"`
}

// DeployedResource is one resource instance from `terraform show -json`.
type DeployedResource struct {
	Address string         `json:"address"` // "aws_sqs_queue.q", "aws_s3_bucket.b[0]"
	Mode    string         `json:"mode"`    // managed | data
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	Index   any            `json:"index,omitempty"`
	NodeID  string         `json:"nodeId,omitempty"`
	Values  map[string]any `json:"values"`
}

// ---------------------------------------------------------------------------
// Endpoints: how to use what is deployed in a sandbox

// EndpointsInfo explains how to reach the sandbox and every deployed
// resource from the host (GET /workflows/{id}/endpoints). Sandbox is empty
// while the sandbox is not running; Resources is empty when nothing is
// deployed.
type EndpointsInfo struct {
	WorkflowID string           `json:"workflowId"`
	Sandbox    []SandboxAccess  `json:"sandbox"`
	Resources  []ResourceAccess `json:"resources"`
	UpdatedAt  time.Time        `json:"updatedAt"`
}

// SandboxAccess is how SDKs and CLIs reach one running emulator.
type SandboxAccess struct {
	Cloud       Cloud         `json:"cloud"`
	URL         string        `json:"url"`
	Region      string        `json:"region,omitempty"`
	Credentials []AccessField `json:"credentials"`
	EnvSnippet  string        `json:"envSnippet"` // shell exports pointing tools at the emulator
	Notes       []string      `json:"notes"`
}

// AccessField is one labelled, copyable value.
type AccessField struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Secret bool   `json:"secret,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// HTTPEndpoint is one callable HTTP route. Proxy marks a greedy route (e.g.
// ANY /{proxy+}): URL carries a sample path the user can change.
type HTTPEndpoint struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Proxy       bool   `json:"proxy,omitempty"`
}

// Snippet is ready-to-run code against the sandbox. Label: curl | AWS CLI |
// JavaScript | Python | ...; Language: bash | javascript | python | json.
type Snippet struct {
	Label    string `json:"label"`
	Language string `json:"language"`
	Code     string `json:"code"`
}

// TryAction is an operation the app can run on a resource for the user.
// Input is http | json | text | none; Writes marks actions that change data.
type TryAction struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Input       string `json:"input"`
	Default     string `json:"default,omitempty"`
	Writes      bool   `json:"writes,omitempty"`
}

// ResourceAccess explains one deployed resource in plain language. Kind is
// http-api | function-url | function | queue | topic | bucket | table |
// stream | event-bus | state-machine | secret | parameter | user-pool |
// database | cache | web-service | key | kubernetes | instance | generic. Supporting marks plumbing (permissions,
// triggers, routing, settings) that beginners don't call directly.
type ResourceAccess struct {
	NodeID    string         `json:"nodeId,omitempty"`
	Address   string         `json:"address"`
	Type      string         `json:"type"`
	Label     string         `json:"label"`
	Kind      string         `json:"kind"`
	Summary   string         `json:"summary"`
	Fields    []AccessField  `json:"fields"`
	Endpoints []HTTPEndpoint `json:"endpoints,omitempty"`
	Snippets  []Snippet      `json:"snippets"`
	Actions   []TryAction    `json:"actions"`
	Warning   string         `json:"warning,omitempty"`
	// Supporting resources are listed apart from the ones people use.
	Supporting bool `json:"supporting,omitempty"`
}

// TryRequest runs Action on the resource of node NodeID (POST
// /workflows/{id}/endpoints/try). HTTP actions use Method, Path (a full
// sandbox URL or a path relative to the endpoint), Headers and Input as
// body; other actions read Input as their argument.
type TryRequest struct {
	NodeID  string            `json:"nodeId"`
	Action  string            `json:"action"`
	Input   string            `json:"input,omitempty"`
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// TryResult is the outcome of a TryRequest; operation failures set Error
// with OK false.
type TryResult struct {
	OK          bool              `json:"ok"`
	Status      string            `json:"status,omitempty"`
	LatencyMs   float64           `json:"latencyMs"`
	ContentType string            `json:"contentType,omitempty"`
	Body        string            `json:"body,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Error       string            `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// Network checks (configuration review and reachability analysis)

// NetworkBasis says where the values of a network analysis came from:
// "deployed" (the sandbox's Terraform state: exact CIDRs, IDs and
// count/for_each instances) or "design" (the workflow's expressions
// evaluated with variable defaults, the active environment and locals).
type NetworkBasis string

const (
	NetworkBasisDeployed NetworkBasis = "deployed"
	NetworkBasisDesign   NetworkBasis = "design"
)

// NetworkSummary is the network model of a workflow
// (GET /workflows/{id}/network). Addresses are instance addresses
// (`aws_subnet.public["a"]`); NodeID is the canvas node they belong to.
// Findings are the "net_*" diagnostics of the same analysis. Notes list
// what was skipped because its inputs are unknown (module calls, values
// computed at deploy time, ...).
type NetworkSummary struct {
	WorkflowID     string             `json:"workflowId"`
	Basis          NetworkBasis       `json:"basis"`
	BasisReason    string             `json:"basisReason"`
	Networks       []NetNetwork       `json:"networks"`
	Subnets        []NetSubnet        `json:"subnets"`
	RouteTables    []NetRouteTable    `json:"routeTables"`
	Gateways       []NetGateway       `json:"gateways"`
	SecurityGroups []NetSecurityGroup `json:"securityGroups"`
	Placements     []NetPlacement     `json:"placements"`
	Findings       []Diagnostic       `json:"findings"`
	Counts         NetFindingCounts   `json:"counts"`
	Notes          []string           `json:"notes"`
}

// NetFindingCounts counts findings by severity.
type NetFindingCounts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Info     int `json:"info"`
}

// NetNetwork is a VPC (AWS), virtual network (Azure) or VPC network (GCP).
type NetNetwork struct {
	NodeID  string   `json:"nodeId,omitempty"`
	Address string   `json:"address"`
	Cloud   Cloud    `json:"cloud"`
	Name    string   `json:"name"`
	CIDRs   []string `json:"cidrs"` // empty when unknown or not applicable (GCP)
	ID      string   `json:"id,omitempty"`
}

// Subnet classifications.
const (
	SubnetPublic   = "public"   // default route to an internet gateway
	SubnetPrivate  = "private"  // outbound internet through NAT (or another gateway), no inbound
	SubnetIsolated = "isolated" // no route to the internet at all
	SubnetUnknown  = "unknown"
)

// NetSubnet is one subnet with its public/private classification and the
// plain-language reason (Why).
type NetSubnet struct {
	NodeID         string `json:"nodeId,omitempty"`
	Address        string `json:"address"`
	Name           string `json:"name"`
	Network        string `json:"network,omitempty"` // address of its VPC/VNet
	CIDR           string `json:"cidr,omitempty"`
	Zone           string `json:"zone,omitempty"` // availability zone
	Classification string `json:"classification"`
	Why            string `json:"why"`
	RouteTable     string `json:"routeTable,omitempty"` // address ("main route table of aws_vpc.main" when implicit)
	NACL           string `json:"nacl,omitempty"`
	MapPublicIP    bool   `json:"mapPublicIp,omitempty"`
	ID             string `json:"id,omitempty"`
}

// NetRouteTable is a route table with its routes and associated subnets.
type NetRouteTable struct {
	NodeID  string     `json:"nodeId,omitempty"`
	Address string     `json:"address"`
	Network string     `json:"network,omitempty"`
	Main    bool       `json:"main,omitempty"`
	Subnets []string   `json:"subnets"`
	Routes  []NetRoute `json:"routes"`
}

// NetRoute is one route: Destination CIDR (or prefix list) to a target.
// TargetKind: local | internet-gateway | egress-only-gateway | nat-gateway
// | vpc-endpoint | peering | transit-gateway | instance | network-interface
// | vpn-gateway | unknown.
type NetRoute struct {
	Destination string `json:"destination"`
	TargetKind  string `json:"targetKind"`
	Target      string `json:"target,omitempty"` // address or ID of the target
	NodeID      string `json:"nodeId,omitempty"` // node of the route (aws_route) or of the table (inline routes)
}

// NetGateway is an internet, egress-only or NAT gateway, VPC endpoint,
// peering connection or transit gateway attachment. Kind uses the
// NetRoute.TargetKind names.
type NetGateway struct {
	NodeID  string `json:"nodeId,omitempty"`
	Address string `json:"address"`
	Kind    string `json:"kind"`
	Network string `json:"network,omitempty"`
	Subnet  string `json:"subnet,omitempty"`  // NAT gateways
	Service string `json:"service,omitempty"` // VPC endpoints: "s3", "dynamodb", ...
	Detail  string `json:"detail,omitempty"`
}

// NetSecurityGroup is a security group (AWS) or network security group
// (Azure) / firewall (GCP) with its effective rules.
type NetSecurityGroup struct {
	NodeID  string    `json:"nodeId,omitempty"`
	Address string    `json:"address"`
	Network string    `json:"network,omitempty"`
	Ingress []NetRule `json:"ingress"`
	Egress  []NetRule `json:"egress"`
	Summary string    `json:"summary"` // "Allows 80, 443 from anywhere; all outbound"
}

// NetRule is one allow rule. Ports: "80", "1024-65535" or "all"; Peer is a
// CIDR, "anywhere", a security group address or "itself".
type NetRule struct {
	Protocol    string `json:"protocol"` // tcp | udp | icmp | all
	Ports       string `json:"ports"`
	Peer        string `json:"peer"`
	Description string `json:"description,omitempty"`
	NodeID      string `json:"nodeId,omitempty"` // node declaring the rule
}

// NetPlacement says where a workload (instance, Lambda, database, load
// balancer, ECS service, EKS cluster, ...) sits in the network.
type NetPlacement struct {
	NodeID         string   `json:"nodeId,omitempty"`
	Address        string   `json:"address"`
	Kind           string   `json:"kind"` // instance | function | database | cache | load-balancer | container-service | kubernetes | interface
	Network        string   `json:"network,omitempty"`
	Subnets        []string `json:"subnets"`
	Zones          []string `json:"zones"`
	SecurityGroups []string `json:"securityGroups"`
	Public         bool     `json:"public"`
	PublicWhy      string   `json:"publicWhy"`
	Ports          []int    `json:"ports,omitempty"` // listening ports when known (database port, listeners)
	InVPC          bool     `json:"inVpc"`           // false: a Lambda outside any VPC
}

// NetEndpoint is one end of a reachability question: Kind "internet" or
// "node" (NodeID: node ID or address; an instance address picks one
// count/for_each instance, otherwise the first one is used).
type NetEndpoint struct {
	Kind   string `json:"kind"`
	NodeID string `json:"nodeId,omitempty"`
}

// ReachabilityRequest is the body of POST /workflows/{id}/network/reachability.
// Protocol is tcp | udp | icmp (Port ignored for icmp). Live also runs real
// connections in the sandbox for the layers the emulator enforces.
type ReachabilityRequest struct {
	Source      NetEndpoint `json:"source"`
	Destination NetEndpoint `json:"destination"`
	Protocol    string      `json:"protocol"`
	Port        int         `json:"port"`
	Live        bool        `json:"live,omitempty"`
}

// Hop verdicts.
const (
	HopAllow   = "allow"
	HopDeny    = "deny"
	HopUnknown = "unknown"
)

// ReachabilityHop is one component on the path. Layer: endpoint | address
// | route | gateway | nacl | security-group | listener | target | service.
// Enforcement is set on live results: "live" when the sandbox emulator
// enforces this layer (the probe verified it), "analysis" when only the
// analyzer checked it (AWS would enforce it; the emulator does not).
type ReachabilityHop struct {
	Component   string `json:"component"`
	Layer       string `json:"layer"`
	NodeID      string `json:"nodeId,omitempty"`
	Verdict     string `json:"verdict"`
	Reason      string `json:"reason"`
	Enforcement string `json:"enforcement,omitempty"`
}

// Reachability verdicts.
const (
	ReachYes     = "reachable"
	ReachNo      = "unreachable"
	ReachUnknown = "unknown"
)

// ReachabilityResult answers a ReachabilityRequest. Reachable is true only
// when every hop allows; Verdict distinguishes a denial from unknown
// inputs. Blockers explain each denial; Suggestions how to fix them.
type ReachabilityResult struct {
	Reachable   bool              `json:"reachable"`
	Verdict     string            `json:"verdict"`
	Basis       NetworkBasis      `json:"basis"`
	Summary     string            `json:"summary"`
	Path        []ReachabilityHop `json:"path"`
	Blockers    []string          `json:"blockers"`
	Suggestions []string          `json:"suggestions"`
	Live        *LiveCheck        `json:"live,omitempty"`
}

// LiveCheck is the outcome of the real connections behind a live
// reachability request. Verified names the layers the probes exercise in
// the emulator; AnalysisOnly the layers the emulator does not enforce (the
// analyzer's verdict stands for them). Unavailable explains why nothing
// could run (sandbox stopped, nothing deployed, unsupported endpoints).
type LiveCheck struct {
	Probes       []LiveProbe `json:"probes"`
	Verified     []string    `json:"verified"`
	AnalysisOnly []string    `json:"analysisOnly"`
	Unavailable  string      `json:"unavailable,omitempty"`
	Note         string      `json:"note,omitempty"`
}

// LiveProbe is one real connection attempt. Connected is what happened;
// Predicted what the analyzer expected for the layers the emulator
// enforces; Matches compares them.
type LiveProbe struct {
	Name      string  `json:"name"`
	Command   string  `json:"command"`
	Connected bool    `json:"connected"`
	Result    string  `json:"result"` // "HTTP 200", "connection refused", "timed out", ...
	Output    string  `json:"output,omitempty"`
	LatencyMs float64 `json:"latencyMs"`
	Predicted bool    `json:"predicted"`
	Matches   bool    `json:"matches"`
}

// TestInstanceRequest is the body of POST /workflows/{id}/network/test-instance:
// the subnet (node ID or address) the test instance goes into.
type TestInstanceRequest struct {
	Subnet string `json:"subnet"`
}

// ---------------------------------------------------------------------------
// Load testing

// LoadTargetKind selects the protocol used to generate load.
type LoadTargetKind string

const (
	TargetHTTP  LoadTargetKind = "http"
	TargetAWS   LoadTargetKind = "aws"
	TargetSQL   LoadTargetKind = "sql"   // PostgreSQL / MySQL connection
	TargetRedis LoadTargetKind = "redis" // Redis connection
	TargetMongo LoadTargetKind = "mongo" // MongoDB connection
)

// LoadTarget is what a load test hits. With ConnectionID the target runs
// against that saved connection (an already-deployed service outside the
// sandbox): HTTP URLs may then be paths relative to the connection's base URL
// and AWS operations hit the real AWS account of the connection's profile.
// SQL, Redis and Mongo targets always require ConnectionID.
type LoadTarget struct {
	Kind         LoadTargetKind `json:"kind"`
	NodeID       string         `json:"nodeId,omitempty"`
	ConnectionID string         `json:"connectionId,omitempty"`
	Label        string         `json:"label"`
	HTTP         *HTTPTarget    `json:"http,omitempty"`
	AWS          *AWSTarget     `json:"aws,omitempty"`
	SQL          *SQLTarget     `json:"sql,omitempty"`
	Redis        *RedisTarget   `json:"redis,omitempty"`
	Mongo        *MongoTarget   `json:"mongo,omitempty"`
}

// SQLTarget runs one statement per request. Args are templates (same
// placeholders as payloads) bound as driver parameters ($1.. for PostgreSQL,
// ? for MySQL) — never interpolated into Query. Mode "query" reads all rows;
// "exec" discards results.
type SQLTarget struct {
	Query string   `json:"query"`
	Args  []string `json:"args,omitempty"`
	Mode  string   `json:"mode"` // query | exec
}

// RedisTarget sends one command per request, e.g. "SET bench:{{seq}} {{uuid}}"
// or "GET bench:{{rand}}". Arguments are split on whitespace; double quotes
// group an argument containing spaces.
type RedisTarget struct {
	Command string `json:"command"`
}

// MongoTarget runs one operation per request. Document is a JSON template:
// the document for insertOne, the filter for findOne/find/updateOne/deleteOne,
// the command for runCommand. Update is the update document for updateOne.
type MongoTarget struct {
	Database   string `json:"database"`
	Collection string `json:"collection,omitempty"` // not used by runCommand
	Operation  string `json:"operation"`            // insertOne | findOne | find | updateOne | deleteOne | runCommand
	Document   string `json:"document,omitempty"`
	Update     string `json:"update,omitempty"`
}

// HTTPTarget is an HTTP request template. URL may be a host URL or an
// emulator URL; the engine rewrites emulator URLs to the sandbox host port.
type HTTPTarget struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// AWSTarget is an AWS API operation executed through the AWS SDK against the
// sandbox. Operation is "<service>:<Operation>", e.g. "sqs:SendMessage",
// "s3:PutObject", "s3:GetObject", "dynamodb:PutItem", "dynamodb:GetItem",
// "lambda:Invoke", "sns:Publish", "kinesis:PutRecord", "events:PutEvents",
// "states:StartExecution". Resource is the identifier the operation needs
// (queue URL, bucket name, table name, function name, topic ARN, stream
// name, bus name, state machine ARN). Payload is a template supporting
// {{seq}}, {{uuid}}, {{now}} and {{rand}} placeholders.
type AWSTarget struct {
	Operation string `json:"operation"`
	Resource  string `json:"resource"`
	Payload   string `json:"payload,omitempty"`
	Key       string `json:"key,omitempty"` // S3 object key / DynamoDB key JSON template
}

// LoadMode selects open (arrival rate) or closed (fixed concurrency) model.
type LoadMode string

const (
	ModeRPS         LoadMode = "rps"
	ModeConcurrency LoadMode = "concurrency"
)

// LoadStage ramps linearly from the previous stage's target (0 for the
// first stage) to Target over DurationSec. DurationSec 0 is an instant jump,
// so holding a level is a jump stage followed by a flat stage.
type LoadStage struct {
	DurationSec int `json:"durationSec"`
	Target      int `json:"target"`
}

// LoadProfile is a traffic shape.
type LoadProfile struct {
	Preset      string      `json:"preset"` // smoke | load | stress | spike | soak | custom
	Label       string      `json:"label,omitempty"`
	Description string      `json:"description,omitempty"`
	Mode        LoadMode    `json:"mode"`
	Stages      []LoadStage `json:"stages"`
}

// Threshold is a pass/fail criterion evaluated on the run summary.
type Threshold struct {
	Metric string  `json:"metric"` // p50_ms | p90_ms | p95_ms | p99_ms | max_ms | error_rate | rps_min
	Op     string  `json:"op"`     // < | <= | > | >=
	Value  float64 `json:"value"`
}

// LoadTestSpec fully describes a load test. ConfirmExternal must be true when
// the target is outside the local sandbox (any ConnectionID, or an HTTP URL
// whose host is not loopback/emulator); the engine refuses otherwise with
// ErrConfirmationRequired so real services never get traffic by accident.
type LoadTestSpec struct {
	Name            string      `json:"name"`
	Target          LoadTarget  `json:"target"`
	Profile         LoadProfile `json:"profile"`
	Thresholds      []Threshold `json:"thresholds"`
	TimeoutMs       int         `json:"timeoutMs"`
	MaxWorkers      int         `json:"maxWorkers,omitempty"` // cap on in-flight requests in rps mode
	ConfirmExternal bool        `json:"confirmExternal,omitempty"`
}

// LatencyStats are in milliseconds.
type LatencyStats struct {
	Min    float64 `json:"min"`
	Mean   float64 `json:"mean"`
	StdDev float64 `json:"stddev"`
	P50    float64 `json:"p50"`
	P90    float64 `json:"p90"`
	P95    float64 `json:"p95"`
	P99    float64 `json:"p99"`
	Max    float64 `json:"max"`
}

// LoadSample is one second of a running test.
type LoadSample struct {
	T        float64 `json:"t"` // seconds since start
	Target   float64 `json:"target"`
	RPS      float64 `json:"rps"`
	Errors   int     `json:"errors"`
	InFlight int     `json:"inFlight"`
	P50      float64 `json:"p50"`
	P95      float64 `json:"p95"`
	P99      float64 `json:"p99"`
	Max      float64 `json:"max"`
}

// LoadSummary aggregates a whole run. Stability is the coefficient of
// variation of per-second p95 (lower is steadier). BreakingPoint is the
// target level at which error rate first exceeded 5% or p95 first exceeded
// 4x the run's initial p95; nil when never reached.
type LoadSummary struct {
	Requests      int            `json:"requests"`
	Errors        int            `json:"errors"`
	ErrorRate     float64        `json:"errorRate"`
	RPS           float64        `json:"rps"`
	PeakRPS       float64        `json:"peakRps"`
	BytesIn       int64          `json:"bytesIn"`
	Latency       LatencyStats   `json:"latency"`
	StatusCodes   map[string]int `json:"statusCodes"`
	ErrorKinds    map[string]int `json:"errorKinds"`
	Stability     float64        `json:"stability"`
	BreakingPoint *float64       `json:"breakingPoint,omitempty"`
}

// ThresholdResult is a threshold evaluated against a summary.
type ThresholdResult struct {
	Threshold
	Actual float64 `json:"actual"`
	Passed bool    `json:"passed"`
}

// LoadTestStatus is the lifecycle of a load test run.
type LoadTestStatus string

const (
	LoadRunning LoadTestStatus = "running"
	LoadPassed  LoadTestStatus = "passed"
	LoadFailed  LoadTestStatus = "failed"
	LoadStopped LoadTestStatus = "stopped"
	LoadErrored LoadTestStatus = "error"
)

// LoadTestRun is a load test execution and its results. Exactly one of
// WorkflowID / ConnectionID is set: runs started from a workflow belong to
// it (even when the target uses a connection); runs started from the
// Connections library belong to the connection.
type LoadTestRun struct {
	ID           string            `json:"id"`
	WorkflowID   string            `json:"workflowId,omitempty"`
	ConnectionID string            `json:"connectionId,omitempty"`
	External     bool              `json:"external,omitempty"` // traffic left the local sandbox
	Spec         LoadTestSpec      `json:"spec"`
	Status       LoadTestStatus    `json:"status"`
	StartedAt    time.Time         `json:"startedAt"`
	EndedAt      *time.Time        `json:"endedAt,omitempty"`
	Timeline     []LoadSample      `json:"timeline"`
	Summary      *LoadSummary      `json:"summary,omitempty"`
	Thresholds   []ThresholdResult `json:"thresholds,omitempty"`
	Error        string            `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// Connections: saved, already-deployed services to load-test (global library
// stored in <home>/connections.json, mode 0600).

// ConnectionKind is the protocol family of a connection.
type ConnectionKind string

const (
	ConnHTTP     ConnectionKind = "http"
	ConnPostgres ConnectionKind = "postgres"
	ConnMySQL    ConnectionKind = "mysql"
	ConnRedis    ConnectionKind = "redis"
	ConnMongo    ConnectionKind = "mongodb"
	ConnAWS      ConnectionKind = "aws"
)

// HTTPAuth is how HTTP requests of a connection authenticate.
type HTTPAuth struct {
	Type     string `json:"type"`               // none | bearer | basic | api_key
	Token    string `json:"token,omitempty"`    // secret: bearer token or API key value
	Username string `json:"username,omitempty"` // basic
	Password string `json:"password,omitempty"` // secret: basic
	Header   string `json:"header,omitempty"`   // api_key header name (default X-API-Key)
}

// Connection is a saved service. Fields used depend on Kind:
//   - http: BaseURL, Headers, Auth, InsecureSkipVerify
//   - postgres/mysql/redis/mongodb: URI (full connection string) or Host,
//     Port, Username, Password, Database, TLS
//   - aws: Profile (shared config/SSO profile; "" = default chain), Region
//
// Secrets (Password, URI, Auth.Token, Auth.Password) are never returned by
// the API: responses blank them and list the stored ones in SecretsSet. On
// update, a blank secret keeps the stored value; names in ClearSecrets
// ("password", "uri", "auth.token", "auth.password") are removed.
type Connection struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Kind        ConnectionKind `json:"kind"`
	Description string         `json:"description,omitempty"`

	BaseURL            string            `json:"baseUrl,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
	Auth               *HTTPAuth         `json:"auth,omitempty"`
	InsecureSkipVerify bool              `json:"insecureSkipVerify,omitempty"`

	URI      string `json:"uri,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Database string `json:"database,omitempty"` // redis: DB number as string
	TLS      bool   `json:"tls,omitempty"`

	Profile string `json:"profile,omitempty"`
	Region  string `json:"region,omitempty"`

	SecretsSet   []string  `json:"secretsSet,omitempty"`   // response only
	ClearSecrets []string  `json:"clearSecrets,omitempty"` // request only
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// ConnectionCheck is the result of POST /connections/{id}/test.
type ConnectionCheck struct {
	OK        bool    `json:"ok"`
	LatencyMs float64 `json:"latencyMs"`
	Message   string  `json:"message"`          // e.g. "PostgreSQL 16.4", "HTTP 200", "AWS account 1234…"
	Detail    string  `json:"detail,omitempty"` // error detail when !OK
}

// ---------------------------------------------------------------------------
// Settings and AI agent (MCP client) setup

// Settings are the user's app preferences (GET/PUT /settings), stored in
// <home>/settings.json. Sandbox fields apply to sandboxes started afterwards.
type Settings struct {
	DefaultMode      string           `json:"defaultMode"`      // "guided" | "pro"
	Docker           string           `json:"docker"`           // docker CLI binary
	Images           map[Cloud]string `json:"images"`           // emulator image per cloud
	TerraformImage   string           `json:"terraformImage"`   // image running terraform
	DefaultAWSRegion string           `json:"defaultAWSRegion"` // new AWS provider blocks, sandbox AWS_REGION
	// BestPractices configures the best-practice checks (bp_* diagnostics).
	BestPractices BestPracticeSettings `json:"bestPractices"`
}

// BestPracticeSettings turns the best-practice checks off as a whole or per
// rule. The zero value runs every rule.
type BestPracticeSettings struct {
	Disabled      bool     `json:"disabled,omitempty"`
	DisabledRules []string `json:"disabledRules"` // rule codes, e.g. "bp_missing_tags"
}

// BestPracticeRule describes one best-practice check (GET /best-practices/rules).
type BestPracticeRule struct {
	Code        string   `json:"code"` // "bp_*"
	Title       string   `json:"title"`
	Category    string   `json:"category"` // inputs | versions | hardcoding | tagging | naming | security | structure
	Severity    Severity `json:"severity"` // default severity (some findings of a rule may be lower)
	Description string   `json:"description"`
}

// AgentClient is an AI agent application Nimbo can register its MCP server
// with (GET /agents).
type AgentClient struct {
	ID         string `json:"id"` // "claude-code", "claude-desktop", "cursor", "codex", "vscode", "windsurf", "gemini-cli"
	Name       string `json:"name"`
	Detected   bool   `json:"detected"`   // app, CLI or config directory present
	Configured bool   `json:"configured"` // a "nimbo" MCP server entry exists
	ConfigPath string `json:"configPath"` // file Install edits
	Snippet    string `json:"snippet"`    // manual setup: config fragment or command
	Note       string `json:"note,omitempty"`
}

// ---------------------------------------------------------------------------
// Setup diagnostics (GET /setup, `nimbo doctor`) and one-click fixes
// (POST /setup/actions/{action})

// SetupCheckStatus is the outcome of one setup check.
type SetupCheckStatus string

const (
	SetupOK      SetupCheckStatus = "ok"
	SetupMissing SetupCheckStatus = "missing" // not installed / not done yet
	SetupWarning SetupCheckStatus = "warning" // works, but may cause trouble
	SetupError   SetupCheckStatus = "error"   // present but broken
)

// Setup fix kinds.
const (
	FixLink    = "link"    // open URL
	FixAction  = "action"  // POST /setup/actions/{Action}
	FixCommand = "command" // shell command the user can copy
)

// SetupFix is one way to resolve a failed check.
type SetupFix struct {
	Kind    string `json:"kind"` // link | action | command
	Label   string `json:"label"`
	URL     string `json:"url,omitempty"`
	Action  string `json:"action,omitempty"`  // id for POST /setup/actions/{action}
	Command string `json:"command,omitempty"` // shell command the user can copy
}

// SetupCheck is one requirement, explained for someone new to Docker.
type SetupCheck struct {
	ID       string           `json:"id"` // wsl (Windows) | docker-cli | docker-daemon | docker-permissions (Linux) | images | docker-socket | docker-memory | disk | internet
	Title    string           `json:"title"`
	Status   SetupCheckStatus `json:"status"`
	Required bool             `json:"required"`
	Summary  string           `json:"summary"` // one line, e.g. "Docker 29.8 via OrbStack"
	Detail   string           `json:"detail"`  // beginner explanation
	Fixes    []SetupFix       `json:"fixes,omitempty"`
}

// SetupStatus is the body of GET /setup.
type SetupStatus struct {
	Ready          bool         `json:"ready"`          // every Required check is ok
	Platform       string       `json:"platform"`       // darwin | linux | windows
	Arch           string       `json:"arch"`           // arm64 | amd64 | …
	OSName         string       `json:"osName"`         // e.g. "macOS 26.0", "Ubuntu 24.04 LTS", "Windows 11"
	Distro         string       `json:"distro"`         // Linux os-release ID (ubuntu, fedora, arch, …); "" elsewhere
	PackageManager string       `json:"packageManager"` // brew | apt | dnf | pacman | zypper | winget | choco | ""
	Checks         []SetupCheck `json:"checks"`
}

// Setup actions (POST /setup/actions/{action}).
const (
	SetupActionStartDocker = "start-docker"
	SetupActionPullImages  = "pull-images"
)

// SetupProgress is the Data of a setup.progress event. Current/Total count
// the action's units (pull-images: images finished; start-docker: 0 or 1 of
// 1). The last event of an action has Done set, and Error when it failed.
type SetupProgress struct {
	Action  string `json:"action"`
	Image   string `json:"image,omitempty"`
	Step    string `json:"step"` // start | pull | skip | image-done | waiting | done
	Current int    `json:"current"`
	Total   int    `json:"total"`
	Message string `json:"message"`
	// LayersDone/LayersTotal: layer progress of Image while Step is "pull".
	LayersDone  int    `json:"layersDone,omitempty"`
	LayersTotal int    `json:"layersTotal,omitempty"`
	Done        bool   `json:"done"`
	Error       string `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// Events (server-sent events on GET /api/v1/events)

// Event types.
const (
	EventWorkflowUpdated    = "workflow.updated"    // Data: Workflow
	EventWorkflowDeleted    = "workflow.deleted"    // Data: {"id": string}
	EventSandboxStatus      = "sandbox.status"      // Data: SandboxStatus
	EventRunStatus          = "run.status"          // Data: Run (without Log)
	EventRunLog             = "run.log"             // Data: {"runId": string, "line": LogLine}
	EventStateUpdated       = "state.updated"       // Data: DeployedState
	EventLoadTestSample     = "loadtest.sample"     // Data: {"runId": string, "sample": LoadSample}
	EventLoadTestStatus     = "loadtest.status"     // Data: LoadTestRun (without Timeline)
	EventConnectionsUpdated = "connections.updated" // Data: {"id": string}; after create/update/delete
	EventSetupProgress      = "setup.progress"      // Data: SetupProgress (no workflow ID)
)

// Event is pushed to SSE subscribers.
type Event struct {
	Type       string `json:"type"`
	WorkflowID string `json:"workflowId,omitempty"`
	Data       any    `json:"data"`
}

// Publisher fans events out to subscribers. Implementations must be safe for
// concurrent use and must not block.
type Publisher interface {
	Publish(Event)
}

// ---------------------------------------------------------------------------
// Folder import

// ImportScan is the result of POST /import/scan: every Terraform project
// (a directory with *.tf files that no other scanned directory uses as a
// local module) found under Root.
type ImportScan struct {
	Root     string          `json:"root"`
	Projects []ImportProject `json:"projects"`
	// Skipped lists directories that are not offered as projects, with the
	// reason, e.g. "modules/network: local module used by envs/dev".
	Skipped []string `json:"skipped"`
}

// ImportProject summarizes one importable Terraform root module.
type ImportProject struct {
	Path        string       `json:"path"` // slash path relative to Root; "." = Root itself
	Name        string       `json:"name"` // suggested workflow name
	Resources   int          `json:"resources"`
	DataSources int          `json:"dataSources"`
	Modules     int          `json:"modules"` // module calls in the project root
	Providers   []string     `json:"providers"`
	Clouds      []Cloud      `json:"clouds"`
	TfvarsFiles []string     `json:"tfvarsFiles"` // relative to Path; each becomes an environment
	HasBackend  bool         `json:"hasBackend"`  // runs with local state in the sandbox
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// ImportResult is the response of POST /import: one workflow per project.
type ImportResult struct {
	Workflows []Workflow `json:"workflows"`
}

// ModuleView is the read-only content of a module call
// (GET /workflows/{id}/modules/{nodeId}). Local modules (copied into the
// workflow files) carry Workflow with unpositioned nodes; registry and
// remote modules carry only Message.
type ModuleView struct {
	Source string `json:"source"`
	Local  bool   `json:"local"`
	// Dir is the module's directory relative to the workflow files
	// ("modules/network") when it is a local module inside them.
	Dir      string    `json:"dir,omitempty"`
	Workflow *Workflow `json:"workflow,omitempty"`
	Message  string    `json:"message,omitempty"`
}

// ModuleTree is the content of every module call of a workflow, nested calls
// included (GET /workflows/{id}/modules), for the Architecture diagram.
type ModuleTree struct {
	// Calls maps a call path ("module.app", "module.app.module.db") to where
	// its content comes from.
	Calls map[string]ModuleCallSource `json:"calls"`
	// Modules are the module directories read, as workflows (nodes
	// unpositioned), by ModuleCallSource.Dir.
	Modules map[string]*Workflow `json:"modules"`
}

// ModuleCallSource is the source of one module call. Dir is set when the
// content is readable: a local module inside the workflow files
// ("modules/network"), or a registry/remote module that `terraform init`
// downloaded into the sandbox working directory (".terraform/modules/vpc").
type ModuleCallSource struct {
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
	Local   bool   `json:"local"`
	Dir     string `json:"dir,omitempty"`
}

// ModuleExtractRequest is the body of POST /workflows/{id}/modules/extract
// (and .../extract/preview): the nodes (IDs or addresses) to move into a new
// local module named Name (snake_case) at modules/<name>.
type ModuleExtractRequest struct {
	NodeIDs []string `json:"nodeIds"`
	Name    string   `json:"name"`
}

// ModuleExtraction describes what extracting nodes into a local module
// creates (POST /workflows/{id}/modules/extract/preview).
type ModuleExtraction struct {
	Name   string `json:"name"`
	Source string `json:"source"` // "./modules/<name>"
	// Inputs are the module's variables: values the moved blocks read from
	// the root (Value = the expression the module call passes).
	Inputs []ModuleIO `json:"inputs"`
	// Outputs are what the root still reads from the moved blocks (Value =
	// the expression inside the module); the root references become
	// module.<name>.<output>.
	Outputs []ModuleIO `json:"outputs"`
	// Moved are the `moved` blocks added to the root so existing state moves
	// into the module instead of being destroyed and recreated.
	Moved []MovedAddress `json:"moved"`
	// Providers are the provider configurations passed through `providers`
	// (only when a non-default alias is used; otherwise defaults inherit).
	Providers []string `json:"providers"`
	// DependsOn are root addresses the module call depends on (from
	// depends_on of moved blocks).
	DependsOn []string `json:"dependsOn"`
	// Files are the created module files, relative to the workflow root.
	Files []string `json:"files"`
}

// ModuleIO is one input variable or output of a module.
type ModuleIO struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"` // inputs: Terraform type constraint
	Description string `json:"description"`
	Value       string `json:"value"`
	Sensitive   bool   `json:"sensitive,omitempty"`
}

// MovedAddress is one `moved { from = From  to = To }` block.
type MovedAddress struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// FileKind groups the files of a workflow's export.
type FileKind string

const (
	FileTerraform FileKind = "terraform" // root *.tf
	FileTfvars    FileKind = "tfvars"    // environment values
	FileEnv       FileKind = "env"       // *.env.example
	FileModule    FileKind = "module"    // local modules (modules/**)
	// FileScaffold are the project files export generates next to the
	// Terraform (README.md, .gitignore, terraform.tfvars.example) unless the
	// workflow has its own file of that name.
	FileScaffold FileKind = "scaffold"
	FileAsset    FileKind = "asset" // everything else: Lambda sources, templates, the lock file
)

// WorkflowFile is one file an export writes (GET /workflows/{id}/files).
// Generated files are rendered from the workflow; the others are assets
// stored with it. Editable generated files change through
// PUT /workflows/{id}/terraform, editable assets through
// PUT /workflows/{id}/files/content. Language names the syntax: "hcl",
// "json", "javascript", "typescript", "python", "go", "yaml", "markdown",
// "shell" or "text".
type WorkflowFile struct {
	Path      string   `json:"path"` // slash-separated, relative to the export root
	Size      int64    `json:"size"`
	Kind      FileKind `json:"kind"`
	Generated bool     `json:"generated"`
	Editable  bool     `json:"editable"`
	Binary    bool     `json:"binary"`
	Language  string   `json:"language"`
}

// WorkflowFileContent is a text file with its content
// (GET /workflows/{id}/files/content).
type WorkflowFileContent struct {
	WorkflowFile
	Content string `json:"content"`
}
