package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Label keys stamped on every container and network the sandbox creates.
const (
	labelWorkflow = "io.nimbo.workflow"
	labelRole     = "io.nimbo.role"
	labelCloud    = "io.nimbo.cloud"

	rolePod       = "pod"
	roleEmulator  = "emulator"
	roleTerraform = "terraform"

	podImage          = "alpine:3"
	pluginCacheVolume = "nimbo-tf-plugins"
)

func networkName(wfID string) string { return "nimbo-" + wfID }
func podName(wfID string) string     { return "nimbo-" + wfID + "-pod" }
func emulatorName(wfID string, c string) string {
	return "nimbo-" + wfID + "-" + c
}

// docker runs the docker CLI and returns trimmed stdout. Errors carry the
// subcommand and stderr only, never the full argument list (it may hold
// environment values).
func (m *Manager) docker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, m.dockerBin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("docker %s: %s", args[0], msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// container is the subset of `docker ps --format '{{json .}}'` we use.
type container struct {
	ID       string `json:"ID"`
	Names    string `json:"Names"`
	State    string `json:"State"`
	Image    string `json:"Image"`
	Labels   string `json:"Labels"`
	Networks string `json:"Networks"`
}

func (c container) running() bool { return c.State == "running" }

func (c container) label(key string) string {
	for _, kv := range strings.Split(c.Labels, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// ps lists all containers (running or not) matching the docker filters.
func (m *Manager) ps(ctx context.Context, filters ...string) ([]container, error) {
	args := []string{"ps", "-a", "--no-trunc", "--format", "{{json .}}"}
	for _, f := range filters {
		args = append(args, "--filter", f)
	}
	out, err := m.docker(ctx, args...)
	if err != nil {
		return nil, err
	}
	var cs []container
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var c container
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("parse docker ps output: %w", err)
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// networkExists reports whether a network with exactly this name exists.
func (m *Manager) networkExists(ctx context.Context, name string) (bool, error) {
	out, err := m.docker(ctx, "network", "ls", "--filter", "name=^"+name+"$", "--format", "{{.Name}}")
	if err != nil {
		return false, err
	}
	for _, n := range strings.Split(out, "\n") {
		if n == name {
			return true, nil
		}
	}
	return false, nil
}

// networkContainers lists containers attached to the network (any state).
func (m *Manager) networkContainers(ctx context.Context, network string) ([]container, error) {
	ok, err := m.networkExists(ctx, network)
	if err != nil || !ok {
		return nil, err
	}
	cs, err := m.ps(ctx, "network="+network)
	if err != nil {
		return nil, err
	}
	out := cs[:0]
	for _, c := range cs {
		if slices.Contains(strings.Split(c.Networks, ","), network) {
			out = append(out, c)
		}
	}
	return out, nil
}

// hostPorts reads the published 127.0.0.1 ports of a running container,
// keyed by container port.
func (m *Manager) hostPorts(ctx context.Context, name string) (map[int]int, error) {
	out, err := m.docker(ctx, "port", name)
	if err != nil {
		return nil, err
	}
	return parsePortOutput(out), nil
}

// parsePortOutput parses `docker port` lines like
// "4566/tcp -> 127.0.0.1:55001" (IPv6 lines are ignored).
func parsePortOutput(out string) map[int]int {
	ports := map[int]int{}
	for _, line := range strings.Split(out, "\n") {
		left, right, ok := strings.Cut(strings.TrimSpace(line), " -> ")
		if !ok {
			continue
		}
		cport, _, _ := strings.Cut(left, "/")
		cp, err1 := strconv.Atoi(cport)
		i := strings.LastIndex(right, ":")
		if i < 0 || strings.HasPrefix(right, "[") {
			continue
		}
		hp, err2 := strconv.Atoi(right[i+1:])
		if err1 != nil || err2 != nil {
			continue
		}
		if _, seen := ports[cp]; !seen {
			ports[cp] = hp
		}
	}
	return ports
}

// ensureImage pulls image when it is not present locally, reporting pull
// output lines through progress.
func (m *Manager) ensureImage(ctx context.Context, image string, progress func(string)) error {
	if _, err := m.docker(ctx, "image", "inspect", "--format", "{{.Id}}", image); err == nil {
		return nil
	}
	progress("Pulling " + image)
	cmd := exec.CommandContext(ctx, m.dockerBin(), "pull", image)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	scanLines(stdout, func(line string) {
		if line = strings.TrimSpace(line); line != "" {
			progress("Pulling " + image + ": " + line)
		}
	})
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("docker pull %s: %s", image, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// removeContainers force-removes containers by name or ID and waits until
// they are gone. Missing containers and removals already in progress (e.g.
// a finished `docker run --rm`) are not errors.
func (m *Manager) removeContainers(ctx context.Context, names ...string) error {
	if len(names) == 0 {
		return nil
	}
	_, err := m.docker(ctx, append([]string{"rm", "-f", "-v"}, names...)...)
	if err != nil {
		for _, line := range strings.Split(err.Error(), "\n") {
			if !strings.Contains(line, "No such container") && !strings.Contains(line, "already in progress") {
				return err
			}
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		// inspect exits non-zero when any name is missing but still prints
		// the IDs of the containers that exist.
		out, _ := exec.CommandContext(ctx, m.dockerBin(), append([]string{"inspect", "--format", "{{.Id}}"}, names...)...).Output()
		left := strings.TrimSpace(string(out))
		if left == "" {
			if err := ctx.Err(); err != nil {
				return err
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("containers still present after removal: %s", strings.ReplaceAll(left, "\n", ", "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func isNoSuch(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "No such") || strings.Contains(err.Error(), "not found"))
}
