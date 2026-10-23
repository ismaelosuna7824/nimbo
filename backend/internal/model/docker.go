package model

// Docker workflows: a docker-compose project (compose files, Dockerfiles,
// .env and build contexts stored as the workflow's files) instead of a
// Terraform root module. The files are the source of truth; Compose.Graph
// is derived from them on every save, like Edges for Terraform.

// WorkflowDocker is Workflow.Kind of a docker-compose workflow ("" is a
// Terraform workflow).
const WorkflowDocker = "docker"

// IsDocker reports whether w is a docker-compose workflow.
func (w *Workflow) IsDocker() bool { return w.Kind == WorkflowDocker }

// ComposeProjectName is the compose project every container, network and
// volume Nimbo starts for workflow id belongs to.
func ComposeProjectName(id string) string { return "nimbo-dk-" + id }

// ComposeServiceID is the graph node ID of a compose service.
func ComposeServiceID(name string) string { return "service:" + name }

// ComposeModel is the compose project of a docker workflow.
type ComposeModel struct {
	// Files are the compose files in `-f` order, slash paths relative to
	// the workflow files (the project directory). Overrides come later.
	Files []string `json:"files"`
	// Project is the compose project name runs use (ComposeProjectName).
	Project string `json:"project"`
	// Profiles are the active profiles; services of other profiles are in
	// the graph with Inactive set.
	Profiles []string `json:"profiles,omitempty"`
	// Derived on every save (read-only for clients).
	// Name is the top-level `name:` of the files (display only).
	Name string `json:"name,omitempty"`
	// Included are the files reached through `include:` and `extends.file`.
	Included []string `json:"included,omitempty"`
	// Dockerfiles are the Dockerfiles the services build.
	Dockerfiles []string `json:"dockerfiles,omitempty"`
	// Unresolved are the variables the files interpolate (${VAR}) without
	// a value in .env and without a default.
	Unresolved []string     `json:"unresolved,omitempty"`
	Graph      ComposeGraph `json:"graph"`
}

// ComposeGraph is the canvas of a docker workflow.
type ComposeGraph struct {
	Nodes []ComposeNode `json:"nodes"`
	Edges []ComposeEdge `json:"edges"`
}

// Compose node kinds (ComposeNode.Kind).
const (
	ComposeService    = "service"
	ComposeBuildStage = "build-stage"
	ComposeNetwork    = "network"
	ComposeVolume     = "volume"
	ComposeConfig     = "config"
	ComposeSecret     = "secret"
	// ComposeExternal is an external network or volume (ExternalKind says
	// which) or a service another file's service extends.
	ComposeExternal = "external"
)

// ComposeNode is one node of a compose graph. IDs are stable across saves:
// "service:<name>", "stage:<service>:<stage name or index>",
// "network:<name>", "volume:<name>", "config:<name>", "secret:<name>",
// "extends:<file>#<service>".
type ComposeNode struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Parent is the service of a build stage.
	Parent string `json:"parent,omitempty"`
	// Position is the saved canvas position; nil = auto layout.
	Position *Position `json:"position,omitempty"`
	// File and Line locate the declaration (1-based line).
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	// External marks external networks/volumes/configs/secrets;
	// ExternalKind is "network", "volume", "config", "secret" or "service"
	// for kind ComposeExternal.
	External     bool   `json:"external,omitempty"`
	ExternalKind string `json:"externalKind,omitempty"`
	// Inactive: a service of a profile that is not active.
	Inactive bool           `json:"inactive,omitempty"`
	Service  *ServiceFacts  `json:"service,omitempty"`
	Stage    *StageFacts    `json:"stage,omitempty"`
	Network  *NetworkFacts  `json:"network,omitempty"`
	Volume   *VolumeFacts   `json:"volume,omitempty"`
	Resource *ResourceFacts `json:"resource,omitempty"` // configs and secrets
}

