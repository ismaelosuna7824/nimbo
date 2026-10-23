package kuberules

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const badApp = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
spec:
  selector:
    matchLabels:
      app: web
  template:
    metadata:
      labels:
        app: website
    spec:
      hostNetwork: true
      containers:
        - name: web
          image: nginx
          ports:
            - containerPort: 80
          env:
            - name: DB_PASSWORD
              value: hunter2
            - name: PORT
              value: 8080
          securityContext:
            privileged: true
          readinessProbe:
            httpGet:
              path: /
              port: http
          envFrom:
            - configMapRef:
                name: missing-config
      volumes:
        - name: host
          hostPath:
            path: /var/run
---
apiVersion: v1
kind: Service
metadata:
  name: web
  namespace: shop
spec:
  selector:
    app: web
  ports:
    - port: 80
      targetPort: http
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: web
  namespace: shop
spec:
  rules:
    - http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: web
                port:
                  number: 8080
          - path: /api
            pathType: Prefix
            backend:
              service:
                name: api
                port:
                  number: 80
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: shop
data:
  API_TOKEN: abc123
  TOKEN_URL: https://auth.example.com/token
  LOG_LEVEL: debug
---
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: old
  namespace: shop
spec: {}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: data
  namespace: shop
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 1Gi
  contianers: []
`

func TestRulesOnBadApp(t *testing.T) {
	diags := checkFiles(t, "", map[string]string{"app.yaml": badApp})
	got := codes(diags)
	want := []string{
		"error:k8s_workload_selector@app.yaml:7",
		"error:k8s_schema@app.yaml:25", // env value 8080 is not a string
		"error:k8s_removed_api@app.yaml:86",
		"error:k8s_schema@app.yaml:103", // contianers
		"info:k8s_namespace_missing@app.yaml:5",
		"warning:k8s_host_namespaces@app.yaml:15",
		"warning:k8s_latest_tag@app.yaml:18",
		"warning:k8s_no_requests@app.yaml:17",
		"warning:k8s_secret_env@app.yaml:23",
		"warning:k8s_privileged@app.yaml:27",
		"warning:k8s_probe_port@app.yaml:31",
		"warning:k8s_unknown_ref@app.yaml:33",
		"warning:k8s_host_path@app.yaml:37",
		"warning:k8s_selector_mismatch@app.yaml:46",
		"warning:k8s_ingress_backend@app.yaml:64",
		"warning:k8s_ingress_backend@app.yaml:71",
		"warning:k8s_secret_in_configmap@app.yaml:82",
		"info:k8s_pvc_storage_class@app.yaml:98",
		"info:k8s_run_as_root@app.yaml:14",
		"info:k8s_no_memory_limit@app.yaml:17",
		"info:k8s_no_liveness_probe@app.yaml:17",
		"info:k8s_hardening@app.yaml:26",
		"info:k8s_network_policy@app.yaml:14",
		"info:k8s_hardening@app.yaml:14", // API token mounted
	}
	if len(got) != len(want) {
		t.Errorf("got %d findings, want %d", len(got), len(want))
	}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("missing %s", w)
		}
	}
	if t.Failed() {
		t.Logf("got:\n%s", strings.Join(got, "\n"))
	}
	for _, d := range diags {
		if d.Detail == "" || d.Docs == "" || d.Fix == "" || d.NodeID == "" {
			t.Errorf("%s at %s:%d lacks why/docs/fix/node: %+v", d.Code, d.File, d.Line, d)
		}
	}
	// TOKEN_URL and LOG_LEVEL are not credentials.
	for _, d := range diags {
		if d.Code == "k8s_secret_in_configmap" && strings.Contains(d.Message, "TOKEN_URL") {
			t.Errorf("TOKEN_URL flagged: %s", d.Message)
		}
	}
}

const goodApp = `apiVersion: v1
kind: Namespace
metadata:
  name: shop
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: shop
spec:
  replicas: 2
  selector:
    matchLabels: {app: api}
  template:
    metadata:
      labels: {app: api, tier: backend}
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 10001
      containers:
        - name: api
          image: api:dev
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: 8080
          env:
            - name: DB_PASSWORD
              valueFrom:
                secretKeyRef: {name: db, key: password}
          readinessProbe:
            httpGet: {path: /healthz, port: http}
          livenessProbe:
            tcpSocket: {port: http}
          resources:
            requests: {cpu: 100m, memory: 128Mi}
            limits: {memory: 256Mi}
