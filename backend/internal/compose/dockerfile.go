package compose

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// A small exact Dockerfile parser. BuildKit's frontend/dockerfile/parser
// would pull protobuf and containerd's typeurl into the daemon through its
// error stack helpers, and it does not return what write-back needs (the
// byte span of each instruction and of its arguments). The grammar is
// small; this follows BuildKit's rules: parser directives (syntax, escape,
// check) before anything else, the escape character continuing lines
// (comment and blank lines inside a continuation are skipped), heredocs
// (<<EOF, <<-EOF, quoted delimiters, several per instruction) for RUN, COPY
// and ADD, JSON (exec) form, and case-insensitive instruction keywords.

// Dockerfile is a parsed Dockerfile.
type Dockerfile struct {
	Directives   map[string]string
	Escape       byte
	Instructions []Instruction
	// GlobalArgs are the ARGs before the first FROM (usable in FROM).
	GlobalArgs []Arg
	Stages     []Stage
	lines      []string
}

// Instruction is one Dockerfile instruction.
type Instruction struct {
	Cmd       string   // upper case: FROM, RUN, ...
	Keyword   string   // as written
	Flags     []string // --from=x, --platform=y (without the leading --)
	Args      string   // the rest after flags, continuations joined with a space
	JSON      []string // exec form arguments; nil for shell form
	Heredocs  []Heredoc
	StartLine int // 1-based
	EndLine   int // last line, heredoc bodies included
}

// Heredoc is a here-document of an instruction.
type Heredoc struct {
	Name    string
	Content string
	Chomp   bool // <<- strips leading tabs
}

// Arg is an ARG declaration.
type Arg struct {
	Name    string
	Default *string
}

// Stage is one FROM ... [AS name] section.
type Stage struct {
	Index    int
	Name     string // lower-cased like BuildKit; "" when unnamed
	Base     string // image as written
	Platform string
	Line     int
	// Instructions indexes Dockerfile.Instructions (FROM included).
	Instructions []int
	Args         []Arg
}

// Problem is a Dockerfile syntax error.
type Problem struct {
	Line    int
	Message string
}

var (
	directiveRe = regexp.MustCompile(`^#\s*([a-zA-Z][a-zA-Z0-9]*)\s*=\s*(.+?)\s*$`)
	heredocRe   = regexp.MustCompile(`<<(-?)\s*(?:"([^"\n]+)"|'([^'\n]+)'|([A-Za-z_][A-Za-z0-9_]*))`)
)

var knownInstructions = map[string]bool{
	"ADD": true, "ARG": true, "CMD": true, "COPY": true, "ENTRYPOINT": true, "ENV": true,
	"EXPOSE": true, "FROM": true, "HEALTHCHECK": true, "LABEL": true, "MAINTAINER": true,
	"ONBUILD": true, "RUN": true, "SHELL": true, "STOPSIGNAL": true, "USER": true,
	"VOLUME": true, "WORKDIR": true,
}

