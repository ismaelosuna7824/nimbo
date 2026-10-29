package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// eksRestartStderr is the error output of an apply against Floci 2.3 while
// it replaced the cluster's k3s node for the first node group: the add-on
// was created but its waiter failed (so it is tainted) and the pod identity
// association was refused.
const eksRestartStderr = `
Error: waiting for EKS Add-On (autoscaler-demo:eks-pod-identity-agent) create: operation error EKS: DescribeAddon, https response error StatusCode: 400, RequestID: 3aa23c57-46fe-4ce5-bf30-10743b8aef28, InvalidRequestException: Cluster must be ACTIVE, but was CREATING

  with aws_eks_addon.pod_identity_agent[0],
  on compute.tf line 52, in resource "aws_eks_addon" "pod_identity_agent":
  52: resource "aws_eks_addon" "pod_identity_agent" {


Error: creating EKS Pod Identity Association

  with aws_eks_pod_identity_association.cluster_autoscaler,
  on compute.tf line 59, in resource "aws_eks_pod_identity_association" "cluster_autoscaler":
  59: resource "aws_eks_pod_identity_association" "cluster_autoscaler" {

operation error EKS: CreatePodIdentityAssociation, https response error
StatusCode: 400, RequestID: 3cc52f6e-cccb-44e2-9273-fa55852b52fa,
InvalidRequestException: Cluster must be ACTIVE
`

const otherApplyError = `
Error: creating S3 Bucket (assets): operation error S3: CreateBucket, https response error StatusCode: 409, BucketAlreadyExists

  with aws_s3_bucket.assets,
  on main.tf line 1, in resource "aws_s3_bucket" "assets":
`

func TestEKSRestartFailures(t *testing.T) {
	lines := func(s string) []string { return strings.Split(s, "\n") }
	for _, tc := range []struct {
		name      string
		stderr    string
		wantOK    bool
		wantAddrs []string
	}{
		{name: "add-on waiter and wrapped association error", stderr: eksRestartStderr, wantOK: true,
			wantAddrs: []string{"aws_eks_addon.pod_identity_agent[0]", "aws_eks_pod_identity_association.cluster_autoscaler"}},
		{name: "another error too", stderr: eksRestartStderr + otherApplyError},
		{name: "another error first", stderr: otherApplyError + eksRestartStderr},
		{name: "another error only", stderr: otherApplyError},
		{name: "no diagnostics", stderr: "Cluster must be ACTIVE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addrs, ok := eksRestartFailures(lines(tc.stderr))
			if ok != tc.wantOK || (tc.wantOK && !slices.Equal(addrs, tc.wantAddrs)) {
				t.Errorf("eksRestartFailures = %q, %v; want %q, %v", addrs, ok, tc.wantAddrs, tc.wantOK)
			}
		})
	}
}

