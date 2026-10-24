package model

// Access editors: structured views of the permissions a node grants, for
// every provider's permission model (AWS policy documents, Azure role
// assignments, GCP IAM members/bindings, OCI policy statements, Kubernetes
// RBAC). The desktop inspector and the MCP tools read and write them
// through these types instead of raw HCL/YAML; whatever an editor can't
// represent is kept verbatim (Editable=false, Source) and never rewritten.
//
// Expressions: fields documented as "HCL expression" hold Terraform source
// ("aws_dynamodb_table.orders.arn", "\"*\"", "\"${aws_s3_bucket.b.arn}/*\"").

// AccessLevel is the coarse grant of the drag-identity-onto-resource
// interaction ("What can it do?").
type AccessLevel string

const (
	AccessRead   AccessLevel = "read"
	AccessWrite  AccessLevel = "write"
	AccessManage AccessLevel = "manage"
)

// AccessLevels in display order.
var AccessLevels = []AccessLevel{AccessRead, AccessWrite, AccessManage}

// AccessRecipeGroup is ConnectionRecipe.Group of grant recipes; their IDs
// are AccessRecipePrefix + level ("access:read").
const (
	AccessRecipeGroup  = "access"
	AccessRecipePrefix = "access:"
)

// AccessView is GET /workflows/{id}/access?node=: the access editors of one
// Terraform node. Exactly the field of Provider is set.
type AccessView struct {
	NodeID   string `json:"nodeId"`
	Provider string `json:"provider"` // aws | azure | gcp | oci
	// AWS holds one document per policy on the node: "policy",
	// "assume_role_policy", "inline_policy[0].policy", or "" for the
	// statements of a data "aws_iam_policy_document".
	AWS   []AWSPolicyDocument `json:"aws,omitempty"`
	Azure *AzureRoleAssignment `json:"azure,omitempty"`
	GCP   *GCPIAMBinding       `json:"gcp,omitempty"`
	OCI   *OCIPolicy           `json:"oci,omitempty"`
	// Related are other nodes of the same permission model worth opening
	// from this one (a role's inline policies, the document a policy reads).
	Related  []AccessRelated `json:"related,omitempty"`
	Warnings []AccessWarning `json:"warnings"`
}

// AccessEdit is PUT /workflows/{id}/access: the new content of one editor
// of NodeID (exactly one provider field). The response is the Workflow.
type AccessEdit struct {
	NodeID string               `json:"nodeId"`
	AWS    *AWSPolicyDocument   `json:"aws,omitempty"` // matched by Path
	Azure  *AzureRoleAssignment `json:"azure,omitempty"`
	GCP    *GCPIAMBinding       `json:"gcp,omitempty"`
	OCI    *OCIPolicy           `json:"oci,omitempty"`
}

// AccessRelated links another node from an access view.
type AccessRelated struct {
	NodeID  string `json:"nodeId"`
	Address string `json:"address"`
	Label   string `json:"label"` // "Inline policy", "Policy document", "Role assignment"
}

// AccessWarning is a least-privilege warning of an access view.
type AccessWarning struct {
	// Code: aws_admin, aws_wildcard_action, aws_wildcard_resource,
	// aws_public_principal, azure_privileged_role, azure_broad_scope,
	// gcp_basic_role, gcp_public_member, gcp_project_scope, oci_manage_all,
	// oci_tenancy_scope, k8s_cluster_admin, k8s_wildcard, k8s_cluster_scope,
	// k8s_secrets_access, ...
	Code     string `json:"code"`
	Severity string `json:"severity"` // warning | info
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"`
	// Path points into the view: "<document path>/statements/<i>" (AWS),
	// "statements/<i>" (OCI), "rules/<i>" (Kubernetes), "role", "scope",
	// "members/<i>"; "" for the whole node.
	Path string `json:"path,omitempty"`
}

// ---------------------------------------------------------------------------
// AWS

// AWSPolicyDocument is one IAM policy document of a node.
type AWSPolicyDocument struct {
	// Path is the attribute holding the document ("policy",
	// "assume_role_policy", "inline_policy[0].policy"), or "" for a data
	// "aws_iam_policy_document" (its statement blocks).
	Path  string `json:"path"`
	Label string `json:"label"` // "Permissions", "Trust policy", "Bucket policy"
	// Encoding of the source: "jsonencode" (jsonencode({...})), "json" (a
	// JSON string or heredoc), "document" (aws_iam_policy_document blocks),
	// "reference" (points at a policy document node, see Linked) or
	// "unsupported" (anything else; Editable is false).
	Encoding string `json:"encoding"`
	Editable bool   `json:"editable"`
	Reason   string `json:"reason,omitempty"` // why it is read-only
	// Source is the attribute's HCL source (read-only display).
	Source string `json:"source,omitempty"`
	// Linked is the node ID of the data "aws_iam_policy_document" a
	// "reference" document reads (data.x.json).
	Linked     string         `json:"linked,omitempty"`
	Version    string         `json:"version,omitempty"`
	Statements []AWSStatement `json:"statements"`
}