// ParseDockerfile parses src. It always returns a Dockerfile (instructions
// up to the problems found).
func ParseDockerfile(src []byte) (*Dockerfile, []Problem) {
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	text = strings.TrimPrefix(text, "\uFEFF")
	df := &Dockerfile{Directives: map[string]string{}, Escape: '\\', lines: strings.Split(text, "\n")}
	var problems []Problem
	lines := df.lines
	i := 0
	// Parser directives: only before the first non-directive line.
	for ; i < len(lines); i++ {
		m := directiveRe.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if m == nil {
			break
		}
		key := strings.ToLower(m[1])
		if _, dup := df.Directives[key]; dup {
			break
		}
		df.Directives[key] = m[2]
		if key == "escape" {
			switch m[2] {
			case "`":
				df.Escape = '`'
			case `\`:
			default:
				problems = append(problems, Problem{i + 1, fmt.Sprintf("invalid escape directive %q: use \\ or `", m[2])})
			}
		}
	}
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}
		start := i
		// Join continuation lines.
		var b strings.Builder
		for {
			line := lines[i]
			t := strings.TrimRightFunc(line, unicode.IsSpace)
			if i > start {
				ts := strings.TrimSpace(line)
				if strings.HasPrefix(ts, "#") || ts == "" {
					if i+1 >= len(lines) {
						break
					}
					i++
					continue
				}
			}
			if len(t) > 0 && t[len(t)-1] == df.Escape {
				b.WriteString(t[:len(t)-1])
				if i+1 >= len(lines) {
					break
				}
				i++
				continue
			}
			b.WriteString(line)
			break
		}
		logical := strings.TrimSpace(b.String())
		in := Instruction{StartLine: start + 1, EndLine: i + 1}
		keyword, rest, _ := strings.Cut(logical, " ")
		if k, r, ok := strings.Cut(logical, "\t"); ok && len(k) < len(keyword) {
			keyword, rest = k, r
		}
		in.Keyword, in.Cmd = keyword, strings.ToUpper(keyword)
		rest = strings.TrimSpace(rest)
		if !knownInstructions[in.Cmd] {
			problems = append(problems, Problem{start + 1, fmt.Sprintf("unknown instruction: %s", keyword)})
		}
		in.Flags, rest = splitFlags(in.Cmd, rest)
		in.Args = rest
		if strings.HasPrefix(rest, "[") {
			var arr []string
			if json.Unmarshal([]byte(rest), &arr) == nil {
				in.JSON = arr
			}
		}
		i++
		if in.Cmd == "RUN" || in.Cmd == "COPY" || in.Cmd == "ADD" {
			for _, loc := range heredocRe.FindAllStringSubmatchIndex(rest, -1) {
				if loc[0] > 0 && rest[loc[0]-1] == '<' || loc[0]+2 < len(rest) && rest[loc[0]+2] == '<' {
					continue // <<< here-string
				}
				sub := func(n int) string {
					if loc[2*n] < 0 {
						return ""
					}
					return rest[loc[2*n]:loc[2*n+1]]
				}
				name := sub(2) + sub(3) + sub(4)
				h := Heredoc{Name: name, Chomp: sub(1) == "-"}
				var body []string
				closed := false
				for i < len(lines) {
					l := lines[i]
					i++
					cmp := l
					if h.Chomp {
						cmp = strings.TrimLeft(l, "\t")
					}
					if cmp == name {
						closed = true
						break
					}
					if h.Chomp {
						l = strings.TrimLeft(l, "\t")
					}
					body = append(body, l)
				}
				if !closed {
					problems = append(problems, Problem{start + 1, fmt.Sprintf("unterminated heredoc %s", name)})
				}
				h.Content = strings.Join(body, "\n") + "\n"
				in.Heredocs = append(in.Heredocs, h)
			}
			in.EndLine = i
		}
		df.Instructions = append(df.Instructions, in)
	}
	problems = append(problems, df.buildStages()...)
	return df, problems
}

// splitFlags splits leading --flags off the arguments of instructions that
// take them.
func splitFlags(cmd, rest string) ([]string, string) {
	switch cmd {
	case "FROM", "RUN", "COPY", "ADD", "HEALTHCHECK":
	default:
		return nil, rest
	}
	var flags []string
	for strings.HasPrefix(rest, "--") {
		end := strings.IndexFunc(rest, unicode.IsSpace)
		if end < 0 {
			end = len(rest)
		}
		flags = append(flags, rest[2:end])
		rest = strings.TrimSpace(rest[end:])
	}
	return flags, rest
}

// Flag returns the value of --name=value.
func (in *Instruction) Flag(name string) (string, bool) {
	for _, f := range in.Flags {
		if k, v, ok := strings.Cut(f, "="); ok && k == name {
			return v, true
		} else if !ok && f == name {
			return "", true
		}
	}
	return "", false
}

