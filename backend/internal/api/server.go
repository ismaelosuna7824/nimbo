// Package api serves the nimbo REST API and orchestrates the store, codec,
// catalog, sandbox and load-test engine.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/agents"
	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/challenge"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/events"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kubecatalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/learn"
	"github.com/ismaelosuna7824/nimbo/backend/internal/loadtest"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
	"github.com/ismaelosuna7824/nimbo/backend/internal/settings"
	"github.com/ismaelosuna7824/nimbo/backend/internal/setup"
	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

// Config wires the server's dependencies. Every field is required.
type Config struct {
	Version string
	Home    string
	Store   *store.Store
	// Connections is the global library of saved connections; the LoadTest
	// engine resolves connection-backed targets through it.
	Connections *store.Connections
	Catalog     *catalog.Catalog
	Sandbox     *sandbox.Manager
	LoadTest    *loadtest.Engine
	Events      *events.Hub
	Logger      *slog.Logger
	// Settings holds the user's preferences; PUT /settings applies them to
	// Sandbox and Catalog.
	Settings *settings.Store
	// Agents registers Nimbo's MCP server with AI agent applications.
	Agents *agents.Manager
	// MCPCatalog lists the MCP server's tools and prompts (GET /mcp/catalog).
	MCPCatalog func() MCPCatalog
	// Setup runs the onboarding checks and one-click fixes (GET /setup).
	Setup *setup.Checker
	// Learn is the learn database (roadmaps, challenges, progress); its
	// Status reports content migrations that failed to load.
	Learn *learn.Store
}

// Server handles /api/v1.
type Server struct {
	cfg Config
	log *slog.Logger
	mux *http.ServeMux
	// origins is allowedOrigins plus NIMBO_DEV_ORIGINS (comma-separated),
	// used for browser checks against a dev server on another port.
	origins map[string]bool

	// mu serializes read-modify-write cycles on workflows so concurrent
	// edits from the UI and agents never lose updates.
	mu sync.Mutex

	session sessionState

	// challenges runs "Check my solution" of challenge workflows.
	challenges *challenge.Checker
}

const (
	maxBody       = 8 << 20
	maxImportBody = 20 << 20
)

var allowedOrigins = map[string]bool{
	"tauri://localhost":       true,
	"http://tauri.localhost":  true,
	"https://tauri.localhost": true,
	"http://localhost:1420":   true,
	"http://127.0.0.1:1420":   true,
}

// New returns a server for cfg.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg, log: cfg.Logger, mux: http.NewServeMux(), origins: map[string]bool{}}
	for o := range allowedOrigins {
		s.origins[o] = true
	}
	for o := range strings.SplitSeq(os.Getenv("NIMBO_DEV_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			s.origins[o] = true
		}
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	s.challenges = s.newChallenges()
	s.routes()
	return s
}

