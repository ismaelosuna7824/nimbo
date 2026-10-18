package setup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Errors of StartAction.
var (
	ErrUnknownAction = errors.New("unknown setup action")
	ErrActionRunning = errors.New("this setup action is already running")
)

const (
	pullTimeout  = 30 * time.Minute
	startTimeout = 2 * time.Minute
)

// StartAction launches action in the background and returns at once; its
// progress is published as setup.progress events ending with Done.
func (c *Checker) StartAction(action string) error {
	var run func(context.Context, *progress) error
	switch action {
	case model.SetupActionPullImages:
		run = c.pullImages
	case model.SetupActionStartDocker:
		run = c.startDocker
	default:
		return fmt.Errorf("%w %q", ErrUnknownAction, action)
	}
	c.mu.Lock()
	if c.running[action] {
		c.mu.Unlock()
		return ErrActionRunning
	}
	c.running[action] = true
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.running, action)
			c.mu.Unlock()
		}()
		p := &progress{c: c, action: action}
		if err := run(context.Background(), p); err != nil {
			p.send(model.SetupProgress{Step: "done", Current: p.current, Total: p.total, Message: err.Error(), Done: true, Error: err.Error()})
		}
	}()
	return nil
}

// progress publishes the events of one action run.
type progress struct {
	c              *Checker
	action         string
	current, total int
}

func (p *progress) send(ev model.SetupProgress) {
	if p.c.cfg.Events == nil {
		return
	}
	ev.Action = p.action
	p.c.cfg.Events.Publish(model.Event{Type: model.EventSetupProgress, Data: ev})
}

func (p *progress) step(step, image, msg string) {
	p.send(model.SetupProgress{Step: step, Image: image, Current: p.current, Total: p.total, Message: msg})
}

func (p *progress) finish(msg string) error {
	p.send(model.SetupProgress{Step: "done", Current: p.current, Total: p.total, Message: msg, Done: true})
	return nil
}

// ---------------------------------------------------------------------------
// pull-images

func (c *Checker) pullImages(ctx context.Context, p *progress) error {
	ctx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()
	images := requiredImages(c.cfg.Settings())
	p.total = len(images)
	p.step("start", "", fmt.Sprintf("Checking %d images", len(images)))
	cli := c.findCLI(ctx, c.cfg.Settings().Docker)
	if cli.err != nil {
		return errors.New("Docker is not installed: " + cli.err.Error())
	}
	if _, err := c.info(ctx, cli.bin); err != nil {
		return errors.New("Docker is not running, start it first (" + firstLine(err.Error()) + ")")
	}
	for _, img := range images {
		if _, err := docker(ctx, 10*time.Second, cli.bin, "image", "inspect", "--format", "{{.Id}}", img.ref); err == nil {
			p.current++
			p.step("skip", img.ref, img.ref+" is already downloaded")
			continue
		}
		p.step("pull", img.ref, "Downloading "+img.ref+" ("+img.purpose+")")
		if err := c.pull(ctx, cli.bin, img.ref, p); err != nil {
			return err
		}
		p.current++
		p.step("image-done", img.ref, img.ref+" downloaded")
	}
	return p.finish(fmt.Sprintf("All %d images are ready", len(images)))
}

// pull runs `docker pull ref`, publishing layer progress.
func (c *Checker) pull(ctx context.Context, bin, ref string, p *progress) error {
	cmd := exec.CommandContext(ctx, bin, "pull", ref)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("docker pull %s: %w", ref, err)
	}
	t := newPullTracker()
	scanLines(stdout, func(line string) {
		if !t.line(line) {
			return
		}
		done, total := t.progress()
		p.send(model.SetupProgress{
			Step: "pull", Image: ref, Current: p.current, Total: p.total,
			Message:    fmt.Sprintf("Downloading %s: %d of %d layers", ref, done, total),
			LayersDone: done, LayersTotal: total,
		})
	})
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("downloading %s took too long", ref)
		}
		msg := firstLine(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("couldn't download %s: %s", ref, msg)
	}
	return nil
}

var layerLine = regexp.MustCompile(`^([0-9a-f]{12,64}): (.+)$`)

// pullTracker follows the layers of non-interactive `docker pull` output
// ("<id>: Pulling fs layer", "<id>: Download complete", "<id>: Pull
// complete", "<id>: Already exists", …).
type pullTracker struct {
	done  map[string]bool // layer id -> finished
	order []string
}

