package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Kubernetes workflows run on one local cluster each: a k3s node container
// model.KubeClusterName(wf) labelled ComposeLabel=<wf> and
// kubeRoleLabel=kube-node, publishing the API server and the ingress
// controller on 127.0.0.1. Nimbo talks to it through its own kubeconfig in
// <home>/workflows/<wf>/kube/kubeconfig and never reads or writes the
// user's kubeconfig, KUBECONFIG or current context.
//
// Why k3s in one container (measured on Docker 29 / OrbStack, arm64):
// rancher/k3s is ~89 MB to pull (248 MB on disk) and reaches a Ready node in
// ~4 s and a serving ingress in ~24 s, with Traefik (Ingress), ServiceLB
// (LoadBalancer Services), local-path (default StorageClass) and
// metrics-server built in. kind's node image is ~346 MB (909 MB on disk),
// its node is Ready after ~30 s and it has no ingress controller,
// LoadBalancer or metrics-server. k3d wraps the same k3s image with two
// extra images and a Docker SDK dependency. Driving the container with the
// docker CLI, like the rest of this package, needs no cgo and no extra Go
// dependency besides client-go.
const (
	// KubeNodeImage is the node image of every cluster (Kubernetes 1.36).
	KubeNodeImage = "rancher/k3s:v1.36.5-k3s1"
	// KubeIngressClass and KubeStorageClass are the cluster's defaults;
	// kubeNginxAlias is an IngressClass served by Traefik so manifests
	// written for ingress-nginx route too.
	KubeIngressClass = "traefik"
	KubeStorageClass = "local-path"
	kubeNginxAlias   = "nginx"

	kubeRoleLabel    = "com.nimbo.role"
	kubeRoleNode     = "kube-node"
	kubeWorkDir      = "kube"
	kubeFieldManager = "nimbo"
	kubeAPIPort      = 6443
	kubeHTTPPort     = 80

	// DefaultKubeWait bounds the rollout wait of up/restart when the
	// request has no timeout.
	DefaultKubeWait = 5 * time.Minute
	kubeAPIWait     = 3 * time.Minute
)

// ErrDockerUnavailable is returned when a Kubernetes action needs Docker
// and the engine does not answer.
var ErrDockerUnavailable = errors.New("Docker is not running: start Docker Desktop, OrbStack or the Docker engine, then try again")

// InvalidKubeError is a Kubernetes request the workflow or the request's
// fields make impossible (unknown workload, stopped cluster, ...).
type InvalidKubeError struct{ Msg string }

func (e *InvalidKubeError) Error() string { return e.Msg }

func invalidKube(format string, args ...any) error {
	return &InvalidKubeError{Msg: fmt.Sprintf(format, args...)}
}

// KubeRunRequest describes one Kubernetes run.
type KubeRunRequest struct {
	Workflow *model.Workflow
	// Action is up | down | restart | cluster-stop | cluster-start |
	// cluster-delete.
	Action model.RunAction
	// SourceDir holds the workflow's files (the build contexts).
	SourceDir string
	// Sources are the rendered sources of the environment, in apply order
	// (up only).
	Sources []KubeApplySource
	// Builds are the images up builds and loads into the cluster: the
	// workflow's explicit builds; KubeBuildPlan adds the images named
	// after a folder with a Dockerfile.
	Builds []model.KubeBuild
	// Workload limits restart to one workload node ID ("" = all).
	Workload string
	// RemoveVolumes makes down delete the PersistentVolumeClaims too;
	// DeleteCluster makes it delete the whole cluster.
	RemoveVolumes bool
	DeleteCluster bool
	Timeout       time.Duration // rollout wait (0 = DefaultKubeWait)
}

// KubeApplySource is one rendered source of a Kubernetes workflow.
type KubeApplySource struct {
	Index int
	Kind  string // model.KubeManifests | KubeKustomize | KubeHelm
	Path  string
	// Namespace receives the namespaced objects without one ("" =
	// "default").
	Namespace string
	// Objects are the rendered objects of manifests and Kustomize
	// sources (each a full Kubernetes object).
	Objects []map[string]any
	// Helm is set for Helm sources.
	Helm *KubeHelmRelease
}

