package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Floci (2.3) runs an EKS cluster as one k3s container sized like a node.
// When the cluster's first node group picks an instance type other than the
// node's, Floci replaces that container and the cluster reports CREATING
// again for a few seconds. EKS calls Terraform makes for the cluster
// meanwhile (add-ons and their create waiters, pod identity associations)
// fail with InvalidRequestException "Cluster must be ACTIVE", which no
// provider version retries, and an add-on created before its waiter failed
// is left tainted. An apply that failed only that way is planned and
// applied again once the clusters are ACTIVE.

// eksNotActive is in Floci's error for EKS calls on a cluster that isn't
// ACTIVE.
const eksNotActive = "Cluster must be ACTIVE"

// eksRetries bounds the applies after EKS cluster restarts in one run.
const eksRetries = 2

// eksRetryPlanFile is where the plan after an EKS cluster restart is saved.
const eksRetryPlanFile = ".nimbo/eks-retry.tfplan"

// replaceTainted is Terraform's action_reason for replacing a tainted
// instance.
const replaceTainted = "replace_because_tainted"

// How long and how often an apply waits for restarted EKS clusters.
var (
	eksActiveTimeout = 3 * time.Minute
	eksPollInterval  = 2 * time.Second
)

// eksRestartFailures reads the error diagnostics of a failed apply (its
// stderr lines, `-no-color`) and reports whether every one of them is
// Floci's cluster-not-ACTIVE error (the message may wrap onto a later line
// of its diagnostic), with the resource addresses they name.
func eksRestartFailures(stderr []string) (addrs []string, ok bool) {
	errs, found := 0, false // found: the current diagnostic is the restart
	for _, line := range stderr {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "Error: ") {
			if errs > 0 && !found {
				return nil, false
			}
			errs, found = errs+1, false
		}
		if errs == 0 {
			continue
		}
		if strings.Contains(t, eksNotActive) {
			found = true
		}
		if a, with := strings.CutPrefix(t, "with "); with && strings.HasSuffix(a, ",") {
			if a = strings.TrimSuffix(a, ","); !slices.Contains(addrs, a) {
				addrs = append(addrs, a)
			}
		}
	}
	if errs == 0 || !found {
		return nil, false
	}
	return addrs, true
}

// waitEKSActive waits until every EKS cluster of the workflow's AWS
// emulator is ACTIVE (Floci's EKS clusters are not regional).
func (m *Manager) waitEKSActive(ctx context.Context, wfID string) error {
	base, ok := m.EmulatorURL(wfID, model.CloudAWS)
	if !ok {
		return errors.New("the AWS emulator is not running")
	}
	deadline := time.Now().Add(eksActiveTimeout)
	for {
		pending, err := eksPending(ctx, base)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("EKS cluster %s is still not ACTIVE after %s", strings.Join(pending, ", "), eksActiveTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(eksPollInterval):
		}
	}
}

// eksPending lists the EKS clusters at the emulator base that are still
// CREATING or UPDATING; a cluster in any other state but ACTIVE is an error.
func eksPending(ctx context.Context, base string) ([]string, error) {
	var list struct {
		Clusters []string `json:"clusters"`
	}
	if err := getJSON(ctx, base+"/clusters", &list); err != nil {
		return nil, fmt.Errorf("list EKS clusters: %w", err)
	}
	var pending []string
	for _, name := range list.Clusters {
		var d struct {
			Cluster struct {
				Status string `json:"status"`
			} `json:"cluster"`
		}
		if err := getJSON(ctx, base+"/clusters/"+url.PathEscape(name), &d); err != nil {
			return nil, fmt.Errorf("describe EKS cluster %s: %w", name, err)
		}
		switch d.Cluster.Status {
		case "ACTIVE":
		case "CREATING", "UPDATING":
			pending = append(pending, name)
		default:
			return nil, fmt.Errorf("EKS cluster %s is %s", name, d.Cluster.Status)
		}
	}
	return pending, nil
}

// getJSON decodes the 200 response of a GET into v.
func getJSON(ctx context.Context, url string, v any) error {
	code, body, err := httpGet(ctx, url)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("HTTP %d", code)
	}
	return json.Unmarshal(body, v)
}

// replanAfterEKSRestart waits for the workflow's EKS clusters to be ACTIVE
// and plans again into eksRetryPlanFile, which it returns. The new plan may
// only replace the tainted instances whose errors were the restart (failed):
// they were created by this run; anything else it deletes or replaces needs
// the user's confirmation, so no plan is returned.
func (m *Manager) replanAfterEKSRestart(ctx context.Context, rs *runState, req RunRequest, work string, env, userEnv []string, failed []string, attempt int) (string, error) {
	m.logLine(rs, "system", "Floci restarted an EKS cluster: it replaces the cluster's k3s node when the first node group sets another instance type, and EKS calls made meanwhile fail with \""+eksNotActive+"\". Waiting for the cluster to be ACTIVE, then applying the rest.")
	if err := m.waitEKSActive(ctx, req.Workflow.ID); err != nil {
		return "", err
	}
	file := eksRetryPlanFile
	path := filepath.Join(work, filepath.FromSlash(file))
	args := []string{"plan", "-input=false", "-no-color", "-out=" + file}
	m.logLine(rs, "system", "$ terraform "+strings.Join(args, " "))
	code, err := m.terraform(ctx, rs, "plan-eks-retry-"+strconv.Itoa(attempt), env, userEnv, args...)
	if err == nil && code != 0 {
		err = fmt.Errorf("terraform plan exited with code %d", code)
	}
	var changes []model.PlannedChange
	if err == nil {
		if changes, err = m.readPlanFile(ctx, req.Workflow, file); err != nil {
			err = fmt.Errorf("could not read the new plan: %w", err)
		}
	}
	for _, d := range destructiveChanges(changes) {
		if err == nil && (d.Cause != replaceTainted || !slices.Contains(failed, d.Address)) {
			err = fmt.Errorf("the new plan also deletes or replaces %s; deploy again to review it", d.Address)
		}
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return file, nil
}
