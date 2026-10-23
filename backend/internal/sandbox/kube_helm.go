package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"helm.sh/helm/v4/pkg/action"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/kube"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/yaml"
)

// helmGetter hands Helm the workflow cluster's clients: Helm never loads a
// kubeconfig (nor KUBECONFIG, ~/.kube/config or ~/.kube/cache) itself.
type helmGetter struct {
	kc *kubeClient
	ns string
}

func (g helmGetter) ToRESTConfig() (*rest.Config, error) { return rest.CopyConfig(g.kc.rest), nil }

func (g helmGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return g.kc.disc, nil
}

func (g helmGetter) ToRESTMapper() (meta.RESTMapper, error) { return g.kc.mapper, nil }

func (g helmGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	overrides := &clientcmd.ConfigOverrides{Context: clientcmdapi.Context{Namespace: g.ns}}
	cfg, err := clientcmd.LoadFromFile(g.kc.kubeconfig)
	if err != nil {
		cfg = clientcmdapi.NewConfig()
	}
	return clientcmd.NewDefaultClientConfig(*cfg, overrides)
}

// runLogWriter turns Helm's warnings into run log lines.
type runLogWriter struct {
	m  *Manager
	rs *runState
}

func (w runLogWriter) Write(p []byte) (int, error) {
	for line := range strings.Lines(string(p)) {
		if line = strings.TrimSpace(line); line != "" {
			w.m.logLine(w.rs, "stderr", "helm: "+line)
		}
	}
	return len(p), nil
}

func (m *Manager) helmConfig(rs *runState, kc *kubeClient, ns string) (*action.Configuration, error) {
	var out io.Writer = io.Discard
	if rs != nil {
		out = runLogWriter{m: m, rs: rs}
	}
	cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err := cfg.Init(helmGetter{kc: kc, ns: ns}, ns, "secret"); err != nil {
		return nil, err
	}
	return cfg, nil
}

func helmNamespace(src KubeApplySource) string {
	if src.Namespace != "" {
		return src.Namespace
	}
	return "default"
}

// pullPolicyRenderer is the Helm post-renderer giving locally built images
// imagePullPolicy IfNotPresent, like the manifests' objects get.
type pullPolicyRenderer struct {
	built map[string]bool
	notes *[]string
}

func (p pullPolicyRenderer) Run(in *bytes.Buffer) (*bytes.Buffer, error) {
	docs := splitYAMLDocs(in.String())
	var out bytes.Buffer
	for _, doc := range docs {
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil || obj == nil {
			out.WriteString("---\n" + doc + "\n")
			continue
		}
		changed := localPullPolicy(obj, p.built)
		if len(changed) == 0 {
			out.WriteString("---\n" + doc + "\n")
			continue
		}
		name, _ := objMeta(obj)
		for _, img := range changed {
			*p.notes = append(*p.notes, fmt.Sprintf("%s %s uses the locally built image %s: imagePullPolicy IfNotPresent", str(obj, "kind"), name, img))
		}
		data, err := yaml.Marshal(obj)
		if err != nil {
			return nil, err
		}
		out.WriteString("---\n")
		out.Write(data)
	}
	return &out, nil
}

// splitYAMLDocs splits a multi-document YAML stream on "---" lines.
func splitYAMLDocs(s string) []string {
	var docs []string
	var cur strings.Builder
	flush := func() {
		if d := strings.TrimSpace(cur.String()); d != "" {
			docs = append(docs, cur.String())
		}
		cur.Reset()
	}
	for line := range strings.Lines(s) {
		if t := strings.TrimRight(line, "\r\n"); t == "---" || strings.HasPrefix(t, "--- ") {
			flush()
			continue
		}
		cur.WriteString(line)
	}
	flush()
	return docs
}