// KubeHelmRelease is a Helm source: up runs helm upgrade --install.
type KubeHelmRelease struct {
	Release string
	Chart   *chartv2.Chart
	Values  map[string]any
}

func (m *Manager) kubeDir(wfID string) string { return filepath.Join(m.wfDir(wfID), kubeWorkDir) }

// KubeconfigPath is Nimbo's kubeconfig of the workflow's cluster (it
// exists while the cluster does).
func (m *Manager) KubeconfigPath(wfID string) string {
	return filepath.Join(m.kubeDir(wfID), "kubeconfig")
}

// kubeClusterFile is <kube dir>/cluster.json: the host ports the node
// container publishes, kept for the cluster's lifetime.
type kubeClusterFile struct {
	Image       string `json:"image"`
	APIPort     int    `json:"apiPort"`
	IngressPort int    `json:"ingressPort"`
}

func (m *Manager) loadKubeCluster(wfID string) kubeClusterFile {
	var f kubeClusterFile
	data, err := os.ReadFile(filepath.Join(m.kubeDir(wfID), "cluster.json"))
	if err == nil {
		_ = json.Unmarshal(data, &f)
	}
	return f
}

func (m *Manager) saveKubeCluster(wfID string, f kubeClusterFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(m.kubeDir(wfID), "cluster.json"), data)
}

// kubeNode is the inspected node container of a workflow's cluster.
type kubeNode struct {
	Name        string
	State       string // created | running | exited | paused | restarting | dead
	Image       string
	APIPort     int
	IngressPort int
}

func (n *kubeNode) running() bool { return n != nil && n.State == "running" }

var nodeNameRe = regexp.MustCompile(`[^a-z0-9-]+`)

