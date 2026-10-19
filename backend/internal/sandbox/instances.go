package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// InstanceAccess is how the host reaches one EC2 instance container of a sandbox.
type InstanceAccess struct {
	InstanceID string
	Container  string // floci-aws-nimbo-<wf>-ec2-<iid>
	Running    bool
	// Ports maps instance TCP ports to host ports on 127.0.0.1: the app
	// ports published by Floci's socat sidecars (running ones only) plus 22
	// for SSH.
	Ports map[int]int
}

// HTTPAppPorts are the instance ports served as plain HTTP (endpoints and
// load-test suggestions offer a GET on them).
var HTTPAppPorts = []int{80, 8080, 8000, 3000, 5000}

// AppPorts returns the instance's published app ports (SSH excluded),
// sorted; none while the container is not running.
func (a InstanceAccess) AppPorts() []int {
	if !a.Running {
		return nil
	}
	ports := make([]int, 0, len(a.Ports))
	for p := range a.Ports {
		if p != 22 {
			ports = append(ports, p)
		}
	}
	slices.Sort(ports)
	return ports
}

// URL is the host URL of a published instance port (https for the usual
// TLS ports); "" when the port is not published.
func (a InstanceAccess) URL(port int) string {
	hp, ok := a.Ports[port]
	if !ok {
		return ""
	}
	scheme := "http"
	if port == 443 || port == 8443 {
		scheme = "https"
	}
	return fmt.Sprintf("%s://127.0.0.1:%d/", scheme, hp)
}

// maxExecOutput caps ExecInstance's combined output.
const maxExecOutput = 8 << 10

// instancePrefix is the name prefix of the EC2 containers Floci spawns for
// wfID's sandbox: instances `<prefix><iid>` and app-port sidecars
// `<prefix>fwd-<iid>-<port>`.
func instancePrefix(wfID string) string { return "floci-aws-" + networkName(wfID) + "-ec2-" }

func instanceContainer(wfID, instanceID string) string { return instancePrefix(wfID) + instanceID }

var (
	instanceIDPattern = regexp.MustCompile(`^i-[0-9a-f]+$`)
	sidecarPattern    = regexp.MustCompile(`^fwd-(i-[0-9a-f]+)-([0-9]+)$`)
)

// Instances lists the EC2 instance containers of wfID's sandbox by instance
// ID. Docker failures are returned as errors.
func (m *Manager) Instances(ctx context.Context, wfID string) (map[string]InstanceAccess, error) {
	prefix := instancePrefix(wfID)
	out, err := m.docker(ctx, "ps", "-a", "--no-trunc", "--filter", "name=^"+prefix, "--format", "{{.Names}}\t{{.State}}\t{{.Ports}}")
	if err != nil {
		return nil, err
	}
	return parseInstances(prefix, out), nil
}

// parseInstances reads `docker ps` lines "<name>\t<state>\t<ports>" of the
// containers named prefix*. Sidecars only count while running; sidecars of
// instances without a container and names of other workflows (whose
// namespace starts with this one's) are ignored.
func parseInstances(prefix, out string) map[string]InstanceAccess {
	res := map[string]InstanceAccess{}
	type published struct {
		iid            string
		port, hostPort int
	}
	var sidecars []published
	for _, line := range strings.Split(out, "\n") {
		name, rest, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || !strings.HasPrefix(name, prefix) {
			continue
		}
		state, ports, _ := strings.Cut(rest, "\t")
		suffix := strings.TrimPrefix(name, prefix)
		if instanceIDPattern.MatchString(suffix) {
			a := InstanceAccess{InstanceID: suffix, Container: name, Running: state == "running", Ports: map[int]int{}}
			if a.Running {
				if hp, ok := parsePsPorts(ports)[22]; ok {
					a.Ports[22] = hp
				}
			}
			res[suffix] = a
			continue
		}
		mm := sidecarPattern.FindStringSubmatch(suffix)
		if mm == nil || state != "running" {
			continue
		}
		port, err := strconv.Atoi(mm[2])
		if err != nil {
			continue
		}
		if hp, ok := parsePsPorts(ports)[port]; ok {
			sidecars = append(sidecars, published{mm[1], port, hp})
		}
	}
	for _, s := range sidecars {
		if a, ok := res[s.iid]; ok {
			a.Ports[s.port] = s.hostPort
		}
	}
	return res
}