// helmUpgrade installs or upgrades a Helm source's release and returns the
// objects of its manifest (for the rollout wait; Helm owns them, so they
// are not in Nimbo's inventory: Source -1).
func (m *Manager) helmUpgrade(ctx context.Context, rs *runState, kc *kubeClient, wfID string, src KubeApplySource, built map[string]bool, timeout time.Duration) ([]kubeRef, error) {
	h := src.Helm
	if h.Chart == nil {
		return nil, invalidKube("helm source %s has no chart", src.Path)
	}
	ns := helmNamespace(src)
	if _, err := kc.ensureNamespace(ctx, ns); err != nil {
		return nil, fmt.Errorf("create namespace %s: %w", ns, err)
	}
	if err := m.applyChartCRDs(ctx, rs, kc, h); err != nil {
		return nil, err
	}
	cfg, err := m.helmConfig(rs, kc, ns)
	if err != nil {
		return nil, err
	}
	var notes []string
	post := pullPolicyRenderer{built: built, notes: &notes}
	hist := action.NewHistory(cfg)
	hist.Max = 1
	_, herr := hist.Run(h.Release)
	var rel any
	switch {
	case errors.Is(herr, driver.ErrReleaseNotFound):
		m.logLine(rs, "system", fmt.Sprintf("helm install %s %s (chart %s %s) in namespace %s", h.Release, src.Path, h.Chart.Name(), h.Chart.Metadata.Version, ns))
		in := action.NewInstall(cfg)
		in.ReleaseName, in.Namespace = h.Release, ns
		in.WaitStrategy = kube.HookOnlyStrategy // the rollout wait follows, with Nimbo's diagnosis
		in.Timeout = timeout
		in.ForceConflicts = true
		in.SkipCRDs = true // applied above, established before the templates need them
		in.PostRenderer = post
		rel, err = in.RunWithContext(ctx, h.Chart, h.Values)
	case herr != nil:
		return nil, herr
	default:
		m.logLine(rs, "system", fmt.Sprintf("helm upgrade %s %s (chart %s %s) in namespace %s", h.Release, src.Path, h.Chart.Name(), h.Chart.Metadata.Version, ns))
		up := action.NewUpgrade(cfg)
		up.Namespace = ns
		up.WaitStrategy = kube.HookOnlyStrategy
		up.Timeout = timeout
		up.ForceConflicts = true
		up.ServerSideApply = "true"
		up.PostRenderer = post
		rel, err = up.RunWithContext(ctx, h.Release, h.Chart, h.Values)
	}
	for _, n := range notes {
		m.logLine(rs, "system", n)
	}
	if err != nil {
		return nil, err
	}
	r, ok := rel.(*releasev1.Release)
	if !ok {
		return nil, fmt.Errorf("unexpected Helm release type %T", rel)
	}
	status := ""
	if r.Info != nil {
		status = string(r.Info.Status)
	}
	m.logLine(rs, "stdout", fmt.Sprintf("Helm release %s: revision %d %s", r.Name, r.Version, status))
	return manifestRefs(r.Manifest, ns, kc), nil
}

// applyChartCRDs server-side applies the crds/ files of the chart and its
// enabled dependencies and waits until each is established. Helm's own CRD
// install only waits with the release's wait strategy (HookOnly here), so
// the templates using them (kube-prometheus-stack's ServiceMonitors) could
// not map yet; Helm also never updates CRDs on upgrade, which apply does.
func (m *Manager) applyChartCRDs(ctx context.Context, rs *runState, kc *kubeClient, h *KubeHelmRelease) error {
	if err := chartutil.ProcessDependencies(h.Chart, h.Values); err != nil {
		return fmt.Errorf("chart dependencies: %w", err)
	}
	crds := h.Chart.CRDObjects()
	if len(crds) == 0 {
		return nil
	}
	m.logLine(rs, "system", fmt.Sprintf("Applying %d CustomResourceDefinition file(s) of chart %s", len(crds), h.Chart.Name()))
	for _, crd := range crds {
		for _, doc := range splitYAMLDocs(string(crd.File.Data)) {
			var obj map[string]any
			if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
				return fmt.Errorf("CRD %s: %w", crd.Filename, err)
			}
			if obj == nil {
				continue
			}
			name, _ := objMeta(obj)
			if err := kc.applyObject(ctx, obj); err != nil {
				return fmt.Errorf("apply CustomResourceDefinition %s: %s", name, apiMessage(err))
			}
			if err := kc.waitCRD(ctx, name); err != nil {
				return err
			}
		}
	}
	return nil
}

// manifestRefs lists the objects of a rendered release manifest.
func manifestRefs(manifest, ns string, kc *kubeClient) []kubeRef {
	var refs []kubeRef
	for _, doc := range splitYAMLDocs(manifest) {
		var obj map[string]any
		if yaml.Unmarshal([]byte(doc), &obj) != nil || obj == nil {
			continue
		}
		name, objNS := objMeta(obj)
		if name == "" || str(obj, "kind") == "" {
			continue
		}
		scoped, err := kc.namespaced(str(obj, "apiVersion"), str(obj, "kind"))
		if err == nil && scoped && objNS == "" {
			objNS = ns
		}
		if !scoped {
			objNS = ""
		}
		refs = append(refs, kubeRef{APIVersion: str(obj, "apiVersion"), Kind: str(obj, "kind"), Namespace: objNS, Name: name, Source: -1})
	}
	return refs
}

// helmUninstall removes a release; a missing one is not an error.
func (m *Manager) helmUninstall(ctx context.Context, rs *runState, kc *kubeClient, rel kubeRelease) error {
	cfg, err := m.helmConfig(rs, kc, rel.Namespace)
	if err != nil {
		return err
	}
	un := action.NewUninstall(cfg)
	un.IgnoreNotFound = true
	un.WaitStrategy = kube.HookOnlyStrategy
	un.Timeout = 2 * time.Minute
	if _, err := un.Run(rel.Name); err != nil && !errors.Is(err, driver.ErrReleaseNotFound) {
		return fmt.Errorf("helm uninstall %s: %w", rel.Name, err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m.logLine(rs, "stdout", "Helm release "+rel.Name+" uninstalled")
	return nil
}