func (s *Server) routes() {
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if err := fn(w, r); err != nil {
				s.writeError(w, r, err)
			}
		})
	}
	const p = "/api/v1"
	h("GET "+p+"/health", s.health)
	h("GET "+p+"/setup", s.getSetup)
	h("POST "+p+"/setup/actions/{action}", s.setupAction)

	h("GET "+p+"/catalog/providers", s.providers)
	h("GET "+p+"/catalog/categories", s.categories)
	h("GET "+p+"/catalog/resources", s.searchResources)
	h("GET "+p+"/catalog/resources/{kind}/{type}", s.resourceSchema)
	h("GET "+p+"/catalog/templates", s.templates)
	h("GET "+p+"/catalog/load-presets", s.loadPresets)
	h("GET "+p+"/catalog/types", s.catalogTypes)
	h("GET "+p+"/catalog/schemas", s.catalogSchemas)
	h("GET "+p+"/catalog/functions", s.catalogFunctions)
	h("GET "+p+"/catalog/docker/images", s.dockerImages)
	h("GET "+p+"/catalog/docker/compose-schema", s.composeSchema)
	h("GET "+p+"/catalog/kubernetes", s.kubeCatalog)
	h("GET "+p+"/catalog/kubernetes/schema", s.kubeSchema)
	h("GET "+p+"/catalog/kubernetes/{item}/yaml", s.kubeCatalogYAML)
	h("POST "+p+"/workflows/{id}/kubernetes/catalog", s.addKubeCatalogItem)
	h("GET "+p+"/catalog/file-templates", s.fileTemplates)

	h("GET "+p+"/workflows", s.listWorkflows)
	h("POST "+p+"/workflows", s.createWorkflow)
	h("GET "+p+"/workflows/{id}", s.getWorkflow)
	h("PUT "+p+"/workflows/{id}", s.putWorkflow)
	h("DELETE "+p+"/workflows/{id}", s.deleteWorkflow)
	h("POST "+p+"/workflows/{id}/duplicate", s.duplicateWorkflow)
	h("POST "+p+"/workflows/{id}/archive", s.archiveWorkflow)
	h("POST "+p+"/workflows/{id}/unarchive", s.unarchiveWorkflow)
	h("POST "+p+"/workflows/{id}/nodes", s.terraformOnly("Terraform nodes", "use /compose/services", s.addNode))
	h("PATCH "+p+"/workflows/{id}/nodes/{nodeId}", s.terraformOnly("Terraform nodes", "use /compose/services, or /compose/layout for positions", s.patchNode))
	h("DELETE "+p+"/workflows/{id}/nodes/{nodeId}", s.terraformOnly("Terraform nodes", "use DELETE /compose/services/{name}", s.deleteNode))
	h("GET "+p+"/workflows/{id}/connections", s.terraformOnly("Terraform connection recipes", "use /compose/connect", s.connections))
	h("POST "+p+"/workflows/{id}/connections", s.terraformOnly("Terraform connection recipes", "use /compose/connect", s.connect))
	// access editors (access.go; Kubernetes RBAC: kube_access.go)
	h("GET "+p+"/workflows/{id}/access", s.terraformOnly("Terraform access editors", "use /kubernetes/access for RBAC", s.getAccess))
	h("PUT "+p+"/workflows/{id}/access", s.terraformOnly("Terraform access editors", "use /kubernetes/access for RBAC", s.putAccess))
	h("GET "+p+"/access/catalog/{provider}", s.accessCatalog)

	h("GET "+p+"/workflows/{id}/terraform", s.terraformOnly("Generated Terraform", "read the files with /files", s.getTerraform))
	h("PUT "+p+"/workflows/{id}/terraform", s.putTerraform)
	h("GET "+p+"/workflows/{id}/terraform/workspace", s.terraformOnly("The Terraform code workspace", "read the files with /files", s.codeWorkspace))
	h("POST "+p+"/workflows/{id}/terraform/check", s.checkCode)
	h("POST "+p+"/workflows/{id}/export", s.exportDir)
	h("GET "+p+"/workflows/{id}/export.zip", s.exportZip)
	h("GET "+p+"/workflows/{id}/files", s.listFiles)
	h("GET "+p+"/workflows/{id}/files/content", s.getFileContent)
	h("PUT "+p+"/workflows/{id}/files/content", s.putFileContent)
	h("GET "+p+"/workflows/{id}/files/raw", s.getFileRaw)
	h("POST "+p+"/import", s.importWorkflow)
	h("POST "+p+"/import/scan", s.scanImport)
	h("GET "+p+"/workflows/{id}/modules", s.terraformOnly("Terraform modules", "", s.moduleTree))
	h("GET "+p+"/workflows/{id}/modules/{nodeId}", s.terraformOnly("Terraform modules", "", s.moduleView))
	h("POST "+p+"/workflows/{id}/modules/extract", s.terraformOnly("Terraform modules", "", s.extractModule))
	h("POST "+p+"/workflows/{id}/modules/extract/preview", s.terraformOnly("Terraform modules", "", s.previewExtract))
	h("POST "+p+"/workflows/{id}/modules/{nodeId}/inline", s.terraformOnly("Terraform modules", "", s.inlineModule))
	h("GET "+p+"/workflows/{id}/structure", s.terraformOnly("The Terraform structure summary", "", s.structure))
	h("POST "+p+"/workflows/{id}/modules/scaffold", s.terraformOnly("Terraform modules", "", s.moduleScaffold))
	h("POST "+p+"/terraform/format", s.formatCode)

	h("POST "+p+"/workflows/{id}/compose/services", s.addComposeService)
	h("PATCH "+p+"/workflows/{id}/compose/services/{name}", s.patchComposeService)
	h("DELETE "+p+"/workflows/{id}/compose/services/{name}", s.deleteComposeService)
	h("POST "+p+"/workflows/{id}/compose/networks", s.addComposeTop("network"))
	h("PATCH "+p+"/workflows/{id}/compose/networks/{name}", s.patchComposeTop("network"))
	h("DELETE "+p+"/workflows/{id}/compose/networks/{name}", s.deleteComposeTop("network"))
	h("POST "+p+"/workflows/{id}/compose/volumes", s.addComposeTop("volume"))
	h("PATCH "+p+"/workflows/{id}/compose/volumes/{name}", s.patchComposeTop("volume"))
	h("DELETE "+p+"/workflows/{id}/compose/volumes/{name}", s.deleteComposeTop("volume"))
	h("GET "+p+"/workflows/{id}/compose/connect", s.composeConnectRecipes)
	h("POST "+p+"/workflows/{id}/compose/connect", s.composeConnect)
	h("PATCH "+p+"/workflows/{id}/compose/layout", s.composeLayout)
	h("PATCH "+p+"/workflows/{id}/compose/stages/{nodeId}", s.patchComposeStage)

	// kubernetes (engine)
	h("PATCH "+p+"/workflows/{id}/kubernetes/layout", s.kubeLayout)
	h("GET "+p+"/workflows/{id}/kubernetes/render", s.kubeRender)
	h("GET "+p+"/workflows/{id}/kubernetes/fields", s.kubeFields)
	h("POST "+p+"/workflows/{id}/kubernetes/objects", s.addKubeObject)
	h("PATCH "+p+"/workflows/{id}/kubernetes/objects", s.patchKubeObject)
	h("DELETE "+p+"/workflows/{id}/kubernetes/objects", s.deleteKubeObject)
	h("GET "+p+"/workflows/{id}/kubernetes/connect", s.kubeConnectRecipes)
	h("POST "+p+"/workflows/{id}/kubernetes/connect", s.kubeConnect)
	h("GET "+p+"/workflows/{id}/kubernetes/access", s.kubeAccess)
	h("PUT "+p+"/workflows/{id}/kubernetes/access", s.putKubeAccess)

	// environments of docker and Kubernetes workflows (environments.go)
	h("GET "+p+"/workflows/{id}/environments", s.listEnvironments)
	h("POST "+p+"/workflows/{id}/environments", s.createEnvironment)
	h("GET "+p+"/workflows/{id}/environments/compare", s.compareEnvironments)
	h("PATCH "+p+"/workflows/{id}/environments/{name}", s.renameEnvironment)
	h("DELETE "+p+"/workflows/{id}/environments/{name}", s.deleteEnvironment)
	h("GET "+p+"/workflows/{id}/environments/{name}/settings", s.environmentSettings)
	h("PATCH "+p+"/workflows/{id}/environments/{name}/settings", s.setEnvironmentSettings)
	h("PUT "+p+"/workflows/{id}/environments/{name}/variables/{key}", s.environmentVariable(false))
	h("DELETE "+p+"/workflows/{id}/environments/{name}/variables/{key}", s.environmentVariable(true))

	h("GET "+p+"/workflows/{id}/sandbox", s.sandboxStatus)
	h("POST "+p+"/workflows/{id}/sandbox/{action}", s.sandboxAction)
	h("POST "+p+"/workflows/{id}/runs", s.startRun)
	h("GET "+p+"/workflows/{id}/runs", s.listRuns)
	h("GET "+p+"/runs/{runId}", s.getRun)
	h("POST "+p+"/runs/{runId}/cancel", s.cancelRun)
	h("GET "+p+"/workflows/{id}/state", s.deployedState)
	h("GET "+p+"/workflows/{id}/compose/status", s.composeStatus)
	h("GET "+p+"/workflows/{id}/compose/ports", s.composePorts)
	h("GET "+p+"/workflows/{id}/compose/logs", s.composeLogs)
	h("GET "+p+"/workflows/{id}/compose/stats", s.composeStats)
	h("GET "+p+"/workflows/{id}/compose/builds", s.composeBuilds)
	h("GET "+p+"/workflows/{id}/kubernetes/status", s.kubeStatus)
	h("GET "+p+"/workflows/{id}/kubernetes/logs", s.kubeLogs)
	h("GET "+p+"/workflows/{id}/kubernetes/stats", s.kubeStats)
	h("POST "+p+"/workflows/{id}/convert/kubernetes", s.convertToKubernetes)
	h("GET "+p+"/workflows/{id}/monitoring", s.getMonitoring)
	h("POST "+p+"/workflows/{id}/monitoring", s.addMonitoring)
	h("GET "+p+"/workflows/{id}/readiness", s.workflowReadiness)
	h("GET "+p+"/workflows/{id}/endpoints", s.getEndpoints)
	h("POST "+p+"/workflows/{id}/endpoints/try", s.tryEndpoint)
	h("GET "+p+"/workflows/{id}/endpoints/postman.json", s.postmanCollection)
	h("GET "+p+"/workflows/{id}/network", s.terraformOnly("Network analysis", "", s.getNetwork))
	h("POST "+p+"/workflows/{id}/network/reachability", s.terraformOnly("Network analysis", "", s.networkReachability))
	h("POST "+p+"/workflows/{id}/network/test-instance", s.terraformOnly("Network analysis", "", s.addTestInstance))

	h("GET "+p+"/workflows/{id}/loadtests/targets", s.loadTargets)
	h("POST "+p+"/workflows/{id}/loadtests", s.startLoadTest)
	h("GET "+p+"/workflows/{id}/loadtests", s.listLoadTests)
	h("GET "+p+"/loadtests/{runId}", s.getLoadTest)
	h("POST "+p+"/loadtests/{runId}/stop", s.stopLoadTest)
	h("GET "+p+"/workflows/{id}/scenarios", s.scenarios)

	// capacity plans from load tests (capacity.go)
	h("POST "+p+"/workflows/{id}/capacity-plan", s.planCapacity)
	h("GET "+p+"/workflows/{id}/capacity-plan", s.getCapacityPlan)
	h("POST "+p+"/workflows/{id}/capacity-plan/apply", s.applyCapacityPlan)

	// challenge workspaces: start, check, solution copy (challenge.go)
	h("POST "+p+"/learn/challenges/{id}/start", s.startChallenge)
	h("GET "+p+"/workflows/{id}/challenge", s.getChallenge)
	h("POST "+p+"/workflows/{id}/challenge/check", s.checkChallenge)
	h("DELETE "+p+"/workflows/{id}/challenge/check", s.cancelChallengeCheck)
	h("POST "+p+"/workflows/{id}/challenge/solution", s.challengeSolution)

	// learn: roadmaps, challenge catalog and progress (learn.go)
	h("GET "+p+"/learn", s.learnOverview)
	h("GET "+p+"/learn/providers/{provider}/roadmap", s.learnRoadmap)
	h("GET "+p+"/learn/challenges", s.learnChallenges)
	h("GET "+p+"/learn/challenges/{id}", s.learnChallenge)
	h("GET "+p+"/learn/progress", s.learnProgress)
	h("PUT "+p+"/learn/progress/{id}", s.putLearnProgress)

	h("GET "+p+"/connections", s.listConnections)
	h("POST "+p+"/connections", s.createConnection)
	h("POST "+p+"/connections/test", s.testConnectionDraft)
	h("GET "+p+"/connections/{id}", s.getConnection)
	h("PUT "+p+"/connections/{id}", s.putConnection)
	h("DELETE "+p+"/connections/{id}", s.deleteConnection)
	h("POST "+p+"/connections/{id}/test", s.testConnection)
	h("GET "+p+"/connections/{id}/targets", s.connectionTargets)
	h("POST "+p+"/connections/{id}/loadtests", s.startConnectionLoadTest)
	h("GET "+p+"/connections/{id}/loadtests", s.listConnectionLoadTests)

	h("GET "+p+"/session", s.getSession)
	h("PUT "+p+"/session", s.putSession)

	h("GET "+p+"/settings", s.getSettings)
	h("PUT "+p+"/settings", s.putSettings)
	h("GET "+p+"/settings/defaults", s.settingsDefaults)
	h("GET "+p+"/best-practices/rules", s.bestPracticeRules)
	h("GET "+p+"/agents", s.listAgents)
	h("POST "+p+"/agents/{id}/install", s.installAgent)
	h("DELETE "+p+"/agents/{id}", s.uninstallAgent)
	h("GET "+p+"/mcp/catalog", s.mcpCatalog)
	h("GET "+p+"/mcp/command", s.mcpCommand)

	s.mux.Handle("GET "+p+"/events", s.cfg.Events)
}