// parsePsPorts parses the Ports column of `docker ps`, e.g.
// "0.0.0.0:30000->80/tcp, :::30000->80/tcp", into TCP container port ->
// host port. Only bindings reachable on 127.0.0.1 count (any address or
// loopback); exposed but unpublished ports ("80/tcp") are skipped and port
// ranges are expanded.
func parsePsPorts(s string) map[int]int {
	ports := map[int]int{}
	for _, part := range strings.Split(s, ",") {
		hostSide, cont, ok := strings.Cut(strings.TrimSpace(part), "->")
		if !ok {
			continue
		}
		cont, proto, _ := strings.Cut(cont, "/")
		if proto != "tcp" {
			continue
		}
		i := strings.LastIndex(hostSide, ":")
		if i < 0 {
			continue
		}
		switch strings.Trim(hostSide[:i], "[]") {
		case "0.0.0.0", "127.0.0.1", "", "::", "::1":
		default:
			continue
		}
		hFrom, hTo, ok1 := portRange(hostSide[i+1:])
		cFrom, cTo, ok2 := portRange(cont)
		if !ok1 || !ok2 || hTo-hFrom != cTo-cFrom {
			continue
		}
		for d := 0; d <= cTo-cFrom; d++ {
			if _, seen := ports[cFrom+d]; !seen {
				ports[cFrom+d] = hFrom + d
			}
		}
	}
	return ports
}

// portRange parses "80" or "30000-30002".
func portRange(s string) (from, to int, ok bool) {
	a, b, isRange := strings.Cut(s, "-")
	from, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, false
	}
	to = from
	if isRange {
		if to, err = strconv.Atoi(b); err != nil || to < from {
			return 0, 0, false
		}
	}
	return from, to, true
}

// ExecInstance runs `sh -c script` inside the instance container (docker
// exec, no TTY) within timeout. It returns the combined output (trimmed,
// capped at 8 KB) and the script's exit code; err is set only when docker
// itself failed: the container is missing or not running, the docker CLI
// could not run, the timeout passed or ctx ended.
func (m *Manager) ExecInstance(ctx context.Context, wfID, instanceID, script string, timeout time.Duration) (output string, exitCode int, err error) {
	name := instanceContainer(wfID, instanceID)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, m.dockerBin(), "exec", name, "sh", "-c", script)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	output = capOutput(buf.String())
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return output, -1, fmt.Errorf("docker exec %s: no answer within %s", name, timeout)
		}
		return output, -1, ctx.Err()
	}
	if runErr == nil {
		return output, 0, nil
	}
	var ee *exec.ExitError
	if !errors.As(runErr, &ee) {
		return output, -1, fmt.Errorf("docker exec: %w", runErr)
	}
	if execFailed(output) {
		return output, ee.ExitCode(), fmt.Errorf("docker exec: %s", output)
	}
	return output, ee.ExitCode(), nil
}

// execFailed reports whether docker exec's output is the CLI's own error
// (missing or stopped container, daemon down) rather than the script's.
func execFailed(out string) bool {
	return strings.HasPrefix(out, "Error response from daemon:") || strings.HasPrefix(out, "Error: No such container") ||
		strings.HasPrefix(out, "Cannot connect to the Docker daemon")
}

func capOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxExecOutput {
		s = strings.ToValidUTF8(s[:maxExecOutput], "") + "\n… (output truncated)"
	}
	return s
}