---
apiVersion: v1
kind: Secret
metadata:
  name: db
  namespace: shop
stringData:
  password: s3cret
---
apiVersion: v1
kind: Service
metadata:
  name: api
  namespace: shop
spec:
  selector: {app: api}
  ports:
    - name: http
      port: 80
      targetPort: http
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: api
  namespace: shop
spec:
  rules:
    - host: api.localhost
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: api
                port: {name: http}
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: api
  namespace: shop
spec:
  minAvailable: 1
  selector:
    matchLabels: {app: api}
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: api
  namespace: shop
spec:
  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: api}
  minReplicas: 2
  maxReplicas: 5
  metrics:
    - type: Resource
      resource:
        name: cpu
        target: {type: Utilization, averageUtilization: 70}
`

func TestRulesQuietOnGoodApp(t *testing.T) {
	diags := checkFiles(t, "prod", map[string]string{"app.yaml": goodApp})
	for _, d := range diags {
		if d.Severity != model.SeverityInfo || !ruleByCode[d.Code].DefaultOff {
			t.Errorf("unexpected %s %s: %s", d.Severity, d.Code, d.Message)
		}
	}
}

func TestSingleReplicaOnlyInProd(t *testing.T) {
	app := strings.Replace(goodApp, "replicas: 2", "replicas: 1", 1)
	app = strings.Replace(app, "minReplicas: 2", "minReplicas: 1", 1)
	if hasCode(checkFiles(t, "dev", map[string]string{"a.yaml": app}), "k8s_single_replica") {
		t.Error("single replica flagged in dev")
	}
	diags := checkFiles(t, "production", map[string]string{"a.yaml": app})
	if !slices.ContainsFunc(diags, func(d model.Diagnostic) bool {
		return d.Code == "k8s_single_replica" && d.Severity == model.SeverityWarning
	}) {
		t.Errorf("single replica not flagged in production: %v", codes(diags))
	}

	// A StatefulSet with its own volumes can't just scale: info.
	sts := `apiVersion: apps/v1
kind: StatefulSet
metadata: {name: db, namespace: shop}
spec:
  serviceName: db
  selector: {matchLabels: {app: db}}
  template:
    metadata: {labels: {app: db}}
    spec:
      containers:
        - name: db
          image: postgres:17-alpine
  volumeClaimTemplates:
    - metadata: {name: data}
      spec:
        accessModes: [ReadWriteOnce]
        storageClassName: local-path
        resources: {requests: {storage: 1Gi}}
