package compose

import (
	"context"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// Environments of a docker workflow follow the official compose pattern:
// an override file per environment next to the main compose file
// (compose.<env>.yaml; docker-compose.<env>.yml and the other extensions
// too) plus an optional .env.<env> read after .env, so its values win:
//
//	docker compose -f compose.yaml -f compose.prod.yaml --env-file .env --env-file .env.prod
//
// The merge is compose's own (compose-go): scalars of the override win,
// mappings (environment, labels, deploy.resources) merge key by key,
// ports/expose/volumes/dns lists are appended (deduplicated) unless the
// override tags them !override, command/entrypoint/healthcheck.test are
// replaced; !reset removes an attribute.

var envOverrideRe = regexp.MustCompile(`^(compose|docker-compose)\.([A-Za-z0-9][A-Za-z0-9_-]*)\.(yaml|yml)$`)

// EnvironmentName is the environment a compose file name stands for
// (compose.prod.yaml → "prod"); "" when base is no environment override
// (compose.override.yaml belongs to the base, like docker compose reads it).
func EnvironmentName(base string) string {
	m := envOverrideRe.FindStringSubmatch(base)
	if m == nil || strings.EqualFold(m[2], "override") {
		return ""
	}
	return m[2]
}

// envNameRe is a usable environment name: a DNS label (it becomes override
// and env file names, Kubernetes overlay directories and namespaces).
var envNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// sampleEnvWords mark env files that document variables rather than hold
// an environment's values (.env.example, prod.sample.env…): never
// environments.
var sampleEnvWords = []string{"example", "examples", "sample", "samples", "template", "tmpl", "tpl", "dist", "default", "defaults"}

// EnvFileName is the environment an env file name stands for: ".env.<env>"
// or "<env>.env". local reports a local-only file (.env.local,
// .env.<env>.local, <env>.local.env: the dotenv convention for untracked
// machine overrides, which docker compose doesn't read either); "" and
// false for .env, sample files and other names.
func EnvFileName(base string) (env string, local bool) {
	var parts []string
	switch {
	case strings.HasPrefix(base, ".env."):
		parts = strings.Split(strings.TrimPrefix(base, ".env."), ".")
	case strings.HasSuffix(base, ".env") && len(base) > len(".env") && !strings.HasPrefix(base, "."):
		parts = strings.Split(strings.TrimSuffix(base, ".env"), ".")
	default:
		return "", false
	}
	for _, p := range parts {
		if slices.Contains(sampleEnvWords, strings.ToLower(p)) {
			return "", false
		}
	}
	if slices.Contains(parts, "local") {
		return "", true
	}
	return strings.Join(parts, "."), false
}

// Environments lists the environments of a project from the names in its
// project directory: an environment is an override file
// (compose.<env>.yaml, docker-compose.<env>.yml…) and/or an env file
// (.env.<env> or <env>.env), either alone being enough. files are the base
// compose files (an override listed there is part of the base);
// serviceEnvFiles are the files services name in env_file (slash paths,
// service-scoped: never environments). When several override files name
// one environment, the one written like the first compose file (stem,
// extension) wins; with both env file styles .env.<env> wins. Files that
// look like environments but can't be used (invalid names, the losing env
// file, local-only files) come back as dk_environment_file info findings.
func Environments(names []string, projectDir string, files, serviceEnvFiles []string) ([]model.ComposeEnvironment, []model.Diagnostic) {
	stem, ext := "compose", "yaml"
	if len(files) > 0 {
		if m := regexp.MustCompile(`^(compose|docker-compose)(?:\.[^.]+)?\.(yaml|yml)$`).FindStringSubmatch(path.Base(files[0])); m != nil {
			stem, ext = m[1], m[2]
		}
	}
	rank := func(name string) int {
		m := envOverrideRe.FindStringSubmatch(name)
		r := 0
		if m[1] != stem {
			r += 2
		}
		if m[3] != ext {
			r++
		}
		return r
	}
	var notes []model.Diagnostic
	note := func(name, msg, fix string) {
		notes = append(notes, model.Diagnostic{Severity: model.SeverityInfo, Code: "dk_environment_file", File: path.Join(projectDir, name), Message: msg, Fix: fix})
	}
	invalid := func(name, env string) bool {
		if envNameRe.MatchString(env) && env != "base" {
			return false
		}
		note(name, fmt.Sprintf("%s is not used as an environment: %q is not a valid environment name", name, env),
			"Rename it with lowercase letters, digits and '-' (e.g. .env.staging or compose.staging.yaml), not \"base\".")
		return true
	}
	overrides, envFiles := map[string]string{}, map[string]string{}
	var locals []string
	for _, n := range slices.Sorted(slices.Values(names)) {
		rel := path.Join(projectDir, n)
		if slices.Contains(files, rel) || slices.Contains(serviceEnvFiles, rel) {
			continue
		}
		if env := EnvironmentName(n); env != "" {
			if invalid(n, env) {
				continue
			}
			if cur, ok := overrides[env]; !ok || rank(n) < rank(cur) {
				overrides[env] = n
			}
			continue
		}
		env, local := EnvFileName(n)
		if local {
			locals = append(locals, n)
		}
		if env == "" || invalid(n, env) {
			continue
		}
		cur, ok := envFiles[env]
		switch {
		case !ok:
			envFiles[env] = n
		default:
			keep, drop := cur, n
			if strings.HasPrefix(n, ".env.") {
				keep, drop = n, cur
			}
			envFiles[env] = keep
			note(drop, fmt.Sprintf("%s is ignored: environment %s already reads %s", drop, env, keep),
				fmt.Sprintf("Merge its variables into %s and delete it.", keep))
		}
	}
	if len(locals) > 0 {
		note(locals[0], fmt.Sprintf("%s: local-only env files are not environments and runs don't read them", strings.Join(locals, ", ")),
			"Move shared values into .env, environment values into .env.<env> (or <env>.env).")
	}
	all := slices.Collect(maps.Keys(overrides))
	for env := range envFiles {
		if _, ok := overrides[env]; !ok {
			all = append(all, env)
		}
	}
	slices.Sort(all)
	out := []model.ComposeEnvironment{}
	for _, env := range all {
		e := model.ComposeEnvironment{Name: env, Files: []string{}}
		if f, ok := overrides[env]; ok {
			e.File = path.Join(projectDir, f)
			e.Files = append(e.Files, e.File)
		}
		if f, ok := envFiles[env]; ok {
			e.EnvFile = path.Join(projectDir, f)
			e.Files = append(e.Files, e.EnvFile)
		}
		out = append(out, e)
	}
	return out, notes
}

// EnvFile is the env file Nimbo creates for environment env (.env.<env>)
// in projectDir; an existing environment may use <env>.env instead
// (ComposeEnvironment.EnvFile).
func EnvFile(projectDir, env string) string { return path.Join(projectDir, ".env."+env) }

// BaseEnvFile is the .env docker compose reads by default.
func BaseEnvFile(projectDir string) string { return path.Join(projectDir, ".env") }

// EnvFileOf is where environment env keeps its variables: its env file,
// else the .env.<env> a first variable creates.
func EnvFileOf(projectDir string, env *model.ComposeEnvironment) string {
	if env.EnvFile != "" {
		return env.EnvFile
	}
	return EnvFile(projectDir, env.Name)
}

// RenamedEnvFile is env file f of environment from renamed to to, keeping
// its style (.env.dev → .env.qa, dev.env → qa.env).
func RenamedEnvFile(f, from, to string) string {
	dir, base := path.Dir(f), path.Base(f)
	if base == from+".env" {
		return path.Join(dir, to+".env")
	}
	return path.Join(dir, ".env."+to)
}

// OverrideFile is the override file a new environment gets: named like the
// first compose file (docker-compose.yml → docker-compose.<env>.yml), next
// to it.
func OverrideFile(files []string, env string) string {
	stem, ext := "compose", "yaml"
	if len(files) > 0 {
		if m := regexp.MustCompile(`^(compose|docker-compose)(?:\.[^.]+)?\.(yaml|yml)$`).FindStringSubmatch(path.Base(files[0])); m != nil {
			stem, ext = m[1], m[2]
		}
	}
	return path.Join(ProjectDir(files), stem+"."+env+"."+ext)
}

// FindEnvironment returns environment name of c (nil when absent).
func FindEnvironment(c *model.ComposeModel, name string) *model.ComposeEnvironment {
	if c == nil {
		return nil
	}
	for i := range c.Environments {
		if c.Environments[i].Name == name {
			return &c.Environments[i]
		}
	}
	return nil
}

// RunFiles are the -f and --env-file lists of the docker workflow's runs in
// its active environment (c.Environment): the base files then its override
// (when it has one). Env files are listed only when the environment has
// its own: .env (when it exists) then .env.<env> / <env>.env. Without
// --env-file docker compose reads .env itself, and with one it doesn't, so
// both must be passed.
func RunFiles(c *model.ComposeModel, exists func(rel string) bool) (files, envFiles []string) {
	if c == nil {
		return nil, nil
	}
	files = c.Files
	env := FindEnvironment(c, c.Environment)
	if env == nil {
		return files, nil
	}
	if env.File != "" {
		files = append(slices.Clone(c.Files), env.File)
	}
	if env.EnvFile != "" {
		if base := BaseEnvFile(ProjectDir(c.Files)); exists(base) {
			envFiles = append(envFiles, base)
		}
		envFiles = append(envFiles, env.EnvFile)
	}
	return files, envFiles
}

// NewOverride is the content of a new, empty environment override: valid
// on its own (`docker compose config` accepts it) and minimal.
func NewOverride(env string, files []string, file string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Environment %s: overrides %s.\n", env, strings.Join(baseNames(files, file), ", "))
	fmt.Fprintf(&b, "# docker compose -f %s -f %s --env-file .env --env-file .env.%s\n", strings.Join(baseNames(files, file), " -f "), path.Base(file), env)
	b.WriteString("# Set only what differs: scalars replace, mappings (environment, deploy.resources)\n")
	b.WriteString("# merge, lists like ports are appended unless tagged !override.\n")
	b.WriteString("services: {}\n")
	return []byte(b.String())
}

// baseNames are files as written from the folder of file (the project
// directory).
func baseNames(files []string, file string) []string {
	dir := path.Dir(file)
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f
		if dir != "." {
			out[i] = strings.TrimPrefix(f, dir+"/")
		}
	}
	return out
}