// ServiceFacts describe a service for the inspector and its icon.
type ServiceFacts struct {
	Image *ImageRef     `json:"image,omitempty"`
	Build *ServiceBuild `json:"build,omitempty"`
	// BaseImages are the external FROM images of every stage of the
	// Dockerfile (ARG defaults applied), final stage last.
	BaseImages []string `json:"baseImages"`
	// Stack are technology hints read from the build context (package.json
	// dependencies, go.mod, pom.xml, ...): "node", "express", "nextjs",
	// "go", "spring", "dotnet", ... for icons.
	Stack       []string            `json:"stack,omitempty"`
	Ports       []ServicePort       `json:"ports"`
	Expose      []string            `json:"expose"` // expose: plus the Dockerfile's EXPOSE
	Command     string              `json:"command,omitempty"`
	Entrypoint  string              `json:"entrypoint,omitempty"`
	Healthcheck *ServiceHealthcheck `json:"healthcheck,omitempty"`
	// HealthcheckFrom is "compose" or "dockerfile" (HEALTHCHECK).
	HealthcheckFrom string            `json:"healthcheckFrom,omitempty"`
	Restart         string            `json:"restart,omitempty"`
	Replicas        *int              `json:"replicas,omitempty"`
	Resources       *ServiceResources `json:"resources,omitempty"`
	Profiles        []string          `json:"profiles,omitempty"`
	// Environment: values of secret-looking keys are left out (Secret set).
	Environment   []ServiceEnv        `json:"environment"`
	EnvFiles      []string            `json:"envFiles,omitempty"`
	Volumes       []ServiceMount      `json:"volumes"`
	Networks      []string            `json:"networks"`
	DependsOn     []ServiceDependency `json:"dependsOn"`
	Links         []string            `json:"links,omitempty"`
	ContainerName string              `json:"containerName,omitempty"`
	Hostname      string              `json:"hostname,omitempty"`
	User          string              `json:"user,omitempty"`
	WorkingDir    string              `json:"workingDir,omitempty"`
	Platform      string              `json:"platform,omitempty"`
	// Extends is the service this one extends ("svc" or "file#svc").
	Extends string `json:"extends,omitempty"`
}

// ImageRef is a parsed image reference ("postgres:16-alpine").
type ImageRef struct {
	Ref    string `json:"ref"`
	Repo   string `json:"repo"` // "postgres", "bitnami/redis", "ghcr.io/org/app"
	Tag    string `json:"tag,omitempty"`
	Digest string `json:"digest,omitempty"`
}