`
	for _, d := range checkFiles(t, "prod", map[string]string{"a.yaml": goodApp, "b.yaml": sts}) {
		if d.Code == "k8s_single_replica" && d.Severity != model.SeverityInfo {
			t.Errorf("stateful single replica is %s: %s", d.Severity, d.Message)
		}
	}
}

func TestLocalImages(t *testing.T) {
	app := strings.Replace(goodApp, "image: api:dev", "image: api", 1)
	diags := checkFiles(t, "", map[string]string{"a.yaml": app})
	if !hasCode(diags, "k8s_local_image") || hasCode(diags, "k8s_latest_tag") {
		t.Errorf("local image: %v", codes(diags))
	}
	app = strings.Replace(goodApp, "imagePullPolicy: IfNotPresent", "imagePullPolicy: Always", 1)
	if !hasCode(checkFiles(t, "", map[string]string{"a.yaml": app}), "k8s_local_image") {
		t.Error("Always pull of a local image not flagged")
	}
}

func TestSchema(t *testing.T) {
	cases := map[string]string{
		"unknown kind":  "apiVersion: apps/v1\nkind: Deploymnet\nmetadata: {name: x}\n",
		"wrong version": "apiVersion: v1\nkind: Deployment\nmetadata: {name: x}\n",
		"bool string":   "apiVersion: v1\nkind: Pod\nmetadata: {name: x}\nspec:\n  hostNetwork: \"true\"\n  containers: [{name: a, image: a:1}]\n",
		"int string":    "apiVersion: v1\nkind: Service\nmetadata: {name: x}\nspec:\n  ports: [{port: \"80\"}]\n",
		"annotation":    "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n  annotations:\n    prometheus.io/scrape: true\n",
		"quantity":      "apiVersion: v1\nkind: Pod\nmetadata: {name: x}\nspec:\n  containers: [{name: a, image: a:1, resources: {requests: {memory: 128MB}}}]\n",
		"required":      "apiVersion: v1\nkind: Pod\nmetadata: {name: x}\nspec:\n  containers: [{image: a:1}]\n",
		"base64":        "apiVersion: v1\nkind: Secret\nmetadata: {name: x}\ndata:\n  password: not base64!\n",
	}
	for name, doc := range cases {
		diags := checkFiles(t, "", map[string]string{"a.yaml": doc})
		if !slices.ContainsFunc(diags, func(d model.Diagnostic) bool { return d.Code == "k8s_schema" && d.Severity == model.SeverityError }) {
			t.Errorf("%s: no k8s_schema error in %v", name, codes(diags))
		}
	}
	diags := checkFiles(t, "", map[string]string{"a.yaml": cases["unknown kind"]})
	if !strings.Contains(diags[0].Fix, "Deployment") {
		t.Errorf("no kind suggestion: %s", diags[0].Fix)
	}
	ok := "apiVersion: v1\nkind: Pod\nmetadata: {name: x, creationTimestamp: null}\nspec:\n  containers: [{name: a, image: a:1, resources: {requests: {cpu: 1, memory: 1.5Gi}}, ports: [{containerPort: 80}]}]\n"
	for _, d := range checkFiles(t, "", map[string]string{"a.yaml": ok}) {
		if d.Code == "k8s_schema" {
			t.Errorf("valid pod: %s", d.Message)
		}
	}
}

func TestCustomResources(t *testing.T) {
	crd := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.example.com}
spec:
  group: example.com
  scope: Namespaced
  names: {kind: Widget, plural: widgets}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                size: {type: integer}
                color: {type: string, enum: [red, blue]}
`
	cr := "apiVersion: example.com/v1\nkind: Widget\nmetadata: {name: w}\nspec:\n  size: big\n  color: green\n  shape: round\n"
	diags := checkFiles(t, "", map[string]string{"crd.yaml": crd, "w.yaml": cr})
	n := 0
	for _, d := range diags {
		if d.Code == "k8s_schema" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("want 3 schema errors on the Widget, got %v", codes(diags))
	}
	// Unknown custom kinds (CRD in the cluster) are skipped.
	if diags := checkFiles(t, "", map[string]string{"c.yaml": "apiVersion: cert-manager.io/v1\nkind: Certificate\nmetadata: {name: c}\nspec: {anything: 1}\n"}); len(diags) != 0 {
		t.Errorf("custom kind without CRD: %v", codes(diags))
	}
}

func TestFilterSettings(t *testing.T) {
	diags := checkFiles(t, "", map[string]string{"app.yaml": badApp})
	out := FilterSettings(slices.Clone(diags), model.BestPracticeSettings{DisabledRules: []string{"k8s_latest_tag", "k8s_schema"}})
	if hasCode(out, "k8s_latest_tag") || !hasCode(out, "k8s_schema") {
		t.Error("disabled warning kept or error dropped")
	}
	if hasCode(out, "k8s_hardening") {
		t.Error("opt-in rule shown by default")
	}
	out = FilterSettings(slices.Clone(diags), model.BestPracticeSettings{EnabledRules: []string{"k8s_hardening"}})
	if !hasCode(out, "k8s_hardening") {
		t.Error("enabled opt-in rule dropped")
	}
}

func TestRuleTable(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range rules {
		if !strings.HasPrefix(r.Code, "k8s_") || r.Title == "" || r.Why == "" || r.Docs == "" || r.Description == "" || seen[r.Code] {
			t.Errorf("bad rule %+v", r)
		}
		seen[r.Code] = true
		if (r.Severity == model.SeverityError) != (r.Category == catErrors) {
			t.Errorf("%s: errors and only errors belong to %s", r.Code, catErrors)
		}
	}
}