// Handler returns the API wrapped in host/origin checks and CORS.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host) {
			// Defends against DNS rebinding: only loopback host names reach the API.
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !s.origins[origin] {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				w.Header().Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		s.mux.ServeHTTP(w, r)
	})
}

func localHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" || host == "tauri.localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ---------------------------------------------------------------------------
// Errors and JSON helpers

type httpError struct {
	status      int
	msg         string
	code        string // machine-readable, e.g. "confirmation_required"
	diagnostics []model.Diagnostic
	conflicts   []model.PortConflict // code "port_conflict"
}

func (e *httpError) Error() string { return e.msg }

func errorf(status int, format string, args ...any) error {
	return &httpError{status: status, msg: fmt.Sprintf(format, args...)}
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var he *httpError
	switch {
	case errors.As(err, &he):
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrConnectionNotFound):
		he = &httpError{status: http.StatusNotFound, msg: err.Error()}
	case errors.Is(err, store.ErrInvalidConnection):
		he = &httpError{status: http.StatusBadRequest, msg: err.Error()}
	case errors.Is(err, sandbox.ErrRunActive):
		he = &httpError{status: http.StatusConflict, msg: err.Error()}
	default:
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		he = &httpError{status: http.StatusInternalServerError, msg: err.Error()}
	}
	body := map[string]any{"error": he.msg}
	if he.code != "" {
		body["code"] = he.code
	}
	if len(he.diagnostics) > 0 {
		body["diagnostics"] = he.diagnostics
	}
	if len(he.conflicts) > 0 {
		body["conflicts"] = he.conflicts
	}
	writeJSON(w, he.status, body)
}

