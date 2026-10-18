// Package setup diagnoses whether this machine can run Nimbo's sandboxes
// (GET /setup, `nimbo doctor`) and performs the one-click fixes (start the
// Docker engine, download the required images). Every message is written
// for someone who has never used Docker.
package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// Check IDs, in the order they are reported. CheckWSL is reported on
// Windows only, CheckDockerPermissions on Linux only.
const (
	CheckWSL               = "wsl"
	CheckDockerCLI         = "docker-cli"
	CheckDockerDaemon      = "docker-daemon"
	CheckDockerPermissions = "docker-permissions"
	CheckImages            = "images"
	CheckDockerSocket      = "docker-socket"
	CheckDockerMemory      = "docker-memory"
	CheckDisk              = "disk"
	CheckInternet          = "internet"
)

const (
	podImage       = "alpine:3"
	minMemory      = 4 << 30
	minDisk        = 5 << 30
	socketCacheTTL = 5 * time.Minute
	registryURL    = "https://registry.terraform.io"
	dockerSocket   = "/var/run/docker.sock"
)

// Config wires a Checker.
type Config struct {
	// Home is the Nimbo data directory; the disk check measures its volume.
	Home string
	// Settings returns the current preferences (docker binary, images).
	Settings func() model.Settings
	// Events receives setup.progress events of actions; nil disables them.
	Events model.Publisher
	// HTTPClient performs the internet check; nil uses a 3s-timeout client.
	HTTPClient *http.Client
	// Platform tailors advice; the zero value detects the running machine.
	Platform Platform
}

// Checker runs the setup checks and actions. It is safe for concurrent use.
type Checker struct {
	cfg  Config
	plat Platform

	mu      sync.Mutex
	socket  socketResult    // last successful socket check
	running map[string]bool // actions in progress
}

type socketResult struct {
	key string // docker binary + context host it was checked against
	at  time.Time
}

