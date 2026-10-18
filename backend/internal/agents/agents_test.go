package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/tailscale/hujson"
)

const testExe = "/opt/nimbo/bin/nimbo"

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	home := t.TempDir()
	return New(Env{
		Home:       home,
		GOOS:       "darwin",
		Executable: testExe,
		TempDirs:   []string{filepath.Join(home, "tmp")},
		Now:        func() time.Time { return time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC) },
	}), home
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// jsoncObject decodes a JSONC document.
func jsoncObject(t *testing.T, s string) map[string]any {
	t.Helper()
	std, err := hujson.Standardize([]byte(s))
	if err != nil {
		t.Fatalf("invalid JSON written:\n%s\n%v", s, err)
	}
	var v map[string]any
	if err := json.Unmarshal(std, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

const backupName = ".20261017-120000.bak"

func TestInstallJSONCPreservesOtherEntries(t *testing.T) {
	m, home := newTestManager(t)
	path := filepath.Join(home, "Library", "Application Support", "Code", "User", "mcp.json")
	original := `{
	// my servers
	"inputs": [],
	"servers": {
		"github": {
			"type": "http",
			"url": "https://api.githubcopilot.com/mcp/"
		}
	}
}
`
	write(t, path, original)

	a, err := m.Install(context.Background(), "vscode")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Configured || !a.Detected || a.ConfigPath != path {
		t.Fatalf("client after install = %+v", a)
	}
	got := read(t, path)
	if !strings.Contains(got, "// my servers") || !strings.Contains(got, "\t\t\"nimbo\": {\n\t\t\t\"type\": \"stdio\"") {
		t.Errorf("comment or layout lost:\n%s", got)
	}
	servers := jsoncObject(t, got)["servers"].(map[string]any)
	if servers["github"].(map[string]any)["url"] != "https://api.githubcopilot.com/mcp/" {
		t.Errorf("github entry changed: %v", servers)
	}
	nimbo := servers["nimbo"].(map[string]any)
	if nimbo["type"] != "stdio" || nimbo["command"] != testExe || nimbo["args"].([]any)[0] != "mcp" {
		t.Errorf("nimbo entry = %v", nimbo)
	}
	if read(t, path+backupName) != original {
		t.Error("backup does not hold the original file")
	}

	// Idempotent: a second install changes nothing.
	if _, err := m.Install(context.Background(), "vscode"); err != nil {
		t.Fatal(err)
	}
	if again := read(t, path); again != got {
		t.Errorf("second install changed the file:\n%s", again)
	}

	a, err = m.Uninstall(context.Background(), "vscode")
	if err != nil {
		t.Fatal(err)
	}
	if a.Configured {
		t.Error("still configured after uninstall")
	}
	if after := read(t, path); after != original {
		t.Errorf("uninstall did not restore the original:\n%s", after)
	}
}

func TestInstallCreatesMissingFile(t *testing.T) {
	m, home := newTestManager(t)
	a, err := m.Get("gemini-cli")
	if err != nil {
		t.Fatal(err)
	}
	if a.Detected || a.Configured {
		t.Fatalf("empty home: %+v", a)
	}
	if _, err := m.Install(context.Background(), "gemini-cli"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".gemini", "settings.json")
	want := "{\n  \"mcpServers\": {\n    \"nimbo\": {\n      \"command\": \"" + testExe + "\",\n      \"args\": [\n        \"mcp\"\n      ]\n    }\n  }\n}\n"
	if got := read(t, path); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if _, err := os.Stat(path + backupName); !errors.Is(err, os.ErrNotExist) {
		t.Error("backup written for a new file")
	}
	if a, _ := m.Get("gemini-cli"); !a.Detected || !a.Configured {
		t.Errorf("after install: %+v", a)
	}
}

func TestInstallReplacesStaleEntryAndKeepsOthers(t *testing.T) {
	m, home := newTestManager(t)
	path := filepath.Join(home, ".cursor", "mcp.json")
	write(t, path, `{"mcpServers": {"nimbo": {"command": "/old/nimbo", "args": ["mcp"]}, "other": {"command": "npx", "args": ["-y", "x"], "env": {"K": "v"}}}, "extra": true}`)
	if a, _ := m.Get("cursor"); !a.Configured || !a.Detected {
		t.Fatalf("stale entry not detected as configured: %+v", a)
	}
	if _, err := m.Install(context.Background(), "cursor"); err != nil {
		t.Fatal(err)
	}
	doc := jsoncObject(t, read(t, path))
	servers := doc["mcpServers"].(map[string]any)
	if servers["nimbo"].(map[string]any)["command"] != testExe {
		t.Errorf("nimbo not updated: %v", servers["nimbo"])
	}
	if servers["other"].(map[string]any)["env"].(map[string]any)["K"] != "v" || doc["extra"] != true {
		t.Errorf("other settings changed: %v", doc)
	}
	if _, err := m.Uninstall(context.Background(), "cursor"); err != nil {
		t.Fatal(err)
	}
	doc = jsoncObject(t, read(t, path))
	if _, ok := doc["mcpServers"].(map[string]any)["nimbo"]; ok {
		t.Error("nimbo still present")
	}
	if _, ok := doc["mcpServers"].(map[string]any)["other"]; !ok {
		t.Error("uninstall removed another server")
	}
}

func TestCodexTOML(t *testing.T) {
	m, home := newTestManager(t)
	path := filepath.Join(home, ".codex", "config.toml")
	original := `# Codex settings
model = "gpt-5-codex"

[mcp_servers.context7]
command = "npx"
args = ["-y", "@upstash/context7-mcp"]

[mcp_servers.nimbo]
command = "/old/nimbo"
args = ["mcp"]

[mcp_servers.nimbo.env]
NIMBO_HOME = "/x"

# profiles
[profiles.fast]
model = "o4-mini"
`
	write(t, path, original)
	if a, _ := m.Get("codex"); !a.Configured || !a.Detected {
		t.Fatalf("before: %+v", a)
	}
	if _, err := m.Install(context.Background(), "codex"); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	want := `# Codex settings
model = "gpt-5-codex"

[mcp_servers.context7]
command = "npx"
args = ["-y", "@upstash/context7-mcp"]

# profiles
[profiles.fast]
model = "o4-mini"

[mcp_servers.nimbo]
command = '/opt/nimbo/bin/nimbo'
args = ['mcp']
`
	if got != want {
		t.Errorf("install got:\n%s\nwant:\n%s", got, want)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string
			Args    []string
		} `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(got, &doc); err != nil || doc.MCPServers["nimbo"].Command != testExe || len(doc.MCPServers) != 2 {
		t.Errorf("decoded %+v, %v", doc, err)
	}
	if read(t, path+backupName) != original {
		t.Error("backup missing")
	}
	if _, err := m.Install(context.Background(), "codex"); err != nil {
		t.Fatal(err)
	}
	if again := read(t, path); again != got {
		t.Errorf("second install changed the file:\n%s", again)
	}

	if _, err := m.Uninstall(context.Background(), "codex"); err != nil {
		t.Fatal(err)
	}
	wantAfter := `# Codex settings
model = "gpt-5-codex"

[mcp_servers.context7]
command = "npx"
args = ["-y", "@upstash/context7-mcp"]

# profiles
[profiles.fast]
model = "o4-mini"
`
	if after := read(t, path); after != wantAfter {
		t.Errorf("uninstall got:\n%s", after)
	}
	if a, _ := m.Get("codex"); a.Configured {
		t.Error("still configured")
	}
}

func TestCodexInlineDefinitionIsRefused(t *testing.T) {
	m, home := newTestManager(t)
	path := filepath.Join(home, ".codex", "config.toml")
	content := "[mcp_servers]\nnimbo = { command = \"/old\", args = [\"mcp\"] }\n"
	write(t, path, content)
	if _, err := m.Install(context.Background(), "codex"); !errors.Is(err, errInlineTOML) {
		t.Fatalf("err = %v", err)
	}
	if read(t, path) != content {
		t.Error("file changed")
	}
}

func TestClaudeCode(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		m, home := newTestManager(t)
		path := filepath.Join(home, ".claude.json")
		write(t, path, `{"numStartups": 3, "projects": {"/x": {"mcpServers": {"p": {"command": "p"}}}}}`)
		if _, err := m.Install(context.Background(), "claude-code"); err != nil {
			t.Fatal(err)
		}
		doc := jsoncObject(t, read(t, path))
		if doc["numStartups"] != float64(3) || doc["mcpServers"].(map[string]any)["nimbo"] == nil {
			t.Errorf("doc = %v", doc)
		}
		if doc["projects"].(map[string]any)["/x"].(map[string]any)["mcpServers"].(map[string]any)["p"] == nil {
			t.Error("project-scoped server lost")
		}
	})
	t.Run("cli", func(t *testing.T) {
		m, home := newTestManager(t)
		var calls []string
		m.env.LookPath = func(name string) (string, error) {
			if name == "claude" {
				return "/usr/local/bin/claude", nil
			}
			return "", os.ErrNotExist
		}
		m.env.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			if args[1] == "add" {
				write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"nimbo":{"command":"`+testExe+`","args":["mcp"]}}}`)
			}
			return nil, nil
		}
		a, err := m.Install(context.Background(), "claude-code")
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			"/usr/local/bin/claude mcp remove --scope user nimbo",
			"/usr/local/bin/claude mcp add --scope user nimbo -- " + testExe + " mcp",
		}
		if strings.Join(calls, "\n") != strings.Join(want, "\n") || !a.Configured || !a.Detected {
			t.Errorf("calls = %q, client = %+v", calls, a)
		}
		if a.Snippet != "claude mcp add --scope user nimbo -- "+testExe+" mcp" {
			t.Errorf("snippet = %q", a.Snippet)
		}
	})
}

