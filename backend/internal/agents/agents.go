// Package agents detects AI agent applications (MCP clients) and registers
// or removes Nimbo's MCP server (`nimbo mcp`) in their user configuration.
//
// Every path derives from Env so tests run against temporary directories.
package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ServerName is the MCP server name written into every client config.
const ServerName = "nimbo"

var (
	// ErrUnknownClient is returned for an unknown client ID.
	ErrUnknownClient = errors.New("unknown AI agent client")
	// ErrUnusableExecutable is returned by Install when the running daemon
	// is a temporary build agents cannot launch later.
	ErrUnusableExecutable = errors.New("unusable nimbo executable")
)

// Env locates the user's configuration and the nimbo binary.
type Env struct {
	Home      string // user home directory
	GOOS      string
	AppData   string // Windows %APPDATA%; "" = <Home>/AppData/Roaming
	XDGConfig string // Linux $XDG_CONFIG_HOME; "" = <Home>/.config
	CodexHome string // $CODEX_HOME; "" = <Home>/.codex
	// AppDirs are searched for application bundles (macOS: /Applications).
	AppDirs []string
	// LookPath finds a CLI; nil means no CLI is ever found.
	LookPath func(name string) (string, error)
	// Run executes a CLI and returns its combined output.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// Executable is the nimbo binary agents launch as `<Executable> mcp`.
	Executable string
	// TempDirs hold throwaway builds; an Executable inside one is refused.
	TempDirs []string
	Now      func() time.Time
}

// DefaultEnv describes the current user and process.
func DefaultEnv() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return Env{}, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	temps := []string{os.TempDir(), "/tmp"}
	for _, t := range append([]string(nil), temps...) {
		if resolved, err := filepath.EvalSymlinks(t); err == nil && resolved != t {
			temps = append(temps, resolved)
		}
	}
	env := Env{
		Home:       home,
		GOOS:       runtime.GOOS,
		AppData:    os.Getenv("APPDATA"),
		XDGConfig:  os.Getenv("XDG_CONFIG_HOME"),
		CodexHome:  os.Getenv("CODEX_HOME"),
		Executable: exe,
		TempDirs:   temps,
		Now:        time.Now,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
	}
	if env.GOOS == "darwin" {
		env.AppDirs = []string{"/Applications", filepath.Join(home, "Applications")}
	}
	// Apps started from the Dock get a minimal PATH; also look where
	// installers put agent CLIs.
	extra := []string{filepath.Join(home, ".local", "bin"), filepath.Join(home, ".claude", "local"),
		"/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".bun", "bin")}
	env.LookPath = func(name string) (string, error) {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
		for _, dir := range extra {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
				return p, nil
			}
		}
		return "", exec.ErrNotFound
	}
	return env, nil
}

func (e Env) appData() string {
	if e.AppData != "" {
		return e.AppData
	}
	return filepath.Join(e.Home, "AppData", "Roaming")
}

func (e Env) xdgConfig() string {
	if e.XDGConfig != "" {
		return e.XDGConfig
	}
	return filepath.Join(e.Home, ".config")
}

func (e Env) codexHome() string {
	if e.CodexHome != "" {
		return e.CodexHome
	}
	return filepath.Join(e.Home, ".codex")
}

// appConfigDir is the per-user application data directory of app.
func (e Env) appConfigDir(app string) string {
	switch e.GOOS {
	case "darwin":
		return filepath.Join(e.Home, "Library", "Application Support", app)
	case "windows":
		return filepath.Join(e.appData(), app)
	default:
		return filepath.Join(e.xdgConfig(), app)
	}
}

func (e Env) cli(name string) (string, bool) {
	if e.LookPath == nil {
		return "", false
	}
	p, err := e.LookPath(name)
	return p, err == nil
}

