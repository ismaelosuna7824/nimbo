package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// client calls the nimbo REST API.
type client struct {
	base string // http://127.0.0.1:PORT/api/v1
	http *http.Client
}

func newClient(baseURL string) *client {
	return &client{base: strings.TrimRight(baseURL, "/") + "/api/v1", http: &http.Client{}}
}

// apiError is a non-2xx daemon response.
type apiError struct {
	Status      int
	Message     string
	Code        string // e.g. "confirmation_required" (HTTP 428)
	Diagnostics []model.Diagnostic
}

func (e *apiError) Error() string {
	if len(e.Diagnostics) == 0 {
		return e.Message
	}
	var b strings.Builder
	b.WriteString(e.Message)
	b.WriteString("\nDiagnostics:")
	for _, d := range e.Diagnostics {
		fmt.Fprintf(&b, "\n- [%s] %s: %s", d.Severity, d.Code, d.Message)
		if d.Attribute != "" {
			fmt.Fprintf(&b, " (attribute %s)", d.Attribute)
		}
		if d.Detail != "" {
			fmt.Fprintf(&b, " — %s", d.Detail)
		}
	}
	return b.String()
}

func isNotFound(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	// Lets the daemon tell the desktop app that an AI agent (not the user in
	// the app) created a workflow, so the app can offer to open it.
	req.Header.Set(model.ClientHeader, model.ClientMCP)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("nimbo daemon unreachable: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		ae := &apiError{Status: res.StatusCode}
		var eb struct {
			Error       string             `json:"error"`
			Code        string             `json:"code"`
			Diagnostics []model.Diagnostic `json:"diagnostics"`
		}
		if json.Unmarshal(data, &eb) == nil && eb.Error != "" {
			ae.Message, ae.Code, ae.Diagnostics = eb.Error, eb.Code, eb.Diagnostics
		} else {
			ae.Message = fmt.Sprintf("%s %s: %s", method, path, res.Status)
		}
		return ae
	}
	if out == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	// Text endpoints (the compliance report's Markdown) are read as is.
	if s, ok := out.(*string); ok && !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		*s = string(data)
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func pathEscape(s string) string { return url.PathEscape(s) }

// currentWorkflow names the workflow open in the desktop app; "open" and an
// omitted workflow argument mean the same.
const currentWorkflow = "current"

// errNothingOpen is returned when an agent refers to the open workflow while
// the desktop app has none open.
var errNothingOpen = errors.New(`no workflow is open in the Nimbo app: open one there, or pass a workflow ID or name (see list_workflows)`)

// openWorkflowID returns the ID of the workflow open in the desktop app, or
// "" when none is open.
func (c *client) openWorkflowID(ctx context.Context) (string, error) {
	var s struct {
		OpenWorkflowID *string `json:"openWorkflowId"`
	}
	if err := c.get(ctx, "/session", &s); err != nil {
		return "", err
	}
	if s.OpenWorkflowID == nil {
		return "", nil
	}
	return *s.OpenWorkflowID, nil
}

// workflow resolves ref as a workflow ID, falling back to a case-insensitive
// name match so agents can refer to workflows by name. An empty ref,
// "current" or "open" means the workflow open in the desktop app.
func (c *client) workflow(ctx context.Context, ref string) (*model.Workflow, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.EqualFold(ref, currentWorkflow) || strings.EqualFold(ref, "open") {
		id, err := c.openWorkflowID(ctx)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, errNothingOpen
		}
		ref = id
	}
	var wf model.Workflow
	err := c.get(ctx, "/workflows/"+pathEscape(ref), &wf)
	if err == nil {
		return &wf, nil
	}
	if !isNotFound(err) {
		return nil, err
	}
	var list []model.WorkflowSummary
	if err := c.get(ctx, "/workflows", &list); err != nil {
		return nil, err
	}
	var match []model.WorkflowSummary
	for _, s := range list {
		if strings.EqualFold(s.Name, ref) {
			match = append(match, s)
		}
	}
	switch len(match) {
	case 1:
		if err := c.get(ctx, "/workflows/"+pathEscape(match[0].ID), &wf); err != nil {
			return nil, err
		}
		return &wf, nil
	case 0:
		return nil, fmt.Errorf("workflow %q not found; call list_workflows", ref)
	default:
		return nil, fmt.Errorf("several workflows are named %q; use the workflow ID", ref)
	}
}

func (c *client) saveWorkflow(ctx context.Context, wf *model.Workflow) (*model.Workflow, error) {
	var out model.Workflow
	if err := c.do(ctx, http.MethodPut, "/workflows/"+pathEscape(wf.ID), wf, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