func TestClaudeDesktopPaths(t *testing.T) {
	for goos, rel := range map[string]string{
		"darwin":  "Library/Application Support/Claude/claude_desktop_config.json",
		"windows": "AppData/Roaming/Claude/claude_desktop_config.json",
		"linux":   ".config/Claude/claude_desktop_config.json",
	} {
		m, home := newTestManager(t)
		m.env.GOOS = goos
		a, _ := m.Get("claude-desktop")
		if a.ConfigPath != filepath.Join(home, filepath.FromSlash(rel)) {
			t.Errorf("%s: %s", goos, a.ConfigPath)
		}
	}
}

func TestRefusesTemporaryExecutable(t *testing.T) {
	m, home := newTestManager(t)
	for _, exe := range []string{filepath.Join(home, "tmp", "nimbo"), "/Users/me/Library/Caches/go-build/ab/nimbo", ""} {
		m.env.Executable = exe
		if _, err := m.Install(context.Background(), "cursor"); !errors.Is(err, ErrUnusableExecutable) {
			t.Errorf("%q: err = %v", exe, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor")); !errors.Is(err, os.ErrNotExist) {
		t.Error("refused install wrote files")
	}
}

func TestUnknownClient(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.Install(context.Background(), "emacs"); !errors.Is(err, ErrUnknownClient) {
		t.Errorf("err = %v", err)
	}
	if len(m.List()) != 7 {
		t.Errorf("clients = %d", len(m.List()))
	}
}
