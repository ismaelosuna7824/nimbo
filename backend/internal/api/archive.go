package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// archiveWorkflow hides a workflow from recents and stops its sandbox while
// keeping all state, so restoring it is instant. Runs and load tests are
// refused until it is unarchived.
func (s *Server) archiveWorkflow(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	wf, err := s.cfg.Store.Get(id)
	if err != nil {
		return err
	}
	// Stopping must not be torn down halfway when the client disconnects.
	// An active run fails the archive (409) so nothing is cut mid-apply.
	// Without Docker there is nothing running; archiving proceeds. A docker
	// workflow's compose project goes down; its named volumes (data) stay
	// for when it is restored. A Kubernetes workflow's cluster stops, with
	// everything in it kept.
	ctx := context.WithoutCancel(r.Context())
	stop := s.cfg.Sandbox.Stop
	switch {
	case wf.IsDocker():
		stop = func(ctx context.Context, id string) error { return s.cfg.Sandbox.ComposeDown(ctx, id, false) }
	case wf.IsKubernetes():
		stop = s.cfg.Sandbox.KubeStop
	}
	if err := stop(ctx, id); err != nil {
		if s.cfg.Sandbox.DockerAvailable(ctx) == nil {
			return fmt.Errorf("stop sandbox: %w", err)
		}
		s.log.Warn("archive without docker", "workflow", id, "err", err)
	}
	if wf.Archived {
		return ok(w, wf)
	}
	wf.Archived = true
	if err := s.save(wf, nil); err != nil {
		return err
	}
	return ok(w, wf)
}

func (s *Server) unarchiveWorkflow(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.mutate(r.PathValue("id"), func(wf *model.Workflow) ([]model.Diagnostic, error) {
		wf.Archived = false
		return nil, nil
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// refuseArchived rejects runtime work (Terraform runs, load tests, sandbox
// start) on archived workflows.
func refuseArchived(wf *model.Workflow) error {
	if wf.Archived {
		return errorf(http.StatusConflict, "workflow is archived")
	}
	return nil
}
