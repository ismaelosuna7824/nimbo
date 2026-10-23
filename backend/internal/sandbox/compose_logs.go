package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// MaxComposeLogTail caps the lines ComposeLogs returns per container.
const MaxComposeLogTail = 5000

// ComposeLogs emits the last tail lines of the containers of service ("" =
// every service) of the workflow's project, oldest first. With follow it
// keeps streaming new lines until ctx ends or every followed container
// stops. emit is never called concurrently.
func (m *Manager) ComposeLogs(ctx context.Context, wfID, service string, tail int, follow bool, emit func(model.ComposeLogLine)) error {
	if err := checkID(wfID); err != nil {
		return err
	}
	if service != "" && !validService.MatchString(service) {
		return invalidCompose("invalid service %q", service)
	}
	tail = max(0, min(tail, MaxComposeLogTail))
	cs, err := m.composeContainers(ctx, wfID)
	if err != nil {
		return err
	}
	cs = slices.DeleteFunc(cs, func(c inspected) bool { return service != "" && c.service() != service })
	slices.SortFunc(cs, func(a, b inspected) int { return strings.Compare(a.Name, b.Name) })
	if !follow {
		var lines []model.ComposeLogLine
		for _, c := range cs {
			cl, err := m.containerLogs(ctx, c, tail)
			if err != nil {
				return err
			}
			lines = append(lines, cl...)
		}
		slices.SortStableFunc(lines, func(a, b model.ComposeLogLine) int { return a.Line.T.Compare(b.Line.T) })
		if len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
		for _, l := range lines {
			emit(l)
		}
		return nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, c := range cs {
		cmd := exec.CommandContext(ctx, m.dockerBin(), "logs", "--follow", "--timestamps", "--tail", strconv.Itoa(tail), c.ID)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		svc, name := c.service(), strings.TrimPrefix(c.Name, "/")
		var streams sync.WaitGroup
		for stream, r := range map[string]io.Reader{"stdout": stdout, "stderr": stderr} {
			streams.Go(func() {
				scanLines(r, func(text string) {
					l := model.ComposeLogLine{Service: svc, Container: name, Line: parseLogLine(stream, text)}
					mu.Lock()
					emit(l)
					mu.Unlock()
				})
			})
		}
		wg.Go(func() {
			streams.Wait()
			_ = cmd.Wait()
		})
	}
	wg.Wait()
	return ctx.Err()
}

// containerLogs reads the last tail lines of one container.
func (m *Manager) containerLogs(ctx context.Context, c inspected, tail int) ([]model.ComposeLogLine, error) {
	cmd := exec.CommandContext(ctx, m.dockerBin(), "logs", "--timestamps", "--tail", strconv.Itoa(tail), c.ID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if isNoSuch(errors.New(stderr.String())) {
			return nil, nil
		}
		return nil, errors.New("docker logs: " + strings.TrimSpace(stderr.String()))
	}
	svc, name := c.service(), strings.TrimPrefix(c.Name, "/")
	var out []model.ComposeLogLine
	for stream, buf := range map[string]*bytes.Buffer{"stdout": &stdout, "stderr": &stderr} {
		scanLines(buf, func(text string) {
			out = append(out, model.ComposeLogLine{Service: svc, Container: name, Line: parseLogLine(stream, text)})
		})
	}
	return out, nil
}

// parseLogLine splits a `docker logs --timestamps` line into its time and
// text.
func parseLogLine(stream, text string) model.LogLine {
	text = strings.TrimRight(text, "\r")
	if ts, rest, ok := strings.Cut(text, " "); ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			return model.LogLine{T: t.UTC(), Stream: stream, Text: rest}
		}
	}
	return model.LogLine{T: time.Now().UTC(), Stream: stream, Text: text}
}