// Words splits shell-form arguments on whitespace (quotes kept together).
func Words(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	in := false
	for _, r := range s {
		switch {
		case quote != 0:
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
			cur.WriteRune(r)
			in = true
		case unicode.IsSpace(r):
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

func (df *Dockerfile) buildStages() []Problem {
	var problems []Problem
	var cur *Stage
	for idx := range df.Instructions {
		in := &df.Instructions[idx]
		switch {
		case in.Cmd == "FROM":
			words := Words(in.Args)
			st := Stage{Index: len(df.Stages), Line: in.StartLine, Instructions: []int{idx}}
			if p, ok := in.Flag("platform"); ok {
				st.Platform = p
			}
			switch {
			case len(words) == 0:
				problems = append(problems, Problem{in.StartLine, "FROM requires an image"})
			case len(words) == 3 && strings.EqualFold(words[1], "as"):
				st.Base, st.Name = words[0], strings.ToLower(words[2])
			case len(words) == 1:
				st.Base = words[0]
			default:
				st.Base = words[0]
				problems = append(problems, Problem{in.StartLine, "FROM takes an image and an optional AS <name>"})
			}
			df.Stages = append(df.Stages, st)
			cur = &df.Stages[len(df.Stages)-1]
		case cur == nil:
			if in.Cmd == "ARG" {
				df.GlobalArgs = append(df.GlobalArgs, parseArgs(in.Args)...)
				continue
			}
			problems = append(problems, Problem{in.StartLine, fmt.Sprintf("%s before the first FROM (only ARG can come first)", in.Cmd)})
		default:
			cur.Instructions = append(cur.Instructions, idx)
			if in.Cmd == "ARG" {
				cur.Args = append(cur.Args, parseArgs(in.Args)...)
			}
		}
	}
	if len(df.Instructions) > 0 && len(df.Stages) == 0 {
		problems = append(problems, Problem{df.Instructions[0].StartLine, "no FROM instruction"})
	}
	return problems
}

func parseArgs(s string) []Arg {
	var out []Arg
	for _, w := range Words(s) {
		name, def, ok := strings.Cut(w, "=")
		a := Arg{Name: name}
		if ok {
			d := unquote(def)
			a.Default = &d
		}
		out = append(out, a)
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		if s[0] == '"' {
			if u, err := strconv.Unquote(s); err == nil {
				return u
			}
		}
		return s[1 : len(s)-1]
	}
	return s
}

var varRe = regexp.MustCompile(`\$(?:\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-+])([^}]*))?\}|([A-Za-z_][A-Za-z0-9_]*))`)

// Expand substitutes $VAR, ${VAR}, ${VAR:-default} and ${VAR:+alt} with
// vars; unknown variables expand to "".
func Expand(s string, vars map[string]string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		sm := varRe.FindStringSubmatch(m)
		name := sm[1] + sm[4]
		v, set := vars[name]
		switch sm[2] {
		case ":-":
			if v == "" {
				return sm[3]
			}
		case "-":
			if !set {
				return sm[3]
			}
		case ":+":
			if v != "" {
				return sm[3]
			}
			return ""
		case "+":
			if set {
				return sm[3]
			}
			return ""
		}
		return v
	})
}

// StageInfo is a stage resolved with build arguments.
type StageInfo struct {
	Stage
	From       string // base with ARGs expanded
	FromStage  string // the stage name/index it builds on, if any
	CopyFrom   []string
	Expose     []string
	User       string
	Workdir    string
	Cmd        string
	Entrypoint string
	Health     *model.ServiceHealthcheck
	Env        map[string]string
}

// Resolve expands the stages with build args (compose build.args win over
// ARG defaults) and links stage references.
func (df *Dockerfile) Resolve(buildArgs map[string]string) []StageInfo {
	global := map[string]string{}
	for _, a := range df.GlobalArgs {
		if v, ok := buildArgs[a.Name]; ok {
			global[a.Name] = v
		} else if a.Default != nil {
			global[a.Name] = Expand(*a.Default, global)
		}
	}
	names := map[string]int{}
	out := make([]StageInfo, 0, len(df.Stages))
	for _, st := range df.Stages {
		si := StageInfo{Stage: st, Env: map[string]string{}}
		si.From = Expand(st.Base, global)
		si.Platform = Expand(st.Platform, global)
		if idx, ok := names[strings.ToLower(si.From)]; ok {
			si.FromStage = stageRef(df.Stages[idx])
			si.Expose = slices.Clone(out[idx].Expose)
			si.User, si.Workdir, si.Cmd, si.Entrypoint, si.Health = out[idx].User, out[idx].Workdir, out[idx].Cmd, out[idx].Entrypoint, out[idx].Health
		}
		vars := map[string]string{}
		for k, v := range global {
			vars[k] = v
		}
		for _, ii := range st.Instructions[1:] {
			in := df.Instructions[ii]
			switch in.Cmd {
			case "ARG":
				for _, a := range parseArgs(in.Args) {
					switch v, ok := buildArgs[a.Name]; {
					case ok:
						vars[a.Name] = v
					case a.Default != nil:
						vars[a.Name] = Expand(*a.Default, vars)
					default:
						if g, ok := global[a.Name]; ok {
							vars[a.Name] = g
						}
					}
				}
			case "ENV":
				for k, v := range parseEnv(in.Args) {
					vars[k] = Expand(v, vars)
					si.Env[k] = vars[k]
				}
			case "EXPOSE":
				for _, w := range Words(Expand(in.Args, vars)) {
					if !slices.Contains(si.Expose, w) {
						si.Expose = append(si.Expose, w)
					}
				}
			case "USER":
				si.User = Expand(in.Args, vars)
			case "WORKDIR":
				si.Workdir = Expand(in.Args, vars)
			case "CMD":
				si.Cmd = commandText(in)
			case "ENTRYPOINT":
				si.Entrypoint = commandText(in)
			case "HEALTHCHECK":
				si.Health = parseHealthcheck(in)
			case "COPY", "ADD":
				if f, ok := in.Flag("from"); ok {
					si.CopyFrom = appendUnique(si.CopyFrom, Expand(f, vars))
				}
			case "RUN":
				for _, f := range in.Flags {
					if v, ok := strings.CutPrefix(f, "mount="); ok {
						for _, kv := range strings.Split(v, ",") {
							if from, ok := strings.CutPrefix(kv, "from="); ok {
								si.CopyFrom = appendUnique(si.CopyFrom, Expand(from, vars))
							}
						}
					}
				}
			}
		}
		if st.Name != "" {
			names[st.Name] = st.Index
		}
		names[strconv.Itoa(st.Index)] = st.Index
		out = append(out, si)
	}
	return out
}

func appendUnique(list []string, v string) []string {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

// stageRef is how other stages name st: its name, else its index.
func stageRef(st Stage) string {
	if st.Name != "" {
		return st.Name
	}
	return strconv.Itoa(st.Index)
}

func commandText(in Instruction) string {
	if in.JSON != nil {
		return strings.Join(in.JSON, " ")
	}
	return in.Args
}

func parseEnv(s string) map[string]string {
	out := map[string]string{}
	words := Words(s)
	if len(words) >= 2 && !strings.Contains(words[0], "=") {
		// Legacy form: ENV KEY value with spaces
		k, v, _ := strings.Cut(strings.TrimSpace(s), " ")
		out[k] = strings.TrimSpace(v)
		return out
	}
	for _, w := range words {
		if k, v, ok := strings.Cut(w, "="); ok {
			out[k] = unquote(v)
		}
	}
	return out
}

func parseHealthcheck(in Instruction) *model.ServiceHealthcheck {
	args := strings.TrimSpace(in.Args)
	if strings.EqualFold(args, "NONE") {
		return &model.ServiceHealthcheck{Disabled: true}
	}
	h := &model.ServiceHealthcheck{}
	for _, f := range in.Flags {
		k, v, _ := strings.Cut(f, "=")
		switch k {
		case "interval":
			h.Interval = v
		case "timeout":
			h.Timeout = v
		case "start-period":
			h.StartPeriod = v
		case "start-interval":
			h.StartInterval = v
		case "retries":
			if n, err := strconv.Atoi(v); err == nil {
				h.Retries = &n
			}
		}
	}
	if rest, ok := cutKeyword(args, "CMD"); ok {
		args = rest
	}
	var arr []string
	if strings.HasPrefix(args, "[") && json.Unmarshal([]byte(args), &arr) == nil {
		h.Test = strings.Join(arr, " ")
	} else {
		h.Test = args
	}
	return h
}

func cutKeyword(s, kw string) (string, bool) {
	if len(s) >= len(kw) && strings.EqualFold(s[:len(kw)], kw) && (len(s) == len(kw) || unicode.IsSpace(rune(s[len(kw)]))) {
		return strings.TrimSpace(s[len(kw):]), true
	}
	return s, false
}

// SetStageBase rewrites the image of the FROM line of stage index in src,
// keeping its flags, AS name, comments and every other byte.
func SetStageBase(src []byte, index int, image string) ([]byte, error) {
	if image == "" || strings.ContainsAny(image, " \t\n") {
		return nil, fmt.Errorf("invalid image %q", image)
	}
	df, problems := ParseDockerfile(src)
	if len(problems) > 0 {
		return nil, fmt.Errorf("line %d: %s", problems[0].Line, problems[0].Message)
	}
	if index < 0 || index >= len(df.Stages) {
		return nil, fmt.Errorf("the Dockerfile has no stage %d", index)
	}
	st := df.Stages[index]
	lines := strings.SplitAfter(string(src), "\n")
	ln := st.Line - 1
	line := lines[ln]
	// The image is the first word after FROM and its flags; it is on the
	// FROM line unless the line continues before it (then edit that line).
	for l := ln; l < len(lines) && l < df.Instructions[st.Instructions[0]].EndLine; l++ {
		line = lines[l]
		pos := 0
		if l == ln {
			pos = strings.Index(strings.ToUpper(line), "FROM") + len("FROM")
		}
		for {
			for pos < len(line) && (line[pos] == ' ' || line[pos] == '\t') {
				pos++
			}
			if strings.HasPrefix(line[pos:], "--") {
				for pos < len(line) && !unicode.IsSpace(rune(line[pos])) {
					pos++
				}
				continue
			}
			break
		}
		if pos >= len(line) || line[pos] == '\n' || line[pos] == '\r' || line[pos] == df.Escape {
			continue
		}
		end := pos
		for end < len(line) && !unicode.IsSpace(rune(line[end])) {
			end++
		}
		lines[l] = line[:pos] + image + line[end:]
		return []byte(strings.Join(lines, "")), nil
	}
	return nil, fmt.Errorf("could not locate the image of stage %d", index)
}