// kubeNodeName is the cluster's (DNS-1123) node and host name.
func kubeNodeName(wfID string) string {
	name := strings.Trim(nodeNameRe.ReplaceAllString(strings.ToLower(model.KubeClusterName(wfID)), "-"), "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

// inspectKubeNode returns the workflow's node container, nil when absent.
// A container with the cluster's name that is not labelled for the
// workflow is an error: Nimbo never adopts other containers.
func (m *Manager) inspectKubeNode(ctx context.Context, wfID string) (*kubeNode, error) {
	name := model.KubeClusterName(wfID)
	out, err := m.docker(ctx, "container", "inspect", "--format", "{{json .}}", name)
	if err != nil {
		if isNoSuch(err) {
			return nil, nil
		}
		return nil, err
	}
	var c struct {
		State struct {
			Status string `json:"Status"`
		} `json:"State"`
		Config struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		HostConfig struct {
			PortBindings map[string][]struct {
				HostPort string `json:"HostPort"`
			} `json:"PortBindings"`
		} `json:"HostConfig"`
	}
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		return nil, fmt.Errorf("parse docker inspect output: %w", err)
	}
	if c.Config.Labels[ComposeLabel] != wfID || c.Config.Labels[kubeRoleLabel] != kubeRoleNode {
		return nil, fmt.Errorf("a container named %s exists but was not created by Nimbo for this workflow; rename or remove it", name)
	}
	n := &kubeNode{Name: name, State: c.State.Status, Image: c.Config.Image}
	port := func(p int) int {
		for _, b := range c.HostConfig.PortBindings[strconv.Itoa(p)+"/tcp"] {
			if v, err := strconv.Atoi(b.HostPort); err == nil && v > 0 {
				return v
			}
		}
		return 0
	}
	n.APIPort, n.IngressPort = port(kubeAPIPort), port(kubeHTTPPort)
	return n, nil
}

// freeLoopbackPort asks the OS for a free 127.0.0.1 TCP port.
func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// createKubeNode starts a new node container. The saved host ports are
// reused while free, so the ingress URL survives a cluster delete.
func (m *Manager) createKubeNode(ctx context.Context, rs *runState, wfID string) (*kubeNode, error) {
	if err := m.ensureImage(ctx, KubeNodeImage, func(line string) {
		if rs != nil {
			m.logLine(rs, "stdout", line)
		}
	}); err != nil {
		return nil, fmt.Errorf("download the Kubernetes node image %s: %w", KubeNodeImage, err)
	}
	saved := m.loadKubeCluster(wfID)
	pick := func(p int) (int, error) {
		if p > 0 && portFree(p) {
			return p, nil
		}
		return freeLoopbackPort()
	}
	name := model.KubeClusterName(wfID)
	host := kubeNodeName(wfID)
	for attempt := 0; ; attempt++ {
		api, err := pick(saved.APIPort)
		if err != nil {
			return nil, err
		}
		ingress, err := pick(saved.IngressPort)
		if err != nil {
			return nil, err
		}
		if ingress == api {
			if ingress, err = freeLoopbackPort(); err != nil {
				return nil, err
			}
		}
		args := []string{"run", "-d", "--name", name, "--hostname", host, "--privileged",
			"--label", ComposeLabel + "=" + wfID, "--label", kubeRoleLabel + "=" + kubeRoleNode,
			"--tmpfs", "/run", "--tmpfs", "/var/run",
			"-p", fmt.Sprintf("127.0.0.1:%d:%d", api, kubeAPIPort),
			"-p", fmt.Sprintf("127.0.0.1:%d:%d", ingress, kubeHTTPPort),
			KubeNodeImage, "server", "--node-name", host,
			"--tls-san", "127.0.0.1", "--tls-san", "localhost",
		}
		_, err = m.docker(ctx, args...)
		if err == nil {
			f := kubeClusterFile{Image: KubeNodeImage, APIPort: api, IngressPort: ingress}
			if err := m.saveKubeCluster(wfID, f); err != nil {
				return nil, err
			}
			return &kubeNode{Name: name, State: "running", Image: KubeNodeImage, APIPort: api, IngressPort: ingress}, nil
		}
		// A failed run may leave a created container behind.
		_, _ = m.docker(context.WithoutCancel(ctx), "rm", "-f", "-v", name)
		if !portBusy(err) || attempt >= 3 {
			return nil, fmt.Errorf("create the cluster container: %w", err)
		}
		saved = kubeClusterFile{}
	}
}

// ensureKubeCluster creates or starts the workflow's cluster and returns a
// client once its API answers, with the Nimbo kubeconfig written.
func (m *Manager) ensureKubeCluster(ctx context.Context, rs *runState, sb *sandbox) (*kubeClient, error) {
	wfID := sb.id
	if err := m.DockerAvailable(ctx); err != nil {
		return nil, ErrDockerUnavailable
	}
	say := func(msg string) {
		if rs != nil {
			m.logLine(rs, "system", msg)
		}
	}
	node, err := m.inspectKubeNode(ctx, wfID)
	if err != nil {
		return nil, err
	}
	switch {
	case node == nil:
		say("Creating the local Kubernetes cluster " + model.KubeClusterName(wfID) + " (" + KubeNodeImage + ")")
		if node, err = m.createKubeNode(ctx, rs, wfID); err != nil {
			return nil, err
		}
	case !node.running():
		say("Starting the cluster " + node.Name)
		if _, err := m.docker(ctx, "start", node.Name); err != nil {
			if portBusy(err) {
				return nil, fmt.Errorf("start the cluster: one of its host ports (API %d, ingress %d) is used by another program; free it or delete the cluster", node.APIPort, node.IngressPort)
			}
			return nil, fmt.Errorf("start the cluster: %w", err)
		}
	}
	m.publishKubeCluster(sb, model.ClusterStarting, "")
	kc, err := m.connectKube(ctx, wfID, node, kubeAPIWait)
	if err != nil {
		return nil, err
	}
	if err := kc.waitServiceIPs(ctx, serviceIPWait); err != nil {
		return nil, err
	}
	say(fmt.Sprintf("Cluster ready: Kubernetes %s, API on 127.0.0.1:%d, ingress on http://127.0.0.1:%d (kubeconfig %s)", kc.version, node.APIPort, node.IngressPort, kc.kubeconfig))
	return kc, nil
}

// Service ClusterIPs on a starting cluster. Since Kubernetes 1.34
// (MultiCIDRServiceAllocator with DisableAllocatorDualWrite) the API server
// allocates ClusterIPs from allocators it builds from the ServiceCIDR
// objects its informer sees. Until the default ServiceCIDR "kubernetes" is
// created, Ready and synced into that informer, every allocation fails with
// `failed to allocate a serviceIP …: range is full` — yet /version already
// answers, so a fresh cluster's first apply failed about 2 times in 3.
// The `kubernetes` Service getting its ClusterIP is the signal: the API
// server assigns it through the same allocator, which only hands out IPs
// once it is Ready. A restarted cluster keeps that Service from its last
// boot while the allocator resyncs, so applying a Service also retries
// that exact error (applyWithRetry).
const (
	serviceIPWait  = time.Minute
	serviceIPPoll  = 250 * time.Millisecond
	serviceIPRetry = 30 * time.Second
)

// waitServiceIPs waits until the API server has given the default
// `kubernetes` Service its ClusterIP, at most timeout.
func (kc *kubeClient) waitServiceIPs(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		svc, err := kc.cs.CoreV1().Services("default").Get(ctx, "kubernetes", metav1.GetOptions{})
		if err == nil && svc.Spec.ClusterIP != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("the cluster could not allocate Service IPs within %s (the default kubernetes Service has no ClusterIP yet)", timeout)
			}
			return ctx.Err()
		case <-time.After(serviceIPPoll):
		}
	}
}