func (e Env) hasApp(bundle string) bool {
	for _, dir := range e.AppDirs {
		if exists(filepath.Join(dir, bundle)) {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// format is how a client stores MCP servers.
type format int

const (
	formatJSON format = iota // {<key>: {nimbo: {command, args}}}
	formatTOML               // [mcp_servers.nimbo]
)

// client describes one supported MCP client.
type client struct {
	id, name string
	format   format
	key      string // JSON object holding the servers
	typed    bool   // entries carry "type": "stdio" (VS Code)
	cli      string // Claude Code: install through this CLI when found
	note     string
	path     func(Env) string
	detect   func(Env) bool
}

// Configuration formats, verified 2026-10-17:
//   - Claude Code: https://code.claude.com/docs/en/mcp (user scope lives in
//     ~/.claude.json "mcpServers"; `claude mcp add --scope user <name> -- <cmd> <args>`)
//   - Claude Desktop: https://modelcontextprotocol.io/quickstart/user and
//     https://api.anthropic.com/mcp-registry/docs/claude-desktop
//     (claude_desktop_config.json "mcpServers": macOS ~/Library/Application Support/Claude,
//     Windows %APPDATA%\Claude, Linux ~/.config/Claude)
//   - Cursor: https://cursor.com/help/customization/mcp (global ~/.cursor/mcp.json "mcpServers")
//   - Codex: https://developers.openai.com/codex/mcp ([mcp_servers.<name>] command/args
//     in ~/.codex/config.toml, $CODEX_HOME overrides the directory)
//   - VS Code: https://code.visualstudio.com/docs/agents/reference/mcp-configuration
//     (user-profile mcp.json, top-level "servers", {"type": "stdio", command, args})
//   - Windsurf: https://docs.windsurf.com/windsurf/cascade/mcp (~/.codeium/windsurf/mcp_config.json "mcpServers")
//   - Gemini CLI: https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/mcp-server.md
//     (~/.gemini/settings.json "mcpServers")
var clients = []client{
	{
		id: "claude-code", name: "Claude Code", format: formatJSON, key: "mcpServers", cli: "claude",
		note: "Available in every project. Run /mcp inside Claude Code to check the connection.",
		path: func(e Env) string { return filepath.Join(e.Home, ".claude.json") },
		detect: func(e Env) bool {
			_, ok := e.cli("claude")
			return ok || exists(filepath.Join(e.Home, ".claude")) || exists(filepath.Join(e.Home, ".claude.json"))
		},
	},
	{
		id: "claude-desktop", name: "Claude Desktop", format: formatJSON, key: "mcpServers",
		note: "Quit and reopen Claude Desktop to load Nimbo.",
		path: func(e Env) string { return filepath.Join(e.appConfigDir("Claude"), "claude_desktop_config.json") },
		detect: func(e Env) bool {
			return exists(e.appConfigDir("Claude")) || e.hasApp("Claude.app")
		},
	},
	{
		id: "cursor", name: "Cursor", format: formatJSON, key: "mcpServers",
		note: "Cursor picks up the change automatically; enable Nimbo under Settings → MCP if it is off.",
		path: func(e Env) string { return filepath.Join(e.Home, ".cursor", "mcp.json") },
		detect: func(e Env) bool {
			_, ok := e.cli("cursor")
			return ok || exists(filepath.Join(e.Home, ".cursor")) || e.hasApp("Cursor.app")
		},
	},
	{
		id: "codex", name: "Codex", format: formatTOML,
		note: "Start a new Codex session to load Nimbo (/mcp lists it).",
		path: func(e Env) string { return filepath.Join(e.codexHome(), "config.toml") },
		detect: func(e Env) bool {
			_, ok := e.cli("codex")
			return ok || exists(e.codexHome()) || e.hasApp("Codex.app")
		},
	},
	{
		id: "vscode", name: "VS Code", format: formatJSON, key: "servers", typed: true,
		note: "Start the server from the MCP Servers view (or when Chat asks) and use it in agent mode.",
		path: func(e Env) string { return filepath.Join(e.appConfigDir("Code"), "User", "mcp.json") },
		detect: func(e Env) bool {
			_, ok := e.cli("code")
			return ok || exists(filepath.Join(e.appConfigDir("Code"), "User")) || e.hasApp("Visual Studio Code.app")
		},
	},
	{
		id: "windsurf", name: "Windsurf", format: formatJSON, key: "mcpServers",
		note: "Press refresh in Windsurf's MCP panel (or restart Windsurf) to load Nimbo.",
		path: func(e Env) string { return filepath.Join(e.Home, ".codeium", "windsurf", "mcp_config.json") },
		detect: func(e Env) bool {
			_, ok := e.cli("windsurf")
			return ok || exists(filepath.Join(e.Home, ".codeium", "windsurf")) || e.hasApp("Windsurf.app")
		},
	},
	{
		id: "gemini-cli", name: "Gemini CLI", format: formatJSON, key: "mcpServers",
		note: "Restart Gemini CLI; /mcp lists Nimbo's tools.",
		path: func(e Env) string { return filepath.Join(e.Home, ".gemini", "settings.json") },
		detect: func(e Env) bool {
			_, ok := e.cli("gemini")
			return ok || exists(filepath.Join(e.Home, ".gemini"))
		},
	},
}

// Manager inspects and edits MCP client configurations.
type Manager struct {
	env Env
}

// New returns a Manager for env.
func New(env Env) *Manager {
	if env.Now == nil {
		env.Now = time.Now
	}
	return &Manager{env: env}
}

// Command returns the command agents run to start Nimbo's MCP server.
func (m *Manager) Command() (string, []string) { return m.env.Executable, []string{"mcp"} }

// CheckExecutable reports why the running binary cannot be registered, or nil.
func (m *Manager) CheckExecutable() error {
	exe := m.env.Executable
	if exe == "" {
		return fmt.Errorf("%w: the path of the running nimbo binary is unknown", ErrUnusableExecutable)
	}
	temp := strings.Contains(filepath.ToSlash(exe), "/go-build")
	for _, dir := range m.env.TempDirs {
		if dir != "" && within(dir, exe) {
			temp = true
		}
	}
	if temp {
		return fmt.Errorf("%w: Nimbo is running from a temporary build (%s) that will disappear; "+
			"build the binary (go build -o bin/nimbo ./cmd/nimbo in backend/) or use the packaged app, then install again", ErrUnusableExecutable, exe)
	}
	return nil
}

func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func find(id string) (*client, error) {
	for i := range clients {
		if clients[i].id == id {
			return &clients[i], nil
		}
	}
	return nil, fmt.Errorf("%w %q", ErrUnknownClient, id)
}

// List returns every supported client, in a stable order.
func (m *Manager) List() []model.AgentClient {
	out := make([]model.AgentClient, len(clients))
	for i := range clients {
		out[i] = m.describe(&clients[i])
	}
	return out
}

// Get returns one client.
func (m *Manager) Get(id string) (model.AgentClient, error) {
	c, err := find(id)
	if err != nil {
		return model.AgentClient{}, err
	}
	return m.describe(c), nil
}

func (m *Manager) describe(c *client) model.AgentClient {
	path := c.path(m.env)
	a := model.AgentClient{
		ID: c.id, Name: c.name, Detected: c.detect(m.env), ConfigPath: path,
		Snippet: m.snippet(c), Note: c.note,
	}
	if data, err := os.ReadFile(path); err == nil {
		if c.format == formatTOML {
			a.Configured = tomlHasServer(data, ServerName)
		} else {
			a.Configured = jsonHasServer(data, c.key, ServerName)
		}
	}
	return a
}

// serverEntry is a stdio MCP server entry; field order is the order written.
type serverEntry struct {
	Type    string   `json:"type,omitempty"` // "stdio" for VS Code
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func (m *Manager) entry(c *client) serverEntry {
	exe, args := m.Command()
	e := serverEntry{Command: exe, Args: args}
	if c.typed {
		e.Type = "stdio"
	}
	return e
}

// snippet is what a user adds by hand: the CLI command for Claude Code,
// otherwise the config fragment.
func (m *Manager) snippet(c *client) string {
	exe, args := m.Command()
	switch {
	case c.cli != "":
		return "claude mcp add --scope user " + ServerName + " -- " + shellQuote(exe) + " " + strings.Join(args, " ")
	case c.format == formatTOML:
		return tomlTable(ServerName, exe, args)
	default:
		b, _ := json.MarshalIndent(map[string]any{c.key: map[string]any{ServerName: m.entry(c)}}, "", "  ")
		return string(b)
	}
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Install registers Nimbo with client id, replacing an existing nimbo entry
// and keeping every other setting. The previous file is copied to
// "<file>.<timestamp>.bak" first.
func (m *Manager) Install(ctx context.Context, id string) (model.AgentClient, error) {
	c, err := find(id)
	if err != nil {
		return model.AgentClient{}, err
	}
	if err := m.CheckExecutable(); err != nil {
		return model.AgentClient{}, err
	}
	path := c.path(m.env)
	if c.cli != "" {
		if bin, ok := m.env.cli(c.cli); ok {
			if err := m.backup(path); err != nil {
				return model.AgentClient{}, err
			}
			exe, args := m.Command()
			// `add` refuses an existing name: remove first so reinstall
			// updates the path.
			_, _ = m.env.Run(ctx, bin, "mcp", "remove", "--scope", "user", ServerName)
			cmdArgs := append([]string{"mcp", "add", "--scope", "user", ServerName, "--", exe}, args...)
			if out, err := m.env.Run(ctx, bin, cmdArgs...); err != nil {
				return model.AgentClient{}, fmt.Errorf("%s %s: %v: %s", c.cli, strings.Join(cmdArgs[:2], " "), err, strings.TrimSpace(string(out)))
			}
			return m.describe(c), nil
		}
	}
	err = m.edit(path, func(data []byte) ([]byte, bool, error) {
		var out []byte
		var err error
		if c.format == formatTOML {
			exe, args := m.Command()
			out, err = tomlSetServer(data, ServerName, exe, args)
		} else {
			out, err = jsonSetServer(data, c.key, ServerName, m.entry(c))
		}
		return out, err == nil && !bytes.Equal(out, data), err
	})
	if err != nil {
		return model.AgentClient{}, err
	}
	return m.describe(c), nil
}

// Uninstall removes only the nimbo entry of client id.
func (m *Manager) Uninstall(ctx context.Context, id string) (model.AgentClient, error) {
	c, err := find(id)
	if err != nil {
		return model.AgentClient{}, err
	}
	path := c.path(m.env)
	if c.cli != "" {
		if bin, ok := m.env.cli(c.cli); ok && m.describe(c).Configured {
			if err := m.backup(path); err != nil {
				return model.AgentClient{}, err
			}
			if out, err := m.env.Run(ctx, bin, "mcp", "remove", "--scope", "user", ServerName); err != nil {
				return model.AgentClient{}, fmt.Errorf("%s mcp remove: %v: %s", c.cli, err, strings.TrimSpace(string(out)))
			}
			return m.describe(c), nil
		}
	}
	err = m.edit(path, func(data []byte) ([]byte, bool, error) {
		if data == nil {
			return nil, false, nil
		}
		if c.format == formatTOML {
			return tomlRemoveServer(data, ServerName)
		}
		return jsonRemoveServer(data, c.key, ServerName)
	})
	if err != nil {
		return model.AgentClient{}, err
	}
	return m.describe(c), nil
}

// edit rewrites path through fn (data is nil when the file does not exist),
// backing the old file up first; nothing is written when fn reports no change.
func (m *Manager) edit(path string, fn func(data []byte) ([]byte, bool, error)) error {
	// Edit the target of a symlinked config (dotfile repos) in place.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	out, changed, err := fn(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return nil
	}
	if err := m.backup(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, out)
}

// backup copies an existing file to "<path>.<timestamp>.bak".
func (m *Manager) backup(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	return os.WriteFile(path+"."+m.env.Now().Format("20060102-150405")+".bak", data, mode)
}

// writeFileAtomic replaces path, keeping the permissions of an existing file
// (new files are 0600: configs may hold other servers' secrets).
func writeFileAtomic(path string, data []byte) error {
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
