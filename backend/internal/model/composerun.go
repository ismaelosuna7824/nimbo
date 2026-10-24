package model

import "time"

// ---------------------------------------------------------------------------
// Docker compose runtime (docker workflows): runs use the compose actions of
// RunAction; see sandbox.ComposeRun.

// ComposeState summarizes containers of a compose project or service.
type ComposeState string

const (
	ComposeAbsent   ComposeState = "absent"   // no containers
	ComposeStarting ComposeState = "starting" // created, restarting or health "starting"
	ComposeRunning  ComposeState = "running"  // every container running (and healthy when checked)
	ComposeStopped  ComposeState = "stopped"  // every container exited with code 0
	ComposeError    ComposeState = "error"    // exited != 0, unhealthy or dead
)

// ComposeStatus is the live state of a docker workflow's compose project
// (GET /workflows/{id}/compose/status, event compose.status).
type ComposeStatus struct {
	WorkflowID string                 `json:"workflowId"`
	Project    string                 `json:"project"` // nimbo-dk-<workflow id>
	State      ComposeState           `json:"state"`
	Services   []ComposeServiceStatus `json:"services"`
	Remaps     []PortRemap            `json:"remaps"`
	Message    string                 `json:"message,omitempty"`
	// Problems: OOM kills, restarts, non-zero exits and unhealthy containers.
	Problems  []ComposeProblem `json:"problems"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// ComposeServiceStatus is one service of the compose files with its
// containers (none while absent). Health is the worst container health.
type ComposeServiceStatus struct {
	Name       string             `json:"name"`
	NodeID     string             `json:"nodeId"`
	State      ComposeState       `json:"state"`
	Health     string             `json:"health,omitempty"` // healthy | unhealthy | starting
	Image      string             `json:"image,omitempty"`  // image reference of the compose files ("" for build-only services)
	Containers []ComposeContainer `json:"containers"`
	// StartupMs: start → healthy of the slowest container (services with a
	// healthcheck, once healthy).
	StartupMs *int64 `json:"startupMs,omitempty"`
}

// ComposeContainer is one container of a compose service.
type ComposeContainer struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	State      string     `json:"state"`            // created | running | restarting | paused | exited | dead | removing
	Health     string     `json:"health,omitempty"` // healthy | unhealthy | starting
	ExitCode   int        `json:"exitCode"`
	Restarts   int        `json:"restarts"`
	Image      string     `json:"image"`
	ImageID    string     `json:"imageId"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	OOMKilled  bool       `json:"oomKilled,omitempty"`
	// StartupMs: start → first healthy check (containers with a healthcheck).
	StartupMs *int64 `json:"startupMs,omitempty"`
	// CPULimit (cores) and MemLimit (bytes) are the container's limits; 0 = none.
	CPULimit float64       `json:"cpuLimit,omitempty"`
	MemLimit int64         `json:"memLimit,omitempty"`
	Ports    []ComposePort `json:"ports"`
}

// ComposePort is a container port published on the host. URL is set for
// TCP ports reachable on 127.0.0.1.
type ComposePort struct {
	Container int    `json:"container"`
	Protocol  string `json:"protocol"` // tcp | udp
	HostIP    string `json:"hostIp"`
	Host      int    `json:"host"`
	URL       string `json:"url,omitempty"`
}

// PortConflict is a fixed host port (or port range) of the compose files
// that another program or container already holds.
type PortConflict struct {
	Service       string `json:"service"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"hostIp,omitempty"`
	HostPort      int    `json:"hostPort"`
	// Count is the size of a published range ("3100-3101": HostPort 3100,
	// Count 2); 0 for a single port.
	Count         int    `json:"count,omitempty"`
	Holder        string `json:"holder,omitempty"` // process name when known
	SuggestedPort int    `json:"suggestedPort"`    // free port (first of Count free ports) a remap would use
}

// PortRemap moves a published port of the compose files (HostPort) to Port
// for Nimbo's runs, through an override file; the user's files are unchanged.
// A range (Count > 1) moves as a whole to Port..Port+Count-1.
type PortRemap struct {
	Service       string `json:"service"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
	HostPort      int    `json:"hostPort"`
	Count         int    `json:"count,omitempty"`
	Port          int    `json:"port"`
}

// ComposePorts is the port pre-check of an up (GET /workflows/{id}/compose/ports).
type ComposePorts struct {
	Conflicts []PortConflict `json:"conflicts"`
	Remaps    []PortRemap    `json:"remaps"`
}

// ComposeLogLine is one line of a compose service's container log.
type ComposeLogLine struct {
	Service   string  `json:"service"`
	Container string  `json:"container"`
	Line      LogLine `json:"line"`
}
