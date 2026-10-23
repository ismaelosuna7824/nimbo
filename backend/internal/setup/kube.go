package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// CheckKubernetes reports the local Kubernetes clusters' prerequisites:
// the k3s node image and the disk space clusters use.
const CheckKubernetes = "kubernetes"

// kubeMinDisk is the free space a cluster with a few workloads needs (node
// image, system images pulled inside it, built images, volumes).
const kubeMinDisk = 3 << 30

const kubeIntro = "Kubernetes workspaces run on a local cluster: one k3s container per workflow, created by Up and managed by Nimbo " +
	"(its kubeconfig lives in Nimbo's folder; your ~/.kube/config is never changed). The node image " + sandbox.KubeNodeImage +
	" is downloaded once (about 90 MB); the first cluster then pulls its system images (CoreDNS, Traefik, metrics-server, about 200 MB more)."

// kubeCheck reports whether the node image is downloaded and the disk has
// room for clusters. It is not required: other workflows never use it.
func (c *Checker) kubeCheck(ctx context.Context, cli cliInfo, info *dockerInfo) model.SetupCheck {
	ch := model.SetupCheck{ID: CheckKubernetes, Title: "Kubernetes node image is downloaded"}
	if cli.err != nil || info == nil {
		ch.Status = model.SetupMissing
		ch.Summary = "Checked once Docker is running"
		ch.Detail = kubeIntro
		return ch
	}
	var size int64
	out, err := docker(ctx, 3*time.Second, cli.bin, "image", "inspect", "--format", "{{.Size}}", sandbox.KubeNodeImage)
	if err == nil {
		fmt.Sscan(out, &size)
	}
	free, ferr := freeBytes(c.homeVolume())
	switch {
	case err != nil:
		ch.Status = model.SetupMissing
		ch.Summary = "Not downloaded yet (about 90 MB)"
		ch.Detail = kubeIntro + " Download it now, or let the first Up of a Kubernetes workflow do it."
		ch.Fixes = []model.SetupFix{{Kind: model.FixAction, Label: "Download the node image", Action: model.SetupActionPullKubeImage,
			Command: "docker pull " + sandbox.KubeNodeImage}}
	case ferr == nil && free < kubeMinDisk:
		ch.Status = model.SetupWarning
		ch.Summary = "Downloaded, but only " + humanBytes(int64(free)) + " free"
		ch.Detail = kubeIntro + " A cluster with a few workloads needs about 3 GB of free disk space: " + diskAdvice(c.plat.GOOS)
	default:
		ch.Status = model.SetupOK
		ch.Summary = "k3s " + kubeVersion() + " (≈ " + humanBytes(size) + ")"
		ch.Detail = kubeIntro
	}
	return ch
}

// kubeVersion is the Kubernetes version of the node image tag.
func kubeVersion() string {
	ref := sandbox.KubeNodeImage
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == ':' {
			return ref[i+1:]
		}
	}
	return ref
}

// homeVolume is the nearest existing folder of the Nimbo home (its disk).
func (c *Checker) homeVolume() string {
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
	return dir
}

// pullKubeImage downloads the node image with layer progress.
func (c *Checker) pullKubeImage(ctx context.Context, p *progress) error {
	ctx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()
	ref := sandbox.KubeNodeImage
	p.total = 1
	p.step("start", ref, "Checking the Kubernetes node image")
	cli := c.findCLI(ctx, c.cfg.Settings().Docker)
	if cli.err != nil {
		return errors.New("Docker is not installed: " + cli.err.Error())
	}
	if _, err := c.info(ctx, cli.bin); err != nil {
		return errors.New("Docker is not running, start it first (" + firstLine(err.Error()) + ")")
	}
	if _, err := docker(ctx, 10*time.Second, cli.bin, "image", "inspect", "--format", "{{.Id}}", ref); err == nil {
		p.current = 1
		p.step("skip", ref, ref+" is already downloaded")
		return p.finish("The Kubernetes node image is ready")
	}
	p.step("pull", ref, "Downloading "+ref+" (the local Kubernetes cluster)")
	if err := c.pull(ctx, cli.bin, ref, p); err != nil {
		return err
	}
	p.current = 1
	p.step("image-done", ref, ref+" downloaded")
	return p.finish("The Kubernetes node image is ready")
}