// AWSStatement is one statement. Untouched statements (same Source and
// fields as parsed) are written back byte for byte.
type AWSStatement struct {
	Sid        string         `json:"sid,omitempty"`
	Effect     string         `json:"effect"`    // Allow | Deny
	Actions    []string       `json:"actions"`   // "dynamodb:GetItem"
	Resources  []string       `json:"resources"` // HCL expressions
	Principals []AWSPrincipal `json:"principals,omitempty"`
	// Extra are keys the editor shows read-only and keeps verbatim
	// (Condition, NotAction, NotResource, NotPrincipal, nested blocks).
	Extra    []AccessExtra `json:"extra,omitempty"`
	Editable bool          `json:"editable"`
	Reason   string        `json:"reason,omitempty"`
	// Source is the statement's original source; empty for new statements.
	Source string `json:"source,omitempty"`
}

// AWSPrincipal is a Principal entry: Type is "AWS", "Service",
// "Federated", "CanonicalUser" or "*"; Values are HCL expressions.
type AWSPrincipal struct {
	Type   string   `json:"type"`
	Values []string `json:"values"`
}

// AccessExtra is a key kept verbatim.
type AccessExtra struct {
	Key    string `json:"key"`
	Source string `json:"source"`
}

// ---------------------------------------------------------------------------
// Azure

// AzureRoleAssignment is an azurerm_role_assignment.
type AzureRoleAssignment struct {
	Scope string `json:"scope"` // HCL expression
	// RoleName is role_definition_name (a built-in role name); RoleID is
	// role_definition_id (HCL expression). One of them is set.
	RoleName      string `json:"roleName,omitempty"`
	RoleID        string `json:"roleId,omitempty"`
	PrincipalID   string `json:"principalId"` // HCL expression
	PrincipalType string `json:"principalType,omitempty"`
	Editable      bool   `json:"editable"`
	Reason        string `json:"reason,omitempty"`
}

// ---------------------------------------------------------------------------
// GCP

// GCPIAMBinding is a google_*_iam_member (one member) or
// google_*_iam_binding (members).
type GCPIAMBinding struct {
	Kind string `json:"kind"` // member | binding
	// ResourceAttr names the attribute identifying the resource ("bucket",
	// "project", "secret_id", "name", "topic", ...); Resource is its HCL
	// expression.
	ResourceAttr string `json:"resourceAttr,omitempty"`
	Resource     string `json:"resource,omitempty"`
	// Role is the role ID ("roles/storage.objectViewer").
	Role string `json:"role"`
	// Members are HCL expressions ("\"group:${var.analysts}\"",
	// "\"serviceAccount:${google_service_account.run.email}\"").
	Members []string `json:"members"`
	// Condition is the condition block's source, kept verbatim.
	Condition string `json:"condition,omitempty"`
	Editable  bool   `json:"editable"`
	Reason    string `json:"reason,omitempty"`
}

// ---------------------------------------------------------------------------
// OCI

// OCIPolicy is the statements of an oci_identity_policy.
type OCIPolicy struct {
	Statements []OCIStatement `json:"statements"`
	Editable   bool           `json:"editable"`
	Reason     string         `json:"reason,omitempty"`
}

// OCIStatement is one policy statement:
// "Allow <subjectType> <subjects> to <verb> <resourceType> in <location> [where <condition>]".
// Subjects and Compartment may contain ${...} interpolations (HCL template
// fragments). Non-editable statements keep Source verbatim.
type OCIStatement struct {
	Source       string   `json:"source,omitempty"` // HCL expression of the list element
	Editable     bool     `json:"editable"`
	Reason       string   `json:"reason,omitempty"`
	SubjectType  string   `json:"subjectType,omitempty"` // group | dynamic-group | any-user | any-group | service
	Subjects     []string `json:"subjects,omitempty"`
	Verb         string   `json:"verb,omitempty"`         // inspect | read | use | manage
	ResourceType string   `json:"resourceType,omitempty"` // "buckets", "virtual-network-family", "all-resources"
	Location     string   `json:"location,omitempty"`     // tenancy | compartment
	Compartment  string   `json:"compartment,omitempty"`
	Condition    string   `json:"condition,omitempty"` // text after "where"
}

// ---------------------------------------------------------------------------
// Kubernetes