// ServiceBuild is a service's `build:`.
type ServiceBuild struct {
	Context    string            `json:"context"`    // as the compose files write it: relative to the project directory ("../services/api")
	Dockerfile string            `json:"dockerfile"` // the workflow file it builds, relative to the workflow's files
	Target     string            `json:"target,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
}

// ServicePort is one `ports:` entry.
type ServicePort struct {
	HostIP    string `json:"hostIp,omitempty"`
	Host      string `json:"host,omitempty"` // published port or range; "" = ephemeral
	Container string `json:"container"`
	Protocol  string `json:"protocol,omitempty"` // tcp (default) | udp
	Published bool   `json:"published"`          // Host is set
}

// ServiceEnv is one environment variable of a service.
type ServiceEnv struct {
	Key   string  `json:"key"`
	Value *string `json:"value,omitempty"` // nil: secret, or a bare key passed from the host
	// Raw is the value as written in the compose file when it differs from
	// Value (it interpolates ${VAR}s or escapes $$): what an editor shows and
	// sends back, so a variable's value (a password from .env) never gets
	// copied into the file. Not set for secrets.
	Raw *string `json:"raw,omitempty"`
	// Secret: the key looks like a credential; its value is not sent.
	Secret bool `json:"secret,omitempty"`
	// FromFile names the env_file the value comes from.
	FromFile string `json:"fromFile,omitempty"`
	// Unresolved: the value interpolates a variable without a value.
	Unresolved bool `json:"unresolved,omitempty"`
}

// ServiceMount is one `volumes:` entry of a service.
type ServiceMount struct {
	Type     string `json:"type"`             // volume | bind | tmpfs
	Source   string `json:"source,omitempty"` // volume name, or bind host path (relative to the project directory when inside the workflow)
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

// ServiceDependency is one `depends_on:` entry.
type ServiceDependency struct {
	Service   string `json:"service"`
	Condition string `json:"condition"` // service_started | service_healthy | service_completed_successfully
	Required  *bool  `json:"required,omitempty"`
	Restart   bool   `json:"restart,omitempty"`
}

// ServiceHealthcheck is a service's (or its image's HEALTHCHECK) check.
type ServiceHealthcheck struct {
	Test          string `json:"test"`
	Interval      string `json:"interval,omitempty"`
	Timeout       string `json:"timeout,omitempty"`
	Retries       *int   `json:"retries,omitempty"`
	StartPeriod   string `json:"startPeriod,omitempty"`
	StartInterval string `json:"startInterval,omitempty"`
	Disabled      bool   `json:"disabled,omitempty"`
}

// ServiceResources are deploy.resources.limits.
type ServiceResources struct {
	CPUs   string `json:"cpus,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// StageFacts describe one stage of a service's Dockerfile.
type StageFacts struct {
	Index int    `json:"index"`
	Name  string `json:"name,omitempty"`
	// From is the stage's base as written with ARGs applied; FromStage is
	// set when it builds on another stage of the same Dockerfile.
	From       string   `json:"from"`
	FromStage  string   `json:"fromStage,omitempty"`
	Platform   string   `json:"platform,omitempty"`
	Dockerfile string   `json:"dockerfile"`
	Line       int      `json:"line"`
	Final      bool     `json:"final,omitempty"` // the stage the service runs (target or last)
	Expose     []string `json:"expose,omitempty"`
	User       string   `json:"user,omitempty"`
	Workdir    string   `json:"workdir,omitempty"`
	Cmd        string   `json:"cmd,omitempty"`
	Entrypoint string   `json:"entrypoint,omitempty"`
}

// NetworkFacts describe a network.
type NetworkFacts struct {
	Driver   string `json:"driver,omitempty"`
	Internal bool   `json:"internal,omitempty"`
	Name     string `json:"name,omitempty"` // `name:` (the real network name)
}

// VolumeFacts describe a named volume.
type VolumeFacts struct {
	Driver string `json:"driver,omitempty"`
	Name   string `json:"name,omitempty"`
}

// ResourceFacts describe a config or secret.
type ResourceFacts struct {
	File        string `json:"file,omitempty"`
	Environment string `json:"environment,omitempty"`
	Content     bool   `json:"content,omitempty"` // inline `content:`
}

// Compose edge kinds (ComposeEdge.Kind). An edge's Source is used by its
// Target: the dependency, network, volume, config or secret of a service,
// the stage another stage or the service builds on.
const (
	ComposeEdgeDependsOn = "depends_on"
	ComposeEdgeLinks     = "links"
	ComposeEdgeNetwork   = "network"
	ComposeEdgeVolume    = "volume"
	ComposeEdgeConfig    = "config"
	ComposeEdgeSecret    = "secret"
	ComposeEdgeBuild     = "build"     // FROM <stage>, and final stage -> service
	ComposeEdgeCopyFrom  = "copy_from" // COPY --from=<stage>
	ComposeEdgeExtends   = "extends"
	// ComposeEdgeInferred: the target's environment names the source's
	// host (DATABASE_URL=postgres://db:5432/app).
	ComposeEdgeInferred = "inferred"
)

// ComposeEdge is derived from the files; it is never authored directly.
type ComposeEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	// Label is a short caption: the depends_on condition, the mount path,
	// the env key of an inferred edge.
	Label     string `json:"label,omitempty"`
	Condition string `json:"condition,omitempty"` // depends_on
	Path      string `json:"path,omitempty"`      // volume/config/secret target in the container
	ReadOnly  bool   `json:"readOnly,omitempty"`
	EnvKey    string `json:"envKey,omitempty"` // inferred
}

// DockerImage is a curated image of the service palette
// (GET /catalog/docker/images).
type DockerImage struct {
	ID          string              `json:"id"`
	Image       string              `json:"image"` // repository, e.g. "postgres"
	Tag         string              `json:"tag"`
	Label       string              `json:"label"`
	Description string              `json:"description"`
	Category    string              `json:"category"` // web | gateway | language | database | cache | search | messaging | storage | observability | auth | workflow | ai | devtools | apps | cms | network | tools
	Icon        string              `json:"icon"`     // tech icon id
	Ports       []int               `json:"ports"`
	Env         []DockerImageEnv    `json:"env,omitempty"`
	Healthcheck *ServiceHealthcheck `json:"healthcheck,omitempty"`
	Volumes     []DockerImageVolume `json:"volumes,omitempty"`
	// Connect are the variables a client service sets to reach this one:
	// templates with {{host}}, {{port}} and {{env.KEY}} (this image's env).
	Connect []DockerImageConnect `json:"connect,omitempty"`
	Command string               `json:"command,omitempty"`
	// Entrypoint replaces the image's entrypoint (e.g. "sh -c" to chain a setup step before Command).
	Entrypoint string `json:"entrypoint,omitempty"`
}

// DockerImageEnv is an environment variable an image reads.
type DockerImageEnv struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Secret      bool   `json:"secret,omitempty"`
	Description string `json:"description,omitempty"`
}

// DockerImageVolume is a data path worth a named volume.
type DockerImageVolume struct {
	Target string `json:"target"`
	Name   string `json:"name,omitempty"` // suggested volume name suffix
}

// DockerImageConnect is one variable of a client's connection settings.
type DockerImageConnect struct {
	Key      string `json:"key"`
	Template string `json:"template"`
}