// RenameOverride rewrites the header NewOverride wrote when an environment
// is renamed or copied (the rest of the file is kept byte for byte).
func RenameOverride(data []byte, from, to, fromFile, toFile string) []byte {
	lines := strings.SplitAfter(string(data), "\n")
	for i := 0; i < len(lines) && i < 2; i++ {
		l := lines[i]
		if strings.HasPrefix(l, "# Environment "+from+":") {
			lines[i] = strings.Replace(l, "# Environment "+from+":", "# Environment "+to+":", 1)
		} else if strings.HasPrefix(l, "# docker compose ") {
			l = strings.Replace(l, " -f "+path.Base(fromFile)+" ", " -f "+path.Base(toFile)+" ", 1)
			lines[i] = strings.Replace(l, "--env-file .env."+from, "--env-file .env."+to, 1)
		}
	}
	return []byte(strings.Join(lines, ""))
}

// ---------------------------------------------------------------------------
// Env files

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// ValidEnvKey checks a variable name of an env file.
func ValidEnvKey(k string) error {
	if !envKeyRe.MatchString(k) {
		return badf("invalid variable name %q: letters, digits and '_' (not starting with a digit)", k)
	}
	return nil
}

var dotenvLineRe = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_.-]*)\s*[=:]`)

// SetDotenv sets (value non-nil) or removes key in the env file data,
// keeping every other line: an existing assignment is rewritten in place,
// a new one appended.
func SetDotenv(data []byte, key string, value *string) []byte {
	text := string(data)
	lines := strings.Split(text, "\n")
	trailing := len(lines) > 0 && lines[len(lines)-1] == ""
	if trailing {
		lines = lines[:len(lines)-1]
	}
	var out []string
	done := false
	for _, l := range lines {
		if m := dotenvLineRe.FindStringSubmatch(l); m != nil && m[1] == key {
			if value == nil || done {
				continue
			}
			out = append(out, key+"="+dotenvValue(*value))
			done = true
			continue
		}
		out = append(out, l)
	}
	if value != nil && !done {
		out = append(out, key+"="+dotenvValue(*value))
	}
	if len(out) == 0 {
		return []byte{}
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

// dotenvValue writes a value so compose's dotenv parser reads it back:
// plain when safe, else double-quoted (which still interpolates ${VAR}).
func dotenvValue(v string) string {
	if v != "" && !strings.ContainsAny(v, " \t#'\"\\\n\r`") {
		return v
	}
	if v == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return `"` + r.Replace(v) + `"`
}