// KubeAccessView is GET /workflows/{id}/kubernetes/access?node=: the RBAC
// of one object (Role, ClusterRole, RoleBinding, ClusterRoleBinding,
// ServiceAccount or a workload).
type KubeAccessView struct {
	NodeID   string        `json:"nodeId"`
	Kind     string        `json:"kind"`
	Editable bool          `json:"editable"`
	Reason   string        `json:"reason,omitempty"`
	Rules    []KubeRule    `json:"rules,omitempty"`   // Role, ClusterRole
	RoleRef  *KubeRoleRef  `json:"roleRef,omitempty"` // bindings
	Subjects []KubeSubject `json:"subjects,omitempty"`
	// ServiceAccount and AutomountToken: the workload's identity, or the
	// ServiceAccount's own automountServiceAccountToken.
	ServiceAccount string `json:"serviceAccount,omitempty"`
	AutomountToken *bool  `json:"automountToken,omitempty"`
	// Related: bindings of a role, the role of a binding, the bindings and
	// workloads of a service account.
	Related  []AccessRelated `json:"related,omitempty"`
	Warnings []AccessWarning `json:"warnings"`
}

// KubeAccessEdit is PUT /workflows/{id}/kubernetes/access (nil fields are
// unchanged). The response is the Workflow.
type KubeAccessEdit struct {
	NodeID         string        `json:"nodeId"`
	Rules          []KubeRule    `json:"rules,omitempty"`
	RoleRef        *KubeRoleRef  `json:"roleRef,omitempty"`
	Subjects       []KubeSubject `json:"subjects,omitempty"`
	ServiceAccount *string       `json:"serviceAccount,omitempty"`
	AutomountToken *bool         `json:"automountToken,omitempty"`
}

// KubeRule is one PolicyRule.
type KubeRule struct {
	APIGroups       []string `json:"apiGroups"`
	Resources       []string `json:"resources,omitempty"`
	ResourceNames   []string `json:"resourceNames,omitempty"`
	Verbs           []string `json:"verbs"`
	NonResourceURLs []string `json:"nonResourceURLs,omitempty"`
}

// KubeRoleRef is a binding's roleRef.
type KubeRoleRef struct {
	Kind string `json:"kind"` // Role | ClusterRole
	Name string `json:"name"`
}

// KubeSubject is a binding subject.
type KubeSubject struct {
	Kind      string `json:"kind"` // ServiceAccount | User | Group
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// ---------------------------------------------------------------------------
// Reference data

// AccessCatalog is GET /access/catalog/{provider}: the official reference
// data an editor picks from, pinned in the daemon with its source.
type AccessCatalog struct {
	Provider string       `json:"provider"` // aws | azure | gcp | oci | kubernetes
	Source   AccessSource `json:"source"`
	// Services (aws): every service prefix; Actions: the actions of the
	// service asked for (?service=).
	Services []AccessService `json:"services,omitempty"`
	Actions  []AccessAction  `json:"actions,omitempty"`
	// Roles (azure built-in roles, gcp predefined roles); with
	// ?resourceType=<terraform type> the roles suggested for it come first
	// with Suggested set.
	Roles []AccessRole `json:"roles,omitempty"`
	// Verbs and ResourceTypes (oci, kubernetes).
	Verbs         []AccessVerb         `json:"verbs,omitempty"`
	ResourceTypes []AccessResourceType `json:"resourceTypes,omitempty"`
}

// AccessSource cites where pinned reference data comes from.
type AccessSource struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Retrieved string `json:"retrieved"` // YYYY-MM-DD
}

// AccessService is an AWS service (IAM prefix).
type AccessService struct {
	Prefix string `json:"prefix"` // "dynamodb"
	Name   string `json:"name"`   // "Amazon DynamoDB"
}

// AccessAction is an AWS action with its access level ("List", "Read",
// "Write", "Permissions management", "Tagging") and resource types.
type AccessAction struct {
	Name          string   `json:"name"` // "dynamodb:GetItem"
	Level         string   `json:"level"`
	ResourceTypes []string `json:"resourceTypes,omitempty"`
}

// AccessRole is a built-in (Azure) or predefined (GCP) role.
type AccessRole struct {
	ID          string `json:"id"`   // GCP "roles/storage.objectViewer"; Azure GUID
	Name        string `json:"name"` // Azure role name; GCP title
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	// Privileged marks roles that grant broad or administrative access
	// (Owner, Contributor, roles/editor, ...).
	Privileged bool `json:"privileged,omitempty"`
	// Suggested is the level this role is suggested for (with ?resourceType=).
	Suggested AccessLevel `json:"suggested,omitempty"`
}

// AccessVerb is an OCI or Kubernetes verb.
type AccessVerb struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// AccessResourceType is an OCI resource type (individual or family) or a
// Kubernetes resource.
type AccessResourceType struct {
	Name        string `json:"name"`
	Group       string `json:"group,omitempty"` // OCI family / Kubernetes API group
	Description string `json:"description,omitempty"`
}
