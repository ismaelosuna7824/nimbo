package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/go-logr/logr"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// kubeClient talks to one workflow's cluster through Nimbo's kubeconfig.
type kubeClient struct {
	wfID       string
	kubeconfig string // path of the Nimbo kubeconfig
	context    string // its only context, model.KubeClusterName
	rest       *rest.Config
	cs         kubernetes.Interface
	dyn        dynamic.Interface
	disc       discovery.CachedDiscoveryInterface
	mapper     *restmapper.DeferredDiscoveryRESTMapper

	version     string
	apiPort     int
	ingressPort int
}

var quietKlog sync.Once

// newKubeClient rewrites the node's kubeconfig (k3s.yaml, server
// https://127.0.0.1:6443) for the published API port with the cluster's
// name as cluster, user and context, writes it to path (0600) and builds
// clients from it alone: no KUBECONFIG, no ~/.kube/config.
func newKubeClient(wfID string, raw []byte, apiPort int, path string) (*kubeClient, error) {
	// client-go's reflectors log every failed watch while a cluster is
	// stopped; the status says it instead.
	quietKlog.Do(func() { klog.SetLogger(logr.Discard()) })
	src, err := clientcmd.Load(raw)
	if err != nil {
		return nil, fmt.Errorf("parse the cluster kubeconfig: %w", err)
	}
	cur := src.Contexts[src.CurrentContext]
	if cur == nil || src.Clusters[cur.Cluster] == nil || src.AuthInfos[cur.AuthInfo] == nil {
		return nil, fmt.Errorf("the cluster kubeconfig has no usable current context")
	}
	name := model.KubeClusterName(wfID)
	cluster := src.Clusters[cur.Cluster].DeepCopy()
	cluster.Server = fmt.Sprintf("https://127.0.0.1:%d", apiPort)
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[name] = cluster
	cfg.AuthInfos[name] = src.AuthInfos[cur.AuthInfo].DeepCopy()
	cfg.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name, Namespace: "default"}
	cfg.CurrentContext = name
	data, err := clientcmd.Write(*cfg)
	if err != nil {
		return nil, err
	}
	if err := writeFile(path, data); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	rc, err := clientcmd.NewDefaultClientConfig(*cfg, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, err
	}
	rc.QPS, rc.Burst = 50, 100
	rc.UserAgent = "nimbo"
	cs, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	disc := memory.NewMemCacheClient(cs.Discovery())
	return &kubeClient{
		wfID: wfID, kubeconfig: path, context: name,
		rest: rc, cs: cs, dyn: dyn, disc: disc, mapper: restmapper.NewDeferredDiscoveryRESTMapper(disc),
	}, nil
}

func (kc *kubeClient) serverVersion(ctx context.Context) (string, error) {
	// ServerVersion has no context parameter; bound it through the REST
	// client instead.
	body, err := kc.cs.Discovery().RESTClient().Get().AbsPath("/version").Do(ctx).Raw()
	if err != nil {
		return "", err
	}
	var v struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", err
	}
	return v.GitVersion, nil
}

// kubeState is a sandbox's Kubernetes runtime state.
type kubeState struct {
	mu     sync.Mutex
	client *kubeClient
	// node is the node container the client was built for (ports, name).
	node kubeNode

	// watch stops the informers (kube_watch.go); synced is closed once
	// their caches synced, listers is set then.
	watch   context.CancelFunc
	synced  chan struct{}
	listers *kubeListers
	gen     int
	// last is the last published status (UpdatedAt zeroed) and lastJSON
	// its encoding, for change detection.
	last     *model.KubeStatus
	lastJSON string
	// links: Service node ID -> workload node IDs (from EndpointSlices),
	// pods: running pod UID -> workload node ID and limits, for the
	// samplers.
	links map[string][]string
	pods  map[string]containerInfo

	forwards *forwarder
	// perf is the cgroup sampler's usage state (kube_stats.go).
	perf composePerf
	// node container the sampler reads (set while it runs).
	sampling string
}

// clientFor returns the cached client when it was built for node's ports.
func (s *kubeState) clientFor(node *kubeNode) *kubeClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil && s.node.APIPort == node.APIPort && s.node.Name == node.Name {
		return s.client
	}
	return nil
}

func (s *kubeState) setClient(kc *kubeClient, node *kubeNode) {
	s.mu.Lock()
	s.client = kc
	s.node = *node
	s.mu.Unlock()
}

func (s *kubeState) current() *kubeClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}