func newPullTracker() *pullTracker { return &pullTracker{done: map[string]bool{}} }

// line records one output line and reports whether layer progress changed.
func (t *pullTracker) line(s string) bool {
	m := layerLine.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return false
	}
	id, status := m[1], m[2]
	finished := status == "Pull complete" || status == "Already exists"
	prev, known := t.done[id]
	if !known {
		t.order = append(t.order, id)
	}
	if known && (prev || !finished) {
		return false
	}
	t.done[id] = finished
	return true
}

// progress returns the finished and total layer counts seen so far.
func (t *pullTracker) progress() (done, total int) {
	for _, id := range t.order {
		if t.done[id] {
			done++
		}
	}
	return done, len(t.order)
}

func scanLines(r io.Reader, fn func(string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fn(sc.Text())
	}
	io.Copy(io.Discard, r)
}

// ---------------------------------------------------------------------------
// start-docker

// engineApps lists the macOS engine apps by flavor, in preference order.
var engineApps = []struct{ flavor, app string }{
	{FlavorOrbStack, "OrbStack.app"},
	{FlavorDockerDesktop, "Docker.app"},
	{FlavorRancher, "Rancher Desktop.app"},
}

const windowsDockerDesktop = `C:\Program Files\Docker\Docker\Docker Desktop.exe`

// engineApp returns the installed engine app to launch, preferring the one
// matching flavor (the current docker context): a macOS .app bundle or the
// Docker Desktop executable on Windows. "" means none is installed.
func (c *Checker) engineApp(flavor string) string {
	switch c.plat.GOOS {
	case "windows":
		if _, err := os.Stat(windowsDockerDesktop); err == nil {
			return windowsDockerDesktop
		}
		return ""
	case "darwin":
	default:
		return ""
	}
	home, _ := os.UserHomeDir()
	find := func(app string) string {
		for _, dir := range []string{"/Applications", filepath.Join(home, "Applications")} {
			if p := filepath.Join(dir, app); dirExists(p) {
				return p
			}
		}
		return ""
	}
	for _, e := range engineApps {
		if e.flavor == flavor {
			if p := find(e.app); p != "" {
				return p
			}
		}
	}
	for _, e := range engineApps {
		if p := find(e.app); p != "" {
			return p
		}
	}
	return ""
}

func (c *Checker) startDocker(ctx context.Context, p *progress) error {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	p.total = 1
	p.step("start", "", "Starting Docker")
	cli := c.findCLI(ctx, c.cfg.Settings().Docker)
	if cli.err != nil {
		return errors.New("Docker is not installed yet: install it first, then start it")
	}
	if _, err := c.info(ctx, cli.bin); err == nil {
		p.current = 1
		return p.finish("Docker is already running")
	}
	if c.plat.GOOS == "linux" {
		return errors.New("Nimbo can't start the Docker service for you because it needs administrator rights. " +
			"Open a terminal and run: sudo systemctl enable --now docker")
	}

	flavor := detectFlavor(cli.versionLine, cli.contextHost, "", "")
	name := ""
	switch app := c.engineApp(flavor); {
	case app != "" && c.plat.GOOS == "darwin":
		name = appName(app)
		if out, err := exec.CommandContext(ctx, "open", "-a", app).CombinedOutput(); err != nil {
			return fmt.Errorf("couldn't open %s: %s", name, firstLine(string(out)))
		}
	case app != "":
		name = "Docker Desktop"
		cmd := exec.Command(app)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("couldn't open Docker Desktop: %w", err)
		}
		go cmd.Wait()
	case c.plat.GOOS == "windows":
		return errors.New("Docker Desktop was not found: install it first")
	default:
		colima, err := exec.LookPath("colima")
		if err != nil {
			return errors.New("no Docker app was found to start: install OrbStack or Docker Desktop first")
		}
		name = "Colima"
		// colima start blocks until the VM is up and must outlive this
		// action; the readiness loop below reports when docker answers.
		cmd := exec.Command(colima, "start")
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("couldn't run colima start: %w", err)
		}
		go cmd.Wait()
	}

	p.step("waiting", "", "Waiting for "+name+" to start (this can take a minute)…")
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s didn't start within %s; open it and look for a prompt or error", name, startTimeout)
		case <-tick.C:
			if _, err := c.info(ctx, cli.bin); err == nil {
				p.current = 1
				return p.finish(name + " is running")
			}
		}
	}
}
