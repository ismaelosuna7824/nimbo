package kube

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// TestUnsetContainerField removes fields of a container (an entry of a
// list item) and of a named volume: the inspector's Mounts and Hardening
// sections clear them.
func TestUnsetContainerField(t *testing.T) {
	dir, wf := rbacProject(t, model.KubeSource{Kind: model.KubeManifests, Path: "k8s"}, map[string]string{"k8s/app.yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
  namespace: shop
spec:
  selector:
    matchLabels: {app: app}
  template:
    metadata:
      labels: {app: app}
    spec:
      containers:
        - name: app
          image: nginx:1.27
          securityContext:
            privileged: true
            capabilities:
              add: ["NET_ADMIN"]
          volumeMounts:
            - name: logs
              mountPath: /logs
      volumes:
        - name: logs
          hostPath:
            path: /var/log
`})
	c := "spec.template.spec.containers[name=app]"
	if err := rbacEdit(t, dir, wf, func(p *Project, e *Editor) error {
		o := object(t, p, "Deployment/shop/app")
		for _, f := range []string{c + ".volumeMounts", c + ".securityContext.privileged", c + ".securityContext.capabilities.add", "spec.template.spec.volumes[name=logs]"} {
			if err := e.Unset(o, f); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "k8s/app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
  namespace: shop
spec:
  selector:
    matchLabels: {app: app}
  template:
    metadata:
      labels: {app: app}
    spec:
      containers:
        - name: app
          image: nginx:1.27
`
	if string(got) != want {
		t.Fatalf("got\n%s", got)
	}
}