// loadTestError maps load-test start failures to HTTP errors; a target
// outside the sandbox without confirmExternal is 428 confirmation_required.
// Everything else (including a target naming an unknown connection) is a
// bad request.
func loadTestError(err error) error {
	switch {
	case errors.Is(err, loadtest.ErrConfirmationRequired):
		return &httpError{status: http.StatusPreconditionRequired, msg: err.Error(), code: "confirmation_required"}
	case errors.Is(err, loadtest.ErrTooManyRuns):
		return errorf(http.StatusConflict, "%v", err)
	default:
		return errorf(http.StatusBadRequest, "%v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v any) error { return writeJSON(w, http.StatusOK, v) }

func noContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func decode(w http.ResponseWriter, r *http.Request, limit int64, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errorf(http.StatusRequestEntityTooLarge, "request body larger than %d bytes", mbe.Limit)
		}
		if errors.Is(err, io.EOF) {
			return errorf(http.StatusBadRequest, "request body is required")
		}
		return errorf(http.StatusBadRequest, "invalid JSON body: %v", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Health and catalog

func (s *Server) health(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	res := map[string]any{"version": s.cfg.Version, "home": s.cfg.Home, "docker": true}
	if err := s.cfg.Sandbox.DockerAvailable(ctx); err != nil {
		res["docker"] = false
		res["dockerError"] = err.Error()
	}
	return ok(w, res)
}

func (s *Server) providers(w http.ResponseWriter, r *http.Request) error {
	return ok(w, s.cfg.Catalog.Providers())
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request) error {
	return ok(w, nonNil(s.cfg.Catalog.Categories(model.Cloud(r.URL.Query().Get("cloud")))))
}

func (s *Server) searchResources(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	query := catalog.Query{
		Text:          q.Get("q"),
		Cloud:         model.Cloud(q.Get("cloud")),
		Kind:          model.NodeKind(q.Get("kind")),
		Category:      q.Get("category"),
		SupportedOnly: q.Get("supported") == "true",
		PopularOnly:   q.Get("popular") == "true",
	}
	if l := q.Get("limit"); l != "" {
		if _, err := fmt.Sscanf(l, "%d", &query.Limit); err != nil || query.Limit < 0 {
			return errorf(http.StatusBadRequest, "invalid limit %q", l)
		}
	}
	return ok(w, nonNil(s.cfg.Catalog.Search(query)))
}

func (s *Server) resourceSchema(w http.ResponseWriter, r *http.Request) error {
	kind, typ := model.NodeKind(r.PathValue("kind")), r.PathValue("type")
	sch, found := s.cfg.Catalog.Schema(kind, typ)
	if !found {
		return errorf(http.StatusNotFound, "unknown %s type %q", kind, typ)
	}
	return ok(w, sch)
}

// templates lists the Terraform templates, then the docker ones (kind
// "docker") and the Kubernetes ones (kind "kubernetes").
func (s *Server) templates(w http.ResponseWriter, r *http.Request) error {
	return ok(w, slices.Concat(nonNil(s.cfg.Catalog.Templates()), compose.Templates(), kubecatalog.Templates()))
}

func (s *Server) loadPresets(w http.ResponseWriter, r *http.Request) error {
	return ok(w, loadtest.Presets())
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}