// ParseEnvFile parses an env file, resolving ${VAR} from lookup.
func ParseEnvFile(data []byte, lookup func(string) (string, bool)) (map[string]string, error) {
	return dotenv.UnmarshalBytesWithLookup(data, lookup)
}

// Variables lists an environment's variables: every key of the base .env
// and of the environment's env file, with the value the environment runs
// with (secret-looking keys without values) and the one it inherits.
func Variables(base, own map[string]string, file string) []model.EnvironmentVariable {
	keys := slices.Sorted(maps.Keys(base))
	for k := range own {
		if _, ok := base[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	out := make([]model.EnvironmentVariable, 0, len(keys))
	for _, k := range keys {
		v := model.EnvironmentVariable{Key: k, Secret: IsSecretKey(k), File: file}
		bv, inBase := base[k]
		ov, overridden := own[k]
		v.Overridden, v.InBase = overridden, inBase
		if !overridden {
			ov = bv
		}
		if !v.Secret {
			val := ov
			v.Value = &val
			if inBase {
				b := bv
				v.Base = &b
			}
		}
		out = append(out, v)
	}
	return out
}

// ---------------------------------------------------------------------------
// Comparing environments

// compareRow is one setting of a service.
type compareRow struct {
	key, label, value string
	// raw is what the comparison uses when value is masked (secrets).
	raw string
}

// serviceRows are the settings of each active service the Environments
// tab compares: image, replicas, resources, ports, healthcheck and every
// environment variable (secret values masked).
func (p *Project) serviceRows() map[string][]compareRow {
	out := map[string][]compareRow{}
	if p.Model == nil {
		return out
	}
	for _, name := range p.SortedServiceNames() {
		if p.Inactive[name] {
			continue
		}
		s := p.Model.Services[name]
		f := p.serviceFacts(s)
		image := ""
		if f.Image != nil {
			image = f.Image.Ref
		}
		if f.Build != nil {
			image = strings.TrimSpace("build " + f.Build.Context + " " + image)
		}
		replicas := "1"
		if f.Replicas != nil {
			replicas = strconv.Itoa(*f.Replicas)
		}
		var cpus, memory string
		if f.Resources != nil {
			cpus, memory = f.Resources.CPUs, f.Resources.Memory
		}
		var ports []string
		for _, pt := range f.Ports {
			ports = append(ports, portText(pt))
		}
		health := ""
		if h := f.Healthcheck; h != nil && f.HealthcheckFrom == "compose" {
			health = h.Test
			if h.Disabled {
				health = "disabled"
			} else if h.Interval != "" {
				health += " every " + h.Interval
			}
		}
		rows := []compareRow{
			{key: "image", label: "Image", value: image},
			{key: "replicas", label: "Replicas", value: replicas},
			{key: "cpus", label: "CPU limit", value: cpus},
			{key: "memory", label: "Memory limit", value: memory},
			{key: "ports", label: "Ports", value: strings.Join(ports, ", ")},
			{key: "healthcheck", label: "Healthcheck", value: health},
		}
		values, _ := p.serviceEnv(s)
		for _, k := range sortedKeys(values) {
			r := compareRow{key: "env:" + k, label: k}
			if v := values[k]; v != nil {
				r.value, r.raw = *v, *v
				if IsSecretKey(k) {
					r.value = "••••••"
				}
			}
			rows = append(rows, r)
		}
		for i := range rows {
			if rows[i].raw == "" {
				rows[i].raw = rows[i].value
			}
		}
		out[name] = rows
	}
	return out
}

// CompareInput opens the project of an environment ("" = the base).
type CompareInput func(env string) *Project

// Compare renders the base and every environment on its own and lines up
// their settings per service; changes are, per environment, the settings
// that differ from the base. Rows that never differ are kept (the tab can
// hide them); a service missing in an environment has empty values.
func Compare(open CompareInput, envs []string) (model.EnvCompare, map[string][]model.EnvironmentChange) {
	out := model.EnvCompare{Columns: []model.EnvCompareColumn{{Name: "", Label: "Base"}}, Groups: []model.EnvCompareGroup{}}
	changes := map[string][]model.EnvironmentChange{}
	names := append([]string{""}, envs...)
	rows := make([]map[string][]compareRow, len(names))
	for i, env := range names {
		if env != "" {
			out.Columns = append(out.Columns, model.EnvCompareColumn{Name: env, Label: env})
		}
		p := open(env)
		if p.Model == nil {
			if out.Errors == nil {
				out.Errors = map[string]string{}
			}
			out.Errors[env] = firstError(p)
		}
		rows[i] = p.serviceRows()
	}
	services := map[string]bool{}
	for _, r := range rows {
		for s := range r {
			services[s] = true
		}
	}
	for _, svc := range slices.Sorted(maps.Keys(services)) {
		g := model.EnvCompareGroup{ID: ServiceID(svc), Label: svc, Kind: "service", Rows: []model.EnvCompareRow{}}
		var order []compareRow
		seen := map[string]bool{}
		for _, r := range rows {
			for _, row := range r[svc] {
				if !seen[row.key] {
					seen[row.key] = true
					order = append(order, row)
				}
			}
		}
		// Settings first in their order, then variables by name.
		slices.SortStableFunc(order, func(a, b compareRow) int {
			ae, be := strings.HasPrefix(a.key, "env:"), strings.HasPrefix(b.key, "env:")
			switch {
			case ae && be:
				return strings.Compare(a.key, b.key)
			case ae:
				return 1
			case be:
				return -1
			}
			return 0
		})
		for _, o := range order {
			row := model.EnvCompareRow{Key: o.key, Label: o.label, Values: make([]string, len(names))}
			raws := make([]string, len(names))
			for i, r := range rows {
				for _, x := range r[svc] {
					if x.key == o.key {
						row.Values[i], raws[i] = x.value, x.raw
					}
				}
			}
			for i, v := range raws[1:] {
				if v != raws[0] {
					row.Differs = true
					changes[names[i+1]] = append(changes[names[i+1]], model.EnvironmentChange{Target: svc, Field: o.label, Base: row.Values[0], Value: row.Values[i+1]})
				}
			}
			g.Rows = append(g.Rows, row)
		}
		out.Groups = append(out.Groups, g)
	}
	return out, changes
}

func firstError(p *Project) string {
	for _, d := range p.Diagnostics {
		if d.Severity == model.SeverityError {
			return d.Message
		}
	}
	return "the compose files don't load"
}

// ---------------------------------------------------------------------------
// Per-environment settings

// Setting keys (model.EnvSetting.Key) of a compose service.
const (
	SettingImage    = "image"
	SettingReplicas = "replicas"
	SettingCPUs     = "cpus"
	SettingMemory   = "memory"
)

// settingOrder is the order settings are written in.
var settingOrder = []string{SettingImage, SettingReplicas, SettingCPUs, SettingMemory}

// settingPaths are where each setting lives under services.<name>.
var settingPaths = map[string][]string{
	SettingImage:    {"image"},
	SettingReplicas: {"deploy", "replicas"},
	SettingCPUs:     {"deploy", "resources", "limits", "cpus"},
	SettingMemory:   {"deploy", "resources", "limits", "memory"},
}

// Settings are the knobs of service svc in environment env (opened as
// env) next to the base's.
func Settings(base, env *Project, svc string) ([]model.EnvSetting, error) {
	if env.Model == nil {
		return nil, &EditError{Msg: "the environment's compose files can't be loaded: " + firstError(env), Conflict: true}
	}
	s, ok := env.Model.Services[svc]
	if !ok {
		return nil, &EditError{Msg: fmt.Sprintf("service %q not found", svc), NotFound: true}
	}
	ef := env.serviceFacts(s)
	var bf *model.ServiceFacts
	if base.Model != nil {
		if bs, ok := base.Model.Services[svc]; ok {
			bf = base.serviceFacts(bs)
		}
	}
	read := func(f *model.ServiceFacts) map[string]string {
		m := map[string]string{SettingReplicas: "1"}
		if f == nil {
			return map[string]string{}
		}
		if f.Image != nil {
			m[SettingImage] = f.Image.Ref
		}
		if f.Replicas != nil {
			m[SettingReplicas] = strconv.Itoa(*f.Replicas)
		}
		if f.Resources != nil {
			m[SettingCPUs], m[SettingMemory] = f.Resources.CPUs, f.Resources.Memory
		}
		return m
	}
	ev, bv := read(ef), read(bf)
	doc := env.Docs[env.Override]
	set := func(key, label, group, typ string) model.EnvSetting {
		st := model.EnvSetting{Key: key, Label: label, Group: group, Type: typ, Value: ev[key], Base: bv[key], Editable: true}
		if doc != nil {
			k, _ := doc.Get(append([]string{"services", svc}, settingPaths[key]...)...)
			st.Overridden = k != nil
		}
		return st
	}
	img := set(SettingImage, "Image", "Image", model.EnvSettingString)
	if ef.Build != nil {
		img.Label = "Image name"
		img.Reason = "Built from " + ef.Build.Context + ": this names the image the environment builds."
	}
	rep := set(SettingReplicas, "Replicas", "Scaling", model.EnvSettingInt)
	if fixedHostPorts(s.Ports, 2) {
		rep.Reason = "Fixed host ports become ranges when replicas > 1, so every replica can bind one."
	}
	return []model.EnvSetting{
		img, rep,
		set(SettingCPUs, "CPU limit", "Resources", model.EnvSettingCPU),
		set(SettingMemory, "Memory limit", "Resources", model.EnvSettingMemory),
	}, nil
}

var memoryRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?([bkmgBKMG]|[kmgKMG][bB])?$`)

// SetSettings writes settings of service svc into the environment's
// override file (env opened with its environment): a value sets it, nil
// removes it from the override so the base's applies. Only the override
// changes; it returns the files to write.
func SetSettings(env *Project, svc string, values map[string]*string) (map[string][]byte, error) {
	if env.Model == nil {
		return nil, &EditError{Msg: "the environment's compose files can't be loaded: " + firstError(env), Conflict: true}
	}
	s, ok := env.Model.Services[svc]
	if !ok {
		return nil, &EditError{Msg: fmt.Sprintf("service %q not found", svc), NotFound: true}
	}
	doc := env.Docs[env.Override]
	if doc == nil {
		return nil, badf("the project is not opened in an environment")
	}
	keys := slices.SortedFunc(maps.Keys(values), func(a, b string) int {
		return slices.Index(settingOrder, a) - slices.Index(settingOrder, b)
	})
	for _, key := range keys {
		p, ok := settingPaths[key]
		if !ok {
			return nil, badf("unknown setting %q: use image, replicas, cpus or memory", key)
		}
		path := append([]string{"services", svc}, p...)
		v := values[key]
		if v == nil {
			if err := doc.Delete(path, true); err != nil {
				return nil, err
			}
			continue
		}
		val := strings.TrimSpace(*v)
		var node *yaml.Node
		switch key {
		case SettingImage:
			if val == "" || strings.ContainsAny(val, " \t") {
				return nil, badf("invalid image %q", val)
			}
			node = Str(val)
		case SettingReplicas:
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 || n > 100 {
				return nil, badf("replicas must be a whole number between 0 and 100")
			}
			node = Int(n)
			if n > 1 && fixedHostPorts(s.Ports, n) {
				if err := doc.Set([]string{"services", svc, "ports"}, Raw("!override "+rangedPorts(s.Ports, n))); err != nil {
					return nil, err
				}
			}
		case SettingCPUs:
			f, err := strconv.ParseFloat(val, 64)
			if err != nil || f <= 0 {
				return nil, badf("cpus must be a positive number (0.5, 1, 2)")
			}
			node = Str(val)
		case SettingMemory:
			if !memoryRe.MatchString(val) {
				return nil, badf("memory must be a size like 256M or 1G")
			}
			node = Str(val)
		}
		if err := doc.Set(path, node); err != nil {
			return nil, err
		}
	}
	return map[string][]byte{env.Override: doc.Bytes()}, nil
}

// hostPortRange is the first host port of a published port and how many
// ports it spans ("3100" → 3100, 1; "3100-3101" → 3100, 2); ok false when
// not published or not numeric.
func hostPortRange(published string) (first, count int, ok bool) {
	lo, hi, isRange := strings.Cut(published, "-")
	a, err := strconv.Atoi(lo)
	if err != nil || published == "" {
		return 0, 0, false
	}
	if !isRange {
		return a, 1, true
	}
	b, err := strconv.Atoi(hi)
	if err != nil || b < a {
		return 0, 0, false
	}
	return a, b - a + 1, true
}

// fixedHostPorts reports a published host port that can't give each of n
// replicas a port of its own (a single port, or a shorter range).
func fixedHostPorts(ports []types.ServicePortConfig, n int) bool {
	for _, p := range ports {
		if _, count, ok := hostPortRange(p.Published); ok && count < n {
			return true
		}
	}
	return false
}

// rangedPorts is the flow list of ports with every published host port
// turned into a range of n ports ("3100:3000" → "3100-3101:3000").
func rangedPorts(ports []types.ServicePortConfig, n int) string {
	items := make([]string, 0, len(ports))
	for _, p := range ports {
		sp := model.ServicePort{HostIP: p.HostIP, Host: p.Published, Container: strconv.Itoa(int(p.Target)), Protocol: p.Protocol, Published: p.Published != ""}
		if sp.Protocol == "tcp" {
			sp.Protocol = ""
		}
		if first, count, ok := hostPortRange(p.Published); ok && count < n {
			sp.Host = fmt.Sprintf("%d-%d", first, first+n-1)
		}
		items = append(items, strconv.Quote(portText(sp)))
	}
	return "[" + strings.Join(items, ", ") + "]"
}

// OpenEnvironments opens the base and every environment of a project.
func OpenEnvironments(ctx context.Context, dir string, files, profiles []string, opts Options) CompareInput {
	return func(env string) *Project {
		o := opts
		o.Environment = env
		return Open(ctx, dir, files, profiles, o)
	}
}
