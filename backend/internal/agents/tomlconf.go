package agents

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Codex's config.toml is edited as text so comments and the layout of every
// other table survive: the [mcp_servers.<name>] table (and its sub-tables
// such as [mcp_servers.<name>.env]) is replaced or removed as a block.

// errInlineTOML is returned when the server is defined in a form the text
// editor cannot rewrite safely (inline table or dotted keys).
var errInlineTOML = errors.New("the nimbo server is defined inline in config.toml; edit or remove it manually")

func tomlServers(data []byte) (map[string]any, error) {
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("not valid TOML: %w", err)
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	return servers, nil
}

func tomlHasServer(data []byte, name string) bool {
	servers, err := tomlServers(data)
	if err != nil {
		return false
	}
	_, ok := servers[name]
	return ok
}

var tomlHeader = regexp.MustCompile(`^\s*\[`)

// tomlTableRE matches the header of [mcp_servers.<name>] and its sub-tables.
func tomlTableRE(name string) *regexp.Regexp {
	q := regexp.QuoteMeta(name)
	return regexp.MustCompile(`^\s*\[\s*mcp_servers\s*\.\s*(?:` + q + `|"` + q + `"|'` + q + `')\s*(?:\.[^\]]*)?\]\s*(?:#.*)?$`)
}

// tomlStrip removes the server's tables from lines; found reports whether
// any table header matched.
func tomlStrip(lines []string, name string) (out []string, found bool) {
	re := tomlTableRE(name)
	for i := 0; i < len(lines); {
		if !re.MatchString(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		found = true
		end := i + 1
		for end < len(lines) && !tomlHeader.MatchString(lines[end]) {
			end++
		}
		// Blank and comment lines right before the next table belong to it:
		// keep them, without doubling a blank separator.
		keep := end
		for keep > i+1 && isBlankOrComment(lines[keep-1]) {
			keep--
		}
		tail := lines[keep:end]
		if len(tail) > 0 && strings.TrimSpace(tail[0]) == "" && (len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == "") {
			tail = tail[1:]
		}
		out = append(out, tail...)
		i = end
	}
	return out, found
}

func isBlankOrComment(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

func splitLines(data []byte) []string {
	s := strings.TrimRight(string(data), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func joinLines(lines []string) []byte {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// tomlSetServer writes [mcp_servers.<name>] with command and args.
func tomlSetServer(data []byte, name, command string, args []string) ([]byte, error) {
	servers, err := tomlServers(data)
	if err != nil {
		return nil, err
	}
	lines, found := tomlStrip(splitLines(data), name)
	if _, exists := servers[name]; exists && !found {
		return nil, errInlineTOML
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, strings.Split(strings.TrimRight(tomlTable(name, command, args), "\n"), "\n")...)
	out := joinLines(lines)
	if _, err := tomlServers(out); err != nil {
		return nil, fmt.Errorf("updated config.toml would be invalid: %w", err)
	}
	return out, nil
}

// tomlRemoveServer deletes the server's tables; changed is false when absent.
func tomlRemoveServer(data []byte, name string) (out []byte, changed bool, err error) {
	servers, err := tomlServers(data)
	if err != nil {
		return nil, false, err
	}
	if _, exists := servers[name]; !exists {
		return data, false, nil
	}
	lines, found := tomlStrip(splitLines(data), name)
	if !found {
		return nil, false, errInlineTOML
	}
	return joinLines(lines), true, nil
}

// tomlTable renders the server table.
func tomlTable(name, command string, args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = tomlString(a)
	}
	return fmt.Sprintf("[mcp_servers.%s]\ncommand = %s\nargs = [%s]\n", name, tomlString(command), strings.Join(quoted, ", "))
}

// tomlString quotes s as a TOML literal string when possible (Windows paths
// keep their backslashes readable), else as a basic string.
func tomlString(s string) string {
	if !strings.ContainsAny(s, "'\n\r") {
		return "'" + s + "'"
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return `"` + r.Replace(s) + `"`
}
