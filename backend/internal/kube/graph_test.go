package kube

import (
	"context"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// TestInferredEdges finds Service hosts in env values and in container
// command/args ("--cache-server=tcp://redis.shop:6379").
func TestInferredEdges(t *testing.T) {
	manifests := `apiVersion: v1
kind: Service
metadata: {name: redis, namespace: shop}
spec:
  selector: {app: redis}
  ports: [{port: 6379}]
---
apiVersion: v1
kind: Service
metadata: {name: backend, namespace: shop}
spec:
  selector: {app: backend}
  ports: [{port: 80}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: backend, namespace: shop}
spec:
  selector: {matchLabels: {app: backend}}
  template:
    metadata: {labels: {app: backend}}
    spec:
      containers:
        - name: backend
          image: echo:1
          args: ["--cache-server=tcp://redis.shop:6379"]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: frontend, namespace: shop}
spec:
  selector: {matchLabels: {app: frontend}}
  template:
    metadata: {labels: {app: frontend}}
    spec:
      containers:
        - name: web
          image: web:1
          env:
            - {name: BACKEND_URL, value: "http://backend.shop/echo"}
`
	wf := &model.Workflow{Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "k8s"}}}}
	p := Open(context.Background(), t.TempDir(), wf, Options{Content: map[string][]byte{"k8s/app.yaml": []byte(manifests)}})
	g := p.Graph(nil)
	want := map[string]string{
		"Deployment/shop/backend->Service/shop/redis":    "args",
		"Deployment/shop/frontend->Service/shop/backend": "BACKEND_URL",
	}
	for _, e := range g.Edges {
		if e.Kind != model.KubeEdgeInferred {
			continue
		}
		label, ok := want[e.Source+"->"+e.Target]
		if !ok || label != e.Label {
			t.Errorf("unexpected inferred edge %+v", e)
		}
		delete(want, e.Source+"->"+e.Target)
	}
	if len(want) > 0 {
		t.Errorf("missing inferred edges %v (edges %+v)", want, g.Edges)
	}
}
