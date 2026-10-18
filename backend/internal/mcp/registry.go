package mcp

import (
	"context"
	"encoding/json"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolInfo describes one MCP tool for display (e.g. the Settings screen).
type ToolInfo struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Group       string `json:"group"`
}

// PromptInfo describes one MCP prompt; Arguments lists argument names.
type PromptInfo struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Arguments   []string `json:"arguments"`
}

// registry collects tool and prompt registrations once, so the server and
// the published catalog can never disagree.
type registry struct {
	group   string // group of the tools being added
	tools   []ToolInfo
	prompts []PromptInfo
	install []func(*sdk.Server)
}

// newRegistry registers every tool and prompt; c may be nil when only the
// catalog is needed (handlers are never called then).
func newRegistry(c *client) *registry {
	r := &registry{}
	registerTools(r, &tools{c: c})
	registerPrompts(r)
	return r
}

func (r *registry) installOn(s *sdk.Server) {
	for _, add := range r.install {
		add(s)
	}
}

// ToolCatalog lists every tool the server exposes, in registration order.
func ToolCatalog() []ToolInfo { return newRegistry(nil).tools }

// PromptCatalog lists every prompt the server exposes, in registration order.
func PromptCatalog() []PromptInfo { return newRegistry(nil).prompts }

// addTool registers a typed tool whose result is rendered as text: strings
// verbatim, everything else as compact JSON.
func addTool[In any](r *registry, name, title, desc string, fn func(context.Context, In) (any, error)) {
	r.tools = append(r.tools, ToolInfo{Name: name, Title: title, Description: desc, Group: r.group})
	r.install = append(r.install, func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{Name: name, Title: title, Description: desc}, func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
			out, err := fn(ctx, in)
			if err != nil {
				return nil, nil, err
			}
			text, ok := out.(string)
			if !ok {
				data, err := json.Marshal(out)
				if err != nil {
					return nil, nil, err
				}
				text = string(data)
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, nil, nil
		})
	})
}

// addPrompt registers a prompt whose single user message is render(args).
func (r *registry) addPrompt(p *sdk.Prompt, render func(args map[string]string) (string, error)) {
	info := PromptInfo{Name: p.Name, Title: p.Title, Description: p.Description, Arguments: []string{}}
	for _, a := range p.Arguments {
		info.Arguments = append(info.Arguments, a.Name)
	}
	r.prompts = append(r.prompts, info)
	r.install = append(r.install, func(s *sdk.Server) {
		s.AddPrompt(p, func(_ context.Context, req *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
			text, err := render(req.Params.Arguments)
			if err != nil {
				return nil, err
			}
			return &sdk.GetPromptResult{
				Description: p.Description,
				Messages:    []*sdk.PromptMessage{{Role: "user", Content: &sdk.TextContent{Text: text}}},
			}, nil
		})
	})
}
