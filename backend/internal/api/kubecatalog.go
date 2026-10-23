package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kubecatalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The Kubernetes palette (package kubecatalog): starters and images add
// YAML documents to a source (like POST …/kubernetes/objects), charts add
// a helm source with the pinned chart vendored.

// kubeCatalog is GET /catalog/kubernetes.
func (s *Server) kubeCatalog(w http.ResponseWriter, r *http.Request) error {
	return ok(w, kubecatalog.Items())
}

// kubeSchema is GET /catalog/kubernetes/schema: the OpenAPI definitions of
// the pinned Kubernetes release the k8s_schema rule validates against.
func (s *Server) kubeSchema(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "application/json")
	_, err := w.Write(kuberules.SchemaJSON())
	return err
}

// kubeCatalogYAML is GET /catalog/kubernetes/{item}/yaml?name=&namespace=:
// the documents a starter or image entry adds (palette preview).
func (s *Server) kubeCatalogYAML(w http.ResponseWriter, r *http.Request) error {
	y, err := kubecatalog.YAML(r.PathValue("item"), r.URL.Query().Get("name"), r.URL.Query().Get("namespace"))
	if err != nil {
		return errorf(http.StatusNotFound, "%s", err)
	}
	return ok(w, map[string]string{"yaml": y})
}