// fakeEKS serves Floci's ListClusters and DescribeCluster: the cluster
// reports CREATING for the first creating describes, then ACTIVE.
func fakeEKS(t *testing.T, creating int, final string) (port int, describes *atomic.Int32) {
	t.Helper()
	describes = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/clusters":
			_, _ = w.Write([]byte(`{"clusters":["autoscaler-demo"]}`))
		case "/clusters/autoscaler-demo":
			status := final
			if int(describes.Add(1)) <= creating {
				status = "CREATING"
			}
			_, _ = w.Write([]byte(`{"cluster":{"name":"autoscaler-demo","status":"` + status + `"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ = strconv.Atoi(u.Port())
	return port, describes
}

// eksPlanJSON is `terraform show -json` of the plan after the restart:
// the tainted add-on is replaced, the association created.
const eksPlanJSON = `{"resource_changes":[
{"address":"aws_eks_addon.pod_identity_agent[0]","mode":"managed","type":"aws_eks_addon","name":"pod_identity_agent","index":0,"action_reason":"replace_because_tainted","change":{"actions":["delete","create"],"before":{},"after":{}}},
{"address":"aws_eks_pod_identity_association.cluster_autoscaler","mode":"managed","type":"aws_eks_pod_identity_association","name":"cluster_autoscaler","change":{"actions":["create"],"before":null,"after":{}}}]}`

// A plan that also replaces the cluster, which the failed apply didn't taint.
const eksPlanReplacesClusterJSON = `{"resource_changes":[
{"address":"aws_eks_addon.pod_identity_agent[0]","mode":"managed","type":"aws_eks_addon","name":"pod_identity_agent","index":0,"action_reason":"replace_because_tainted","change":{"actions":["delete","create"],"before":{},"after":{}}},
{"address":"aws_eks_cluster.main","mode":"managed","type":"aws_eks_cluster","name":"main","change":{"actions":["delete","create"],"before":{},"after":{}}}]}`

// TestApplyRetriesAfterEKSRestart drives applyPlan with a docker CLI
// stand-in whose first applies fail with applyErr, and a fake Floci EKS API.
func TestApplyRetriesAfterEKSRestart(t *testing.T) {
	eksPollInterval = time.Millisecond
	t.Cleanup(func() { eksPollInterval = 2 * time.Second })
	for _, tc := range []struct {
		name        string
		applyErr    string
		fails       int    // applies that fail
		plan        string // show -json of the plan after the restart
		cluster     string // its status once it stops CREATING
		wantApplies int
		wantErr     bool
		wantLog     string
	}{
		{name: "applies the rest once the cluster is ACTIVE", applyErr: eksRestartStderr, fails: 1, plan: eksPlanJSON, cluster: "ACTIVE",
			wantApplies: 2, wantLog: "Floci restarted an EKS cluster"},
		{name: "a restart during the retry", applyErr: eksRestartStderr, fails: 2, plan: eksPlanJSON, cluster: "ACTIVE",
			wantApplies: 3},
		{name: "gives up after the retries", applyErr: eksRestartStderr, fails: 1 + eksRetries + 1, plan: eksPlanJSON, cluster: "ACTIVE",
			wantApplies: 1 + eksRetries, wantErr: true},
		{name: "other errors are not retried", applyErr: otherApplyError, fails: 1, plan: eksPlanJSON, cluster: "ACTIVE",
			wantApplies: 1, wantErr: true},
		{name: "the new plan destroys what the user didn't review", applyErr: eksRestartStderr, fails: 1, plan: eksPlanReplacesClusterJSON, cluster: "ACTIVE",
			wantApplies: 1, wantErr: true, wantLog: "Not applying again: the new plan also deletes or replaces aws_eks_cluster.main"},
		{name: "the cluster failed", applyErr: eksRestartStderr, fails: 1, plan: eksPlanJSON, cluster: "FAILED",
			wantApplies: 1, wantErr: true, wantLog: "Not applying again: EKS cluster autoscaler-demo is FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			for name, content := range map[string]string{
				"apply.err": tc.applyErr, "plan.json": tc.plan, "fails": strconv.Itoa(tc.fails),
			} {
				if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			calls := filepath.Join(home, "calls")
			docker := filepath.Join(home, "docker")
			script := "#!/bin/sh\necho \"$*\" >> " + calls + "\n" +
				"case \"$*\" in\n" +
				"  *' show -json '*) cat " + filepath.Join(home, "plan.json") + ";;\n" +
				"  *' apply '*) n=$(cat " + filepath.Join(home, "fails") + ")\n" +
				"    if [ \"$n\" -gt 0 ]; then echo $((n-1)) > " + filepath.Join(home, "fails") + "; cat " + filepath.Join(home, "apply.err") + " >&2; exit 1; fi\n" +
				"    echo 'Apply complete! Resources: 2 added, 0 changed, 1 destroyed.';;\n" +
				"  *' plan '*) echo 'Plan: 2 to add, 0 to change, 1 to destroy.';;\n" +
				"esac\n"
			if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			m, err := New(Config{Home: home, Docker: docker})
			if err != nil {
				t.Fatal(err)
			}
			wf := &model.Workflow{ID: "wf-eks", Clouds: []model.Cloud{model.CloudAWS}}
			port, describes := fakeEKS(t, 2, tc.cluster)
			m.sandbox(wf.ID).ports = map[int]int{emulators[model.CloudAWS].port: port}
			work := m.WorkDir(wf.ID)
			if err := os.MkdirAll(filepath.Join(work, ".nimbo"), 0o755); err != nil {
				t.Fatal(err)
			}
			reviewed := &model.PlanSummary{Add: 26}
			rs := &runState{run: model.Run{ID: "r1", WorkflowID: wf.ID, Plan: reviewed}}

			err = m.applyPlan(context.Background(), rs, RunRequest{Workflow: wf, Action: model.ActionApply}, work, nil, nil, pendingPlanFile("r1"))

			raw, _ := os.ReadFile(calls)
			var applies, checks int
			for _, call := range strings.Split(string(raw), "\n") {
				switch {
				case strings.Contains(call, " apply "):
					applies++
				case strings.Contains(call, " -detailed-exitcode "):
					checks++
				}
			}
			var log strings.Builder
			for _, l := range rs.run.Log {
				log.WriteString(l.Text + "\n")
			}
			if applies != tc.wantApplies {
				t.Errorf("applies = %d, want %d\n%s", applies, tc.wantApplies, raw)
			}
			if (err != nil) != tc.wantErr || (err != nil && err.Error() != "terraform apply exited with code 1") {
				t.Errorf("err = %v, want error %v\n%s", err, tc.wantErr, log.String())
			}
			if tc.wantLog != "" && !strings.Contains(log.String(), tc.wantLog) {
				t.Errorf("log lacks %q:\n%s", tc.wantLog, log.String())
			}
			if tc.wantErr {
				if rs.applied || rs.run.ExitCode != 1 {
					t.Errorf("applied %v, exit %d", rs.applied, rs.run.ExitCode)
				}
				return
			}
			if !rs.applied || rs.run.ExitCode != 0 || checks != 1 {
				t.Errorf("applied %v, exit %d, post-deploy checks %d", rs.applied, rs.run.ExitCode, checks)
			}
			if rs.run.Plan != reviewed {
				t.Errorf("plan summary = %+v, want the reviewed plan's", rs.run.Plan)
			}
			if describes.Load() < 3 {
				t.Errorf("describes = %d: the retry didn't wait for the cluster to be ACTIVE", describes.Load())
			}
			if _, err := os.Stat(filepath.Join(work, filepath.FromSlash(eksRetryPlanFile))); !os.IsNotExist(err) {
				t.Errorf("the retry's plan file is left behind: %v", err)
			}
		})
	}
}