// serviceIPNotReady reports the API server's error for a ClusterIP it
// cannot allocate while its ServiceCIDR allocators start.
func serviceIPNotReady(err error) bool {
	return apierrors.IsInternalError(err) && strings.Contains(err.Error(), "failed to allocate a serviceIP") && strings.Contains(err.Error(), "range is full")
}

// applyWithRetry runs apply, retrying with backoff (200 ms doubling to 2 s)
// while it fails with serviceIPNotReady, for at most limit.
func applyWithRetry(ctx context.Context, limit time.Duration, apply func() error) error {
	deadline := time.Now().Add(limit)
	delay := 200 * time.Millisecond
	for {
		err := apply()
		if err == nil || !serviceIPNotReady(err) || time.Now().Add(delay).After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay = min(2*delay, 2*time.Second)
	}
}

// connectKube waits for the node's kubeconfig and API server, writes the
// Nimbo kubeconfig and caches the client on the sandbox.
func (m *Manager) connectKube(ctx context.Context, wfID string, node *kubeNode, wait time.Duration) (*kubeClient, error) {
	sb := m.sandbox(wfID)
	if kc := sb.kube.clientFor(node); kc != nil {
		return kc, nil
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var last error
	for {
		raw, err := m.docker(ctx, "exec", node.Name, "cat", "/etc/rancher/k3s/k3s.yaml")
		if err == nil && raw != "" {
			kc, cerr := newKubeClient(wfID, []byte(raw), node.APIPort, m.KubeconfigPath(wfID))
			if cerr == nil {
				if v, verr := kc.serverVersion(ctx); verr == nil {
					kc.version = v
					kc.ingressPort = node.IngressPort
					kc.apiPort = node.APIPort
					sb.kube.setClient(kc, node)
					return kc, nil
				} else {
					last = verr
				}
			} else {
				last = cerr
			}
		} else if err != nil {
			last = err
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("the cluster's API server did not become ready within %s: %v", wait, last)
			}
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
