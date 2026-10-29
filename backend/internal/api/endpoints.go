package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/endpoints"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// endpointsSandbox gathers what the endpoints builder needs about the
// sandbox of wf. A stopped or unreachable sandbox is not an error: its
// resources keep their stable URLs and Running stays empty. With live set,
// Running comes from a full status check (containers and emulator health);
// otherwise from the pod's cached host ports, which costs no docker call so
// "Try it" requests stay fast — a stopped emulator then surfaces as a
// connection error in the result. When st has EC2 instances, their
// containers are listed (one docker ps) for their published host ports.
func (s *Server) endpointsSandbox(ctx context.Context, wf *model.Workflow, st *model.DeployedState, live bool) endpoints.Sandbox {
	sources, services := s.codeSources(wf.ID)
	sb := endpoints.Sandbox{
		Bases:          map[model.Cloud]string{},
		Running:        map[model.Cloud]bool{},
		Region:         s.cfg.Settings.Get().DefaultAWSRegion,
		Sources:        sources,
		ServiceSources: services,
	}
	if live {
		if st, err := s.cfg.Sandbox.Status(ctx, wf.ID); err == nil && st.State == model.SandboxRunning {
			for _, e := range st.Emulators {
				if e.State == model.SandboxRunning && e.URL != "" {
					sb.Running[e.Cloud] = true
					sb.Bases[e.Cloud] = e.URL
				}
			}
		}
	} else {
		for _, c := range model.AllClouds {
			if u, ok := s.cfg.Sandbox.EmulatorURL(wf.ID, c); ok {
				sb.Running[c] = true
				sb.Bases[c] = u
			}
		}
	}
	for _, c := range model.AllClouds {
		if sb.Bases[c] != "" {
			continue
		}
		if u, ok := s.cfg.Sandbox.StableEmulatorURL(wf.ID, c); ok {
			sb.Bases[c] = u
		}
	}
	sb.Instances = s.sandboxInstances(ctx, wf, st)
	return sb
}

// sandboxInstances lists wf's EC2 instance containers (one docker ps) when
// st has instances; nil otherwise or when Docker can't be asked.
func (s *Server) sandboxInstances(ctx context.Context, wf *model.Workflow, st *model.DeployedState) map[string]sandbox.InstanceAccess {
	if !hasResourceType(st, "aws_instance") {
		return nil
	}
	insts, err := s.cfg.Sandbox.Instances(ctx, wf.ID)
	if err != nil {
		s.log.Debug("list sandbox EC2 instances", "workflow", wf.ID, "err", err)
		return nil
	}
	return insts
}

func hasResourceType(st *model.DeployedState, typ string) bool {
	if st == nil {
		return false
	}
	for _, r := range st.Resources {
		if r.Type == typ && (r.Mode == "" || r.Mode == "managed") {
			return true
		}
	}
	return false
}

// Source files scanned for route hints: code only, bounded in size.
var codeExts = map[string]bool{".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".py": true, ".go": true, ".rb": true, ".java": true, ".cs": true}

const (
	maxSourceFile  = 256 << 10
	maxSourceTotal = 2 << 20
)

// codeSources reads the workflow's code assets: every file (Lambda
// sources), and per service folder services/<svc>/ without test files.
func (s *Server) codeSources(wfID string) (all []string, services map[string][]string) {
	services = map[string][]string{}
	total := 0
	root := s.cfg.Store.AssetsDir(wfID)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !codeExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxSourceFile || total+int(info.Size()) > maxSourceTotal {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		total += len(data)
		all = append(all, string(data))
		if rel, err := filepath.Rel(root, path); err == nil {
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) > 2 && parts[0] == "services" && !testFile(parts[len(parts)-1]) {
				services[parts[1]] = append(services[parts[1]], string(data))
			}
		}
		return nil
	})
	return all, services
}

// testFile reports whether name is a test source (Go, JS/TS, Python).
func testFile(name string) bool {
	base := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	return strings.HasSuffix(base, "_test") || strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".spec") || strings.HasPrefix(base, "test_")
}

func (s *Server) endpointsInput(r *http.Request, wf *model.Workflow, live bool) (*model.DeployedState, endpoints.Sandbox, error) {
	st, err := s.cfg.Sandbox.State(r.Context(), wf)
	if err != nil {
		return nil, endpoints.Sandbox{}, err
	}
	return st, s.endpointsSandbox(r.Context(), wf, st, live), nil
}

func (s *Server) getEndpoints(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	switch {
	case wf.IsDocker():
		rt, err := s.composeRuntime(r.Context(), wf)
		if err != nil {
			return err
		}
		return ok(w, endpoints.BuildCompose(wf, rt))
	case wf.IsKubernetes():
		st, err := s.cfg.Sandbox.KubeStatus(r.Context(), wf.ID)
		if err != nil {
			return err
		}
		return ok(w, endpoints.BuildKube(wf, st))
	}
	st, sb, err := s.endpointsInput(r, wf, true)
	if err != nil {
		return err
	}
	return ok(w, endpoints.Build(wf, st, sb))
}

func (s *Server) tryEndpoint(w http.ResponseWriter, r *http.Request) error {
	var req model.TryRequest
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.NodeID) == "" || strings.TrimSpace(req.Action) == "" {
		return errorf(http.StatusBadRequest, "nodeId and action are required")
	}
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	var res model.TryResult
	switch {
	case wf.IsDocker():
		rt, rerr := s.composeRuntime(r.Context(), wf)
		if rerr != nil {
			return rerr
		}
		res, err = endpoints.TryCompose(r.Context(), rt, req)
	case wf.IsKubernetes():
		st, serr := s.cfg.Sandbox.KubeStatus(r.Context(), wf.ID)
		if serr != nil {
			return serr
		}
		res, err = endpoints.TryKube(r.Context(), st, req)
	default:
		st, sb, ierr := s.endpointsInput(r, wf, false)
		if ierr != nil {
			return ierr
		}
		res, err = endpoints.Try(r.Context(), wf, st, sb, req)
	}
	var re *endpoints.RequestError
	switch {
	case errors.Is(err, endpoints.ErrNotFound):
		return errorf(http.StatusNotFound, "%s: %q", err.Error(), req.NodeID)
	case errors.As(err, &re):
		return errorf(http.StatusBadRequest, "%s", re.Msg)
	case err != nil:
		return err
	}
	return ok(w, res)
}

func (s *Server) postmanCollection(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	var col endpoints.PostmanCollection
	switch {
	case wf.IsDocker():
		rt, err := s.composeRuntime(r.Context(), wf)
		if err != nil {
			return err
		}
		col = endpoints.PostmanCompose(wf, rt)
	case wf.IsKubernetes():
		st, err := s.cfg.Sandbox.KubeStatus(r.Context(), wf.ID)
		if err != nil {
			return err
		}
		col = endpoints.PostmanKube(wf, st)
	default:
		st, sb, err := s.endpointsInput(r, wf, false)
		if err != nil {
			return err
		}
		col = endpoints.Postman(wf, st, sb)
	}
	data, err := json.MarshalIndent(col, "", "  ")
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-nimbo-sandbox.postman_collection.json"`, wf.ID))
	_, err = w.Write(data)
	return err
}