// New returns a Checker for cfg.
func New(cfg Config) *Checker {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{
			Timeout: 3 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	if cfg.Platform.GOOS == "" {
		cfg.Platform = DetectPlatform()
	}
	return &Checker{cfg: cfg, plat: cfg.Platform, running: map[string]bool{}}
}

// requiredImage is an image every sandbox needs, with what it is for.
type requiredImage struct {
	ref     string
	purpose string
}

// requiredImages lists the images a first AWS deploy needs: the Floci AWS
// emulator from settings, the Terraform runner and the network-holder pod.
// Other clouds' emulators are pulled on demand.
func requiredImages(s model.Settings) []requiredImage {
	aws := s.Images[model.CloudAWS]
	if aws == "" {
		aws = sandbox.DefaultImages()[model.CloudAWS]
	}
	tf := s.TerraformImage
	if tf == "" {
		tf = sandbox.DefaultTerraformImage
	}
	return []requiredImage{
		{aws, "the offline copy of AWS (Floci)"},
		{tf, "Terraform, which builds your infrastructure"},
		{podImage, "a tiny helper that gives each sandbox its private network"},
	}
}

// dockerInfo is the subset of `docker info --format '{{json .}}'` we use.
type dockerInfo struct {
	ServerVersion   string
	OperatingSystem string
	Name            string
	MemTotal        int64
}

// cliInfo describes the docker CLI found on this machine.
type cliInfo struct {
	bin         string
	versionLine string
	contextHost string
	err         error
}

// Run evaluates every check. Independent checks run concurrently and each
// docker call has its own short timeout, so Run answers within a few
// seconds even when Docker hangs.
func (c *Checker) Run(ctx context.Context) model.SetupStatus {
	settings := c.cfg.Settings()

	var wg sync.WaitGroup
	var disk, internet, wsl model.SetupCheck
	wg.Go(func() { disk = c.checkDisk() })
	wg.Go(func() { internet = c.checkInternet(ctx) })
	if c.plat.GOOS == "windows" {
		wg.Go(func() { wsl = c.checkWSL(ctx) })
	}

	cli := c.findCLI(ctx, settings.Docker)
	var info *dockerInfo
	var infoErr error
	if cli.err == nil {
		info, infoErr = c.info(ctx, cli.bin)
	}
	flavor := detectFlavor(cli.versionLine, cli.contextHost, "", "")
	if info != nil {
		flavor = detectFlavor(cli.versionLine, cli.contextHost, info.OperatingSystem, info.Name)
	}

	var checks []model.SetupCheck
	add := func(ch model.SetupCheck) { checks = append(checks, ch) }
	add(c.cliCheck(cli, flavor))
	add(c.daemonCheck(cli, info, infoErr, flavor))
	if c.plat.GOOS == "linux" {
		add(c.permissionsCheck(cli, info, infoErr))
	}
	images, present := c.imagesCheck(ctx, cli, info, requiredImages(settings))
	add(images)
	add(c.socketCheck(ctx, cli, info, present[podImage], flavor))
	add(c.memoryCheck(info, flavor))
	wg.Wait()
	if c.plat.GOOS == "windows" {
		checks = append([]model.SetupCheck{wsl}, checks...)
	}
	add(disk)
	add(internet)

	return model.SetupStatus{
		Ready: allRequiredOK(checks), Platform: c.plat.GOOS, Arch: c.plat.Arch, OSName: c.plat.OSName,
		Distro: c.plat.Distro, PackageManager: c.plat.PackageManager, Checks: checks,
	}
}

// allRequiredOK reports whether every required check passed.
func allRequiredOK(checks []model.SetupCheck) bool {
	for _, ch := range checks {
		if ch.Required && ch.Status != model.SetupOK {
			return false
		}
	}
	return true
}

// docker runs the docker CLI with a timeout and returns trimmed stdout, or
// an error carrying stderr.
func docker(ctx context.Context, timeout time.Duration, bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("docker %s did not answer within %s", args[0], timeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (c *Checker) findCLI(ctx context.Context, setting string) cliInfo {
	bin := sandbox.ResolveDocker(setting)
	if !filepath.IsAbs(bin) {
		if p, err := exec.LookPath(bin); err == nil {
			bin = p
		} else {
			return cliInfo{bin: bin, err: fmt.Errorf("%q was not found on this computer", bin)}
		}
	}
	if info, err := os.Stat(bin); err != nil || info.IsDir() {
		return cliInfo{bin: bin, err: fmt.Errorf("%s does not exist", bin)}
	}
	ci := cliInfo{bin: bin}
	var wg sync.WaitGroup
	wg.Go(func() {
		ci.versionLine, ci.err = docker(ctx, 3*time.Second, bin, "--version")
	})
	wg.Go(func() {
		ci.contextHost, _ = docker(ctx, 3*time.Second, bin, "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
	})
	wg.Wait()
	return ci
}

func (c *Checker) info(ctx context.Context, bin string) (*dockerInfo, error) {
	out, err := docker(ctx, 3*time.Second, bin, "info", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	var info dockerInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return nil, fmt.Errorf("unexpected docker info output: %w", err)
	}
	if info.ServerVersion == "" {
		return nil, errors.New("the Docker engine did not report a version")
	}
	return &info, nil
}

// ---------------------------------------------------------------------------
// images

func (c *Checker) imagesCheck(ctx context.Context, cli cliInfo, info *dockerInfo, images []requiredImage) (model.SetupCheck, map[string]bool) {
	ch := model.SetupCheck{ID: CheckImages, Title: "Sandbox images are downloaded", Required: true}
	present := map[string]bool{}
	intro := "Nimbo's sandboxes are built from a few ready-made Docker images. They are downloaded once (about 1 GB in total) " +
		"and reused for every project afterwards."
	if cli.err != nil || info == nil {
		ch.Status = model.SetupMissing
		ch.Summary = "Checked once Docker is running"
		ch.Detail = intro
		return ch, present
	}
	sizes := make([]int64, len(images))
	found := make([]bool, len(images))
	var wg sync.WaitGroup
	for i, img := range images {
		wg.Go(func() {
			out, err := docker(ctx, 3*time.Second, cli.bin, "image", "inspect", "--format", "{{.Size}}", img.ref)
			if err == nil {
				found[i] = true
				fmt.Sscan(out, &sizes[i])
			}
		})
	}
	wg.Wait()
	var total int64
	var have int
	var missing []string
	for i, img := range images {
		if found[i] {
			have++
			total += sizes[i]
			present[img.ref] = true
		} else {
			missing = append(missing, img.ref+" ("+img.purpose+")")
		}
	}
	ch.Summary = fmt.Sprintf("%d of %d downloaded (≈ %s)", have, len(images), humanBytes(total))
	if have == len(images) {
		ch.Status = model.SetupOK
		ch.Summary = fmt.Sprintf("All %d downloaded (≈ %s)", have, humanBytes(total))
		ch.Detail = intro
		return ch, present
	}
	ch.Status = model.SetupMissing
	ch.Detail = intro + " Still missing: " + strings.Join(missing, "; ") + "."
	ch.Fixes = []model.SetupFix{{Kind: model.FixAction, Label: "Download images", Action: model.SetupActionPullImages,
		Command: pullCommand(c.plat.GOOS, images)}}
	return ch, present
}

// pullCommand is the shell equivalent of the pull-images action.
func pullCommand(goos string, images []requiredImage) string {
	sep := " && "
	if goos == "windows" {
		sep = "; " // Windows PowerShell 5.1 has no &&
	}
	cmds := make([]string, len(images))
	for i, img := range images {
		cmds[i] = "docker pull " + img.ref
	}
	return strings.Join(cmds, sep)
}

// ---------------------------------------------------------------------------
// docker-socket

func (c *Checker) socketCheck(ctx context.Context, cli cliInfo, info *dockerInfo, podPresent bool, flavor string) model.SetupCheck {
	ch := model.SetupCheck{ID: CheckDockerSocket, Title: "Sandboxes can reach Docker", Required: true}
	intro := "Some emulated services (Lambda functions, RDS databases, ECS tasks…) start their own containers, " +
		"so the AWS emulator needs access to Docker's control socket from inside its container."
	switch {
	case cli.err != nil || info == nil:
		ch.Status = model.SetupMissing
		ch.Summary = "Checked once Docker is running"
		ch.Detail = intro
		return ch
	case !podPresent:
		ch.Status = model.SetupMissing
		ch.Summary = "Checked once the images are downloaded"
		ch.Detail = intro
		return ch
	}
	key := cli.bin + "|" + cli.contextHost
	c.mu.Lock()
	cached := c.socket.key == key && time.Since(c.socket.at) < socketCacheTTL
	c.mu.Unlock()
	if !cached {
		_, err := docker(ctx, 8*time.Second, cli.bin, "run", "--rm", "-v", dockerSocket+":"+dockerSocket, podImage, "test", "-S", dockerSocket)
		if err != nil {
			ch.Status = model.SetupError
			ch.Summary = "Containers can't see the Docker socket"
			ch.Detail = intro + " A test container could not find " + dockerSocket + " (" + firstLine(err.Error()) + "). " +
				socketAdvice(c.plat.GOOS, flavor)
			if flavor == FlavorDockerDesktop || flavor == "" && c.plat.GOOS != "linux" {
				ch.Fixes = []model.SetupFix{{Kind: model.FixLink, Label: "Docker Desktop settings reference",
					URL: "https://docs.docker.com/desktop/settings-and-maintenance/settings/#advanced"}}
			}
			return ch
		}
		c.mu.Lock()
		c.socket = socketResult{key: key, at: time.Now()}
		c.mu.Unlock()
	}
	ch.Status = model.SetupOK
	ch.Summary = "Containers can use the Docker socket"
	ch.Detail = intro
	return ch
}

func socketAdvice(goos, flavor string) string {
	switch {
	case flavor == FlavorColima:
		return "Restart Colima (\"colima stop && colima start\"); it exposes the socket inside its virtual machine at " + dockerSocket + "."
	case flavor == FlavorRancher:
		return "In Rancher Desktop pick the \"dockerd (moby)\" container engine under Preferences → Container Engine, then check again."
	case flavor == FlavorPodman:
		return "Podman needs its Docker-compatible socket: run \"podman machine stop && podman machine set --rootful && podman machine start\"."
	case flavor == FlavorOrbStack:
		return "Restart OrbStack from its menu bar icon (Quit, then open it again) and check again."
	case goos == "windows":
		return "In Docker Desktop open Settings → General, make sure \"Use the WSL 2 based engine\" is on and that Docker runs " +
			"Linux containers (tray icon → \"Switch to Linux containers…\" if offered), then check again."
	case goos == "linux" && flavor != FlavorDockerDesktop:
		return "Nimbo needs the system Docker service (rootless Docker keeps its socket elsewhere). Run \"docker context use default\" " +
			"and \"sudo systemctl enable --now docker\", then check again."
	}
	return "In Docker Desktop open Settings → Advanced, turn on \"Allow the default Docker socket to be used\" " +
		"(it asks for your password), click Apply & restart, then check again."
}

// ---------------------------------------------------------------------------
// docker-memory

func (c *Checker) memoryCheck(info *dockerInfo, flavor string) model.SetupCheck {
	ch := model.SetupCheck{ID: CheckDockerMemory, Title: "Docker has enough memory"}
	intro := "The cloud emulator and Terraform together need about 4 GB of memory to run smoothly."
	switch {
	case info == nil:
		ch.Status = model.SetupMissing
		ch.Summary = "Checked once Docker is running"
		ch.Detail = intro
	case info.MemTotal < minMemory:
		ch.Status = model.SetupWarning
		ch.Summary = "Docker can use only " + humanBytes(info.MemTotal) + " of memory"
		ch.Detail = intro + " Sandboxes may be slow or crash. " + memoryAdvice(c.plat.GOOS, flavor)
	default:
		ch.Status = model.SetupOK
		ch.Summary = humanBytes(info.MemTotal) + " available to Docker"
		ch.Detail = intro
	}
	return ch
}

func memoryAdvice(goos, flavor string) string {
	switch {
	case flavor == FlavorDockerDesktop && goos == "windows":
		return "Docker Desktop on Windows gets its memory from WSL 2: create or edit the file %UserProfile%\\.wslconfig with the lines " +
			"\"[wsl2]\" and \"memory=6GB\", run \"wsl --shutdown\" in PowerShell, then restart Docker Desktop."
	case flavor == FlavorDockerDesktop:
		return "Raise it in Docker Desktop: Settings → Resources → Memory limit (6 GB or more), then Apply & restart."
	case flavor == FlavorOrbStack:
		return "Raise it in OrbStack: Settings → System → Memory limit."
	case flavor == FlavorRancher:
		return "Raise it in Rancher Desktop: Preferences → Virtual Machine → Memory."
	case flavor == FlavorColima:
		return "Restart Colima with more memory: \"colima stop && colima start --memory 6\"."
	case flavor == FlavorPodman:
		return "Recreate the Podman machine with more memory: \"podman machine stop && podman machine set --memory 6144 && podman machine start\"."
	case goos == "linux":
		return "Docker on Linux uses your computer's memory directly: close other memory-hungry apps or add more RAM."
	}
	return "Give Docker more memory, or close other memory-hungry apps."
}

// ---------------------------------------------------------------------------
// disk

func (c *Checker) checkDisk() model.SetupCheck {
	ch := model.SetupCheck{ID: CheckDisk, Title: "Enough free disk space"}
	intro := "Docker images, emulator data and Terraform providers need a few gigabytes of disk space."
	dir := c.cfg.Home
	for dir != "" && !dirExists(dir) {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	free, err := freeBytes(dir)
	switch {
	case err != nil:
		ch.Status = model.SetupWarning
		ch.Summary = "Couldn't measure free space"
		ch.Detail = intro + " (" + err.Error() + ")"
	case free < minDisk:
		ch.Status = model.SetupWarning
		ch.Summary = "Only " + humanBytes(int64(free)) + " free"
		ch.Detail = intro + " Free up at least 5 GB: " + diskAdvice(c.plat.GOOS)
	default:
		ch.Status = model.SetupOK
		ch.Summary = humanBytes(int64(free)) + " free"
		ch.Detail = intro
	}
	return ch
}

func diskAdvice(goos string) string {
	switch goos {
	case "darwin":
		return "empty the Trash, remove apps you no longer use (System Settings → General → Storage), " +
			"or delete old Docker images with \"docker system prune\"."
	case "windows":
		return "run Disk Cleanup from the Start menu, uninstall apps you no longer use (Settings → Apps), " +
			"or delete old Docker images with \"docker system prune\"."
	}
	return "remove files or packages you no longer use (for example \"sudo apt autoremove\" or your distribution's equivalent), " +
		"or delete old Docker images with \"docker system prune\"."
}

// ---------------------------------------------------------------------------
// internet

func (c *Checker) checkInternet(ctx context.Context) model.SetupCheck {
	ch := model.SetupCheck{ID: CheckInternet, Title: "Internet connection"}
	intro := "The first deploy downloads the Terraform providers it needs (and the images, if not yet downloaded). " +
		"After that, sandboxes work fully offline."
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, registryURL, nil)
	if err == nil {
		var res *http.Response
		if res, err = c.cfg.HTTPClient.Do(req); err == nil {
			res.Body.Close()
		}
	}
	if err != nil {
		ch.Status = model.SetupWarning
		ch.Summary = "Can't reach the Terraform registry"
		ch.Detail = intro + " Nimbo couldn't reach registry.terraform.io, so a first deploy will fail until you are online. " +
			"Projects you already deployed keep working."
		return ch
	}
	ch.Status = model.SetupOK
	ch.Summary = "Online"
	ch.Detail = intro
	return ch
}

// ---------------------------------------------------------------------------
// helpers

func humanBytes(n int64) string {
	const (
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case n >= 10*gb:
		return fmt.Sprintf("%d GB", n/gb)
	case n >= gb:
		return fmt.Sprintf("%.1f GB", float64(n)/gb)
	default:
		return fmt.Sprintf("%d MB", n/mb)
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
