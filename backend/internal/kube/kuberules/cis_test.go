package kuberules

import (
	"slices"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// cisFindings lists the findings of the CIS rules as
// "severity:code@node attribute".
func cisFindings(diags []model.Diagnostic) []string {
	var out []string
	for _, d := range diags {
		if slices.ContainsFunc(cisRules, func(r Rule) bool { return r.Code == d.Code }) {
			out = append(out, string(d.Severity)+":"+d.Code+"@"+d.NodeID+" "+d.Attribute)
		}
	}
	slices.Sort(out)
	return out
}

func TestCISWorkloadRules(t *testing.T) {
	diags := checkFiles(t, "", map[string]string{"app.yaml": `apiVersion: apps/v1
kind: Deployment
metadata: {name: bad, namespace: shop}
spec:
  selector: {matchLabels: {app: bad}}
  template:
    metadata: {labels: {app: bad}}
    spec:
      containers:
        - name: app
          image: nginx:1.27
          ports: [{containerPort: 80, hostPort: 80}]
          securityContext:
            seccompProfile: {type: Unconfined}
            capabilities: {add: [NET_RAW, SYS_TIME]}
          env:
            - name: DB_PASSWORD
              valueFrom: {secretKeyRef: {name: db, key: password}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: good, namespace: shop}
spec:
  selector: {matchLabels: {app: good}}
  template:
    metadata: {labels: {app: good}}
    spec:
      automountServiceAccountToken: false
      securityContext: {seccompProfile: {type: RuntimeDefault}}
      containers:
        - name: app
          image: nginx:1.27
          ports: [{containerPort: 8080}]
          securityContext:
            capabilities: {drop: [ALL], add: [NET_BIND_SERVICE]}
          volumeMounts: [{name: db, mountPath: /run/secrets/db, readOnly: true}]
      volumes: [{name: db, secret: {secretName: db}}]
---
apiVersion: apps/v1
kind: DaemonSet
metadata: {name: agent, namespace: shop}
spec:
  selector: {matchLabels: {app: agent}}
  template:
    metadata: {labels: {app: agent}}
    spec:
      automountServiceAccountToken: false
      securityContext: {seccompProfile: {type: RuntimeDefault}}
      containers:
        - name: agent
          image: agent:1.0
          ports: [{containerPort: 9100, hostPort: 9100}]
          securityContext: {capabilities: {drop: [ALL]}}
          envFrom: [{secretRef: {name: agent}}]
---
apiVersion: v1
kind: Pod
metadata: {name: win, namespace: shop}
spec:
  os: {name: windows}
  automountServiceAccountToken: false
  hostNetwork: true
  securityContext: {windowsOptions: {hostProcess: true}}
  containers:
    - name: tool
      image: tool:1.0
`})
	bad, agent, win := model.KubeNodeID("Deployment", "shop", "bad"), model.KubeNodeID("DaemonSet", "shop", "agent"), model.KubeNodeID("Pod", "shop", "win")
	c0 := "spec.template.spec.containers[0]"
	want := []string{
		"warning:k8s_host_port@" + bad + " " + c0 + ".ports[0].hostPort",
		"info:k8s_host_port@" + agent + " " + c0 + ".ports[0].hostPort", // a node agent
		"info:k8s_net_raw@" + bad + " " + c0 + ".securityContext.capabilities",
		"info:k8s_capabilities@" + bad + " " + c0 + ".securityContext.capabilities",
		"info:k8s_seccomp@" + bad + " " + c0 + ".securityContext.seccompProfile",
		"info:k8s_secret_env_ref@" + bad + " " + c0 + ".env[0].valueFrom",
		"info:k8s_secret_env_ref@" + agent + " " + c0 + ".envFrom[0].secretRef",
		"info:k8s_automount_token@" + bad + " spec.template.spec",
		"warning:k8s_windows_host_process@" + win + " spec.securityContext.windowsOptions.hostProcess",
	}
	slices.Sort(want)
	if got := cisFindings(diags); !slices.Equal(got, want) {
		t.Errorf("findings:\n got %q\nwant %q", got, want)
	}
}

func TestCISRBACRules(t *testing.T) {
	diags := checkFiles(t, "", map[string]string{"rbac.yaml": `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: everything}
rules:
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: risky, namespace: shop}
rules:
  - apiGroups: [""]
    resources: [secrets]
    verbs: [list]
  - apiGroups: [""]
    resources: [pods, persistentvolumes]
    verbs: [create]
  - apiGroups: [rbac.authorization.k8s.io]
    resources: [clusterroles]
    verbs: [escalate]
  - apiGroups: [""]
    resources: [nodes/proxy, serviceaccounts/token]
    verbs: [get, create]
  - apiGroups: [certificates.k8s.io]
    resources: [certificatesigningrequests/approval]
    verbs: [update]
  - apiGroups: [admissionregistration.k8s.io]
    resources: [mutatingwebhookconfigurations]
    verbs: [patch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: narrow, namespace: shop}
rules:
  - apiGroups: [""]
    resources: [secrets]
    resourceNames: [db]
    verbs: [get]
  - apiGroups: [""]
    resources: [configmaps, pods/log]
    verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: admins}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cluster-admin}
subjects:
  - {kind: Group, name: system:masters, apiGroup: rbac.authorization.k8s.io}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: app, namespace: shop}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: narrow}
subjects:
  - {kind: ServiceAccount, name: default, namespace: shop}
  - {kind: ServiceAccount, name: api, namespace: shop}
`})
	all, risky := model.KubeNodeID("ClusterRole", "", "everything"), model.KubeNodeID("Role", "shop", "risky")
	admins, app := model.KubeNodeID("ClusterRoleBinding", "", "admins"), model.KubeNodeID("RoleBinding", "shop", "app")
	want := []string{
		// A role granting everything grants every sensitive permission.
		"info:k8s_rbac_wildcard@" + all + " rules[0]",
		"info:k8s_rbac_secrets@" + all + " rules[0]",
		"info:k8s_rbac_create_pods@" + all + " rules[0]",
		"info:k8s_rbac_escalate@" + all + " rules[0]",
		"info:k8s_rbac_create_pv@" + all + " rules[0]",
		"info:k8s_rbac_node_proxy@" + all + " rules[0]",
		"info:k8s_rbac_csr_approval@" + all + " rules[0]",
		"info:k8s_rbac_webhook_config@" + all + " rules[0]",
		"info:k8s_rbac_sa_token@" + all + " rules[0]",
		"info:k8s_rbac_secrets@" + risky + " rules[0]",
		"info:k8s_rbac_create_pods@" + risky + " rules[1]",
		"info:k8s_rbac_create_pv@" + risky + " rules[1]",
		"info:k8s_rbac_escalate@" + risky + " rules[2]",
		"info:k8s_rbac_node_proxy@" + risky + " rules[3]",
		"info:k8s_rbac_sa_token@" + risky + " rules[3]",
		"info:k8s_rbac_csr_approval@" + risky + " rules[4]",
		"info:k8s_rbac_webhook_config@" + risky + " rules[5]",
		"warning:k8s_rbac_cluster_admin@" + admins + " roleRef",
		"warning:k8s_rbac_system_masters@" + admins + " subjects[0]",
		"warning:k8s_rbac_default_sa@" + app + " subjects[0]",
	}
	slices.Sort(want)
	if got := cisFindings(diags); !slices.Equal(got, want) {
		t.Errorf("findings:\n got %q\nwant %q", got, want)
	}
}

func TestCISRulesRegistered(t *testing.T) {
	loud := map[string]bool{"k8s_host_port": true, "k8s_windows_host_process": true, "k8s_rbac_cluster_admin": true,
		"k8s_rbac_system_masters": true, "k8s_rbac_default_sa": true, "k8s_rbac_wildcard": true}
	for _, r := range cisRules {
		got, ok := ruleByCode[r.Code]
		if !ok || !slices.ContainsFunc(Rules(), func(b model.BestPracticeRule) bool { return b.Code == r.Code }) {
			t.Errorf("%s is not registered", r.Code)
		}
		if got.DefaultOff == loud[r.Code] {
			t.Errorf("%s: opt-in %v", r.Code, got.DefaultOff)
		}
		if r.Docs == "" || r.Why == "" || r.Description == "" {
			t.Errorf("%s lacks docs, why or description", r.Code)
		}
	}
}