type addKubeCatalogRequest struct {
	ItemID    string `json:"itemId"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// Source is the index of the source starters/images go to (nil: the
	// first manifests or kustomize source, like POST …/kubernetes/objects).
	Source   *int            `json:"source"`
	Position *model.Position `json:"position"`
}

// addKubeCatalogItem is POST /workflows/{id}/kubernetes/catalog: add a
// palette entry to a Kubernetes workflow. It answers 201 {workflow,
// nodeIds} (the objects added).
func (s *Server) addKubeCatalogItem(w http.ResponseWriter, r *http.Request) error {
	var req addKubeCatalogRequest
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	id := r.PathValue("id")
	item, found := kubecatalog.Find(strings.TrimSpace(req.ItemID))
	if !found {
		return errorf(http.StatusBadRequest, "unknown palette entry %q (GET /catalog/kubernetes lists them)", req.ItemID)
	}
	if req.Name != "" && kubecatalog.ObjectName(req.Name) == "" {
		return errorf(http.StatusBadRequest, "name %q has no letters or digits", req.Name)
	}
	if req.Namespace != "" && kubecatalog.ObjectName(req.Namespace) != req.Namespace {
		return errorf(http.StatusBadRequest, "namespace %q must be a DNS label (lowercase letters, digits and -)", req.Namespace)
	}
	var (
		wf    *model.Workflow
		nodes []string
		err   error
	)
	if item.Group == kubecatalog.GroupChart {
		wf, nodes, err = s.addKubeChart(r, id, item, req)
	} else {
		ns := req.Namespace
		if ns == "" {
			if ns, err = s.kubeTargetNamespace(id, req.Source); err != nil {
				return err
			}
		}
		name, err := s.freeKubeName(id, orName(kubecatalog.ObjectName(req.Name), kubecatalog.DefaultName(item.ID)), ns)
		if err != nil {
			return err
		}
		var y string
		if y, err = kubecatalog.YAML(item.ID, name, ns); err != nil {
			return errorf(http.StatusBadRequest, "%s", err)
		}
		source := -1
		if req.Source != nil {
			source = *req.Source
		}
		wf, nodes, err = s.addKubeObjects(id, []byte(y), source, "")
	}
	if err != nil {
		return err
	}
	if req.Position != nil && len(nodes) > 0 {
		if wf, err = s.placeKubeNodes(id, nodes, *req.Position); err != nil {
			return err
		}
	}
	return writeJSON(w, http.StatusCreated, map[string]any{"workflow": wf, "nodeIds": nonNil(nodes)})
}

// addKubeChart downloads the pinned chart (outside the edit lock) and adds
// its wrapper chart as a new helm source.
func (s *Server) addKubeChart(r *http.Request, id string, item kubecatalog.Item, req addKubeCatalogRequest) (*model.Workflow, []string, error) {
	cur, err := s.cfg.Store.Get(id)
	if err != nil {
		return nil, nil, err
	}
	if !cur.IsKubernetes() {
		return nil, nil, errorf(http.StatusBadRequest, "%s is a %s workflow; the Kubernetes palette adds to Kubernetes workflows", cur.Name, model.KindLabel(cur.Kind))
	}
	cs, err := kubecatalog.AddChart(r.Context(), http.DefaultClient, item.ID, req.Name, req.Namespace)
	if err != nil {
		return nil, nil, errorf(http.StatusBadGateway, "%s", err)
	}
	index := -1
	wf, err := s.editKube(id, func(wf *model.Workflow, _ *kube.Project, _ *kube.Editor, files map[string][]byte) error {
		for _, src := range wf.Kubernetes.Sources {
			if src.Path == cs.Source.Path {
				return errorf(http.StatusConflict, "%s already exists: pass another name", cs.Source.Path)
			}
		}
		for p, data := range cs.Files {
			files[p] = data
		}
		index = len(wf.Kubernetes.Sources)
		wf.Kubernetes.Sources = append(wf.Kubernetes.Sources, cs.Source)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	var nodes []string
	for _, n := range wf.Kubernetes.Graph.Nodes {
		if n.Source == index {
			nodes = append(nodes, n.ID)
		}
	}
	return wf, nodes, nil
}

// placeKubeNodes puts the first added node at pos and the others below it
// (the canvas lays out nodes without a position).
func (s *Server) placeKubeNodes(id string, nodes []string, pos model.Position) (*model.Workflow, error) {
	return s.editKube(id, func(wf *model.Workflow, _ *kube.Project, _ *kube.Editor, _ map[string][]byte) error {
		for i, nodeID := range nodes {
			for j := range wf.Kubernetes.Graph.Nodes {
				if n := &wf.Kubernetes.Graph.Nodes[j]; n.ID == nodeID {
					n.Position = &model.Position{X: pos.X, Y: pos.Y + float64(i)*120}
				}
			}
		}
		return nil
	})
}

// kubeTargetNamespace is the namespace palette objects go to when the
// request names none: "" when the target source sets one (objects without
// a namespace land there), else the namespace most of the source's
// namespaced objects use ("" = default).
func (s *Server) kubeTargetNamespace(id string, source *int) (string, error) {
	wf, err := s.cfg.Store.Get(id)
	if err != nil {
		return "", err
	}
	if !wf.IsKubernetes() || wf.Kubernetes == nil {
		return "", nil
	}
	idx := -1
	if source != nil {
		idx = *source
	} else {
		for i, src := range wf.Kubernetes.Sources {
			if src.Kind == model.KubeManifests || src.Kind == model.KubeKustomize {
				idx = i
				break
			}
		}
	}
	if idx < 0 || idx >= len(wf.Kubernetes.Sources) || wf.Kubernetes.Sources[idx].Namespace != "" {
		return "", nil
	}
	count := map[string]int{}
	best := ""
	for _, n := range wf.Kubernetes.Graph.Nodes {
		if n.Source != idx || n.Namespace == "" {
			continue
		}
		count[n.Namespace]++
		if c := count[n.Namespace]; c > count[best] || c == count[best] && n.Namespace < best {
			best = n.Namespace
		}
	}
	if best == "default" {
		return "", nil
	}
	return best, nil
}

// freeKubeName turns base into a name no object of namespace ns uses yet
// (base-2, base-3, ...), so palette objects never replace existing ones.
func (s *Server) freeKubeName(id, base, ns string) (string, error) {
	wf, err := s.cfg.Store.Get(id)
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	if wf.Kubernetes != nil {
		for _, n := range wf.Kubernetes.Graph.Nodes {
			// Without a namespace the objects land in the source's (or
			// default): any namespace's names are avoided.
			if ns == "" || n.Namespace == ns || n.Namespace == "" {
				used[n.Name] = true
				used[strings.TrimSuffix(n.Name, "-secrets")] = true
			}
		}
	}
	name := base
	for i := 2; used[name]; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name, nil
}
